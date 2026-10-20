package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type hookView struct {
	Exists  bool   `json:"exists"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"` // admins only: the address is the secret
}

type automationView struct {
	Schedules []Schedule `json:"schedules"`
	Hook      hookView   `json:"hook"`
	Starts    []Start    `json:"starts"`
}

func (s *Server) hookAddress(token string) string {
	return s.cfg.PublicURL + "/api/workflow-hooks/" + token
}

// automation is everything that can start a workflow by itself, and what happened to recent starts.
func (s *Server) automation(w http.ResponseWriter, r *http.Request, c wsCtx) {
	id := r.PathValue("id")
	if _, err := s.repo.GetWorkflow(r.Context(), c.WS, id); err != nil {
		s.fail(w, r, err)
		return
	}
	v := automationView{}
	var err error
	if v.Schedules, err = s.repo.ListSchedules(r.Context(), c.WS, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if v.Starts, err = s.repo.ListStarts(r.Context(), c.WS, id, 20); err != nil {
		s.fail(w, r, err)
		return
	}
	h, err := s.repo.GetHook(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if h != nil {
		v.Hook = hookView{Exists: true, Enabled: h.Enabled}
		if roleRank[c.Role] >= roleRank["admin"] {
			if token, err := s.sealer.Open(h.TokenEn, "wfhook|"+id); err == nil {
				v.Hook.URL = s.hookAddress(token)
			}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var sc Schedule
	if !decode(w, r, &sc) {
		return
	}
	sc.ID, sc.WorkflowID = newID("sc"), r.PathValue("id")
	if sc.Freq != "monthly" {
		sc.DayOfMonth = 1
	}
	if sc.Freq != "weekly" {
		sc.Weekdays = 0
	}
	if err := sc.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.repo.CreateSchedule(r.Context(), c.WS, c.User.ID, sc); err != nil {
		s.fail(w, r, err)
		return
	}
	s.automation(w, r, c)
}

func (s *Server) patchSchedule(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.repo.SetScheduleEnabled(r.Context(), c.WS, r.PathValue("sid"), req.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	s.automation(w, r, c)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request, c wsCtx) {
	if err := s.repo.DeleteSchedule(r.Context(), c.WS, r.PathValue("sid")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.automation(w, r, c)
}

// putHook creates the workflow's webhook address, or replaces it (the old address stops working).
func (s *Server) putHook(w http.ResponseWriter, r *http.Request, c wsCtx) {
	id := r.PathValue("id")
	token := randomToken()
	if err := s.repo.SetHook(r.Context(), c.WS, id, hashToken(token), s.sealer.Seal(token, "wfhook|"+id)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.automation(w, r, c)
}

func (s *Server) patchHook(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.repo.SetHookEnabled(r.Context(), c.WS, r.PathValue("id"), req.Enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	s.automation(w, r, c)
}

func (s *Server) deleteHook(w http.ResponseWriter, r *http.Request, c wsCtx) {
	if err := s.repo.DeleteHook(r.Context(), c.WS, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.automation(w, r, c)
}

// startNow queues a background run of the workflow.
func (s *Server) startNow(w http.ResponseWriter, r *http.Request, c wsCtx) {
	st := Start{ID: newID("st"), WorkspaceID: c.WS, WorkflowID: r.PathValue("id"), Source: "manual"}
	if err := s.repo.QueueStart(r.Context(), st, c.User.ID); err != nil {
		if errors.Is(err, errQueueFull) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	s.wakeWorker()
	s.automation(w, r, c)
}

// incomingHook is the public address a service posts to. The token in the address is the only credential, so
// unknown, replaced and switched-off addresses all answer the same way.
func (s *Server) incomingHook(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	ws, wf, enabled, err := s.repo.HookByToken(r.Context(), token)
	if err != nil || !enabled {
		http.NotFound(w, r)
		return
	}
	if !s.hookCalls.allow(wf) {
		writeError(w, http.StatusTooManyRequests, "too many requests, try again in a minute")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the body is larger than 64 KB")
		return
	}
	input := cut(strings.TrimSpace(string(body)), maxHookInput)
	st := Start{ID: newID("st"), WorkspaceID: ws, WorkflowID: wf, Source: "webhook", Input: input}
	if err := s.repo.QueueStart(r.Context(), st, ""); err != nil {
		if errors.Is(err, errQueueFull) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.wakeWorker()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": st.ID, "queued": true, "at": time.Now().UTC().Format(rfc)})
}

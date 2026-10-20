package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

func newLongID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func (s *Server) hookURL(id string) string { return s.cfg.PublicURL + "/api/hooks/" + id }

func reviewProvider(w http.ResponseWriter, id string) *provider {
	p := providers[id]
	if p == nil || p.parseHook == nil {
		writeError(w, http.StatusBadRequest, "provider must be github or gitlab")
		return nil
	}
	return p
}

type triggerView struct {
	Trigger
	WebhookURL string `json:"webhookUrl,omitempty"`
	Secret     string `json:"secret,omitempty"` // only for admins
}

func (s *Server) viewTrigger(t Trigger, c wsCtx) triggerView {
	v := triggerView{Trigger: t}
	if roleRank[c.Role] >= roleRank["admin"] {
		v.WebhookURL = s.hookURL(t.ID)
		if sec, err := s.sealer.Open(t.SecretEnc, "trigger|"+t.ID); err == nil {
			v.Secret = sec
		}
	}
	return v
}

func (s *Server) listTriggers(w http.ResponseWriter, r *http.Request, c wsCtx) {
	list, err := s.repo.ListTriggers(r.Context(), c.WS)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []triggerView{}
	for _, t := range list {
		out = append(out, s.viewTrigger(t, c))
	}
	writeJSON(w, http.StatusOK, out)
}

type triggerRequest struct {
	WorkflowID string `json:"workflowId"`
	Provider   string `json:"provider"`
	Project    string `json:"project"`
	ReviewMode string `json:"reviewMode"`
	OnPush     bool   `json:"onPush"`
}

func validMode(m string) bool { return m == "draft" || m == "post" }

// createTrigger sets up "start this workflow for every new pull or merge request in a project".
func (s *Server) createTrigger(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req triggerRequest
	if !decode(w, r, &req) {
		return
	}
	p := reviewProvider(w, req.Provider)
	if p == nil {
		return
	}
	req.Project = strings.Trim(strings.TrimSpace(req.Project), "/")
	if !p.validProject(req.Project) {
		writeError(w, http.StatusBadRequest, "project must look like \"owner/repo\" (GitLab: \"group/project\")")
		return
	}
	if req.ReviewMode == "" {
		req.ReviewMode = "draft"
	}
	if !validMode(req.ReviewMode) {
		writeError(w, http.StatusBadRequest, "reviewMode must be draft or post")
		return
	}
	if _, err := s.repo.GetWorkflow(r.Context(), c.WS, req.WorkflowID); err != nil {
		s.fail(w, r, err)
		return
	}
	ts, err := s.tokensFor(r.Context(), c.WS, p)
	if err != nil {
		if errors.Is(err, errNotConnected) {
			writeError(w, http.StatusConflict, "Connect "+p.Label+" on the Integrations tab first.")
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	id := newLongID("tr")
	secret := randomToken()
	t := Trigger{ID: id, WorkspaceID: c.WS, WorkflowID: req.WorkflowID, Provider: p.ID, Project: req.Project, ReviewMode: req.ReviewMode, OnPush: req.OnPush,
		Enabled: true, HookStatus: "manual", SecretEnc: s.sealer.Seal(secret, "trigger|"+id)}
	// Try to register the webhook; if the account or the address does not allow it, the admin sets it up by hand.
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if hookID, err := p.createHook(ctx, s.appFor(ctx, c.WS, p), ts, req.Project, s.hookURL(id), secret); err != nil {
		t.HookError = cut(err.Error(), 300)
	} else {
		t.HookID, t.HookStatus = hookID, "registered"
	}
	if err := s.repo.CreateTrigger(r.Context(), t, c.User.ID); err != nil {
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code == "23505" {
			if t.HookID != "" { // do not leave a hook behind that nothing listens to
				_ = p.deleteHook(ctx, s.appFor(ctx, c.WS, p), ts, req.Project, t.HookID)
			}
			writeError(w, http.StatusConflict, "That workflow already has a trigger for this project.")
			return
		}
		s.fail(w, r, err)
		return
	}
	created, err := s.repo.GetTrigger(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.viewTrigger(created, c))
}

func (s *Server) patchTrigger(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Enabled    *bool   `json:"enabled"`
		ReviewMode *string `json:"reviewMode"`
		OnPush     *bool   `json:"onPush"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.ReviewMode != nil && !validMode(*req.ReviewMode) {
		writeError(w, http.StatusBadRequest, "reviewMode must be draft or post")
		return
	}
	id := r.PathValue("tid")
	if err := s.repo.UpdateTrigger(r.Context(), c.WS, id, req.Enabled, req.ReviewMode, req.OnPush); err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.repo.GetTrigger(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.viewTrigger(t, c))
}

func (s *Server) deleteTrigger(w http.ResponseWriter, r *http.Request, c wsCtx) {
	id := r.PathValue("tid")
	t, err := s.repo.GetTrigger(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if t.HookID != "" { // best effort: remove the webhook at the provider too
		if p := providers[t.Provider]; p != nil {
			if ts, err := s.tokensFor(r.Context(), c.WS, p); err == nil {
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				_ = p.deleteHook(ctx, s.appFor(ctx, c.WS, p), ts, t.Project, t.HookID)
				cancel()
			}
		}
	}
	if err := s.repo.DeleteTrigger(r.Context(), c.WS, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- the webhook ----------

const maxActiveRuns = 50

// webhook receives deliveries from GitHub and GitLab. The trigger id in the address is unguessable and every
// delivery must also carry the trigger's secret (GitHub: HMAC signature, GitLab: token).
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	t, err := s.repo.TriggerByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such webhook")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20+1))
	if err != nil || len(body) > 2<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	p := providers[t.Provider]
	secret, err := s.sealer.Open(t.SecretEnc, "trigger|"+t.ID)
	if p == nil || p.parseHook == nil || err != nil {
		writeError(w, http.StatusInternalServerError, "webhook is not usable")
		return
	}
	ev, ignore, err := p.parseHook(r.Header, body, secret, t.OnPush)
	if errors.Is(err, errBadSignature) {
		writeError(w, http.StatusUnauthorized, "bad signature")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable delivery")
		return
	}
	ignored := func(why string) { writeJSON(w, http.StatusOK, map[string]string{"ignored": why}) }
	if ignore != "" {
		ignored(ignore)
		return
	}
	if !t.Enabled {
		ignored("trigger is switched off")
		return
	}
	if !strings.EqualFold(ev.Project, t.Project) {
		ignored("a different project")
		return
	}
	n, err := s.repo.ActiveRuns(r.Context(), t.WorkspaceID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if n >= maxActiveRuns {
		writeError(w, http.StatusTooManyRequests, "too many runs are waiting")
		return
	}
	run := Run{ID: newLongID("r"), WorkspaceID: t.WorkspaceID, WorkflowID: t.WorkflowID, TriggerID: t.ID, Source: "webhook", ReviewMode: t.ReviewMode, Input: ev}
	key := strings.Join([]string{ev.Provider, strings.ToLower(ev.Project), strconv.Itoa(ev.Number), ev.HeadSHA}, "|")
	created, err := s.repo.EnqueueRun(r.Context(), run, key, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !created {
		ignored("already reviewed this commit")
		return
	}
	s.wakeWorker()
	writeJSON(w, http.StatusAccepted, map[string]string{"run": run.ID})
}

// ---------- runs ----------

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, c wsCtx) {
	runs, err := s.repo.ListRuns(r.Context(), c.WS, r.PathValue("id"), 30)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// listWorkspaceRuns is the newest runs of every workflow, for the Runs page.
func (s *Server) listWorkspaceRuns(w http.ResponseWriter, r *http.Request, c wsCtx) {
	runs, err := s.repo.ListRuns(r.Context(), c.WS, "", 60)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// startRun reviews one pull or merge request now, without waiting for a webhook. It is also the way to try the setup.
// Manual runs always save drafts: posting directly is a decision for the trigger's admin.
func (s *Server) startRun(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Provider string `json:"provider"`
		Project  string `json:"project"`
		Number   int    `json:"number"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := reviewProvider(w, req.Provider)
	if p == nil {
		return
	}
	req.Project = strings.Trim(strings.TrimSpace(req.Project), "/")
	if !p.validProject(req.Project) || req.Number < 1 || req.Number > 10_000_000 {
		writeError(w, http.StatusBadRequest, "give a project like \"owner/repo\" and the pull or merge request number")
		return
	}
	wfID := r.PathValue("id")
	if _, err := s.repo.GetWorkflow(r.Context(), c.WS, wfID); err != nil {
		s.fail(w, r, err)
		return
	}
	ts, err := s.tokensFor(r.Context(), c.WS, p)
	if err != nil {
		writeError(w, http.StatusConflict, "Connect "+p.Label+" on the Integrations tab first.")
		return
	}
	if n, err := s.repo.ActiveRuns(r.Context(), c.WS); err == nil && n >= maxActiveRuns {
		writeError(w, http.StatusTooManyRequests, "too many runs are waiting")
		return
	}
	ev, err := p.fetchChange(r.Context(), s.appFor(r.Context(), c.WS, p), ts, req.Project, req.Number)
	if err != nil {
		writeError(w, http.StatusBadGateway, cut(err.Error(), 200))
		return
	}
	run := Run{ID: newLongID("r"), WorkspaceID: c.WS, WorkflowID: wfID, Source: "manual", ReviewMode: "draft", Input: ev}
	if _, err := s.repo.EnqueueRun(r.Context(), run, "", c.User.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.wakeWorker()
	got, err := s.repo.GetRun(r.Context(), c.WS, run.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, got)
}

func (s *Server) rerun(w http.ResponseWriter, r *http.Request, c wsCtx) {
	old, err := s.repo.GetRun(r.Context(), c.WS, r.PathValue("rid"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if old.Status == "queued" || old.Status == "running" {
		writeError(w, http.StatusConflict, "That run has not finished yet.")
		return
	}
	run := Run{ID: newLongID("r"), WorkspaceID: c.WS, WorkflowID: old.WorkflowID, Source: "manual", ReviewMode: "draft", Input: old.Input}
	if _, err := s.repo.EnqueueRun(r.Context(), run, "", c.User.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.wakeWorker()
	got, err := s.repo.GetRun(r.Context(), c.WS, run.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, got)
}

// ---------- approving drafts ----------

func (s *Server) approveAction(w http.ResponseWriter, r *http.Request, c wsCtx) {
	id := r.PathValue("aid")
	a, err := s.repo.GetAction(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := providers[a.Provider]
	if p == nil || p.postReview == nil {
		writeError(w, http.StatusConflict, "unknown provider")
		return
	}
	ok, err := s.repo.ClaimAction(r.Context(), c.WS, id, "posting", c.User.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "That draft was already handled.")
		return
	}
	link, perr := s.deliverReview(r.Context(), c.WS, p, a)
	if perr != nil {
		_ = s.repo.FinishAction(r.Context(), id, "failed", cut(perr.Error(), 300))
	} else {
		_ = s.repo.FinishAction(r.Context(), id, "posted", link)
	}
	got, err := s.repo.GetAction(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) discardAction(w http.ResponseWriter, r *http.Request, c wsCtx) {
	id := r.PathValue("aid")
	if _, err := s.repo.GetAction(r.Context(), c.WS, id); err != nil {
		s.fail(w, r, err)
		return
	}
	ok, err := s.repo.ClaimAction(r.Context(), c.WS, id, "discarded", c.User.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "That draft was already handled.")
		return
	}
	got, err := s.repo.GetAction(r.Context(), c.WS, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

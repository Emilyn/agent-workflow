package main

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request, u User) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	ws, err := s.repo.CreateWorkspace(r.Context(), u.ID, req.Name)
	if err != nil {
		s.fail(w, r, wrapInput(err))
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

func (s *Server) renameWorkspace(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.repo.RenameWorkspace(r.Context(), c.WS, req.Name); err != nil {
		s.fail(w, r, wrapInput(err))
		return
	}
	writeJSON(w, http.StatusOK, WorkspaceInfo{ID: c.WS, Name: strings.TrimSpace(req.Name), Role: c.Role})
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request, c wsCtx) {
	if err := s.repo.DeleteWorkspace(r.Context(), c.WS); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, c wsCtx) {
	list, err := s.repo.Members(r.Context(), c.WS)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Teammates see names and roles; only admins also see email addresses.
	if roleRank[c.Role] < roleRank["admin"] {
		for i := range list {
			if list[i].UserID != c.User.ID {
				list[i].Email = ""
			}
		}
	}
	writeJSON(w, http.StatusOK, list)
}

// changeRole: admins manage members and admins; only owners create or change owners.
func (s *Server) changeRole(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	target := r.PathValue("uid")
	trole, err := s.repo.Role(r.Context(), c.WS, target)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if c.Role != "owner" && (trole == "owner" || req.Role == "owner") {
		writeError(w, http.StatusForbidden, "Only an owner can change owners.")
		return
	}
	if err := s.repo.SetRole(r.Context(), c.WS, target, req.Role); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listMembers(w, r, c)
}

// removeMember: anyone can leave; admins remove members; owners remove anyone.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, c wsCtx) {
	target := r.PathValue("uid")
	if target != c.User.ID {
		trole, err := s.repo.Role(r.Context(), c.WS, target)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if roleRank[c.Role] < roleRank["admin"] || (roleRank[trole] >= roleRank[c.Role] && c.Role != "owner") {
			writeError(w, http.StatusForbidden, "You can't remove that person.")
			return
		}
	}
	if err := s.repo.RemoveMember(r.Context(), c.WS, target); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- invite links ----------

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, c wsCtx) {
	list, err := s.repo.Invites(r.Context(), c.WS)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// createInvite returns the join link once. Only a hash of its token is stored, so it can't be shown again.
func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Note string `json:"note"`
		Role string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role == "admin" && c.Role != "owner" {
		writeError(w, http.StatusForbidden, "Only an owner can invite admins.")
		return
	}
	inv, token, err := s.repo.CreateInvite(r.Context(), c.WS, c.User.ID, req.Note, req.Role)
	if err != nil {
		s.fail(w, r, wrapInput(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inv, "token": token})
}

func (s *Server) deleteInvite(w http.ResponseWriter, r *http.Request, c wsCtx) {
	if err := s.repo.DeleteInvite(r.Context(), c.WS, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) invitePreview(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginsIP.allow("invite|" + ip) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	ws, err := s.repo.InvitePreview(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"workspace": ws.Name, "role": ws.Role})
}

func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request, u User) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !s.loginsIP.allow("invite|" + s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	ws, err := s.repo.AcceptInvite(r.Context(), strings.TrimSpace(req.Token), u.ID)
	if err != nil {
		if errors.Is(err, errAlreadyIn) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

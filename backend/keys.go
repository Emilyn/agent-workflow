package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

type claudeStatus struct {
	Connected bool   `json:"connected"`
	Source    string `json:"source"` // "workspace" | "server" | ""
	KeyHint   string `json:"keyHint,omitempty"`
	Model     string `json:"model"`
}

// keyFor returns the API key a workspace's runs should use: its own encrypted
// key, or the server-wide ANTHROPIC_API_KEY when it has not connected one.
func (s *Server) keyFor(ctx context.Context, wsID string) (key, source string, err error) {
	enc, _, err := s.repo.Key(ctx, wsID)
	if err != nil {
		return "", "", err
	}
	if enc != nil {
		key, err = s.sealer.Open(enc, wsID)
		if err != nil {
			return "", "", err
		}
		return key, "workspace", nil
	}
	if s.cfg.ServerKey != "" {
		return s.cfg.ServerKey, "server", nil
	}
	return "", "", nil
}

func (s *Server) statusFor(ctx context.Context, wsID string) (claudeStatus, error) {
	st := claudeStatus{Model: s.claude.Model}
	enc, hint, err := s.repo.Key(ctx, wsID)
	if err != nil {
		return st, err
	}
	switch {
	case enc != nil:
		st.Connected, st.Source, st.KeyHint = true, "workspace", hint
	case s.cfg.ServerKey != "":
		st.Connected, st.Source = true, "server"
	}
	return st, nil
}

func (s *Server) getClaude(w http.ResponseWriter, r *http.Request, c wsCtx) {
	st, err := s.statusFor(r.Context(), c.WS)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) putClaudeKey(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		APIKey string `json:"apiKey"`
	}
	if !decode(w, r, &req) {
		return
	}
	key := strings.TrimSpace(req.APIKey)
	switch {
	case strings.HasPrefix(key, "sk-ant-oat"):
		writeError(w, http.StatusBadRequest, "That looks like a Claude subscription token. Those cannot be used here. Create an API key in the Claude Console instead.")
		return
	case !strings.HasPrefix(key, "sk-ant-") || len(key) < 20 || len(key) > 400 || strings.ContainsAny(key, " \t\r\n"):
		writeError(w, http.StatusBadRequest, "That does not look like an Anthropic API key. Keys start with sk-ant-.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.claude.Verify(ctx, key); err != nil {
		if errors.Is(err, errKeyRejected) {
			writeError(w, http.StatusBadRequest, "Anthropic rejected that key. Check it and try again.")
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	hint := ""
	if len(key) >= 12 {
		hint = "…" + key[len(key)-4:]
	}
	if err := s.repo.SetKey(r.Context(), c.WS, c.User.ID, s.sealer.Seal(key, c.WS), hint); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getClaude(w, r, c)
}

func (s *Server) deleteClaudeKey(w http.ResponseWriter, r *http.Request, c wsCtx) {
	if err := s.repo.DeleteKey(r.Context(), c.WS); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getClaude(w, r, c)
}

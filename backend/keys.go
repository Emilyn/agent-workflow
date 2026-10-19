package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

type claudeStatus struct {
	Connected bool       `json:"connected"`
	Source    string     `json:"source"` // "workspace" | "server" | "claude_code" | ""
	KeyHint   string     `json:"keyHint,omitempty"`
	Model     string     `json:"model"`
	Mode      string     `json:"mode"` // "api" or "claude_code"
	Code      codeStatus `json:"claudeCode"`
}

// codeStatus describes the "use the Claude Code on this machine" option.
type codeStatus struct {
	Enabled      bool         `json:"enabled"` // the operator turned it on (CLAUDE_CODE_ENABLED)
	Account      *codeAccount `json:"account,omitempty"`
	Model        string       `json:"model,omitempty"`
	Effort       string       `json:"effort,omitempty"`
	MaxTurns     int          `json:"maxTurns,omitempty"`
	MaxBudgetUSD float64      `json:"maxBudgetUsd,omitempty"`
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

// notReady says why a workspace cannot run agents yet, or "" when it can.
func (s *Server) notReady(ctx context.Context, wsID string) string {
	mode, err := s.repo.AgentMode(ctx, wsID)
	if err != nil {
		return "could not read the workspace settings"
	}
	if mode == modeClaudeCode {
		if acct := s.codeAccount(ctx, false); !acct.OK {
			return "Claude Code is not ready: " + acct.Error
		}
		return ""
	}
	if key, _, err := s.keyFor(ctx, wsID); err != nil || key == "" {
		return "Claude is not connected: an admin needs to add an Anthropic API key in Settings"
	}
	return ""
}

func (s *Server) statusFor(ctx context.Context, wsID string) (claudeStatus, error) {
	cc := s.cfg.ClaudeCode
	st := claudeStatus{Model: s.claude.Model, Mode: modeAPI, Code: codeStatus{Enabled: cc.Enabled}}
	mode, err := s.repo.AgentMode(ctx, wsID)
	if err != nil {
		return st, err
	}
	if cc.Enabled {
		acct := s.codeAccount(ctx, false)
		st.Code.Account = &acct
		st.Code.Model, st.Code.Effort, st.Code.MaxTurns, st.Code.MaxBudgetUSD = cc.Model, cc.Effort, cc.MaxTurns, cc.MaxBudgetUSD
	}
	if mode == modeClaudeCode {
		st.Mode, st.Model = modeClaudeCode, cc.Model
		if st.Code.Account != nil && st.Code.Account.OK {
			st.Connected, st.Source = true, "claude_code"
		}
		return st, nil
	}
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

// putClaudeMode switches a workspace between its API key and the Claude Code on the server.
func (s *Server) putClaudeMode(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &req) {
		return
	}
	switch req.Mode {
	case modeAPI:
	case modeClaudeCode:
		if !s.cfg.ClaudeCode.Enabled {
			writeError(w, http.StatusBadRequest, "Claude Code is not turned on for this server. The person running it sets CLAUDE_CODE_ENABLED=true.")
			return
		}
		if acct := s.codeAccount(r.Context(), true); !acct.OK {
			writeError(w, http.StatusBadRequest, acct.Error)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode must be api or claude_code")
		return
	}
	if err := s.repo.SetAgentMode(r.Context(), c.WS, c.User.ID, req.Mode); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getClaude(w, r, c)
}

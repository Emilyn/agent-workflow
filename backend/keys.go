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
	Enabled      bool          `json:"enabled"` // the operator turned it on (CLAUDE_CODE_ENABLED)
	Account      *codeAccount  `json:"account,omitempty"`
	Model        string        `json:"model,omitempty"` // what this workspace runs with: its own setting, else the server's
	Effort       string        `json:"effort,omitempty"`
	MaxTurns     int           `json:"maxTurns,omitempty"`
	MaxBudgetUSD float64       `json:"maxBudgetUsd,omitempty"`
	TimeoutSecs  int           `json:"timeoutSeconds,omitempty"`
	Custom       CodeOverrides `json:"custom"`   // the workspace's own settings; null fields use Defaults
	Defaults     codeDefaults  `json:"defaults"` // the server's settings
}

type codeDefaults struct {
	Model          string  `json:"model"`
	Effort         string  `json:"effort"`
	MaxTurns       int     `json:"maxTurns"`
	MaxBudgetUSD   float64 `json:"maxBudgetUsd"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
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
		eff, custom, err := s.codeSettings(ctx, wsID)
		if err != nil {
			return st, err
		}
		st.Code.Model, st.Code.Effort, st.Code.MaxTurns, st.Code.MaxBudgetUSD, st.Code.TimeoutSecs = eff.Model, eff.Effort, eff.MaxTurns, eff.MaxBudgetUSD, int(eff.Timeout.Seconds())
		st.Code.Custom = custom
		st.Code.Defaults = codeDefaults{cc.Model, cc.Effort, cc.MaxTurns, cc.MaxBudgetUSD, int(cc.Timeout.Seconds())}
		cc = eff
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

// putClaudeCodeSettings saves a workspace's own Claude Code settings. A missing or null field goes back
// to the server's default. Where the program lives (CLAUDE_CODE_PATH) and whether the mode exists at all
// (CLAUDE_CODE_ENABLED) stay with whoever runs the server: a workspace admin must not be able to choose
// which program the server executes.
func (s *Server) putClaudeCodeSettings(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var o CodeOverrides
	if !decode(w, r, &o) {
		return
	}
	if !s.cfg.ClaudeCode.Enabled {
		writeError(w, http.StatusBadRequest, "Claude Code is not turned on for this server. The person running it sets CLAUDE_CODE_ENABLED=true.")
		return
	}
	if o.Model != nil {
		m := strings.TrimSpace(*o.Model)
		o.Model = &m
		if m == "" {
			o.Model = nil
		} else if !validModel(m) {
			writeError(w, http.StatusBadRequest, "That model name does not look right. Use a name like claude-sonnet-5-5 or an alias like sonnet.")
			return
		}
	}
	if o.Effort != nil {
		e := strings.ToLower(strings.TrimSpace(*o.Effort))
		o.Effort = &e
		if e == "" {
			o.Effort = nil
		} else if !validEffort(e) {
			writeError(w, http.StatusBadRequest, "Effort must be low, medium, high, xhigh or max.")
			return
		}
	}
	if o.MaxTurns != nil && !validMaxTurns(*o.MaxTurns) {
		writeError(w, http.StatusBadRequest, "Turns per run must be a number from 1 to 100.")
		return
	}
	if o.MaxBudgetUSD != nil && !validBudget(*o.MaxBudgetUSD) {
		writeError(w, http.StatusBadRequest, "Spending cap must be more than 0 and at most 1000 dollars.")
		return
	}
	if o.TimeoutSeconds != nil && !validTimeout(*o.TimeoutSeconds) {
		writeError(w, http.StatusBadRequest, "Time limit must be from 30 to 3600 seconds.")
		return
	}
	if err := s.repo.SetCodeOverrides(r.Context(), c.WS, c.User.ID, o); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getClaude(w, r, c)
}

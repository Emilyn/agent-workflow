package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Claude Code mode: instead of calling the Messages API with a key, start the `claude` program that
// is installed on the server and signed in there. Our tools (GitHub, GitLab, the review tool and so
// on) are offered to it through a small MCP endpoint on this server, so they still run here, with the
// same checks and the same binding to the run. Claude Code's own tools (files, shell, web) are off.

const (
	modeAPI        = "api"
	modeClaudeCode = "claude_code"
	mcpServerName  = "aw"
)

type longRunKey struct{}

// withLongRun marks a background run, which may take longer than a request is allowed to.
func withLongRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, longRunKey{}, true)
}

// ---------- who the agent runs as ----------

type codeAccount struct {
	OK     bool   `json:"ok"`
	Method string `json:"method,omitempty"` // how that login was made, for example "oauth_token" or "claude.ai"
	Email  string `json:"email,omitempty"`
	Org    string `json:"org,omitempty"`
	Plan   string `json:"plan,omitempty"`
	Error  string `json:"error,omitempty"`
}

type codeProbe struct {
	mu   sync.Mutex
	at   time.Time
	acct codeAccount
}

const probeTTL = 20 * time.Second

// codeAccount asks the installed Claude Code who it is signed in as. The answer is cached for a few seconds.
func (s *Server) codeAccount(ctx context.Context, fresh bool) codeAccount {
	cc := s.cfg.ClaudeCode
	if !cc.Enabled {
		return codeAccount{Error: "Claude Code is not turned on for this server."}
	}
	s.probe.mu.Lock()
	defer s.probe.mu.Unlock()
	if !fresh && !s.probe.at.IsZero() && time.Since(s.probe.at) < probeTTL {
		return s.probe.acct
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cc.Path, "auth", "status", "--json")
	cmd.Env = codeEnv()
	cmd.Dir = os.TempDir()
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	acct := parseAccount(out.Bytes(), err)
	s.probe.at, s.probe.acct = time.Now(), acct
	return acct
}

func parseAccount(out []byte, runErr error) codeAccount {
	var st struct {
		LoggedIn bool   `json:"loggedIn"`
		Method   string `json:"authMethod"`
		Email    string `json:"email"`
		Org      string `json:"orgName"`
		Plan     string `json:"subscriptionType"`
	}
	if jerr := json.Unmarshal(bytes.TrimSpace(out), &st); jerr != nil {
		var ee *exec.Error
		if errors.As(runErr, &ee) || errors.Is(runErr, os.ErrNotExist) {
			return codeAccount{Error: "Claude Code was not found on the server. Install it, or set CLAUDE_CODE_PATH to its location."}
		}
		return codeAccount{Error: "Could not read the sign-in status of Claude Code on the server."}
	}
	if !st.LoggedIn {
		return codeAccount{Error: "Claude Code on the server is not signed in. On that machine run claude, then /login."}
	}
	return codeAccount{OK: true, Method: st.Method, Email: st.Email, Org: st.Org, Plan: st.Plan}
}

// codeEnv is the environment for Claude Code: the server's own, without API keys, so it uses the login.
func codeEnv() []string {
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") || strings.HasPrefix(e, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		env = append(env, e)
	}
	return env
}

// ---------- running a prompt ----------

type codeResult struct {
	Type    string  `json:"type"`
	Subtype string  `json:"subtype"`
	IsError bool    `json:"is_error"`
	Result  string  `json:"result"`
	Cost    float64 `json:"total_cost_usd"`
	Turns   int     `json:"num_turns"`
}

// generateCode runs one prompt in Claude Code, with our tools when there are any.
func (s *Server) generateCode(ctx context.Context, wsID, system, user string, tools []ToolDef, run ToolRunner) (string, error) {
	cc := s.cfg.ClaudeCode
	if acct := s.codeAccount(ctx, false); !acct.OK {
		return "", errors.New(acct.Error)
	}
	if !s.claudeCalls.allow(wsID) {
		return "", errClaudeBusy
	}
	limit := 100 * time.Second // a request is cut off at 120 s
	if ctx.Value(longRunKey{}) != nil {
		limit = cc.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	dir, err := os.MkdirTemp("", "aw-claude-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	args := []string{"-p", "--output-format", "json", "--no-session-persistence",
		"--model", cc.Model, "--max-turns", strconv.Itoa(cc.MaxTurns), "--max-budget-usd", strconv.FormatFloat(cc.MaxBudgetUSD, 'f', -1, 64),
		"--system-prompt", system, "--tools", "", "--strict-mcp-config", "--setting-sources", "", "--permission-mode", "dontAsk"}
	if cc.Effort != "" {
		args = append(args, "--effort", cc.Effort)
	}
	if len(tools) > 0 {
		token, stop := s.bridge.open(tools, run)
		defer stop()
		cfgJSON, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{mcpServerName: map[string]any{"type": "http", "url": s.mcpURL(token)}}})
		args = append(args, "--mcp-config", string(cfgJSON), "--allowedTools", "mcp__"+mcpServerName)
	}

	cmd := exec.CommandContext(ctx, cc.Path, args...)
	cmd.Dir = dir
	cmd.Env = codeEnv()
	cmd.Stdin = strings.NewReader(user)
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &capBuffer{max: 4 << 20}, &capBuffer{max: 8 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()

	var res codeResult
	if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &res); jerr != nil {
		if ctx.Err() != nil {
			return "", errors.New("Claude Code took too long to answer")
		}
		s.log.Warn("claude code produced no result", "err", runErr, "stderr", cut(stderr.String(), 400))
		return "", errors.New("Claude Code did not return an answer")
	}
	s.log.Info("claude code run", "ws", wsID, "turns", res.Turns, "cost_usd", res.Cost, "subtype", res.Subtype)
	switch {
	case res.Subtype == "error_max_turns":
		return "", errors.New("Claude Code reached its turn limit before finishing")
	case res.Subtype == "error_max_budget_usd":
		return "", errors.New("Claude Code reached its spending limit for one run before finishing")
	case res.IsError:
		return "", errors.New("Claude Code reported an error: " + cut(strings.TrimSpace(res.Result), 300))
	}
	text := strings.TrimSpace(res.Result)
	if text == "" {
		return "", errors.New("claude returned an empty reply")
	}
	return text, nil
}

type capBuffer struct {
	bytes.Buffer
	max int
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil // keep draining the pipe even when full
}

func (s *Server) mcpURL(token string) string {
	base := s.cfg.ClaudeCode.CallbackURL
	if base == "" {
		base = "http://127.0.0.1:" + s.cfg.Port
	}
	return base + "/internal/mcp/" + token
}

// ---------- the tools, offered to Claude Code over MCP ----------

type mcpSession struct {
	tools []ToolDef
	run   ToolRunner
	mu    sync.Mutex
	calls int
}

type codeBridge struct {
	mu       sync.Mutex
	sessions map[string]*mcpSession
}

func newCodeBridge() *codeBridge { return &codeBridge{sessions: map[string]*mcpSession{}} }

// open makes the tools reachable at an unguessable address until stop is called.
func (b *codeBridge) open(tools []ToolDef, run ToolRunner) (token string, stop func()) {
	token = newLongID("")
	b.mu.Lock()
	b.sessions[token] = &mcpSession{tools: tools, run: run}
	b.mu.Unlock()
	return token, func() {
		b.mu.Lock()
		delete(b.sessions, token)
		b.mu.Unlock()
	}
}

func (b *codeBridge) get(token string) *mcpSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions[token]
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func rpcReply(w http.ResponseWriter, id json.RawMessage, result any, rpcErr map[string]any) {
	body := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		body["error"] = rpcErr
	} else {
		body["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// mcp is the endpoint Claude Code connects to: POST /internal/mcp/{token}. It speaks the small part of
// the Model Context Protocol that listing and calling tools needs.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	sess := s.bridge.get(r.PathValue("token"))
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	if s.cfg.ClaudeCode.CallbackURL == "" {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
			http.NotFound(w, r)
			return
		}
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		rpcReply(w, nil, nil, map[string]any{"code": -32700, "message": "parse error"})
		return
	}
	if len(req.ID) == 0 { // a notification: no reply
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-03-26"
		}
		rpcReply(w, req.ID, map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "agent-workflow", "version": "1"}}, nil)
	case "ping":
		rpcReply(w, req.ID, map[string]any{}, nil)
	case "tools/list":
		list := make([]map[string]any, 0, len(sess.tools))
		for _, t := range sess.tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		rpcReply(w, req.ID, map[string]any{"tools": list}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcReply(w, req.ID, nil, map[string]any{"code": -32602, "message": "bad parameters"})
			return
		}
		known := false
		for _, t := range sess.tools {
			known = known || t.Name == p.Name
		}
		if !known {
			rpcReply(w, req.ID, nil, map[string]any{"code": -32602, "message": "unknown tool"})
			return
		}
		sess.mu.Lock()
		sess.calls++
		over := sess.calls > maxToolCalls
		sess.mu.Unlock()
		text, isErr := "Tool call limit reached. Answer with what you have.", true
		if !over {
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			out, err := sess.run(ctx, p.Name, p.Arguments)
			cancel()
			text, isErr = out, false
			if err != nil {
				text, isErr = err.Error(), true
			}
		}
		rpcReply(w, req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}, nil)
	default:
		rpcReply(w, req.ID, nil, map[string]any{"code": -32601, "message": fmt.Sprintf("method %q is not supported", req.Method)})
	}
}

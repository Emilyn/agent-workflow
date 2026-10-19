package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ---------- a stand-in for the claude program ----------
//
// The test program runs itself as `claude` (see TestMain). AW_FAKE_CLAUDE picks what it does:
//   ok        answers "fake answer"
//   loggedout says it is not signed in
//   review    reads the diff and posts a review through the MCP endpoint it is given
//   maxturns, error, garbage   fail in the ways the real program does
// AW_FAKE_LOG is a file it writes what it was started with into.

const fakeClaudeEnv = "AW_FAKE_CLAUDE"

type fakeRun struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
	Env   []string `json:"env"`
	Dir   string   `json:"dir"`
	URL   string   `json:"url"`
}

func fakeClaudeMain() {
	mode := os.Getenv(fakeClaudeEnv)
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "auth" {
		if mode == "loggedout" {
			fmt.Println(`{"loggedIn": false}`)
			return
		}
		fmt.Println(`{"loggedIn": true, "authMethod": "claude.ai", "email": "ops@example.com", "subscriptionType": "max"}`)
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	dir, _ := os.Getwd()
	rec := fakeRun{Args: args, Stdin: string(stdin), Dir: dir}
	for _, e := range os.Environ() {
		if name := strings.SplitN(e, "=", 2)[0]; name == "ANTHROPIC_API_KEY" || name == "ANTHROPIC_AUTH_TOKEN" {
			rec.Env = append(rec.Env, name)
		}
	}
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) {
			var c struct {
				Servers map[string]struct{ URL string } `json:"mcpServers"`
			}
			_ = json.Unmarshal([]byte(args[i+1]), &c)
			rec.URL = c.Servers["aw"].URL
		}
	}
	result := func(sub string, isErr bool, text string) {
		b, _ := json.Marshal(map[string]any{"type": "result", "subtype": sub, "is_error": isErr, "result": text, "num_turns": 3, "total_cost_usd": 0.01})
		fmt.Println(string(b))
	}
	switch mode {
	case "maxturns":
		result("error_max_turns", true, "")
	case "error":
		result("success", true, "Not logged in · Please run /login")
	case "garbage":
		fmt.Println("this is not json")
	case "review":
		rpc := func(method string, params any) map[string]any {
			id := 1
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
			resp, err := http.Post(rec.URL, "application/json", bytes.NewReader(body))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			defer resp.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&out)
			return out
		}
		rpc("initialize", map[string]any{"protocolVersion": "2025-03-26"})
		list := rpc("tools/list", nil)
		names := []string{}
		for _, t := range list["result"].(map[string]any)["tools"].([]any) {
			names = append(names, t.(map[string]any)["name"].(string))
		}
		rec.Args = append(rec.Args, "TOOLS="+strings.Join(names, ","))
		var calls []struct {
			Name string         `json:"name"`
			Args map[string]any `json:"args"`
		}
		_ = json.Unmarshal([]byte(os.Getenv("AW_FAKE_CALLS")), &calls)
		for _, c := range calls {
			out := rpc("tools/call", map[string]any{"name": c.Name, "arguments": c.Args})
			b, _ := json.Marshal(out)
			rec.Args = append(rec.Args, "CALL="+c.Name+":"+string(b))
		}
		result("success", false, "Review drafted by the fake claude.")
	default:
		result("success", false, "fake answer")
	}
	if f := os.Getenv("AW_FAKE_LOG"); f != "" {
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(f, b, 0o600)
	}
}

func readFakeRun(t *testing.T, file string) fakeRun {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the fake claude did not run: %v", err)
	}
	var r fakeRun
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func hasArgPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func fakeClaudeSetup(t *testing.T, mode string) (logFile string) {
	t.Helper()
	logFile = t.TempDir() + "/run.json"
	t.Setenv(fakeClaudeEnv, mode)
	t.Setenv("AW_FAKE_LOG", logFile)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-must-not-reach-claude-code")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token-must-not-reach-claude-code")
	return logFile
}

func (p *person) setMode(want int, mode string) map[string]any {
	p.t.Helper()
	var out map[string]any
	p.mustCall(want, "PUT", p.path("/claude/mode"), map[string]string{"mode": mode}, &out)
	return out
}

// ---------- status and switching ----------

func TestClaudeCodeStatusAndMode(t *testing.T) {
	fakeClaudeSetup(t, "ok")

	off := newEnv(t) // the operator has not turned Claude Code on
	a := off.signup("Ann", "ann@example.com")
	var st claudeStatus
	a.mustCall(200, "GET", a.path("/claude"), nil, &st)
	if st.Mode != "api" || st.Code.Enabled || st.Code.Account != nil {
		t.Fatalf("%+v", st)
	}
	a.setMode(400, "claude_code")

	e := newEnv(t, envOpts{claudeCode: true})
	admin := e.signup("Ann", "ann@example.com")
	member := e.signup("Mo", "mo@example.com")
	e.join(admin, member, "member")
	a2 := admin
	a2.mustCall(200, "GET", a2.path("/claude"), nil, &st)
	if !st.Code.Enabled || st.Code.Account == nil || !st.Code.Account.OK || st.Code.Account.Email != "ops@example.com" || st.Code.Model != "test-model" || st.Code.MaxTurns != 7 || st.Connected {
		t.Fatalf("before switching: %+v", st)
	}
	member.mustCall(403, "PUT", admin.path("/claude/mode"), map[string]string{"mode": "claude_code"}, nil)
	admin.setMode(400, "something-else")
	admin.mustCall(200, "PUT", admin.path("/claude/mode"), map[string]string{"mode": "claude_code"}, &st)
	if st.Mode != "claude_code" || !st.Connected || st.Source != "claude_code" || st.Model != "test-model" {
		t.Fatalf("after switching: %+v", st)
	}
	// a teammate sees it too, and an API key can still be stored for later
	member.mustCall(200, "GET", admin.path("/claude"), nil, &st)
	if st.Mode != "claude_code" || !st.Connected {
		t.Fatalf("%+v", st)
	}
	admin.mustCall(200, "PUT", admin.path("/claude/mode"), map[string]string{"mode": "api"}, &st)
	if st.Mode != "api" || st.Connected {
		t.Fatalf("%+v", st)
	}
}

func TestClaudeCodeNotSignedInCannotBeSelected(t *testing.T) {
	fakeClaudeSetup(t, "loggedout")
	e := newEnv(t, envOpts{claudeCode: true})
	admin := e.signup("Ann", "ann@example.com")
	var st claudeStatus
	admin.mustCall(200, "GET", admin.path("/claude"), nil, &st)
	if st.Code.Account == nil || st.Code.Account.OK || !strings.Contains(st.Code.Account.Error, "not signed in") {
		t.Fatalf("%+v", st.Code)
	}
	var body map[string]string
	admin.mustCall(400, "PUT", admin.path("/claude/mode"), map[string]string{"mode": "claude_code"}, &body)
	if !strings.Contains(body["error"], "/login") {
		t.Fatalf("%v", body)
	}
}

// ---------- running a step ----------

func TestClaudeCodeRunsAStep(t *testing.T) {
	logFile := fakeClaudeSetup(t, "ok")
	an := newScriptedAnthropic(t, finalText("the API must not be called"))
	e := newEnv(t, envOpts{claudeCode: true, anthropicURL: an.URL, serverKey: "k"})
	admin := e.signup("Ann", "ann@example.com")
	admin.setMode(200, "claude_code")

	var wfs []Summary
	admin.mustCall(200, "GET", admin.path("/workflows"), nil, &wfs)
	var res runResponse
	admin.mustCall(200, "POST", admin.path("/workflows/"+wfs[0].ID+"/run/step"), map[string]string{"stepId": "n2"}, &res)
	if res.Text != "fake answer" || res.Simulated {
		t.Fatalf("%+v", res)
	}
	if n := len(an.requests()); n != 0 {
		t.Fatalf("the Messages API was called %d times", n)
	}
	run := readFakeRun(t, logFile)
	for _, want := range [][2]string{{"--tools", ""}, {"--model", "test-model"}, {"--max-turns", "7"}, {"--max-budget-usd", "0.5"}, {"--setting-sources", ""}, {"--permission-mode", "dontAsk"}} {
		if !hasArgPair(run.Args, want[0], want[1]) {
			t.Errorf("missing %v in %v", want, run.Args)
		}
	}
	joined := strings.Join(run.Args, " ")
	if !strings.Contains(joined, "-p") || !strings.Contains(joined, "--strict-mcp-config") || strings.Contains(joined, "--mcp-config") {
		t.Errorf("args: %v", run.Args)
	}
	if len(run.Env) != 0 {
		t.Errorf("API keys reached Claude Code: %v", run.Env)
	}
	if !strings.Contains(run.Stdin, "Research") && !strings.Contains(run.Stdin, "step") {
		t.Errorf("the prompt was not sent on stdin: %q", run.Stdin)
	}
	if strings.Contains(run.Dir, "agent-workflow") || run.Dir == "" {
		t.Errorf("working directory: %q", run.Dir)
	}
	if _, err := os.Stat(run.Dir); err == nil {
		t.Errorf("the temporary working directory was not removed: %s", run.Dir)
	}
}

func TestClaudeCodeWhenTurnedOffFallsBackToSimulated(t *testing.T) {
	e := newEnv(t) // the workspace picked Claude Code, then the operator turned it off
	admin := e.signup("Ann", "ann@example.com")
	if err := e.repo.SetAgentMode(t.Context(), admin.WS, admin.User.ID, "claude_code"); err != nil {
		t.Fatal(err)
	}
	var wfs []Summary
	admin.mustCall(200, "GET", admin.path("/workflows"), nil, &wfs)
	var res runResponse
	admin.mustCall(200, "POST", admin.path("/workflows/"+wfs[0].ID+"/run/step"), map[string]string{"stepId": "n2"}, &res)
	if !res.Simulated {
		t.Fatalf("%+v", res)
	}
}

// ---------- a review run, tools over MCP ----------

func TestClaudeCodeReviewRunUsesOurTools(t *testing.T) {
	logFile := fakeClaudeSetup(t, "review")
	calls, _ := json.Marshal([]map[string]any{
		{"name": "github_get_pull_request_diff", "args": map[string]any{"owner": "acme", "repo": "app", "number": 5}},
		{"name": "post_review", "args": map[string]any{"summary": "Looks fine.", "comments": []map[string]any{{"path": "main.go", "line": 11, "body": "Check for nil here."}}}},
		{"name": "github_get_repo", "args": map[string]any{"owner": "../evil", "repo": "x"}}, // bad input must still be refused
	})
	t.Setenv("AW_FAKE_CALLS", string(calls))

	f := newFakeProviders(t)
	an := newScriptedAnthropic(t, finalText("the API must not be called"))
	e := newEnv(t, envOpts{claudeCode: true, providersURL: f.URL, anthropicURL: an.URL})
	admin := e.signup("Ann", "ann@example.com")
	admin.setMode(200, "claude_code")
	connect(t, admin, "github")
	var wf Workflow
	admin.mustCall(201, "POST", admin.path("/workflows"), map[string]string{"name": "Review", "template": "review"}, &wf)
	r := &reviewEnv{env: e, f: f, an: an, admin: admin, wf: wf.ID}
	admin.mustCall(202, "POST", admin.path("/workflows/"+wf.ID+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, nil)

	run := r.waitRun()
	if run.Status != "done" || len(run.Steps) != 1 || run.Steps[0].Text != "Review drafted by the fake claude." {
		t.Fatalf("%+v", run)
	}
	if got := strings.Join(run.Steps[0].Used, ","); got != "github_get_pull_request_diff,post_review,github_get_repo" {
		t.Errorf("used: %s", got)
	}
	if len(run.Actions) != 1 || run.Actions[0].Status != "pending" || run.Actions[0].Summary != "Looks fine." || len(run.Actions[0].Comments) != 1 {
		t.Fatalf("a draft review was expected: %+v", run.Actions)
	}
	if n := len(an.requests()); n != 0 {
		t.Fatalf("the Messages API was called %d times", n)
	}

	rec := readFakeRun(t, logFile)
	if !hasArgPair(rec.Args, "--allowedTools", "mcp__aw") || !strings.Contains(rec.URL, "/internal/mcp/") {
		t.Errorf("args: %v url %q", rec.Args, rec.URL)
	}
	var tools, callLog []string
	for _, a := range rec.Args {
		if v, ok := strings.CutPrefix(a, "TOOLS="); ok {
			tools = strings.Split(v, ",")
		}
		if strings.HasPrefix(a, "CALL=") {
			callLog = append(callLog, a)
		}
	}
	if !inList(tools, "post_review") || !inList(tools, "github_get_pull_request_diff") || inList(tools, "gitlab_get_merge_request_diff") {
		t.Errorf("tools offered: %v", tools)
	}
	if len(callLog) != 3 || !strings.Contains(callLog[0], `"isError":false`) || !strings.Contains(callLog[2], `"isError":true`) {
		t.Errorf("calls: %v", callLog)
	}

	// the address stops working when the run is over
	resp, err := http.Post(rec.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("the tool address still works after the run: %d", resp.StatusCode)
	}
	// and approving the draft posts it as before
	r.admin.mustCall(200, "POST", r.admin.path("/review-actions/"+run.Actions[0].ID+"/approve"), map[string]string{}, nil)
	if got := r.f.bodies("github", "POST /github/api/repos/acme/app/pulls/5/reviews"); len(got) != 1 {
		t.Errorf("the approved review was not posted: %v", got)
	}
}

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- failures ----------

func TestClaudeCodeFailuresAreClear(t *testing.T) {
	cases := []struct{ mode, want string }{
		{"maxturns", "turn limit"},
		{"error", "Claude Code reported an error: Not logged in"},
		{"garbage", "did not return an answer"},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			fakeClaudeSetup(t, c.mode)
			e := newEnv(t, envOpts{claudeCode: true})
			admin := e.signup("Ann", "ann@example.com")
			admin.setMode(200, "claude_code")
			var wfs []Summary
			admin.mustCall(200, "GET", admin.path("/workflows"), nil, &wfs)
			var body map[string]string
			admin.mustCall(502, "POST", admin.path("/workflows/"+wfs[0].ID+"/run/step"), map[string]string{"stepId": "n2"}, &body)
			if !strings.Contains(body["error"], c.want) {
				t.Fatalf("%q does not mention %q", body["error"], c.want)
			}
		})
	}
}

func TestClaudeCodeSignedOutFailsARunClearly(t *testing.T) {
	fakeClaudeSetup(t, "ok")
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{claudeCode: true, providersURL: f.URL})
	admin := e.signup("Ann", "ann@example.com")
	admin.setMode(200, "claude_code")
	connect(t, admin, "github")
	var wf Workflow
	admin.mustCall(201, "POST", admin.path("/workflows"), map[string]string{"name": "R", "template": "review"}, &wf)
	t.Setenv(fakeClaudeEnv, "loggedout") // someone signs Claude Code out on the server
	e.srv.probe.mu.Lock()
	e.srv.probe.at = time.Time{} // forget the cached answer
	e.srv.probe.mu.Unlock()
	r := &reviewEnv{env: e, f: f, admin: admin, wf: wf.ID}
	admin.mustCall(202, "POST", admin.path("/workflows/"+wf.ID+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, nil)
	run := r.waitRun()
	if run.Status != "failed" || !strings.Contains(run.Error, "Claude Code is not ready") || !strings.Contains(run.Error, "not signed in") {
		t.Fatalf("%+v", run)
	}
}

// ---------- the MCP endpoint itself ----------

func TestMCPEndpoint(t *testing.T) {
	e := newEnv(t, envOpts{claudeCode: true})
	tools := []ToolDef{{Name: "echo", Description: "Echo", InputSchema: map[string]any{"type": "object"}}}
	token, stop := e.srv.bridge.open(tools, func(_ context.Context, name string, in json.RawMessage) (string, error) {
		if strings.Contains(string(in), "boom") {
			return "", fmt.Errorf("it broke")
		}
		return "echo:" + string(in), nil
	})
	defer stop()
	url := e.ts.URL + "/internal/mcp/" + token
	post := func(body string) (int, map[string]any) {
		resp, err := http.Post(url, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); code != 200 || out["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %d %v", code, out)
	}
	if code, _ := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification: %d", code)
	}
	if _, out := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); len(out["result"].(map[string]any)["tools"].([]any)) != 1 {
		t.Fatalf("list: %v", out)
	}
	_, out := post(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`)
	res := out["result"].(map[string]any)
	if res["isError"] != false || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), `echo:{"a":1}`) {
		t.Fatalf("call: %v", out)
	}
	if _, out := post(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"x":"boom"}}}`); out["result"].(map[string]any)["isError"] != true {
		t.Fatalf("a failing tool must report isError: %v", out)
	}
	if _, out := post(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"other","arguments":{}}}`); out["error"] == nil {
		t.Fatalf("an unknown tool must be refused: %v", out)
	}
	if _, out := post(`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`); out["error"] == nil {
		t.Fatalf("unsupported method: %v", out)
	}
	for i := 0; i < maxToolCalls+2; i++ { // the cap on calls per run
		_, out = post(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	}
	if out["result"].(map[string]any)["isError"] != true {
		t.Fatalf("the call limit was not enforced: %v", out)
	}
	if resp, _ := http.Get(url); resp.StatusCode != 405 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	if resp, _ := http.Post(e.ts.URL+"/internal/mcp/not-a-token", "application/json", bytes.NewReader([]byte(`{}`))); resp.StatusCode != 404 {
		t.Fatalf("unknown token: %d", resp.StatusCode)
	}
	_ = io.Discard
}

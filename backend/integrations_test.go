package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var accounts = map[string]string{"github": "octo-admin", "gitlab": "gl-admin", "jira": "Acme Site", "figma": "fig-admin"}

// noRedirect makes the client stop at redirects so tests can read where the server sent the browser.
func noRedirect(c *client) {
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}

func (c *client) rawGet(path string) *http.Response {
	c.t.Helper()
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func location(resp *http.Response) string { return resp.Header.Get("Location") }

func (p *person) start(provider string) (state string) {
	p.t.Helper()
	var out struct {
		URL string `json:"url"`
	}
	p.mustCall(200, "POST", p.path("/integrations/"+provider+"/start"), map[string]string{}, &out)
	u := mustURL(out.URL)
	if u.Query().Get("client_id") != "cid" || u.Query().Get("redirect_uri") != "http://app.test/api/integrations/"+provider+"/callback" {
		p.t.Fatalf("bad authorize url %s", out.URL)
	}
	return u.Query().Get("state")
}

func (p *person) callback(provider, code, state string) string {
	return location(p.rawGet("/api/integrations/" + provider + "/callback?code=" + code + "&state=" + url.QueryEscape(state)))
}

func connect(t *testing.T, p *person, provider string) {
	t.Helper()
	noRedirect(p.client)
	loc := p.callback(provider, "good", p.start(provider))
	if !strings.HasPrefix(loc, "/?integration="+provider+"&result=connected") {
		t.Fatalf("%s callback went to %q", provider, loc)
	}
}

func (p *person) statuses() map[string]integrationStatus {
	p.t.Helper()
	var out map[string]integrationStatus
	p.mustCall(200, "GET", p.path("/integrations"), nil, &out)
	return out
}

func TestAuthorizeURLs(t *testing.T) {
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{providersURL: f.URL})
	p := e.signup("Ann", "ann@example.com")
	want := map[string][]string{
		"github": {"/github/login/oauth/authorize", "scope=public_repo+read%3Auser"},
		"gitlab": {"/gitlab/oauth/authorize", "response_type=code"},
		"jira":   {"/jira/authorize", "audience=api.atlassian.com", "prompt=consent", "response_type=code"},
		"figma":  {"/figma/oauth?", "response_type=code"},
	}
	for id, subs := range want {
		var out struct{ URL string }
		p.mustCall(200, "POST", p.path("/integrations/"+id+"/start"), map[string]string{}, &out)
		if !contains(out.URL, subs...) {
			t.Errorf("%s: %s lacks %v", id, out.URL, subs)
		}
	}
	p.mustCall(404, "POST", p.path("/integrations/slack/start"), map[string]string{}, nil)
}

func TestConnectEveryProvider(t *testing.T) {
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{providersURL: f.URL})
	admin := e.signup("Ann", "ann@example.com")
	for _, id := range providerOrder {
		connect(t, admin, id)
	}
	st := admin.statuses()
	for _, id := range providerOrder {
		if !st[id].Configured || !st[id].Connected || st[id].Account != accounts[id] || st[id].Scopes != "granted-"+id && id != "github" {
			t.Errorf("%s status %+v", id, st[id])
		}
		var enc []byte
		if err := e.db.QueryRow(`SELECT token_enc FROM integration_tokens WHERE workspace_id=$1 AND provider=$2`, admin.WS, id).Scan(&enc); err != nil {
			t.Fatal(err)
		}
		if len(enc) == 0 || bytes.Contains(enc, []byte("at-"+id)) {
			t.Errorf("%s token stored in the clear", id)
		}
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "at-") || strings.Contains(string(raw), "rt-") {
		t.Fatal("token leaked in status")
	}
	// Disconnect one; the others stay.
	var after map[string]integrationStatus
	admin.mustCall(200, "DELETE", admin.path("/integrations/gitlab"), nil, &after)
	if after["gitlab"].Connected || !after["github"].Connected || !after["jira"].Connected || !after["figma"].Connected {
		t.Fatalf("after disconnect %+v", after)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.count("gitlab", "/oauth/revoke") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.count("gitlab", "/oauth/revoke") != 1 {
		t.Error("gitlab grant was not revoked")
	}
	admin.mustCall(200, "DELETE", admin.path("/integrations/github"), nil, nil)
	deadline = time.Now().Add(2 * time.Second)
	for f.count("github", "/grant") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.count("github", "DELETE /github/api/applications/cid/grant") != 1 {
		t.Error("github grant was not revoked")
	}
}

func TestRolesAndIsolation(t *testing.T) {
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{providersURL: f.URL})
	admin := e.signup("Ann", "ann@example.com")
	member := e.signup("Mo", "mo@example.com")
	outsider := e.signup("Out", "out@example.com")
	e.join(admin, member, "member")
	for _, id := range providerOrder {
		member.mustCall(403, "POST", admin.path("/integrations/"+id+"/start"), map[string]string{}, nil)
		member.mustCall(403, "DELETE", admin.path("/integrations/"+id), nil, nil)
		outsider.mustCall(404, "POST", admin.path("/integrations/"+id+"/start"), map[string]string{}, nil)
		outsider.mustCall(404, "DELETE", admin.path("/integrations/"+id), nil, nil)
	}
	member.mustCall(200, "GET", admin.path("/integrations"), nil, nil)
	outsider.mustCall(404, "GET", admin.path("/integrations"), nil, nil)
	connect(t, admin, "jira")
	for id, s := range outsider.statuses() {
		if s.Connected {
			t.Errorf("%s leaked into another workspace", id)
		}
	}
}

func TestCallbackRejections(t *testing.T) {
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{providersURL: f.URL})
	admin := e.signup("Ann", "ann@example.com")
	other := e.signup("Eve", "eve@example.com")
	noRedirect(admin.client)
	noRedirect(other.client)
	reason := func(loc, want string) bool {
		return strings.Contains(loc, "result=error") && strings.Contains(loc, "reason="+want)
	}
	connected := func() bool { return admin.statuses()["github"].Connected }

	if loc := admin.callback("github", "good", "nonsense"); !reason(loc, "state") {
		t.Errorf("garbage state: %s", loc)
	}
	st := admin.start("github")
	if loc := admin.callback("github", "good", st[:len(st)-3]+"AAA"); !reason(loc, "state") {
		t.Errorf("tampered state: %s", loc)
	}
	exp := e.srv.signState("github", admin.WS, admin.User.ID, "n", time.Now().Add(-time.Minute))
	if loc := admin.callback("github", "good", exp); !reason(loc, "expired") {
		t.Errorf("expired: %s", loc)
	}
	admin.start("github") // a newer cookie nonce replaces the one st was made with
	if loc := admin.callback("github", "good", st); !reason(loc, "state") {
		t.Errorf("cookie mismatch: %s", loc)
	}
	// A state made for GitLab cannot be used at GitHub's callback, even in the same browser.
	glState := admin.start("gitlab")
	if loc := admin.callback("github", "good", glState); !reason(loc, "state") {
		t.Errorf("cross-provider state: %s", loc)
	}
	// Valid state replayed in another user's session.
	st2 := admin.start("github")
	other.start("github")
	if loc := other.callback("github", "good", st2); !reason(loc, "state") && !reason(loc, "session") {
		t.Errorf("other user: %s", loc)
	}
	anon := e.client()
	noRedirect(anon)
	if loc := location(anon.rawGet("/api/integrations/github/callback?code=good&state=" + url.QueryEscape(st2))); !strings.Contains(loc, "reason=") {
		t.Errorf("anonymous: %s", loc)
	}
	st3 := admin.start("github")
	if loc := location(admin.rawGet("/api/integrations/github/callback?error=access_denied&state=" + url.QueryEscape(st3))); !reason(loc, "denied") {
		t.Errorf("denied: %s", loc)
	}
	st4 := admin.start("github")
	if loc := admin.callback("github", "bad", st4); !reason(loc, "exchange") {
		t.Errorf("exchange: %s", loc)
	}
	f.mu.Lock()
	f.failToken["jira"] = true
	f.mu.Unlock()
	if loc := admin.callback("jira", "good", admin.start("jira")); !reason(loc, "exchange") {
		t.Errorf("jira exchange: %s", loc)
	}
	if connected() || admin.statuses()["jira"].Connected {
		t.Fatal("a rejected callback still connected something")
	}
	st5 := admin.start("github")
	if _, err := e.db.Exec(`UPDATE memberships SET role='member' WHERE workspace_id=$1 AND user_id=$2`, admin.WS, admin.User.ID); err != nil {
		t.Fatal(err)
	}
	if loc := admin.callback("github", "good", st5); !reason(loc, "forbidden") {
		t.Errorf("demoted: %s", loc)
	}
}

func TestNotConfigured(t *testing.T) {
	e := newEnv(t)
	admin := e.signup("Ann", "ann@example.com")
	for _, id := range providerOrder {
		admin.mustCall(409, "POST", admin.path("/integrations/"+id+"/start"), map[string]string{}, nil)
	}
	for id, s := range admin.statuses() {
		if s.Configured || s.Connected {
			t.Errorf("%s %+v", id, s)
		}
	}
}

// ---------- agent tools ----------

type scriptedAnthropic struct {
	*httptest.Server
	mu      sync.Mutex
	replies []map[string]any
	bodies  []map[string]any
}

func newScriptedAnthropic(t *testing.T, replies ...map[string]any) *scriptedAnthropic {
	f := &scriptedAnthropic{replies: replies}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		i := len(f.bodies) - 1
		rep := f.replies[len(f.replies)-1]
		if i < len(f.replies) {
			rep = f.replies[i]
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(rep)
	}))
	t.Cleanup(f.Close)
	return f
}

func toolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"stop_reason": "tool_use", "content": []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": input}}}
}

func finalText(s string) map[string]any {
	return map[string]any{"stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": s}}}
}

func (f *scriptedAnthropic) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.bodies...)
}

func (f *scriptedAnthropic) lastToolResults() []map[string]any {
	reqs := f.requests()
	msgs, _ := reqs[len(reqs)-1]["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	blocks, _ := last["content"].([]any)
	var out []map[string]any
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok && m["type"] == "tool_result" {
			out = append(out, m)
		}
	}
	return out
}

func (f *scriptedAnthropic) toolNames() []string {
	var names []string
	tools, _ := f.requests()[0]["tools"].([]any)
	for _, t := range tools {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	return names
}

func workflowWithTools(t *testing.T, p *person, tools []string) string {
	t.Helper()
	var wf Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"name": "Tools", "template": "example"}, &wf)
	for i := range wf.Steps {
		for j := range wf.Steps[i] {
			if wf.Steps[i][j].ID == "n2" {
				wf.Steps[i][j].Tools = tools
			}
		}
	}
	p.mustCall(200, "PUT", p.path("/workflows/"+wf.ID), wf, nil)
	return wf.ID
}

func runN2(p *person, id string) runResponse {
	p.t.Helper()
	var out runResponse
	p.mustCall(200, "POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n2"}, &out)
	return out
}

// Each provider: one real-looking call through the full path, checking the token used and the answer's content.
func TestEachProvidersTools(t *testing.T) {
	cases := []struct {
		provider, tool string
		input          map[string]any
		want           []string
	}{
		{"github", "github_get_repo", map[string]any{"owner": "acme", "repo": "app"}, []string{"acme/app", "Go"}},
		{"gitlab", "gitlab_get_project", map[string]any{"project": "acme/group/app"}, []string{"acme/group/app", "trunk"}},
		{"gitlab", "gitlab_list_issues", map[string]any{"project": "acme/group/app"}, []string{"Slow page", "perf"}},
		{"gitlab", "gitlab_read_file", map[string]any{"project": "acme/group/app", "path": "docs/guide.md"}, []string{"hey"}},
		{"jira", "jira_get_issue", map[string]any{"key": "ABC-1"}, []string{"Checkout fails", "Card payments\\ntime out", "Seen on Safari", "Dana"}},
		{"jira", "jira_search_issues", map[string]any{"jql": "project = ABC AND updated >= -7d"}, []string{"ABC-1", "In Progress"}},
		{"figma", "figma_get_file", map[string]any{"file_key": "FILEKEY12345"}, []string{"Design system", "Login", "1:2"}},
		{"figma", "figma_get_node_text", map[string]any{"file_key": "FILEKEY12345", "node_id": "1:2"}, []string{"Welcome back"}},
		{"figma", "figma_get_comments", map[string]any{"file_key": "FILEKEY12345"}, []string{"Make the button bigger", "pm"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			f := newFakeProviders(t)
			an := newScriptedAnthropic(t, toolUse("t1", c.tool, c.input), finalText("ok"))
			e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
			p := e.signup("Ann", "ann@example.com")
			connect(t, p, c.provider)
			label := providers[c.provider].Label
			out := runN2(p, workflowWithTools(t, p, []string{label}))
			if out.Text != "ok" || len(out.Used) != 1 || out.Used[0] != c.tool {
				t.Fatalf("%+v", out)
			}
			res := an.lastToolResults()
			if len(res) != 1 || res[0]["is_error"] == true {
				t.Fatalf("tool result %v", res)
			}
			body := res[0]["content"].(string)
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("result lacks %q: %s", w, body)
				}
			}
			// The call carried this workspace's token.
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, q := range f.reqs {
				if q.Provider == c.provider && strings.Contains(q.URI, providerMarker(c.provider)) && q.Auth != "Bearer at-"+c.provider+"-1" {
					t.Errorf("%s used %q", q.URI, q.Auth)
				}
			}
		})
	}
}

func providerMarker(p string) string {
	return map[string]string{"github": "/repos/", "gitlab": "/projects/", "jira": "/rest/api/", "figma": "/files/"}[p]
}

func TestOnlyListedConnectedProvidersGetTools(t *testing.T) {
	f := newFakeProviders(t)
	an := newScriptedAnthropic(t, finalText("plain"))
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	p := e.signup("Ann", "ann@example.com")

	// Listed but not connected: no tools, and the prompt does not promise any.
	out := runN2(p, workflowWithTools(t, p, []string{"GitHub", "Jira"}))
	if len(out.Used) != 0 {
		t.Fatal(out.Used)
	}
	if _, has := an.requests()[0]["tools"]; has {
		t.Error("tools offered while nothing is connected")
	}
	if sys, _ := an.requests()[0]["system"].(string); strings.Contains(sys, "provided tools") {
		t.Error("prompt promises tools that were not offered")
	}

	// Connected GitHub and Figma; the step lists GitHub and Jira: only GitHub's seven tools.
	connect(t, p, "github")
	connect(t, p, "figma")
	runN2(p, workflowWithTools(t, p, []string{"GitHub", "Jira"}))
	names := an.toolNames2()
	if len(names) != 8 || !strings.HasPrefix(names[0], "github_") {
		t.Errorf("tools %v", names)
	}
	// Listing several connected providers offers all of them.
	runN2(p, workflowWithTools(t, p, []string{"GitHub", "Figma", "Docs"}))
	reqs := an.requests()
	tools, _ := reqs[len(reqs)-1]["tools"].([]any)
	if len(tools) != 8+3 {
		t.Errorf("%d tools", len(tools))
	}
	if sys, _ := reqs[len(reqs)-1]["system"].(string); !strings.Contains(sys, "GitHub, Figma") {
		t.Errorf("prompt: %s", sys)
	}
	// Connected but not listed on the step: nothing.
	runN2(p, workflowWithTools(t, p, []string{"Docs"}))
	reqs = an.requests()
	if _, has := reqs[len(reqs)-1]["tools"]; has {
		t.Error("tools offered to a step that does not list them")
	}
}

// toolNames2 returns the tool names of the most recent request.
func (f *scriptedAnthropic) toolNames2() []string {
	reqs := f.requests()
	var names []string
	tools, _ := reqs[len(reqs)-1]["tools"].([]any)
	for _, t := range tools {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	return names
}

func TestToolInputsAreValidated(t *testing.T) {
	f := newFakeProviders(t)
	bad := func(id, name string, in map[string]any) map[string]any {
		return map[string]any{"type": "tool_use", "id": id, "name": name, "input": in}
	}
	an := newScriptedAnthropic(t,
		map[string]any{"stop_reason": "tool_use", "content": []map[string]any{
			bad("a", "github_get_repo", map[string]any{"owner": "..", "repo": "app"}),
			bad("b", "github_read_file", map[string]any{"owner": "acme", "repo": "app", "path": "../../etc/passwd"}),
			bad("c", "gitlab_get_project", map[string]any{"project": "acme/../admin"}),
			bad("d", "gitlab_get_project", map[string]any{"project": "acme/app?x=1"}),
			bad("e", "gitlab_read_file", map[string]any{"project": "acme/group/app", "path": "a/../b"}),
			bad("f", "jira_get_issue", map[string]any{"key": "ABC-1/../../x"}),
			bad("g", "jira_get_issue", map[string]any{"key": "abc-1"}),
			bad("h", "jira_search_issues", map[string]any{"jql": strings.Repeat("a", 501)}),
			bad("i", "figma_get_file", map[string]any{"file_key": "../me"}),
			bad("j", "figma_get_node_text", map[string]any{"file_key": "FILEKEY12345", "node_id": "1:2&depth=99"}),
			bad("k", "figma_get_comments", map[string]any{"file_key": "abc"}),
			bad("l", "github_get_repo", map[string]any{"owner": "acme", "repo": "app"}),
			bad("m", "delete_everything", map[string]any{}),
			bad("n", "jira_get_issue", map[string]any{"key": "ABC-1"}), // jira is not connected here
		}},
		finalText("done"),
	)
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	p := e.signup("Ann", "ann@example.com")
	for _, id := range []string{"github", "gitlab", "figma"} {
		connect(t, p, id)
	}
	runN2(p, workflowWithTools(t, p, []string{"GitHub", "GitLab", "Figma", "Jira"}))
	res := an.lastToolResults()
	if len(res) != 14 {
		t.Fatalf("%d results", len(res))
	}
	for i, r := range res {
		wantErr := i != 11
		if (r["is_error"] == true) != wantErr {
			t.Errorf("call %d: is_error=%v, content %v", i, r["is_error"], r["content"])
		}
	}
	for _, id := range []string{"gitlab", "figma", "jira"} {
		if n := f.apiCalls(id); n != 0 {
			t.Errorf("invalid input reached %s (%d calls)", id, n)
		}
	}
	if n := f.apiCalls("github"); n != 1 {
		t.Errorf("github calls %d, want 1", n)
	}
}

func TestToolLoopIsBounded(t *testing.T) {
	f := newFakeProviders(t)
	an := newScriptedAnthropic(t, toolUse("x", "github_get_repo", map[string]any{"owner": "acme", "repo": "app"}))
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	p := e.signup("Ann", "ann@example.com")
	connect(t, p, "github")
	id := workflowWithTools(t, p, []string{"GitHub"})
	p.call("POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n2"}, nil)
	if n := len(an.requests()); n > maxToolTurns+1 {
		t.Errorf("%d model calls, cap is %d", n, maxToolTurns)
	}
	if n := f.apiCalls("github"); n > maxToolCalls {
		t.Errorf("%d provider calls, cap is %d", n, maxToolCalls)
	}
}

func TestOtherWorkspaceCannotUseTheToken(t *testing.T) {
	f := newFakeProviders(t)
	an := newScriptedAnthropic(t, finalText("plain"))
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	a := e.signup("Ann", "ann@example.com")
	b := e.signup("Bea", "bea@example.com")
	connect(t, a, "figma")
	runN2(b, workflowWithTools(t, b, []string{"Figma"}))
	if _, has := an.requests()[0]["tools"]; has {
		t.Error("workspace B was given workspace A's access")
	}
}

// ---------- expiring tokens ----------

// expire rewrites the stored expiry of a provider's token to the past.
func expire(t *testing.T, e *env, ws, provider string) {
	t.Helper()
	p := providers[provider]
	_, enc, err := e.repo.Integration(t.Context(), ws, provider)
	if err != nil || enc == nil {
		t.Fatal("not connected", err)
	}
	ts, err := e.srv.openTokens(enc, ws, p)
	if err != nil {
		t.Fatal(err)
	}
	ts.Expiry = time.Now().Add(-time.Minute).Unix()
	raw, _ := json.Marshal(ts)
	if _, err := e.db.Exec(`UPDATE integration_tokens SET token_enc=$3 WHERE workspace_id=$1 AND provider=$2`, ws, provider, e.srv.sealer.Seal(string(raw), ws+"|"+provider)); err != nil {
		t.Fatal(err)
	}
}

func stored(t *testing.T, e *env, ws, provider string) tokenSet {
	t.Helper()
	_, enc, _ := e.repo.Integration(t.Context(), ws, provider)
	ts, err := e.srv.openTokens(enc, ws, providers[provider])
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestExpiredTokensAreRefreshedAndRotated(t *testing.T) {
	for _, id := range []string{"gitlab", "jira", "figma"} {
		t.Run(id, func(t *testing.T) {
			f := newFakeProviders(t)
			e := newEnv(t, envOpts{providersURL: f.URL})
			p := e.signup("Ann", "ann@example.com")
			connect(t, p, id)
			first := stored(t, e, p.WS, id)
			if first.Refresh == "" || first.Expiry == 0 {
				t.Fatalf("no refresh token or expiry stored: %+v", first)
			}
			expire(t, e, p.WS, id)
			ts, err := e.srv.tokensFor(t.Context(), p.WS, providers[id])
			if err != nil {
				t.Fatal(err)
			}
			if ts.Access == first.Access || ts.Access != f.accessToken(id) {
				t.Errorf("not refreshed: %q vs first %q", ts.Access, first.Access)
			}
			if ts.expiring() {
				t.Error("still expiring after refresh")
			}
			saved := stored(t, e, p.WS, id)
			if saved.Access != ts.Access {
				t.Error("refreshed token not saved")
			}
			if id == "figma" && saved.Refresh != first.Refresh {
				t.Error("figma keeps its refresh token")
			}
			if id != "figma" && saved.Refresh == first.Refresh {
				t.Error("the rotated refresh token was not stored")
			}
			if id == "jira" && saved.Extra["cloud"] != "cloud-123" {
				t.Errorf("provider specifics lost on refresh: %v", saved.Extra)
			}
			// A second expiry works again with the rotated token.
			expire(t, e, p.WS, id)
			if _, err := e.srv.tokensFor(t.Context(), p.WS, providers[id]); err != nil {
				t.Fatalf("second refresh: %v", err)
			}
		})
	}
}

func TestConcurrentRefreshHappensOnce(t *testing.T) {
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{providersURL: f.URL})
	p := e.signup("Ann", "ann@example.com")
	connect(t, p, "jira")
	expire(t, e, p.WS, "jira")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.srv.tokensFor(t.Context(), p.WS, providers["jira"])
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent refresh: %v", err)
		}
	}
	if n := f.count("jira", "POST /jira/oauth/token"); n != 2 { // one sign-in, one refresh
		t.Errorf("%d token requests, want 2 (sign-in plus exactly one refresh)", n)
	}
}

func TestFailedRefreshAsksToReconnect(t *testing.T) {
	f := newFakeProviders(t)
	an := newScriptedAnthropic(t, toolUse("t", "gitlab_get_project", map[string]any{"project": "acme/group/app"}), finalText("could not read"))
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	p := e.signup("Ann", "ann@example.com")
	connect(t, p, "gitlab")
	expire(t, e, p.WS, "gitlab")
	f.mu.Lock()
	f.failRefresh = true
	f.mu.Unlock()
	out := runN2(p, workflowWithTools(t, p, []string{"GitLab"}))
	if out.Text != "could not read" {
		t.Fatalf("the step should still finish: %+v", out)
	}
	res := an.lastToolResults()
	if len(res) != 1 || res[0]["is_error"] != true || !strings.Contains(res[0]["content"].(string), "reconnect") {
		t.Errorf("tool result %v", res)
	}
	if strings.Contains(res[0]["content"].(string), "at-gitlab") || strings.Contains(res[0]["content"].(string), "rt-gitlab") {
		t.Error("token text in tool error")
	}
}

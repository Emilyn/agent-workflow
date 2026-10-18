package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConnectKeyStoresItEncryptedAndNeverReturnsIt(t *testing.T) {
	const key = "sk-ant-api03-SECRETSECRETSECRET1234"
	api := newFakeAnthropic(t, "ok", key)
	e := newEnv(t, envOpts{anthropicURL: api.URL})
	p := e.signup("Amara", "amara@example.com")

	var st claudeStatus
	p.mustCall(200, "GET", p.path("/claude"), nil, &st)
	if st.Connected {
		t.Fatal("connected before a key was added")
	}

	p.mustCall(200, "PUT", p.path("/claude/key"), map[string]string{"apiKey": "  " + key + " "}, &st)
	if !st.Connected || st.Source != "workspace" || st.KeyHint != "…1234" {
		t.Fatalf("status: %+v", st)
	}

	// The database holds ciphertext only, bound to this workspace.
	var enc []byte
	var hint string
	if err := e.db.QueryRow(`SELECT anthropic_key_enc, key_hint FROM workspace_secrets WHERE workspace_id = $1`, p.WS).Scan(&enc, &hint); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("SECRETSECRET")) {
		t.Fatal("the key is stored in plain text")
	}
	if got, err := e.srv.sealer.Open(enc, p.WS); err != nil || got != key {
		t.Errorf("cannot decrypt with the right workspace: %q %v", got, err)
	}
	if _, err := e.srv.sealer.Open(enc, "s-other"); err == nil {
		t.Error("the stored key opens for a different workspace")
	}

	// Nothing the API returns contains the key.
	for _, path := range []string{"/claude", "/settings", "/members", "/workflows", "/invites"} {
		var raw map[string]any
		_ = raw
		resp, err := p.http.Get(p.base + p.path(path))
		if err != nil {
			t.Fatal(err)
		}
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		resp.Body.Close()
		if strings.Contains(buf.String(), "SECRETSECRET") {
			t.Errorf("GET %s leaks the key", path)
		}
	}

	p.mustCall(200, "DELETE", p.path("/claude/key"), nil, &st)
	if st.Connected {
		t.Error("still connected after disconnect")
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM workspace_secrets`).Scan(&n)
	if n != 0 {
		t.Error("the encrypted key was not deleted")
	}
}

func TestConnectKeyRejections(t *testing.T) {
	const good = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa"
	api := newFakeAnthropic(t, "ok", good)
	e := newEnv(t, envOpts{anthropicURL: api.URL})
	p := e.signup("Amara", "amara@example.com")
	for name, k := range map[string]string{
		"subscription token": "sk-ant-oat01-aaaaaaaaaaaaaaaaaaaaaaaa",
		"wrong format":       "hello",
		"too short":          "sk-ant-x",
		"whitespace inside":  "sk-ant-api03-aaaa aaaaaaaaaaaaaaaa",
	} {
		if got := p.call("PUT", p.path("/claude/key"), map[string]string{"apiKey": k}, nil); got != 400 {
			t.Errorf("%s: %d", name, got)
		}
	}
	var er map[string]string
	p.mustCall(400, "PUT", p.path("/claude/key"), map[string]string{"apiKey": "sk-ant-api03-bbbbbbbbbbbbbbbbbbbb"}, &er)
	if !strings.Contains(er["error"], "rejected") {
		t.Errorf("a key Anthropic rejects: %v", er)
	}
	var st claudeStatus
	p.mustCall(200, "GET", p.path("/claude"), nil, &st)
	if st.Connected {
		t.Error("a rejected key was stored")
	}
}

func TestEachWorkspaceUsesItsOwnKeyAndFallsBackToTheServerKey(t *testing.T) {
	const ka, kb, ks = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa", "sk-ant-api03-bbbbbbbbbbbbbbbbbbbb", "sk-ant-api03-serverserverserverser"
	api := newFakeAnthropic(t, "ok", ka, kb, ks)
	e := newEnv(t, envOpts{anthropicURL: api.URL, serverKey: ks})
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	c := e.signup("Cleo", "cleo@example.com")
	a.mustCall(200, "PUT", a.path("/claude/key"), map[string]string{"apiKey": ka}, nil)
	b.mustCall(200, "PUT", b.path("/claude/key"), map[string]string{"apiKey": kb}, nil)

	for _, tc := range []struct {
		p    *person
		want string
		src  string
	}{{a, ka, "workspace"}, {b, kb, "workspace"}, {c, ks, "server"}} {
		var st claudeStatus
		tc.p.mustCall(200, "GET", tc.p.path("/claude"), nil, &st)
		if !st.Connected || st.Source != tc.src {
			t.Errorf("%s status %+v", tc.p.User.Name, st)
		}
		tc.p.mustCall(200, "POST", tc.p.path("/workflows/"+tc.p.firstWorkflow().ID+"/run/step"), map[string]string{"stepId": "n2"}, nil)
		if _, used := api.sent(); used != tc.want {
			t.Errorf("%s's run used %q, want %q", tc.p.User.Name, used, tc.want)
		}
	}
	var st claudeStatus
	c.mustCall(200, "GET", c.path("/claude"), nil, &st)
	if st.KeyHint != "" {
		t.Errorf("the server key's hint is exposed: %q", st.KeyHint)
	}
}

func TestClaudeCallsAreRateLimitedPerWorkspace(t *testing.T) {
	const key = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa"
	api := newFakeAnthropic(t, "ok", key)
	e := newEnv(t, envOpts{anthropicURL: api.URL})
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	a.mustCall(200, "PUT", a.path("/claude/key"), map[string]string{"apiKey": key}, nil)
	b.mustCall(200, "PUT", b.path("/claude/key"), map[string]string{"apiKey": key}, nil)
	id := a.firstWorkflow().ID

	limited := 0
	for i := 0; i < 70; i++ {
		if a.call("POST", a.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n2"}, nil) == 429 {
			limited++
		}
	}
	if limited != 10 {
		t.Errorf("expected the last 10 of 70 calls to be refused, %d were", limited)
	}
	// Another workspace is unaffected.
	b.mustCall(200, "POST", b.path("/workflows/"+b.firstWorkflow().ID+"/run/step"), map[string]string{"stepId": "n2"}, nil)
}

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOAuthMCP is an MCP server that wants OAuth, with its own authorization server on the same host.
type fakeOAuthMCP struct {
	*httptest.Server
	t        *testing.T
	mu       sync.Mutex
	callback string // the redirect address the app must register
	expires  int    // seconds, in token answers
	needSec  string // when set, the token endpoint demands this client secret
	badIss   bool

	seq        int
	clients    map[string]string // client id -> redirect uri
	codes      map[string]fakeCode
	access     map[string]time.Time
	refresh    map[string]string // refresh token -> client id
	regs       int
	refs       int
	calls      int
	lastBearer string
}

type fakeCode struct{ challenge, redirect, client, resource string }

func newFakeOAuthMCP(t *testing.T, callback string) *fakeOAuthMCP {
	f := &fakeOAuthMCP{t: t, callback: callback, expires: 3600, clients: map[string]string{}, codes: map[string]fakeCode{}, access: map[string]time.Time{}, refresh: map[string]string{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	base := f.URL
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		iss := base
		if f.badIss {
			iss = "https://elsewhere.example"
		}
		json.NewEncoder(w).Encode(map[string]any{"issuer": iss, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
			"registration_endpoint": base + "/register", "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"}})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Redirects []string `json:"redirect_uris"`
			Method    string   `json:"token_endpoint_auth_method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(req.Redirects) != 1 || req.Redirects[0] != f.callback || req.Method != "none" {
			w.WriteHeader(400)
			return
		}
		f.regs++
		id := fmt.Sprintf("client-%d", f.regs)
		f.clients[id] = req.Redirects[0]
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"client_id": id})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		bad := func() { w.WriteHeader(400); json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"}) }
		if f.needSec != "" && r.PostForm.Get("client_secret") != f.needSec {
			w.WriteHeader(401)
			return
		}
		client := r.PostForm.Get("client_id")
		issue := func() {
			f.seq++
			acc, ref := fmt.Sprintf("access-%d", f.seq), fmt.Sprintf("refresh-%d", f.seq)
			f.access[acc] = time.Now().Add(time.Duration(f.expires) * time.Second)
			f.refresh[ref] = client
			json.NewEncoder(w).Encode(map[string]any{"access_token": acc, "refresh_token": ref, "token_type": "Bearer", "expires_in": f.expires})
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			c, ok := f.codes[r.PostForm.Get("code")]
			delete(f.codes, r.PostForm.Get("code"))
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if !ok || c.client != client || c.redirect != r.PostForm.Get("redirect_uri") || c.challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || r.PostForm.Get("resource") != c.resource {
				bad()
				return
			}
			issue()
		case "refresh_token":
			rt := r.PostForm.Get("refresh_token")
			if f.refresh[rt] != client || client == "" {
				bad()
				return
			}
			delete(f.refresh, rt) // rotated: the old one stops working
			f.refs++
			issue()
		default:
			w.WriteHeader(400)
		}
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.lastBearer = tok
		if exp, ok := f.access[tok]; !ok || time.Now().After(exp) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(401)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var res any
		switch req.Method {
		case "initialize":
			res = map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "Notion-like"}}
		case "tools/list":
			res = map[string]any{"tools": []map[string]any{{"name": "search", "description": "Search pages", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}}}
		case "tools/call":
			f.calls++
			res = map[string]any{"content": []map[string]any{{"type": "text", "text": "3 pages found"}}}
		default:
			res = map[string]any{}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": res})
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	t.Cleanup(f.Close)
	return f
}

// authorize plays the part of the admin's browser at the consent page: it checks what the app asked for and
// returns the address the service would send the browser back to.
func (f *fakeOAuthMCP) authorize(authURL, wantResource string) string {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, f.URL+"/authorize?") {
		f.t.Fatalf("authorize address %q", authURL)
	}
	q := u.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("redirect_uri") != f.callback {
		f.t.Fatalf("bad authorize query %v", q)
	}
	if _, ok := f.clients[q.Get("client_id")]; !ok && f.needSec == "" {
		f.t.Fatalf("unknown client %q", q.Get("client_id"))
	}
	if q.Get("resource") != wantResource {
		f.t.Fatalf("resource %q, want %q", q.Get("resource"), wantResource)
	}
	f.seq++
	code := fmt.Sprintf("code-%d", f.seq)
	f.codes[code] = fakeCode{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), client: q.Get("client_id"), resource: q.Get("resource")}
	return fmt.Sprintf("%s?code=%s&state=%s", oauthCallback, code, url.QueryEscape(q.Get("state")))
}

func (f *fakeOAuthMCP) revoke() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access = map[string]time.Time{}
	f.refresh = map[string]string{}
}

func (f *fakeOAuthMCP) dropAccess() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access = map[string]time.Time{}
}

func oauthEnv(t *testing.T, opt ...envOpts) (*env, *person, *fakeOAuthMCP) {
	e := newEnv(t, opt...)
	f := newFakeOAuthMCP(t, e.srv.callbackAddress())
	admin := e.signup("Ann", "ann@example.com")
	return e, admin, f
}

func allowPrivate(e *env) {
	e.srv.cfg.AllowPrivateConnectors = true
	e.srv.mcpGuard = guard{allowPrivate: true, anyPort: true}
}

// signIn adds the connector and completes the sign-in the way a browser would.
func signIn(t *testing.T, e *env, admin *person, f *fakeOAuthMCP, body map[string]any) connectorsView {
	t.Helper()
	noRedirect(admin.client)
	var v connectorsView
	admin.mustCall(200, "POST", admin.path("/connectors/mcp"), body, &v)
	if v.AuthorizeURL == "" {
		t.Fatalf("no sign-in address: %+v", v)
	}
	back := f.authorize(v.AuthorizeURL, f.URL+"/mcp")
	loc := location(admin.rawGet(back))
	if !strings.HasPrefix(loc, "/?connector=") || !strings.Contains(loc, "result=connected") {
		t.Fatalf("callback went to %q", loc)
	}
	var out connectorsView
	admin.mustCall(200, "GET", admin.path("/connectors"), nil, &out)
	return out
}

func TestMCPOAuthFullFlow(t *testing.T) {
	an := newScriptedAnthropic(t, toolUse("t1", "mcp_notion_search", map[string]any{}), finalText("done"))
	e, admin, f := oauthEnv(t, envOpts{anthropicURL: an.URL, serverKey: "k"})

	// the real configuration refuses to look at a loopback server
	var out map[string]string
	admin.mustCall(400, "POST", admin.path("/connectors/mcp"), map[string]any{"name": "Notion", "url": f.URL + "/mcp", "oauth": true}, &out)
	if !strings.Contains(out["error"], "https") {
		t.Errorf("strict config said: %v", out["error"])
	}
	e.srv.cfg.AllowPrivateConnectors = true // plain http now, but still the guard against private addresses
	admin.mustCall(502, "POST", admin.path("/connectors/mcp"), map[string]any{"name": "Notion", "url": f.URL + "/mcp", "oauth": true}, &out)
	if !strings.Contains(out["error"], "not allowed") {
		t.Errorf("the guard said: %v", out["error"])
	}
	allowPrivate(e)
	v := signIn(t, e, admin, f, map[string]any{"name": "Notion", "url": f.URL + "/mcp", "oauth": true})
	if len(v.MCP) != 1 || v.MCP[0].Auth != "oauth" || !v.MCP[0].SignedIn || len(v.MCP[0].Tools) != 1 || v.MCP[0].ServerName != "Notion-like" {
		t.Fatalf("%+v", v.MCP)
	}
	if f.regs != 1 {
		t.Fatalf("registered %d times", f.regs)
	}
	raw, _ := json.Marshal(v)
	var enc []byte
	if err := e.db.QueryRow(`SELECT oauth_enc FROM mcp_connectors WHERE id = $1`, v.MCP[0].ID).Scan(&enc); err != nil || len(enc) == 0 || strings.Contains(string(enc), "access-") || strings.Contains(string(raw), "access-") || strings.Contains(string(raw), "refresh-") {
		t.Fatalf("tokens stored or returned in the clear: %v", err)
	}

	admin.mustCall(200, "PUT", admin.path("/connectors/mcp/"+v.MCP[0].ID+"/tools"), map[string][]string{"enabled": {"search"}}, nil)
	res := runN2(admin, workflowWithTools(t, admin, []string{"Notion"}))
	if res.Text != "done" || f.calls != 1 || !strings.HasPrefix(f.lastBearer, "access-") {
		t.Fatalf("%+v calls %d bearer %q", res, f.calls, f.lastBearer)
	}
}

func TestMCPOAuthRefreshesTokens(t *testing.T) {
	e, admin, f := oauthEnv(t)
	allowPrivate(e)
	f.expires = 30 // inside the safety margin: every use refreshes first
	v := signIn(t, e, admin, f, map[string]any{"name": "Notion", "url": f.URL + "/mcp", "oauth": true})
	conn, err := e.repo.GetConnector(admin.client.t.Context(), admin.WS, v.MCP[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := e.srv.openOAuth(conn, admin.WS)
	if f.refs < 1 {
		t.Fatalf("no proactive refresh, refreshes=%d", f.refs)
	}
	// concurrent users of the connector refresh once, and the rotated token is kept
	f.expires = 3600
	tok, err := e.srv.mcpAccessToken(admin.client.t.Context(), admin.WS, conn, true)
	if err != nil || tok == first.Access {
		t.Fatalf("%v %q", err, tok)
	}
	conn, _ = e.repo.GetConnector(admin.client.t.Context(), admin.WS, v.MCP[0].ID)
	if sec, _ := e.srv.openOAuth(conn, admin.WS); sec.Access != tok || sec.Refresh == first.Refresh {
		t.Fatalf("the rotated tokens were not stored: %+v", sec)
	}

	// the server drops the access token early: the call refreshes once and retries
	f.dropAccess()
	cl, err := e.srv.connectorClient(admin.client.t.Context(), conn, admin.WS)
	if err != nil {
		t.Fatal(err)
	}
	f.dropAccess() // again, after the client picked up its token
	if _, _, err := cl.ListTools(admin.client.t.Context()); err != nil {
		t.Fatalf("the retry after a refused token failed: %v", err)
	}

	// everything revoked: asking for a new sign-in is the only way forward, and it says so
	f.revoke()
	conn, _ = e.repo.GetConnector(admin.client.t.Context(), admin.WS, v.MCP[0].ID)
	cl, err = e.srv.connectorClient(admin.client.t.Context(), conn, admin.WS)
	if err == nil {
		_, _, err = cl.ListTools(admin.client.t.Context())
	}
	if err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("a revoked sign-in: %v", err)
	}
}

func TestMCPOAuthCallbackChecks(t *testing.T) {
	e, admin, f := oauthEnv(t)
	allowPrivate(e)
	noRedirect(admin.client)
	var v connectorsView
	admin.mustCall(200, "POST", admin.path("/connectors/mcp"), map[string]any{"name": "Notion", "url": f.URL + "/mcp", "oauth": true}, &v)
	back := f.authorize(v.AuthorizeURL, f.URL+"/mcp")
	u, _ := url.Parse(back)
	state := u.Query().Get("state")
	reason := func(path string) string {
		loc := location(admin.rawGet(path))
		if i := strings.Index(loc, "reason="); i >= 0 {
			return strings.SplitN(loc[i+7:], "#", 2)[0]
		}
		return loc
	}
	if got := reason(oauthCallback + "?code=x&state=garbage"); got != "state" {
		t.Errorf("forged state: %q", got)
	}
	if got := reason(oauthCallback + "?error=access_denied&state=" + url.QueryEscape(state)); got != "denied" {
		t.Errorf("denied: %q", got)
	}
	// the denial above consumed the browser's cookie but not the pending sign-in: start over properly
	admin.mustCall(200, "POST", admin.path("/connectors/mcp/"+v.MCP[0].ID+"/signin"), map[string]string{}, &v)
	back = f.authorize(v.AuthorizeURL, f.URL+"/mcp")
	if loc := location(admin.rawGet(back)); !strings.Contains(loc, "result=connected") {
		t.Fatalf("%q", loc)
	}
	// replaying the same return address does nothing
	if got := reason(back); got == "" || strings.Contains(got, "connected") {
		t.Errorf("a replay was accepted: %q", got)
	}
	// someone else's browser (no cookie) cannot finish a sign-in
	other := e.signup("Zed", "zed@example.com")
	noRedirect(other.client)
	if loc := location(other.rawGet(back)); !strings.Contains(loc, "reason=") {
		t.Errorf("another user finished it: %q", loc)
	}
}

func TestMCPOAuthDiscoveryAndClientChoices(t *testing.T) {
	e, admin, f := oauthEnv(t)
	allowPrivate(e)
	var out map[string]string

	// a server with no OAuth at all
	plain := newFakeMCP(t)
	admin.mustCall(502, "POST", admin.path("/connectors/mcp"), map[string]any{"name": "Plain", "url": plain.URL, "oauth": true}, &out)
	if !strings.Contains(out["error"], "does not offer OAuth") {
		t.Errorf("%v", out)
	}
	// metadata that names a different issuer is not trusted
	f.badIss = true
	admin.mustCall(502, "POST", admin.path("/connectors/mcp"), map[string]any{"name": "Spoof", "url": f.URL + "/mcp", "oauth": true}, &out)
	if !strings.Contains(out["error"], "does not match") {
		t.Errorf("%v", out)
	}
	f.badIss = false
	// bad scopes and client values are refused before anything is contacted
	for _, bad := range []map[string]any{
		{"name": "S", "url": f.URL + "/mcp", "oauth": true, "scopes": "read; rm"},
		{"name": "S", "url": f.URL + "/mcp", "oauth": true, "clientId": "has space"},
	} {
		admin.mustCall(400, "POST", admin.path("/connectors/mcp"), bad, nil)
	}
	if f.regs != 0 {
		t.Fatalf("registered while only validating: %d", f.regs)
	}
	// a client the admin typed in: no automatic registration, and the secret is sent to the token endpoint
	f.needSec = "s3cret"
	v := signIn(t, e, admin, f, map[string]any{"name": "Manual", "url": f.URL + "/mcp", "oauth": true, "clientId": "my-client", "clientSecret": "s3cret", "scopes": "read  write"})
	if f.regs != 0 || !v.MCP[0].SignedIn {
		t.Fatalf("regs %d, %+v", f.regs, v.MCP)
	}
	conn, _ := e.repo.GetConnector(admin.client.t.Context(), admin.WS, v.MCP[0].ID)
	if conn.OAuth.Scopes != "read write" || conn.OAuth.ClientID != "my-client" || conn.OAuth.AuthMethod != "client_secret_post" {
		t.Errorf("%+v", conn.OAuth)
	}
}

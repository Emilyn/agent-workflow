package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestSignupCreatesAccountWorkspaceAndExample(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/auth/signup", strings.NewReader(`{"name":"Amara","email":"Amara@Example.com","password":"correct horse battery"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge <= 0 {
		t.Fatalf("session cookie missing or weak: %+v", cookie)
	}

	var me meResponse
	c.mustCall(200, "GET", "/api/me", nil, &me)
	if me.User.Name != "Amara" || len(me.Workspaces) != 1 || me.Workspaces[0].Role != "owner" || !strings.Contains(me.Workspaces[0].Name, "Amara") {
		t.Fatalf("me: %+v", me)
	}
	p := &person{client: c, User: me.User, WS: me.Workspaces[0].ID}
	list := []Summary{}
	p.mustCall(200, "GET", p.path("/workflows"), nil, &list)
	if len(list) != 1 || list[0].Name != "Weekly customer digest" {
		t.Fatalf("starter workflow missing: %+v", list)
	}
	// Only a hash of the token may be stored.
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM sessions WHERE token_hash = $1`, []byte(cookie.Value)).Scan(&n)
	if n != 0 {
		t.Error("the raw session token is stored")
	}
	var stored string
	_ = e.db.QueryRow(`SELECT password_hash FROM users`).Scan(&stored)
	if strings.Contains(stored, "correct horse") || !strings.HasPrefix(stored, "pbkdf2-sha256$") {
		t.Errorf("password not hashed: %q", stored)
	}
}

func TestSignupValidation(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	good := map[string]string{"name": "Amara", "email": "a@example.com", "password": "correct horse battery"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, body := range map[string]map[string]string{
		"bad email":      with("email", "not-an-email"),
		"display name":   with("email", "Amara <a@example.com>"),
		"short password": with("password", "short"),
		"no name":        with("name", "  "),
		"long name":      with("name", strings.Repeat("a", 81)),
	} {
		if got := c.call("POST", "/api/auth/signup", body, nil); got != 400 {
			t.Errorf("%s: status %d", name, got)
		}
	}
	c.mustCall(200, "POST", "/api/auth/signup", good, nil)
	other := e.client()
	if got := other.call("POST", "/api/auth/signup", with("email", "A@EXAMPLE.COM"), nil); got != 409 {
		t.Errorf("duplicate email (different case): status %d", got)
	}
}

func TestSignupCanBeClosed(t *testing.T) {
	off := false
	e := newEnv(t, envOpts{allowSignup: &off})
	if got := e.client().call("POST", "/api/auth/signup", map[string]string{"name": "A", "email": "a@example.com", "password": "correct horse battery"}, nil); got != 403 {
		t.Errorf("status %d", got)
	}
}

func TestLoginLogout(t *testing.T) {
	e := newEnv(t)
	e.signup("Amara", "amara@example.com")

	c := e.client()
	if got := c.call("GET", "/api/me", nil, nil); got != 401 {
		t.Errorf("anonymous /me: %d", got)
	}
	var wrongPw, unknown map[string]string
	if got := c.call("POST", "/api/auth/login", map[string]string{"email": "amara@example.com", "password": "wrong password!"}, &wrongPw); got != 401 {
		t.Errorf("wrong password: %d", got)
	}
	if got := c.call("POST", "/api/auth/login", map[string]string{"email": "nobody@example.com", "password": "wrong password!"}, &unknown); got != 401 {
		t.Errorf("unknown email: %d", got)
	}
	if wrongPw["error"] != unknown["error"] {
		t.Errorf("the messages differ, which reveals which emails exist: %q vs %q", wrongPw["error"], unknown["error"])
	}
	c.mustCall(200, "POST", "/api/auth/login", map[string]string{"email": "AMARA@example.com", "password": "correct horse battery"}, nil)
	c.mustCall(200, "GET", "/api/me", nil, nil)

	// Logging out ends that session on the server, not just in the browser.
	cookie := c.http.Jar.Cookies(mustURL(e.ts.URL))
	c.mustCall(200, "POST", "/api/auth/logout", nil, nil)
	if got := c.call("GET", "/api/me", nil, nil); got != 401 {
		t.Errorf("after logout: %d", got)
	}
	stolen := e.client()
	stolen.http.Jar.SetCookies(mustURL(e.ts.URL), cookie)
	if got := stolen.call("GET", "/api/me", nil, nil); got != 401 {
		t.Errorf("the old session cookie still works: %d", got)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	p.mustCall(200, "GET", "/api/me", nil, nil)
	if _, err := e.db.Exec(`UPDATE sessions SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if got := p.call("GET", "/api/me", nil, nil); got != 401 {
		t.Errorf("expired session: %d", got)
	}
	if n, err := e.repo.PurgeSessions(t.Context()); err != nil || n != 1 {
		t.Errorf("purge: %d %v", n, err)
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	e := newEnv(t)
	e.signup("Amara", "amara@example.com")
	c := e.client()
	last := 0
	for i := 0; i < 12; i++ {
		last = c.call("POST", "/api/auth/login", map[string]string{"email": "amara@example.com", "password": "wrong password!"}, nil)
	}
	if last != 429 {
		t.Errorf("after 12 bad attempts the status is %d", last)
	}
	// Even the right password is refused while locked out.
	if got := c.call("POST", "/api/auth/login", map[string]string{"email": "amara@example.com", "password": "correct horse battery"}, nil); got != 429 {
		t.Errorf("locked out login: %d", got)
	}
}

func TestCrossSiteWritesAndWrongContentTypeAreRefused(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")

	do := func(origin, ctype string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+p.path("/workflows"), strings.NewReader(`{"name":"x"}`))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := p.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("https://evil.example", "application/json"); got != 403 {
		t.Errorf("cross-site origin: %d", got)
	}
	if got := do(e.ts.URL, "application/json"); got != 201 {
		t.Errorf("same origin: %d", got)
	}
	if got := do("", "text/plain"); got != 415 {
		t.Errorf("text/plain body: %d", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("X-Request-Id") == "" {
		t.Errorf("missing headers: %v", resp.Header)
	}
	if got := e.client().call("GET", "/api/ready", nil, nil); got != 200 {
		t.Errorf("ready: %d", got)
	}
}

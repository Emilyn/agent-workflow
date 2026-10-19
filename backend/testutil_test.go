package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeClaudeEnv) != "" { // this test program is also the stand-in for the claude program
		fakeClaudeMain()
		return
	}
	passwordIterations = 1000 // fast hashing in tests; production uses pbkdf2Iterations
	os.Exit(m.Run())
}

var schemaCounter atomic.Int64

func testDBURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://postgres@localhost:5433/aw_test?sslmode=disable"
}

// env is a running server on a fresh, empty Postgres schema.
type env struct {
	t      *testing.T
	db     *sql.DB
	repo   *Repo
	srv    *Server
	ts     *httptest.Server
	schema string
}

type envOpts struct {
	anthropicURL string
	serverKey    string
	allowSignup  *bool
	providersURL string // fake GitHub/GitLab/Jira/Figma (see fakeProviders)
	claudeCode   bool   // turn on Claude Code mode, using this test program as the claude program
}

func newEnv(t *testing.T, o ...envOpts) *env {
	t.Helper()
	var opt envOpts
	if len(o) > 0 {
		opt = o[0]
	}
	base := testDBURL()
	admin, err := sql.Open("postgres", base)
	if err == nil {
		err = admin.Ping()
	}
	if err != nil {
		if os.Getenv("REQUIRE_DB") != "" {
			t.Fatalf("database required but not reachable: %v", err)
		}
		t.Skipf("no test database (%v); set TEST_DATABASE_URL", err)
	}
	schema := fmt.Sprintf("t_%d_%d", os.Getpid(), schemaCounter.Add(1))
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := OpenDB(context.Background(), u.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	cfg := Config{
		Port: "0", StaticDir: t.TempDir(), Secret: key, Model: "test-model",
		AnthropicURL: opt.anthropicURL, ServerKey: opt.serverKey, AllowSignup: true,
	}
	if opt.anthropicURL == "" {
		cfg.AnthropicURL = "http://127.0.0.1:1"
	}
	if opt.providersURL != "" {
		cfg.PublicURL = "http://app.test"
		r := opt.providersURL
		app := func(id, scopes, auth, api string) OAuthApp {
			return OAuthApp{ClientID: "cid", ClientSecret: "csecret", Scopes: scopes, AuthURL: r + "/" + id + auth, APIURL: r + "/" + id + api}
		}
		cfg.Apps = map[string]OAuthApp{
			"github": app("github", "public_repo read:user", "", "/api"),
			"gitlab": app("gitlab", "read_api read_user", "", "/api/v4"),
			"jira":   app("jira", "read:jira-work offline_access", "", "/api"),
			"figma":  app("figma", "file_content:read", "", "/api"),
		}
	}
	if opt.claudeCode {
		cfg.ClaudeCode = ClaudeCodeConfig{Enabled: true, Path: os.Args[0], Model: "test-model", MaxTurns: 7, MaxBudgetUSD: 0.5, Timeout: 20 * time.Second}
	}
	if opt.allowSignup != nil {
		cfg.AllowSignup = *opt.allowSignup
	}
	repo := NewRepo(db)
	srv, err := NewServer(cfg, repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	stopWorker := srv.StartWorker(context.Background(), 2)
	ts := httptest.NewServer(srv.Routes())
	if opt.claudeCode {
		srv.cfg.ClaudeCode.CallbackURL = ts.URL // where the fake claude reaches the tools
	}
	e := &env{t: t, db: db, repo: repo, srv: srv, ts: ts, schema: schema}
	t.Cleanup(func() {
		ts.Close()
		stopWorker()
		db.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})
	return e
}

// client is one browser: it keeps cookies between calls.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, base: e.ts.URL, http: &http.Client{Jar: jar}}
}

// call sends a JSON request and decodes a JSON response into out (if not nil). It returns the status.
func (c *client) call(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: bad JSON %q", method, path, raw)
		}
	}
	return resp.StatusCode
}

func (c *client) mustCall(want int, method, path string, body any, out any) {
	c.t.Helper()
	if got := c.call(method, path, body, out); got != want {
		c.t.Fatalf("%s %s: status %d, want %d", method, path, got, want)
	}
}

// person is a signed-in user with their first workspace.
type person struct {
	*client
	User User
	WS   string
}

func (e *env) signup(name, email string) *person {
	e.t.Helper()
	c := e.client()
	var me meResponse
	c.mustCall(200, "POST", "/api/auth/signup", map[string]string{"name": name, "email": email, "password": "correct horse battery"}, &me)
	if len(me.Workspaces) != 1 {
		e.t.Fatalf("signup gave %d workspaces", len(me.Workspaces))
	}
	return &person{client: c, User: me.User, WS: me.Workspaces[0].ID}
}

func (p *person) path(rest string) string { return "/api/workspaces/" + p.WS + rest }

func (p *person) firstWorkflow() Summary {
	p.t.Helper()
	var list []Summary
	p.mustCall(200, "GET", p.path("/workflows"), nil, &list)
	return list[0]
}

// join makes other a member of p's workspace with the given role, through the invite flow.
func (e *env) join(p, other *person, role string) {
	e.t.Helper()
	var inv struct {
		Token string `json:"token"`
	}
	p.mustCall(201, "POST", p.path("/invites"), map[string]string{"role": role}, &inv)
	other.mustCall(200, "POST", "/api/invites/accept", map[string]string{"token": inv.Token}, nil)
}

// fakeAnthropic answers /v1/models and /v1/messages and records the last messages request and key used.
type fakeAPI struct {
	*httptest.Server
	lastBody atomic.Value // []byte
	lastKey  atomic.Value // string
	status   atomic.Int32
}

func newFakeAnthropic(t *testing.T, reply string, validKeys ...string) *fakeAPI {
	f := &fakeAPI{}
	f.status.Store(200)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-api-key")
		ok := false
		for _, k := range validKeys {
			ok = ok || k == key
		}
		if !ok {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/v1/messages":
			body, _ := io.ReadAll(r.Body)
			f.lastBody.Store(body)
			f.lastKey.Store(key)
			if st := int(f.status.Load()); st != 200 {
				w.WriteHeader(st)
				_, _ = w.Write([]byte(`{"error":{"type":"overloaded_error","message":"try later"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": reply}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) sent() (messagesRequest, string) {
	var m messagesRequest
	if b, ok := f.lastBody.Load().([]byte); ok {
		_ = json.Unmarshal(b, &m)
	}
	k, _ := f.lastKey.Load().(string)
	return m, k
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func contains(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

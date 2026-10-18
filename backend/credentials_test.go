package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

const validKey = "sk-ant-api03-valid-key-1234567890"

// fakeAPI accepts only validKey and counts calls it receives.
func fakeAPI(t *testing.T, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.Header.Get("x-api-key") != validKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
		case "/v1/messages":
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": "real reply"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func connectedServer(t *testing.T, api *httptest.Server) (*Server, http.Handler) {
	t.Helper()
	claude := &Claude{Model: "test-model", BaseURL: api.URL, HTTP: api.Client()}
	return newTestServer(t, claude)
}

func TestConnectKeyStoresItPrivatelyAndEnablesClaude(t *testing.T) {
	var calls int32
	api := fakeAPI(t, &calls)
	defer api.Close()
	srv, h := connectedServer(t, api)

	rec := do(t, h, "PUT", "/api/claude/key", map[string]string{"apiKey": "  " + validKey + "\n"})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var st claudeStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if !st.Connected || st.Source != "app" || st.KeyHint != "…7890" {
		t.Fatalf("unexpected status %+v", st)
	}

	// The key never leaves the server.
	for _, path := range []string{"/api/claude", "/api/workspace", "/api/workflows", "/api/health"} {
		if body := do(t, h, "GET", path, nil).Body.String(); strings.Contains(body, validKey) {
			t.Errorf("%s leaks the key", path)
		}
	}
	if strings.Contains(rec.Body.String(), validKey) {
		t.Error("connect response leaks the key")
	}

	info, err := os.Stat(srv.credsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode %v, want 0600", info.Mode().Perm())
	}
	if got, _ := loadCredentials(srv.credsPath); got != validKey {
		t.Errorf("stored key %q", got)
	}

	// Agent steps now call Claude for real.
	run := do(t, h, "POST", "/api/workflows/"+firstID(srv)+"/run/step", map[string]string{"stepId": "n2"})
	var out runResponse
	_ = json.Unmarshal(run.Body.Bytes(), &out)
	if run.Code != 200 || out.Simulated || out.Text != "real reply" {
		t.Fatalf("run after connect: %d %+v", run.Code, out)
	}
}

func TestConnectRejectedKeyIsNotStored(t *testing.T) {
	var calls int32
	api := fakeAPI(t, &calls)
	defer api.Close()
	srv, h := connectedServer(t, api)

	rec := do(t, h, "PUT", "/api/claude/key", map[string]string{"apiKey": "sk-ant-api03-wrong-key-0000000000"})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "rejected") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if srv.claude.Enabled() {
		t.Error("a rejected key enabled Claude")
	}
	if _, err := os.Stat(srv.credsPath); err == nil {
		t.Error("a rejected key was written to disk")
	}
}

func TestConnectRejectsBadFormatsWithoutCallingAnthropic(t *testing.T) {
	var calls int32
	api := fakeAPI(t, &calls)
	defer api.Close()
	_, h := connectedServer(t, api)

	cases := map[string]string{
		"not a key":          "hello",
		"too short":          "sk-ant-abc",
		"has spaces":         "sk-ant-api03-with space-inside-1234567",
		"subscription token": "sk-ant-oat01-" + strings.Repeat("x", 40),
		"empty":              "",
	}
	for name, key := range cases {
		rec := do(t, h, "PUT", "/api/claude/key", map[string]string{"apiKey": key})
		if rec.Code != 400 {
			t.Errorf("%s: status %d", name, rec.Code)
		}
		if name == "subscription token" && !strings.Contains(rec.Body.String(), "subscription") {
			t.Errorf("subscription token message: %s", rec.Body.String())
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("Anthropic was called %d times for invalid input", calls)
	}
}

func TestDisconnectRemovesTheKey(t *testing.T) {
	var calls int32
	api := fakeAPI(t, &calls)
	defer api.Close()
	srv, h := connectedServer(t, api)
	do(t, h, "PUT", "/api/claude/key", map[string]string{"apiKey": validKey})

	rec := do(t, h, "DELETE", "/api/claude/key", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var st claudeStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Connected || srv.claude.Enabled() {
		t.Fatalf("still connected: %+v", st)
	}
	if _, err := os.Stat(srv.credsPath); err == nil {
		t.Error("credentials file still exists")
	}
}

func TestEnvironmentKeyCannotBeChangedInTheApp(t *testing.T) {
	srv, h := newTestServer(t, &Claude{APIKey: validKey, Model: "m", HTTP: http.DefaultClient})
	srv.envKey = true
	if st := srv.claudeStatus(); !st.Connected || st.Source != "environment" {
		t.Fatalf("unexpected status %+v", st)
	}
	if rec := do(t, h, "PUT", "/api/claude/key", map[string]string{"apiKey": validKey}); rec.Code != 409 {
		t.Errorf("connect: status %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/claude/key", nil); rec.Code != 409 {
		t.Errorf("disconnect: status %d", rec.Code)
	}
	if !srv.claude.Enabled() {
		t.Error("the environment key was removed")
	}
}

func TestUnreachableAnthropicIsABadGateway(t *testing.T) {
	api := httptest.NewServer(http.NotFoundHandler())
	url := api.URL
	api.Close() // nothing listens any more
	claude := &Claude{Model: "m", BaseURL: url, HTTP: http.DefaultClient}
	_, h := newTestServer(t, claude)
	rec := do(t, h, "PUT", "/api/claude/key", map[string]string{"apiKey": validKey})
	if rec.Code != 502 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

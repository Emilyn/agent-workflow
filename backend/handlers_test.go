package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, claude *Claude) (*Server, http.Handler) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if claude == nil {
		claude = &Claude{Model: "test-model", HTTP: http.DefaultClient}
	}
	srv := &Server{store: store, claude: claude, credsPath: filepath.Join(t.TempDir(), "credentials.json")}
	return srv, srv.Routes(t.TempDir())
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGetStateSeeded(t *testing.T) {
	_, h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/state", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var st State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Steps) != 6 || st.Name == "" {
		t.Fatalf("unexpected seed: %d rows, name %q", len(st.Steps), st.Name)
	}
	if strings.Contains(rec.Body.String(), "null") {
		t.Fatalf("response contains null: %s", rec.Body.String())
	}
}

func TestPutKeepsServerOutputsAndDropsRemovedSteps(t *testing.T) {
	srv, h := newTestServer(t, nil)
	_ = srv.store.SetOutput("n2", Output{Text: "research output"})
	_ = srv.store.SetOutput("n3", Output{Text: "analysis output"})

	st := srv.store.Get()
	st.Outputs = map[string]Output{"n2": {Text: "tampered"}}
	st.Steps[1] = st.Steps[1][:1] // remove n3
	rec := do(t, h, "PUT", "/api/state", st)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := srv.store.Get()
	if got.Outputs["n2"].Text != "research output" {
		t.Errorf("client overwrote server output: %q", got.Outputs["n2"].Text)
	}
	if _, ok := got.Outputs["n3"]; ok {
		t.Error("output of a removed step was kept")
	}
}

func TestPutRejectsBadWorkflows(t *testing.T) {
	srv, h := newTestServer(t, nil)
	base := srv.store.Get()

	empty := base
	empty.Steps = [][]Node{}
	if rec := do(t, h, "PUT", "/api/state", empty); rec.Code != 400 {
		t.Errorf("empty workflow: status %d", rec.Code)
	}

	emptyRow := base
	emptyRow.Steps = append([][]Node{{}}, base.Steps...)
	if rec := do(t, h, "PUT", "/api/state", emptyRow); rec.Code != 400 {
		t.Errorf("empty row: status %d", rec.Code)
	}

	badProgress := base
	badProgress.Progress = 99
	if rec := do(t, h, "PUT", "/api/state", badProgress); rec.Code != 400 {
		t.Errorf("bad progress: status %d", rec.Code)
	}

	dup := base
	dup.Steps = [][]Node{{base.Steps[0][0], base.Steps[0][0]}}
	if rec := do(t, h, "PUT", "/api/state", dup); rec.Code != 400 {
		t.Errorf("duplicate ids: status %d", rec.Code)
	}
}

func TestRunStepSimulatedWithoutKey(t *testing.T) {
	srv, h := newTestServer(t, nil)
	rec := do(t, h, "POST", "/api/run/step", map[string]string{"stepId": "n2"})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out runResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Simulated || !strings.Contains(out.Text, "Simulated") {
		t.Fatalf("expected simulated output, got %+v", out)
	}
	if srv.store.Get().Outputs["n2"].Text != out.Text {
		t.Error("output was not stored")
	}
}

func TestRunStepRejectsPeopleAndUnknownSteps(t *testing.T) {
	_, h := newTestServer(t, nil)
	if rec := do(t, h, "POST", "/api/run/step", map[string]string{"stepId": "n5"}); rec.Code != 409 {
		t.Errorf("human step: status %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/run/step", map[string]string{"stepId": "nope"}); rec.Code != 404 {
		t.Errorf("unknown step: status %d", rec.Code)
	}
}

func fakeAnthropic(t *testing.T, reply string, captured *[]byte, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("bad request: %s key=%q", r.URL.Path, r.Header.Get("x-api-key"))
		}
		body, _ := io.ReadAll(r.Body)
		if captured != nil {
			*captured = body
		}
		w.WriteHeader(status)
		if status != 200 {
			_, _ = w.Write([]byte(`{"error":{"type":"overloaded_error","message":"try later"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": reply}}})
	}))
}

func TestRunStepCallsClaudeWithPreviousOutput(t *testing.T) {
	var captured []byte
	api := fakeAnthropic(t, "Here is the digest.", &captured, 200)
	defer api.Close()
	claude := &Claude{APIKey: "secret", Model: "test-model", BaseURL: api.URL, HTTP: api.Client()}
	srv, h := newTestServer(t, claude)
	_ = srv.store.SetOutput("n2", Output{Text: "RESEARCH-FINDINGS"})
	_ = srv.store.SetOutput("n3", Output{Text: "ANALYSIS-THEMES"})

	rec := do(t, h, "POST", "/api/run/step", map[string]string{"stepId": "n4"})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out runResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Simulated || out.Text != "Here is the digest." {
		t.Fatalf("unexpected response %+v", out)
	}
	var sent messagesRequest
	_ = json.Unmarshal(captured, &sent)
	if sent.Model != "test-model" || len(sent.Messages) != 1 {
		t.Fatalf("unexpected request %+v", sent)
	}
	user := sent.Messages[0].Content
	if !strings.Contains(user, "RESEARCH-FINDINGS") || !strings.Contains(user, "ANALYSIS-THEMES") {
		t.Errorf("previous outputs missing from prompt: %q", user)
	}
	if !strings.Contains(sent.System, "under 300 words") {
		t.Errorf("step instructions missing from system prompt: %q", sent.System)
	}
}

func TestRunStepSurfacesClaudeErrors(t *testing.T) {
	api := fakeAnthropic(t, "", nil, 529)
	defer api.Close()
	claude := &Claude{APIKey: "secret", Model: "m", BaseURL: api.URL, HTTP: api.Client()}
	srv, h := newTestServer(t, claude)
	rec := do(t, h, "POST", "/api/run/step", map[string]string{"stepId": "n2"})
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "try later") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if _, ok := srv.store.Get().Outputs["n2"]; ok {
		t.Error("a failed run stored an output")
	}
}

func TestChat(t *testing.T) {
	_, h := newTestServer(t, nil)
	rec := do(t, h, "POST", "/api/chat", map[string]string{"agentName": "Writer agent", "instruction": "Write", "message": "keep it short"})
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/chat", map[string]string{"message": "   "}); rec.Code != 400 {
		t.Errorf("blank message: status %d", rec.Code)
	}
}

func TestRunResetClearsOutputsAndProgress(t *testing.T) {
	srv, h := newTestServer(t, nil)
	_ = srv.store.SetOutput("n2", Output{Text: "x"})
	rec := do(t, h, "POST", "/api/run/reset", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	got := srv.store.Get()
	if len(got.Outputs) != 0 || got.Progress != 0 {
		t.Fatalf("not reset: %+v progress %d", got.Outputs, got.Progress)
	}
}

func TestStorePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	a, _ := OpenStore(path)
	st := a.Get()
	st.Name = "Renamed"
	if err := a.Replace(st); err != nil {
		t.Fatal(err)
	}
	b, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.Get().Name != "Renamed" {
		t.Fatalf("name not persisted: %q", b.Get().Name)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	_, h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/nope", nil)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

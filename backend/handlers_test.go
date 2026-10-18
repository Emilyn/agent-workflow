package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func firstID(srv *Server) string { return srv.store.List()[0].ID }

func create(t *testing.T, h http.Handler, body map[string]string) Workflow {
	t.Helper()
	rec := do(t, h, "POST", "/api/workflows", body)
	if rec.Code != 201 {
		t.Fatalf("create %v: status %d: %s", body, rec.Code, rec.Body.String())
	}
	var wf Workflow
	_ = json.Unmarshal(rec.Body.Bytes(), &wf)
	return wf
}

func TestListStartsWithTheExample(t *testing.T) {
	_, h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/workflows", nil)
	var list []Summary
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list) != 1 || list[0].Name != "Weekly customer digest" {
		t.Fatalf("status %d list %+v", rec.Code, list)
	}
	if list[0].Steps != 6 || list[0].Agents != 4 || list[0].People != 2 {
		t.Errorf("summary counts wrong: %+v", list[0])
	}
	if strings.Contains(rec.Body.String(), "null") {
		t.Errorf("null in response: %s", rec.Body.String())
	}
}

func TestCreateBlankExampleAndCopy(t *testing.T) {
	srv, h := newTestServer(t, nil)
	src := firstID(srv)

	blank := create(t, h, map[string]string{"name": "  Sprint review  "})
	if blank.Name != "Sprint review" || len(blank.Steps) != 1 || blank.Steps[0][0].Kind != "trigger" || blank.Progress != 0 {
		t.Fatalf("unexpected blank workflow: %+v", blank)
	}
	if blank.ID == "" || blank.ID == src {
		t.Errorf("bad id %q", blank.ID)
	}

	untitled := create(t, h, map[string]string{})
	if untitled.Name != "Untitled workflow" {
		t.Errorf("default name %q", untitled.Name)
	}

	example := create(t, h, map[string]string{"name": "Another digest", "template": "example"})
	if len(example.Steps) != 6 || example.ID == src {
		t.Errorf("example template: %d rows, id %q", len(example.Steps), example.ID)
	}

	_ = srv.store.SetOutput(src, "n2", Output{Text: "x"})
	cp := create(t, h, map[string]string{"copyOf": src})
	if cp.Name != "Copy of Weekly customer digest" || len(cp.Steps) != 6 || cp.Progress != 0 || len(cp.Outputs) != 0 {
		t.Errorf("copy: name %q rows %d progress %d outputs %d", cp.Name, len(cp.Steps), cp.Progress, len(cp.Outputs))
	}
	for _, m := range cp.Messages {
		if m.Author == "Amara" {
			t.Error("copy carried over the original chat")
		}
	}

	if got := len(srv.store.List()); got != 5 {
		t.Errorf("want 5 workflows, have %d", got)
	}
}

func TestCreateValidation(t *testing.T) {
	srv, h := newTestServer(t, nil)
	for name, body := range map[string]map[string]string{
		"unknown template": {"name": "x", "template": "nope"},
		"name too long":    {"name": strings.Repeat("a", 81)},
	} {
		if rec := do(t, h, "POST", "/api/workflows", body); rec.Code != 400 {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	if rec := do(t, h, "POST", "/api/workflows", map[string]string{"copyOf": "missing"}); rec.Code != 404 {
		t.Errorf("copy of unknown: status %d", rec.Code)
	}
	if len(srv.store.List()) != 1 {
		t.Error("a failed create added a workflow")
	}
}

func TestWorkflowsAreIndependent(t *testing.T) {
	srv, h := newTestServer(t, nil)
	a := firstID(srv)
	b := create(t, h, map[string]string{"name": "B", "template": "example"}).ID

	// Rename and edit A only.
	wfA, _ := srv.store.Get(a)
	wfA.Name = "A renamed"
	wfA.Steps[2][0].Instruction = "changed in A"
	if rec := do(t, h, "PUT", "/api/workflows/"+a, wfA); rec.Code != 200 {
		t.Fatalf("put A: %d %s", rec.Code, rec.Body.String())
	}
	gotB, _ := srv.store.Get(b)
	if gotB.Name != "B" || gotB.Steps[2][0].Instruction == "changed in A" {
		t.Errorf("editing A changed B: %q / %q", gotB.Name, gotB.Steps[2][0].Instruction)
	}

	// Running a step in B leaves A's outputs alone.
	if rec := do(t, h, "POST", "/api/workflows/"+b+"/run/step", map[string]string{"stepId": "n2"}); rec.Code != 200 {
		t.Fatalf("run in B: %d", rec.Code)
	}
	gotA, _ := srv.store.Get(a)
	gotB, _ = srv.store.Get(b)
	if len(gotA.Outputs) != 0 || len(gotB.Outputs) != 1 {
		t.Errorf("outputs leaked: A %d, B %d", len(gotA.Outputs), len(gotB.Outputs))
	}

	// Progress and chat are per workflow too.
	gotB.Progress = 2
	gotB.Messages = append(gotB.Messages, Message{Author: "You", Kind: "human", Text: "only in B"})
	do(t, h, "PUT", "/api/workflows/"+b, gotB)
	gotA, _ = srv.store.Get(a)
	if gotA.Progress != 4 {
		t.Errorf("A progress changed to %d", gotA.Progress)
	}
	for _, m := range gotA.Messages {
		if m.Text == "only in B" {
			t.Error("B's chat appeared in A")
		}
	}
}

func TestPutKeepsServerOutputsAndDropsRemovedSteps(t *testing.T) {
	srv, h := newTestServer(t, nil)
	id := firstID(srv)
	_ = srv.store.SetOutput(id, "n2", Output{Text: "research output"})
	_ = srv.store.SetOutput(id, "n3", Output{Text: "analysis output"})

	wf, _ := srv.store.Get(id)
	wf.Outputs = map[string]Output{"n2": {Text: "tampered"}}
	wf.Steps[1] = wf.Steps[1][:1] // remove n3
	if rec := do(t, h, "PUT", "/api/workflows/"+id, wf); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := srv.store.Get(id)
	if got.Outputs["n2"].Text != "research output" {
		t.Errorf("client overwrote server output: %q", got.Outputs["n2"].Text)
	}
	if _, ok := got.Outputs["n3"]; ok {
		t.Error("output of a removed step was kept")
	}
}

func TestPutRejectsBadWorkflows(t *testing.T) {
	srv, h := newTestServer(t, nil)
	id := firstID(srv)
	base, _ := srv.store.Get(id)
	put := func(mut func(*Workflow)) int {
		wf := clone(base)
		mut(&wf)
		return do(t, h, "PUT", "/api/workflows/"+id, wf).Code
	}
	if c := put(func(w *Workflow) { w.Name = "   " }); c != 400 {
		t.Errorf("blank name: %d", c)
	}
	if c := put(func(w *Workflow) { w.Steps = [][]Node{} }); c != 400 {
		t.Errorf("no steps: %d", c)
	}
	if c := put(func(w *Workflow) { w.Steps = append([][]Node{{}}, w.Steps...) }); c != 400 {
		t.Errorf("empty row: %d", c)
	}
	if c := put(func(w *Workflow) { w.Progress = 99 }); c != 400 {
		t.Errorf("bad progress: %d", c)
	}
	if c := put(func(w *Workflow) { w.Steps = [][]Node{{w.Steps[0][0], w.Steps[0][0]}} }); c != 400 {
		t.Errorf("duplicate ids: %d", c)
	}
	if c := put(func(w *Workflow) { w.Steps[0][0].Kind = "robot" }); c != 400 {
		t.Errorf("unknown kind: %d", c)
	}
	if rec := do(t, h, "PUT", "/api/workflows/missing", base); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
	// A body that claims another id cannot overwrite a different workflow.
	other := create(t, h, map[string]string{"name": "Other"})
	hijack := clone(base)
	hijack.ID = other.ID
	do(t, h, "PUT", "/api/workflows/"+id, hijack)
	if got, _ := srv.store.Get(other.ID); got.Name != "Other" {
		t.Errorf("body id overwrote another workflow: %q", got.Name)
	}
}

func TestDeleteWorkflow(t *testing.T) {
	srv, h := newTestServer(t, nil)
	first := firstID(srv)
	second := create(t, h, map[string]string{"name": "Second"}).ID

	rec := do(t, h, "DELETE", "/api/workflows/"+second, nil)
	var list []Summary
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list) != 1 || list[0].ID != first {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "GET", "/api/workflows/"+second, nil); rec.Code != 404 {
		t.Errorf("deleted workflow still readable: %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/workflows/"+first, nil); rec.Code != 409 {
		t.Errorf("deleting the last workflow: %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/workflows/missing", nil); rec.Code != 404 {
		t.Errorf("deleting unknown: %d", rec.Code)
	}
}

func TestWorkspaceIsShared(t *testing.T) {
	_, h := newTestServer(t, nil)
	ws := Workspace{
		Connected:    map[string]bool{"jira": true, "bogus": true},
		GitProvider:  "gitlab",
		CustomAgents: []Agent{{ID: "c1", Name: "Release notes agent", Instruction: "Write notes", Mine: true}},
	}
	rec := do(t, h, "PUT", "/api/workspace", ws)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got Workspace
	_ = json.Unmarshal(do(t, h, "GET", "/api/workspace", nil).Body.Bytes(), &got)
	if !got.Connected["jira"] || got.GitProvider != "gitlab" || len(got.CustomAgents) != 1 {
		t.Fatalf("not saved: %+v", got)
	}
	if _, ok := got.Connected["bogus"]; ok {
		t.Error("unknown integration key was kept")
	}
	if got.CustomAgents[0].Tools == nil {
		t.Error("agent tools serialised as null")
	}

	dup := Workspace{CustomAgents: []Agent{{ID: "a", Name: "A"}, {ID: "a", Name: "B"}}}
	if rec := do(t, h, "PUT", "/api/workspace", dup); rec.Code != 400 {
		t.Errorf("duplicate agent ids: %d", rec.Code)
	}
	nameless := Workspace{CustomAgents: []Agent{{ID: "a", Name: " "}}}
	if rec := do(t, h, "PUT", "/api/workspace", nameless); rec.Code != 400 {
		t.Errorf("nameless agent: %d", rec.Code)
	}
}

func TestRunStepSimulatedWithoutKey(t *testing.T) {
	srv, h := newTestServer(t, nil)
	id := firstID(srv)
	rec := do(t, h, "POST", "/api/workflows/"+id+"/run/step", map[string]string{"stepId": "n2"})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out runResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Simulated || !strings.Contains(out.Text, "Simulated") {
		t.Fatalf("expected simulated output, got %+v", out)
	}
	if got, _ := srv.store.Get(id); got.Outputs["n2"].Text != out.Text {
		t.Error("output was not stored")
	}
}

func TestRunStepRejectsPeopleAndUnknownSteps(t *testing.T) {
	srv, h := newTestServer(t, nil)
	id := firstID(srv)
	run := func(wf, step string) int {
		return do(t, h, "POST", "/api/workflows/"+wf+"/run/step", map[string]string{"stepId": step}).Code
	}
	if c := run(id, "n5"); c != 409 {
		t.Errorf("human step: %d", c)
	}
	if c := run(id, "nope"); c != 404 {
		t.Errorf("unknown step: %d", c)
	}
	if c := run("missing", "n2"); c != 404 {
		t.Errorf("unknown workflow: %d", c)
	}
	blank := create(t, h, map[string]string{"name": "Blank"})
	if c := run(blank.ID, "n2"); c != 404 {
		t.Errorf("a step from another workflow ran: %d", c)
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

func TestRunStepCallsClaudeWithPreviousOutputAndOwnWorkflowName(t *testing.T) {
	var captured []byte
	api := fakeAnthropic(t, "Here is the digest.", &captured, 200)
	defer api.Close()
	claude := &Claude{APIKey: "secret", Model: "test-model", BaseURL: api.URL, HTTP: api.Client()}
	srv, h := newTestServer(t, claude)
	id := firstID(srv)
	_ = srv.store.SetOutput(id, "n2", Output{Text: "RESEARCH-FINDINGS"})
	_ = srv.store.SetOutput(id, "n3", Output{Text: "ANALYSIS-THEMES"})

	rec := do(t, h, "POST", "/api/workflows/"+id+"/run/step", map[string]string{"stepId": "n4"})
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
	user := sent.Messages[0].Content
	if !strings.Contains(user, "RESEARCH-FINDINGS") || !strings.Contains(user, "ANALYSIS-THEMES") {
		t.Errorf("previous outputs missing from prompt: %q", user)
	}
	if !strings.Contains(sent.System, "under 300 words") || !strings.Contains(sent.System, "Weekly customer digest") {
		t.Errorf("instructions or workflow name missing from system prompt: %q", sent.System)
	}
}

func TestRunStepDoesNotUseOutputsFromAnotherWorkflow(t *testing.T) {
	var captured []byte
	api := fakeAnthropic(t, "ok", &captured, 200)
	defer api.Close()
	claude := &Claude{APIKey: "secret", Model: "m", BaseURL: api.URL, HTTP: api.Client()}
	srv, h := newTestServer(t, claude)
	a := firstID(srv)
	b := create(t, h, map[string]string{"name": "B", "template": "example"}).ID
	_ = srv.store.SetOutput(a, "n2", Output{Text: "SECRET-FROM-A"})

	do(t, h, "POST", "/api/workflows/"+b+"/run/step", map[string]string{"stepId": "n4"})
	if strings.Contains(string(captured), "SECRET-FROM-A") {
		t.Error("an output from workflow A reached workflow B's prompt")
	}
}

func TestRunStepSurfacesClaudeErrors(t *testing.T) {
	api := fakeAnthropic(t, "", nil, 529)
	defer api.Close()
	claude := &Claude{APIKey: "secret", Model: "m", BaseURL: api.URL, HTTP: api.Client()}
	srv, h := newTestServer(t, claude)
	id := firstID(srv)
	rec := do(t, h, "POST", "/api/workflows/"+id+"/run/step", map[string]string{"stepId": "n2"})
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "try later") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if got, _ := srv.store.Get(id); len(got.Outputs) != 0 {
		t.Error("a failed run stored an output")
	}
}

func TestChat(t *testing.T) {
	srv, h := newTestServer(t, nil)
	id := firstID(srv)
	rec := do(t, h, "POST", "/api/workflows/"+id+"/chat", map[string]string{"agentName": "Writer agent", "instruction": "Write", "message": "keep it short"})
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/workflows/"+id+"/chat", map[string]string{"message": "   "}); rec.Code != 400 {
		t.Errorf("blank message: status %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/workflows/missing/chat", map[string]string{"message": "hi"}); rec.Code != 404 {
		t.Errorf("unknown workflow: status %d", rec.Code)
	}
}

func TestRunResetClearsOnlyThisWorkflow(t *testing.T) {
	srv, h := newTestServer(t, nil)
	a := firstID(srv)
	b := create(t, h, map[string]string{"name": "B", "template": "example"}).ID
	_ = srv.store.SetOutput(a, "n2", Output{Text: "x"})
	_ = srv.store.SetOutput(b, "n2", Output{Text: "y"})

	if rec := do(t, h, "POST", "/api/workflows/"+a+"/run/reset", nil); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	gotA, _ := srv.store.Get(a)
	gotB, _ := srv.store.Get(b)
	if len(gotA.Outputs) != 0 || gotA.Progress != 0 {
		t.Errorf("A not reset: %d outputs, progress %d", len(gotA.Outputs), gotA.Progress)
	}
	if len(gotB.Outputs) != 1 || gotB.Progress != 4 {
		t.Errorf("B was reset too: %d outputs, progress %d", len(gotB.Outputs), gotB.Progress)
	}
}

func TestStorePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	a, _ := OpenStore(path)
	wf, err := a.Create("Persisted", "blank", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetWorkspace(Workspace{CustomAgents: []Agent{{ID: "c1", Name: "Mine agent"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.Get(wf.ID); err != nil || got.Name != "Persisted" {
		t.Fatalf("workflow not persisted: %v %+v", err, got)
	}
	if len(b.List()) != 2 || len(b.Workspace().CustomAgents) != 1 {
		t.Errorf("after restart: %d workflows, %d agents", len(b.List()), len(b.Workspace().CustomAgents))
	}
}

func TestMigratesTheSingleWorkflowFile(t *testing.T) {
	legacy := `{
	  "name": "Old workflow",
	  "steps": [[{"id":"n1","kind":"trigger","title":"Start","desc":"","who":"You","instruction":"","tools":[]}],
	            [{"id":"n2","kind":"agent","title":"Writer agent","desc":"","who":"Claude Sonnet","instruction":"Write","tools":["Docs"]}]],
	  "progress": 1,
	  "nextId": 3,
	  "connected": {"jira": true},
	  "gitProvider": "gitlab",
	  "customAgents": [{"id":"c1","name":"Mine agent","role":"","instruction":"x","tools":[],"model":"Claude Sonnet"}],
	  "messages": [{"author":"Amara","kind":"human","time":"9:00","text":"hello"}],
	  "outputs": {"n2": {"text":"kept","simulated":false}}
	}`
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].Name != "Old workflow" {
		t.Fatalf("migrated list: %+v", list)
	}
	wf, _ := s.Get(list[0].ID)
	if wf.Progress != 1 || wf.NextID != 3 || wf.Outputs["n2"].Text != "kept" || len(wf.Messages) != 1 {
		t.Errorf("workflow data lost in migration: %+v", wf)
	}
	ws := s.Workspace()
	if !ws.Connected["jira"] || ws.GitProvider != "gitlab" || len(ws.CustomAgents) != 1 {
		t.Errorf("workspace data lost in migration: %+v", ws)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	_, h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/nope", nil)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "GET", "/api/state", nil); rec.Code != 404 {
		t.Errorf("the old single-workflow endpoint should be gone, got %d", rec.Code)
	}
}

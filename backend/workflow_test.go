package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateBlankExampleAndCopy(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	src := p.firstWorkflow().ID

	var blank Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"name": "  Sprint review  "}, &blank)
	if blank.Name != "Sprint review" || len(blank.Steps) != 1 || blank.Steps[0][0].Kind != "trigger" || blank.Progress != 0 || blank.Version != 1 {
		t.Fatalf("blank: %+v", blank)
	}
	if len(blank.Messages) != 1 || blank.Messages[0].Author != "Claude" {
		t.Errorf("blank should start with a welcome message: %+v", blank.Messages)
	}
	var untitled Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{}, &untitled)
	if untitled.Name != "Untitled workflow" {
		t.Errorf("default name %q", untitled.Name)
	}
	var example Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"name": "Digest 2", "template": "example"}, &example)
	if len(example.Steps) != 6 {
		t.Errorf("example has %d rows", len(example.Steps))
	}

	p.mustCall(200, "POST", p.path("/workflows/"+src+"/chat"), map[string]string{"message": "hello"}, nil)
	_ = e.repo.SetOutput(t.Context(), p.WS, src, "n2", Output{Text: "x"})
	var cp Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"copyOf": src}, &cp)
	if cp.Name != "Copy of Weekly customer digest" || len(cp.Steps) != 6 || len(cp.Outputs) != 0 {
		t.Errorf("copy: %q rows %d outputs %d", cp.Name, len(cp.Steps), len(cp.Outputs))
	}
	for _, m := range cp.Messages {
		if m.Text == "hello" {
			t.Error("copy carried over the chat")
		}
	}
	var list []Summary
	p.mustCall(200, "GET", p.path("/workflows"), nil, &list)
	if len(list) != 5 {
		t.Errorf("%d workflows", len(list))
	}
	if list[0].Agents != 4 || list[0].People != 2 || list[0].Steps != 6 {
		t.Errorf("summary counts: %+v", list[0])
	}

	for name, body := range map[string]map[string]string{
		"unknown template": {"name": "x", "template": "nope"},
		"long name":        {"name": strings.Repeat("a", 81)},
	} {
		if got := p.call("POST", p.path("/workflows"), body, nil); got != 400 {
			t.Errorf("%s: %d", name, got)
		}
	}
	if got := p.call("POST", p.path("/workflows"), map[string]string{"copyOf": "missing"}, nil); got != 404 {
		t.Errorf("copy of unknown: %d", got)
	}
}

func TestSaveVersionsAndConflicts(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	e.join(a, b, "member")
	b.WS = a.WS
	id := a.firstWorkflow().ID

	var wa, wb Workflow
	a.mustCall(200, "GET", a.path("/workflows/"+id), nil, &wa)
	b.mustCall(200, "GET", b.path("/workflows/"+id), nil, &wb)

	wa.Name = "Amara's title"
	var saved Workflow
	a.mustCall(200, "PUT", a.path("/workflows/"+id), wa, &saved)
	if saved.Version != wa.Version+1 {
		t.Errorf("version %d -> %d", wa.Version, saved.Version)
	}

	// Bruno saves a copy that predates Amara's change: refused, and he is handed the current version.
	wb.Name = "Bruno's title"
	var conflict struct {
		Error    string   `json:"error"`
		Workflow Workflow `json:"workflow"`
	}
	b.mustCall(409, "PUT", b.path("/workflows/"+id), wb, &conflict)
	if conflict.Workflow.Name != "Amara's title" || conflict.Workflow.Version != saved.Version {
		t.Errorf("conflict body: %+v", conflict.Workflow.Name)
	}
	var now Workflow
	a.mustCall(200, "GET", a.path("/workflows/"+id), nil, &now)
	if now.Name != "Amara's title" {
		t.Errorf("a stale save overwrote the newer one: %q", now.Name)
	}
	// After reloading, Bruno can save.
	conflict.Workflow.Name = "Both agreed"
	b.mustCall(200, "PUT", b.path("/workflows/"+id), conflict.Workflow, nil)
}

func TestSaveKeepsServerOutputsAndValidates(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	id := p.firstWorkflow().ID
	ctx := t.Context()
	_ = e.repo.SetOutput(ctx, p.WS, id, "n2", Output{Text: "research output"})
	_ = e.repo.SetOutput(ctx, p.WS, id, "n3", Output{Text: "analysis output"})

	var wf Workflow
	p.mustCall(200, "GET", p.path("/workflows/"+id), nil, &wf)
	wf.Outputs = map[string]Output{"n2": {Text: "tampered"}}
	wf.Steps[1] = wf.Steps[1][:1] // drop n3
	var saved Workflow
	p.mustCall(200, "PUT", p.path("/workflows/"+id), wf, &saved)
	if saved.Outputs["n2"].Text != "research output" {
		t.Errorf("client overwrote an output: %q", saved.Outputs["n2"].Text)
	}
	if _, ok := saved.Outputs["n3"]; ok {
		t.Error("output of a removed step kept")
	}

	put := func(mut func(*Workflow)) int {
		var cur Workflow
		p.mustCall(200, "GET", p.path("/workflows/"+id), nil, &cur)
		mut(&cur)
		return p.call("PUT", p.path("/workflows/"+id), cur, nil)
	}
	for name, mut := range map[string]func(*Workflow){
		"blank name":    func(w *Workflow) { w.Name = "  " },
		"no steps":      func(w *Workflow) { w.Steps = [][]Node{} },
		"empty row":     func(w *Workflow) { w.Steps = append([][]Node{{}}, w.Steps...) },
		"bad progress":  func(w *Workflow) { w.Progress = 99 },
		"duplicate ids": func(w *Workflow) { w.Steps = [][]Node{{w.Steps[0][0], w.Steps[0][0]}} },
		"unknown kind":  func(w *Workflow) { w.Steps[0][0].Kind = "robot" },
		"name too long": func(w *Workflow) { w.Name = strings.Repeat("a", 81) },
	} {
		if got := put(mut); got != 400 {
			t.Errorf("%s: status %d", name, got)
		}
	}
	if got := p.call("PUT", p.path("/workflows/missing"), wf, nil); got != 404 {
		t.Errorf("unknown id: %d", got)
	}
}

func TestDeleteAndLastWorkflow(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	first := p.firstWorkflow().ID
	var second Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"name": "Second"}, &second)

	var list []Summary
	p.mustCall(200, "DELETE", p.path("/workflows/"+second.ID), nil, &list)
	if len(list) != 1 || list[0].ID != first {
		t.Fatalf("after delete: %+v", list)
	}
	if got := p.call("GET", p.path("/workflows/"+second.ID), nil, nil); got != 404 {
		t.Errorf("deleted workflow readable: %d", got)
	}
	if got := p.call("DELETE", p.path("/workflows/"+first), nil, nil); got != 409 {
		t.Errorf("deleting the last workflow: %d", got)
	}
	if got := p.call("DELETE", p.path("/workflows/missing"), nil, nil); got != 404 {
		t.Errorf("deleting unknown: %d", got)
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM messages WHERE workflow_id = $1`, second.ID).Scan(&n)
	if n != 0 {
		t.Error("messages of the deleted workflow remain")
	}
}

func TestChatIsSharedAndSigned(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	e.join(a, b, "member")
	b.WS = a.WS
	id := a.firstWorkflow().ID

	var res chatResponse
	a.mustCall(200, "POST", a.path("/workflows/"+id+"/chat"), map[string]string{"agentName": "Writer agent", "instruction": "Write", "message": "keep it short"}, &res)
	if len(res.Messages) != 2 || res.Messages[0].Author != "Amara" || res.Messages[0].Kind != "human" || res.Messages[1].Author != "Writer agent" || !res.Simulated {
		t.Fatalf("chat result: %+v", res)
	}
	// A teammate who joins in sees the thread, and can add to it as themselves.
	var posted Message
	b.mustCall(201, "POST", b.path("/workflows/"+id+"/messages"), map[string]string{"text": "I agree", "author": "Amara"}, &posted)
	if posted.Author != "Bruno" || posted.Kind != "human" {
		t.Errorf("the author was taken from the request: %+v", posted)
	}
	var note Message
	b.mustCall(201, "POST", b.path("/workflows/"+id+"/messages"), map[string]string{"text": "X is now step 3.", "kind": "note"}, &note)
	if note.Author != "Claude" || note.Kind != "agent" {
		t.Errorf("note: %+v", note)
	}

	var wf Workflow
	a.mustCall(200, "GET", a.path("/workflows/"+id), nil, &wf)
	var texts []string
	for _, m := range wf.Messages {
		texts = append(texts, m.Author+": "+m.Text)
	}
	got := strings.Join(texts, "|")
	if !contains(got, "Amara: keep it short", "Writer agent:", "Bruno: I agree", "Claude: X is now step 3.") {
		t.Errorf("thread: %s", got)
	}

	for _, body := range []map[string]string{{"message": "   "}, {"message": strings.Repeat("x", 4001)}} {
		if got := a.call("POST", a.path("/workflows/"+id+"/chat"), body, nil); got != 400 {
			t.Errorf("bad chat message: %d", got)
		}
	}
	if got := a.call("POST", a.path("/workflows/"+id+"/messages"), map[string]string{"text": " "}, nil); got != 400 {
		t.Errorf("blank post: %d", got)
	}
}

func TestSyncDeliversNewMessagesOutputsAndChanges(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	e.join(a, b, "member")
	b.WS = a.WS
	id := a.firstWorkflow().ID

	var wf Workflow
	b.mustCall(200, "GET", b.path("/workflows/"+id), nil, &wf)
	last := wf.Messages[len(wf.Messages)-1].ID

	var idle syncResponse
	b.mustCall(200, "GET", b.path("/workflows/"+id+"/sync?after="+itoa(last)+"&version="+itoa(int64(wf.Version))), nil, &idle)
	if len(idle.Messages) != 0 || idle.Workflow != nil {
		t.Errorf("nothing changed but sync returned %+v", idle)
	}

	a.mustCall(201, "POST", a.path("/workflows/"+id+"/messages"), map[string]string{"text": "ping"}, nil)
	_ = e.repo.SetOutput(t.Context(), a.WS, id, "n2", Output{Text: "fresh result"})
	wf.Name = "Renamed by Amara"
	a.mustCall(200, "PUT", a.path("/workflows/"+id), wf, nil)

	var s syncResponse
	b.mustCall(200, "GET", b.path("/workflows/"+id+"/sync?after="+itoa(last)+"&version="+itoa(int64(wf.Version))), nil, &s)
	if len(s.Messages) != 1 || s.Messages[0].Text != "ping" {
		t.Errorf("messages: %+v", s.Messages)
	}
	if s.Outputs["n2"].Text != "fresh result" {
		t.Errorf("outputs: %+v", s.Outputs)
	}
	if s.Workflow == nil || s.Workflow.Name != "Renamed by Amara" {
		t.Errorf("a stale client should be handed the new workflow: %+v", s.Workflow)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestRunStepSimulatedWithoutKey(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	id := p.firstWorkflow().ID
	var out runResponse
	p.mustCall(200, "POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n2"}, &out)
	if !out.Simulated || !strings.Contains(out.Text, "Simulated") {
		t.Fatalf("expected simulated output: %+v", out)
	}
	var wf Workflow
	p.mustCall(200, "GET", p.path("/workflows/"+id), nil, &wf)
	if wf.Outputs["n2"].Text != out.Text {
		t.Error("output not stored")
	}
	p.mustCall(409, "POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n5"}, nil)
	p.mustCall(404, "POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "nope"}, nil)
}

func TestRunUsesTheWorkspacesKeyAndPreviousOutputs(t *testing.T) {
	const key = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa"
	api := newFakeAnthropic(t, "Here is the digest.", key)
	e := newEnv(t, envOpts{anthropicURL: api.URL})
	p := e.signup("Amara", "amara@example.com")
	p.mustCall(200, "PUT", p.path("/claude/key"), map[string]string{"apiKey": key}, nil)
	id := p.firstWorkflow().ID
	_ = e.repo.SetOutput(t.Context(), p.WS, id, "n2", Output{Text: "RESEARCH-FINDINGS"})
	_ = e.repo.SetOutput(t.Context(), p.WS, id, "n3", Output{Text: "ANALYSIS-THEMES"})

	var out runResponse
	p.mustCall(200, "POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n4"}, &out)
	if out.Simulated || out.Text != "Here is the digest." {
		t.Fatalf("%+v", out)
	}
	sent, usedKey := api.sent()
	if usedKey != key {
		t.Errorf("wrong key used: %q", usedKey)
	}
	if !contains(sent.Messages[0].Content, "RESEARCH-FINDINGS", "ANALYSIS-THEMES") {
		t.Errorf("previous outputs missing: %q", sent.Messages[0].Content)
	}
	if !contains(sent.System, "under 300 words", "Weekly customer digest") {
		t.Errorf("system prompt: %q", sent.System)
	}

	// A reply from Claude also lands in the shared chat.
	var res chatResponse
	p.mustCall(200, "POST", p.path("/workflows/"+id+"/chat"), map[string]string{"message": "what next?"}, &res)
	if res.Simulated || res.Messages[1].Text != "Here is the digest." {
		t.Errorf("%+v", res)
	}
}

func TestRunErrorsAreShownAndNotStored(t *testing.T) {
	const key = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa"
	api := newFakeAnthropic(t, "x", key)
	e := newEnv(t, envOpts{anthropicURL: api.URL})
	p := e.signup("Amara", "amara@example.com")
	p.mustCall(200, "PUT", p.path("/claude/key"), map[string]string{"apiKey": key}, nil)
	id := p.firstWorkflow().ID
	api.status.Store(529)

	var er map[string]string
	p.mustCall(502, "POST", p.path("/workflows/"+id+"/run/step"), map[string]string{"stepId": "n2"}, &er)
	if !strings.Contains(er["error"], "try later") {
		t.Errorf("error: %v", er)
	}
	var wf Workflow
	p.mustCall(200, "GET", p.path("/workflows/"+id), nil, &wf)
	if len(wf.Outputs) != 0 {
		t.Error("a failed run stored an output")
	}
	// In chat the failure becomes a visible reply instead of an error.
	var res chatResponse
	p.mustCall(200, "POST", p.path("/workflows/"+id+"/chat"), map[string]string{"message": "hi"}, &res)
	if !strings.Contains(res.Messages[1].Text, "I could not reply") {
		t.Errorf("%+v", res.Messages)
	}
}

func TestRunResetClearsOnlyThatWorkflow(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	a := p.firstWorkflow().ID
	var other Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"template": "example"}, &other)
	_ = e.repo.SetOutput(t.Context(), p.WS, a, "n2", Output{Text: "x"})
	_ = e.repo.SetOutput(t.Context(), p.WS, other.ID, "n2", Output{Text: "y"})
	p.mustCall(200, "POST", p.path("/workflows/"+a+"/run/reset"), nil, nil)
	var wa, wo Workflow
	p.mustCall(200, "GET", p.path("/workflows/"+a), nil, &wa)
	p.mustCall(200, "GET", p.path("/workflows/"+other.ID), nil, &wo)
	if len(wa.Outputs) != 0 || len(wo.Outputs) != 1 {
		t.Errorf("outputs: %d / %d", len(wa.Outputs), len(wo.Outputs))
	}
}

func TestSettingsAreSanitised(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	var got Workspace
	p.mustCall(200, "PUT", p.path("/settings"), Workspace{Connected: map[string]bool{"jira": true, "bogus": true}, GitProvider: "svn"}, &got)
	if !got.Connected["jira"] || got.GitProvider != "github" {
		t.Errorf("%+v", got)
	}
	if _, ok := got.Connected["bogus"]; ok {
		t.Error("unknown integration kept")
	}
	if got.CustomAgents == nil {
		t.Error("agents serialised as null")
	}
	for name, ws := range map[string]Workspace{
		"duplicate ids": {CustomAgents: []Agent{{ID: "a", Name: "A"}, {ID: "a", Name: "B"}}},
		"nameless":      {CustomAgents: []Agent{{ID: "a", Name: " "}}},
	} {
		if status := p.call("PUT", p.path("/settings"), ws, nil); status != 400 {
			t.Errorf("%s: %d", name, status)
		}
	}
}

func TestSummaryShowsWhoTheRunWaitsOn(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Ann", "ann@example.com")
	var wf Workflow
	p.mustCall(200, "GET", p.path("/workflows/"+p.firstWorkflow().ID), nil, &wf)
	row := -1
	for i, g := range wf.Steps {
		for j := range g {
			if g[j].Kind == "human" {
				wf.Steps[i][j].Assignee = "Mo"
				if row < 0 {
					row = i
				}
			}
		}
	}
	if row < 0 {
		t.Skip("the starting workflow has no person step")
	}
	if s := p.firstWorkflow(); s.WaitingOn != "" {
		t.Fatalf("waiting before the run reached the person: %+v", s)
	}
	wf.Progress = row
	p.mustCall(200, "PUT", p.path("/workflows/"+wf.ID), wf, nil)
	if s := p.firstWorkflow(); s.WaitingOn != "Mo" || s.WaitingTitle == "" {
		t.Fatalf("%+v", s)
	}
	wf.Progress = len(wf.Steps)
	p.mustCall(200, "GET", p.path("/workflows/"+wf.ID), nil, &wf) // pick up the new version
	wf.Progress = len(wf.Steps)
	p.mustCall(200, "PUT", p.path("/workflows/"+wf.ID), wf, nil)
	if s := p.firstWorkflow(); s.WaitingOn != "" {
		t.Fatalf("still waiting after the run finished: %+v", s)
	}
}

func TestWorkspaceRunsListsEveryWorkflow(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Ann", "ann@example.com")
	other := e.signup("Zed", "zed@example.com")
	first := p.firstWorkflow()
	var second Workflow
	p.mustCall(201, "POST", p.path("/workflows"), map[string]string{"name": "Second"}, &second)
	for i, wfID := range []string{first.ID, second.ID} {
		ok, err := e.repo.EnqueueRun(context.Background(), Run{ID: newLongID("run"), WorkspaceID: p.WS, WorkflowID: wfID, Source: "manual", ReviewMode: "draft", Input: changeEvent{Provider: "github", Project: "a/b", Number: i + 1, Title: "t"}}, "", p.User.ID)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	var runs []Run
	p.mustCall(200, "GET", p.path("/runs"), nil, &runs)
	if len(runs) != 2 || runs[0].WorkflowID == runs[1].WorkflowID {
		t.Fatalf("%+v", runs)
	}
	var theirs []Run
	other.mustCall(200, "GET", other.path("/runs"), nil, &theirs)
	if len(theirs) != 0 {
		t.Fatalf("another workspace saw %d runs", len(theirs))
	}
	var onlyFirst []Run
	p.mustCall(200, "GET", p.path("/workflows/"+first.ID+"/runs"), nil, &onlyFirst)
	if len(onlyFirst) != 1 || onlyFirst[0].WorkflowID != first.ID {
		t.Fatalf("%+v", onlyFirst)
	}
}

package main

import (
	"strings"
	"testing"
)

const goodDraft = "Here you go:\n```json\n" + `{"name":"Weekly report","steps":[
 {"kind":"agent","title":"Pull numbers","desc":"Reads the sheet","instruction":"Read last week's numbers.","tools":["sheets","Slack","Sheets"]},
 {"kind":"human","title":"Approve","desc":"Mo checks","instruction":"Approve or send back.","tools":["GitHub"]}]}` + "\n```"

func TestParseDraft(t *testing.T) {
	d, err := parseDraft(goodDraft)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Weekly report" || len(d.Steps) != 2 {
		t.Fatalf("%+v", d)
	}
	if got := strings.Join(d.Steps[0].Tools, ","); got != "Sheets" { // known tools only, once, canonical case
		t.Errorf("tools %q", got)
	}
	if len(d.Steps[1].Tools) != 0 { // a person step never lists tools
		t.Errorf("human tools %v", d.Steps[1].Tools)
	}
	for _, bad := range []string{
		"no json here",
		`{"name":"x","steps":[]}`,
		`{"name":"x","steps":[{"kind":"robot","title":"a","instruction":"b"}]}`,
		`{"name":"x","steps":[{"kind":"agent","title":"","instruction":"b"}]}`,
	} {
		if _, err := parseDraft(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	long := `{"name":"x","steps":[` + strings.Repeat(`{"kind":"agent","title":"a","instruction":"b"},`, 12) + `{"kind":"agent","title":"a","instruction":"b"}]}`
	if d, err := parseDraft(long); err != nil || len(d.Steps) != maxDraftSteps {
		t.Errorf("long draft: %v %d", err, len(d.Steps))
	}
}

func TestDraftEndpoint(t *testing.T) {
	an := newScriptedAnthropic(t, finalText(goodDraft))
	e := newEnv(t, envOpts{anthropicURL: an.URL, serverKey: "k"})
	p := e.signup("Ann", "ann@example.com")
	path := p.path("/workflows/draft")

	p.mustCall(400, "POST", path, map[string]string{"description": "short"}, nil)
	p.mustCall(400, "POST", path, map[string]string{"description": strings.Repeat("x", maxDraftDescription+1)}, nil)

	var out draftResult
	p.mustCall(200, "POST", path, map[string]string{"description": "Every Monday, summarise the numbers and have Mo approve"}, &out)
	if out.Name != "Weekly report" || len(out.Steps) != 2 || out.Steps[1].Kind != "human" {
		t.Fatalf("%+v", out)
	}
	// nothing is saved by drafting
	var list []Summary
	p.mustCall(200, "GET", p.path("/workflows"), nil, &list)
	if len(list) != 1 {
		t.Fatalf("drafting created a workflow: %d", len(list))
	}
}

func TestDraftNeedsClaude(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Ann", "ann@example.com")
	var body map[string]string
	p.mustCall(409, "POST", p.path("/workflows/draft"), map[string]string{"description": "Every Monday, summarise the numbers"}, &body)
	if !strings.Contains(body["error"], "Claude") {
		t.Fatalf("%v", body)
	}
}

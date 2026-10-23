package main

import (
	"strings"
	"testing"
)

const goodAgent = "```json\n" + `{"name":"Release notes","role":"Turns merged PRs into notes","instruction":"You write release notes.\n1. Read the PRs.","tools":["github","Slack","GitHub"],"model":"claude opus"}` + "\n```"

func TestParseDraftAgent(t *testing.T) {
	a, err := parseDraftAgent(goodAgent)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Release notes" || !strings.HasPrefix(a.Instruction, "You write") {
		t.Fatalf("%+v", a)
	}
	if got := strings.Join(a.Tools, ","); got != "GitHub" { // known tools only, once, canonical case
		t.Errorf("tools %q", got)
	}
	if pickModel(goodAgent) != "Claude Opus" || pickModel(`{"model":"gpt-9"}`) != "Claude Sonnet" || pickModel("nope") != "Claude Sonnet" {
		t.Error("model choice")
	}
	for _, bad := range []string{"no json", `{"name":"","instruction":"x"}`, `{"name":"x","instruction":""}`} {
		if _, err := parseDraftAgent(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDraftAgentEndpoint(t *testing.T) {
	an := newScriptedAnthropic(t, finalText(goodAgent))
	e := newEnv(t, envOpts{anthropicURL: an.URL, serverKey: "k"})
	p := e.signup("Ann", "ann@example.com")
	path := p.path("/agents/draft")

	p.mustCall(400, "POST", path, map[string]string{"description": "short"}, nil)
	p.mustCall(400, "POST", path, map[string]string{"description": strings.Repeat("x", maxAgentDescription+1)}, nil)

	var out draftAgentResult
	p.mustCall(200, "POST", path, map[string]string{"description": "Turns merged pull requests into customer release notes"}, &out)
	if out.Name != "Release notes" || out.Model != "Claude Opus" || len(out.Tools) != 1 {
		t.Fatalf("%+v", out)
	}
	// improving existing instructions works without a fresh description
	p.mustCall(200, "POST", path, map[string]string{"current": "write release notes from the merged prs please"}, nil)
}

func TestDraftAgentNeedsClaude(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Ann", "ann@example.com")
	var body map[string]string
	p.mustCall(409, "POST", p.path("/agents/draft"), map[string]string{"description": "Turns merged pull requests into notes"}, &body)
	if !strings.Contains(body["error"], "Claude") {
		t.Fatalf("%v", body)
	}
}

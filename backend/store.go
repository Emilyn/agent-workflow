package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Node is one step in a workflow: a trigger, an agent or a person.
type Node struct {
	ID          string   `json:"id"`
	AgentID     string   `json:"agentId,omitempty"`
	Kind        string   `json:"kind"` // trigger | agent | human
	Title       string   `json:"title"`
	Desc        string   `json:"desc"`
	Who         string   `json:"who"`
	Assignee    string   `json:"assignee,omitempty"`
	Instruction string   `json:"instruction"`
	Tools       []string `json:"tools"`
}

// Agent is a reusable agent definition created by a user. Agents are shared by
// every workflow in the workspace.
type Agent struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Instruction string   `json:"instruction"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model"`
	Mine        bool     `json:"mine,omitempty"`
}

type Message struct {
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Kind      string `json:"kind"` // human | agent
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

// Output is what an agent step produced on the latest run. It is owned by the
// server: clients cannot overwrite it when they save a workflow.
type Output struct {
	Text      string   `json:"text"`
	Simulated bool     `json:"simulated"`
	Used      []string `json:"used,omitempty"` // tools the agent called on this run
}

// Workflow is one flow of steps with its own run state and chat.
type Workflow struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Steps    [][]Node          `json:"steps"`
	Progress int               `json:"progress"`
	NextID   int               `json:"nextId"`
	Messages []Message         `json:"messages"` // read-only here: chat has its own endpoints
	Outputs  map[string]Output `json:"outputs"`
	Version  int               `json:"version"` // bumped on every save; used to detect lost updates
	Updated  string            `json:"updated"`
}

// Workspace holds what all workflows share.
type Workspace struct {
	Connected    map[string]bool `json:"connected"`
	GitProvider  string          `json:"gitProvider"`
	CustomAgents []Agent         `json:"customAgents"`
}

// Summary is the short form shown in the workflow list.
type Summary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Steps    int    `json:"steps"`
	Agents   int    `json:"agents"`
	People   int    `json:"people"`
	Progress int    `json:"progress"`
	Updated  string `json:"updated"`
}

var (
	errNotFound     = errors.New("not found")
	errLastWorkflow = errors.New("keep at least one workflow")
	errConflict     = errors.New("this workflow was changed by someone else")
)

const (
	maxMessages  = 200 // returned with a workflow
	maxWorkflows = 200
	maxNameLen   = 80
	maxAgents    = 100
)

// newID returns a short random id such as "w3fa9c1d2e4b7". The prefix says what it names.
func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

// ---------- normalising and validating ----------

func (w *Workflow) normalize() {
	for i := range w.Steps {
		for j := range w.Steps[i] {
			if w.Steps[i][j].Tools == nil {
				w.Steps[i][j].Tools = []string{}
			}
		}
	}
	if w.Messages == nil {
		w.Messages = []Message{}
	}
	if w.Outputs == nil {
		w.Outputs = map[string]Output{}
	}
	if w.NextID < 1 {
		w.NextID = 1
	}
}

func (w *Workflow) validate() error {
	w.Name = strings.TrimSpace(w.Name)
	if w.Name == "" {
		return errors.New("a workflow needs a name")
	}
	if len([]rune(w.Name)) > maxNameLen {
		return fmt.Errorf("the name is longer than %d characters", maxNameLen)
	}
	if len(w.Steps) == 0 {
		return errors.New("a workflow needs at least one step")
	}
	seen := map[string]bool{}
	for i, g := range w.Steps {
		if len(g) == 0 {
			return fmt.Errorf("row %d has no steps", i+1)
		}
		for _, n := range g {
			if n.ID == "" {
				return fmt.Errorf("row %d has a step without an id", i+1)
			}
			if seen[n.ID] {
				return fmt.Errorf("duplicate step id %q", n.ID)
			}
			seen[n.ID] = true
			if n.Kind != "trigger" && n.Kind != "agent" && n.Kind != "human" {
				return fmt.Errorf("step %q has an unknown kind %q", n.ID, n.Kind)
			}
		}
	}
	if w.Progress < 0 || w.Progress > len(w.Steps) {
		return fmt.Errorf("progress %d is outside the workflow", w.Progress)
	}
	return nil
}

func (ws *Workspace) normalize() {
	if ws.Connected == nil {
		ws.Connected = map[string]bool{}
	}
	clean := map[string]bool{}
	for _, k := range []string{"git", "jira", "figma"} {
		clean[k] = ws.Connected[k]
	}
	ws.Connected = clean
	if ws.GitProvider != "gitlab" {
		ws.GitProvider = "github"
	}
	if ws.CustomAgents == nil {
		ws.CustomAgents = []Agent{}
	}
	for i := range ws.CustomAgents {
		if ws.CustomAgents[i].Tools == nil {
			ws.CustomAgents[i].Tools = []string{}
		}
	}
}

func (ws *Workspace) validate() error {
	if len(ws.CustomAgents) > maxAgents {
		return fmt.Errorf("at most %d agents", maxAgents)
	}
	seen := map[string]bool{}
	for _, a := range ws.CustomAgents {
		if a.ID == "" || strings.TrimSpace(a.Name) == "" {
			return errors.New("every agent needs an id and a name")
		}
		if seen[a.ID] {
			return fmt.Errorf("duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
	}
	return nil
}

// ---------- starting points ----------

const welcomeText = "Use the + between steps to add agents and people. Mention an agent by name here to add your message to its instructions."

func blankWorkflow() Workflow {
	return Workflow{
		NextID: 1,
		Steps: [][]Node{
			{{ID: "n1", Kind: "trigger", Title: "Start manually", Desc: "Starts the workflow when you press Run.", Who: "You", Tools: []string{}}},
		},
		Messages: []Message{},
	}
}

// reviewWorkflow reviews every new pull or merge request that a trigger sends it.
func reviewWorkflow() Workflow {
	return Workflow{
		NextID: 3,
		Steps: [][]Node{
			{{ID: "n1", Kind: "trigger", Title: "New pull or merge request", Desc: "Starts when GitHub or GitLab reports a new request in a project you choose (Settings → Review triggers).", Who: "GitHub or GitLab", Tools: []string{}}},
			{{ID: "n2", AgentID: "reviewer", Kind: "agent", Title: "Code reviewer", Desc: "Reads the changes and writes a review.", Who: "Claude Sonnet",
				Instruction: "Review the code changes in this request. Look for bugs, security problems, missing error handling or tests, and code that is hard to follow. Be specific and brief, point to exact lines, and say so plainly when the change looks good.",
				Tools:       []string{"GitHub", "GitLab"}}},
		},
		Messages: []Message{},
	}
}

// exampleWorkflow is the weekly digest a fresh install starts with.
func exampleWorkflow() Workflow {
	return Workflow{
		Name:     "Weekly customer digest",
		Progress: 0,
		NextID:   1,
		Steps: [][]Node{
			{{ID: "n1", Kind: "trigger", Title: "Every Monday, 9:00", Desc: "Starts the weekly run automatically.", Who: "Schedule"}},
			{
				{ID: "n2", AgentID: "research", Kind: "agent", Title: "Research agent", Desc: "Pulls last week’s tickets, reviews and call notes.", Who: "Claude Sonnet", Instruction: "Collect every support ticket, app review and call note from the last 7 days. Remove duplicates and anything marked private.", Tools: []string{"Web search", "Docs"}},
				{ID: "n3", AgentID: "analyst", Kind: "agent", Title: "Analyst agent", Desc: "Clusters themes and tracks sentiment shifts.", Who: "Claude Sonnet", Instruction: "Group feedback into themes, compare with the previous four weeks and flag anything that grew sharply.", Tools: []string{"Sheets"}},
			},
			{{ID: "n4", AgentID: "writer", Kind: "agent", Title: "Writer agent", Desc: "Drafts the digest in the team’s voice.", Who: "Claude Sonnet", Instruction: "Write the weekly digest in under 300 words. Lead with the top three themes and quote customers only from cleared tickets.", Tools: []string{"Docs"}}},
			{{ID: "n5", Kind: "human", Title: "Review and edit", Desc: "Check the draft, fix wording, confirm every quote.", Who: "Unassigned", Instruction: "Read the draft next to the source tickets. Edit anything unclear and confirm each customer quote is cleared."}},
			{{ID: "n6", Kind: "human", Title: "Approve and send", Desc: "Final yes or no before anything is shared.", Who: "Unassigned", Instruction: "Approve the reviewed digest, or send it back with a note for the Writer agent."}},
			{{ID: "n7", Kind: "agent", Title: "Distribution agent", Desc: "Posts to the insights channel and emails team leads.", Who: "Claude Sonnet", Instruction: "Post the approved digest to the insights channel and email it to team leads with a link to the full data.", Tools: []string{"Slack", "Email"}}},
		},
		Messages: []Message{},
	}
}

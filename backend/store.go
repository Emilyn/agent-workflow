package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Node is one step in the workflow: a trigger, an agent or a person.
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

// Agent is a reusable agent definition created by a user.
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
	Author string `json:"author"`
	Kind   string `json:"kind"` // human | agent
	Time   string `json:"time"`
	Text   string `json:"text"`
}

// Output is what an agent step produced on the latest run. It is owned by the
// server: clients cannot overwrite it through PUT /api/state.
type Output struct {
	Text      string `json:"text"`
	Simulated bool   `json:"simulated"`
}

type State struct {
	Name         string            `json:"name"`
	Steps        [][]Node          `json:"steps"`
	Progress     int               `json:"progress"`
	NextID       int               `json:"nextId"`
	Connected    map[string]bool   `json:"connected"`
	GitProvider  string            `json:"gitProvider"`
	CustomAgents []Agent           `json:"customAgents"`
	Messages     []Message         `json:"messages"`
	Outputs      map[string]Output `json:"outputs"`
}

const maxMessages = 200

// normalize makes sure nothing serialises as null.
func (s *State) normalize() {
	for i := range s.Steps {
		for j := range s.Steps[i] {
			if s.Steps[i][j].Tools == nil {
				s.Steps[i][j].Tools = []string{}
			}
		}
	}
	for i := range s.CustomAgents {
		if s.CustomAgents[i].Tools == nil {
			s.CustomAgents[i].Tools = []string{}
		}
	}
	if s.Connected == nil {
		s.Connected = map[string]bool{}
	}
	for _, k := range []string{"git", "jira", "figma"} {
		if _, ok := s.Connected[k]; !ok {
			s.Connected[k] = false
		}
	}
	if s.GitProvider != "gitlab" {
		s.GitProvider = "github"
	}
	if s.CustomAgents == nil {
		s.CustomAgents = []Agent{}
	}
	if s.Messages == nil {
		s.Messages = []Message{}
	}
	if len(s.Messages) > maxMessages {
		s.Messages = s.Messages[len(s.Messages)-maxMessages:]
	}
	if s.Outputs == nil {
		s.Outputs = map[string]Output{}
	}
}

func (s *State) validate() error {
	if len(s.Steps) == 0 {
		return errors.New("a workflow needs at least one step")
	}
	seen := map[string]bool{}
	for i, g := range s.Steps {
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
	if s.Progress < 0 || s.Progress > len(s.Steps) {
		return fmt.Errorf("progress %d is outside the workflow", s.Progress)
	}
	return nil
}

// Store keeps the workflow in memory and persists it to a JSON file.
type Store struct {
	mu    sync.Mutex
	path  string
	state State
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.state); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if err := s.state.validate(); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		s.state = SeedState()
	default:
		return nil, err
	}
	s.state.normalize()
	return s, nil
}

// Get returns a deep copy of the current state.
func (s *Store) Get() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyLocked()
}

func (s *Store) copyLocked() State {
	raw, _ := json.Marshal(s.state)
	var out State
	_ = json.Unmarshal(raw, &out)
	out.normalize()
	return out
}

// Replace swaps in the client's version of the workflow. Outputs stay as the
// server has them, minus outputs of steps that no longer exist.
func (s *Store) Replace(in State) error {
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]Output{}
	for _, g := range in.Steps {
		for _, n := range g {
			if o, ok := s.state.Outputs[n.ID]; ok {
				keep[n.ID] = o
			}
		}
	}
	in.Outputs = keep
	s.state = in
	return s.persistLocked()
}

func (s *Store) SetOutput(id string, o Output) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Outputs[id] = o
	return s.persistLocked()
}

func (s *Store) ResetOutputs() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Outputs = map[string]Output{}
	s.state.Progress = 0
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// SeedState is the example workflow a fresh install starts with.
func SeedState() State {
	return State{
		Name:     "Weekly customer digest",
		Progress: 4,
		NextID:   1,
		Steps: [][]Node{
			{{ID: "n1", Kind: "trigger", Title: "Every Monday, 9:00", Desc: "Starts the weekly run automatically.", Who: "Schedule"}},
			{
				{ID: "n2", AgentID: "research", Kind: "agent", Title: "Research agent", Desc: "Pulls last week’s tickets, reviews and call notes.", Who: "Claude Sonnet", Instruction: "Collect every support ticket, app review and call note from the last 7 days. Remove duplicates and anything marked private.", Tools: []string{"Web search", "Docs"}},
				{ID: "n3", AgentID: "analyst", Kind: "agent", Title: "Analyst agent", Desc: "Clusters themes and tracks sentiment shifts.", Who: "Claude Sonnet", Instruction: "Group feedback into themes, compare with the previous four weeks and flag anything that grew sharply.", Tools: []string{"Sheets"}},
			},
			{{ID: "n4", AgentID: "writer", Kind: "agent", Title: "Writer agent", Desc: "Drafts the digest in the team’s voice.", Who: "Claude Sonnet", Instruction: "Write the weekly digest in under 300 words. Lead with the top three themes and quote customers only from cleared tickets.", Tools: []string{"Docs"}}},
			{{ID: "n5", Kind: "human", Title: "Review and edit", Desc: "Check the draft, fix wording, confirm every quote.", Who: "Theo", Assignee: "Theo", Instruction: "Read the draft next to the source tickets. Edit anything unclear and confirm each customer quote is cleared."}},
			{{ID: "n6", Kind: "human", Title: "Approve and send", Desc: "Final yes or no before anything is shared.", Who: "Amara", Assignee: "Amara", Instruction: "Approve the reviewed digest, or send it back with a note for the Writer agent."}},
			{{ID: "n7", Kind: "agent", Title: "Distribution agent", Desc: "Posts to the insights channel and emails team leads.", Who: "Claude Sonnet", Instruction: "Post the approved digest to the insights channel and email it to team leads with a link to the full data.", Tools: []string{"Slack", "Email"}}},
		},
		Messages: []Message{
			{Author: "Amara", Kind: "human", Time: "9:12", Text: "Can we add a human check before the digest goes out? Last week it quoted a ticket we had not cleared."},
			{Author: "Claude", Kind: "agent", Time: "9:13", Text: "Done. I added a Review & edit step after the Writer and a final approval before sending. Theo, do you want to own the review?"},
			{Author: "Theo", Kind: "human", Time: "9:15", Text: "@Writer agent keep the digest under 300 words and lead with the top three themes."},
			{Author: "Writer agent", Kind: "agent", Time: "9:16", Text: "Understood. I will cap the draft at 300 words and open with the top three themes. The draft is ready for Theo to review."},
		},
	}
}

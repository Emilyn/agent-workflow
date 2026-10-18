package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	Author string `json:"author"`
	Kind   string `json:"kind"` // human | agent
	Time   string `json:"time"`
	Text   string `json:"text"`
}

// Output is what an agent step produced on the latest run. It is owned by the
// server: clients cannot overwrite it when they save a workflow.
type Output struct {
	Text      string `json:"text"`
	Simulated bool   `json:"simulated"`
}

// Workflow is one flow of steps with its own run state and chat.
type Workflow struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Steps    [][]Node          `json:"steps"`
	Progress int               `json:"progress"`
	NextID   int               `json:"nextId"`
	Messages []Message         `json:"messages"`
	Outputs  map[string]Output `json:"outputs"`
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

type fileData struct {
	Workspace Workspace  `json:"workspace"`
	Workflows []Workflow `json:"workflows"`
}

var (
	errNotFound     = errors.New("workflow not found")
	errLastWorkflow = errors.New("keep at least one workflow")
)

const (
	maxMessages  = 200
	maxWorkflows = 200
	maxNameLen   = 80
	maxAgents    = 100
)

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("w%d", time.Now().UnixNano())
	}
	return "w" + hex.EncodeToString(b)
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
	if len(w.Messages) > maxMessages {
		w.Messages = w.Messages[len(w.Messages)-maxMessages:]
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

// ---------- store ----------

// Store keeps all workflows in memory and persists them to one JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	data fileData
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		d, err := parseFile(raw)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		s.data = d
	case errors.Is(err, os.ErrNotExist):
		s.data = fileData{Workflows: []Workflow{exampleWorkflow()}}
	default:
		return nil, err
	}
	s.data.Workspace.normalize()
	for i := range s.data.Workflows {
		s.data.Workflows[i].normalize()
	}
	return s, nil
}

// legacyState is the single-workflow file written by earlier versions.
type legacyState struct {
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

func parseFile(raw []byte) (fileData, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fileData{}, err
	}
	var d fileData
	if _, ok := probe["workflows"]; ok {
		if err := json.Unmarshal(raw, &d); err != nil {
			return fileData{}, err
		}
	} else if _, ok := probe["steps"]; ok {
		var old legacyState
		if err := json.Unmarshal(raw, &old); err != nil {
			return fileData{}, err
		}
		d.Workspace = Workspace{Connected: old.Connected, GitProvider: old.GitProvider, CustomAgents: old.CustomAgents}
		d.Workflows = []Workflow{{
			ID: newID(), Name: old.Name, Steps: old.Steps, Progress: old.Progress, NextID: old.NextID,
			Messages: old.Messages, Outputs: old.Outputs, Updated: now(),
		}}
	} else {
		return fileData{}, errors.New("unrecognised file")
	}
	if len(d.Workflows) == 0 {
		return fileData{}, errors.New("the file has no workflows")
	}
	for i := range d.Workflows {
		d.Workflows[i].normalize()
		if d.Workflows[i].ID == "" {
			d.Workflows[i].ID = newID()
		}
		if err := d.Workflows[i].validate(); err != nil {
			return fileData{}, fmt.Errorf("workflow %q: %w", d.Workflows[i].Name, err)
		}
	}
	return d, nil
}

func clone[T any](v T) T {
	raw, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Store) indexLocked(id string) int {
	for i := range s.data.Workflows {
		if s.data.Workflows[i].ID == id {
			return i
		}
	}
	return -1
}

func summarize(w Workflow) Summary {
	sum := Summary{ID: w.ID, Name: w.Name, Steps: len(w.Steps), Progress: w.Progress, Updated: w.Updated}
	for _, g := range w.Steps {
		for _, n := range g {
			switch n.Kind {
			case "agent":
				sum.Agents++
			case "human":
				sum.People++
			}
		}
	}
	return sum
}

func (s *Store) List() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Summary, 0, len(s.data.Workflows))
	for _, w := range s.data.Workflows {
		out = append(out, summarize(w))
	}
	return out
}

func (s *Store) Get(id string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return Workflow{}, errNotFound
	}
	w := clone(s.data.Workflows[i])
	w.normalize()
	return w, nil
}

// Create adds a workflow. template is "blank" (default) or "example"; copyOf,
// when set, duplicates that workflow's steps instead.
func (s *Store) Create(name, template, copyOf string) (Workflow, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > maxNameLen {
		return Workflow{}, fmt.Errorf("the name is longer than %d characters", maxNameLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Workflows) >= maxWorkflows {
		return Workflow{}, fmt.Errorf("at most %d workflows", maxWorkflows)
	}
	var w Workflow
	switch {
	case copyOf != "":
		i := s.indexLocked(copyOf)
		if i < 0 {
			return Workflow{}, errNotFound
		}
		src := clone(s.data.Workflows[i])
		w = Workflow{Steps: src.Steps, NextID: src.NextID, Messages: []Message{welcomeMessage()}}
		if name == "" {
			name = "Copy of " + src.Name
		}
	case template == "example":
		w = exampleWorkflow()
	case template == "" || template == "blank":
		w = blankWorkflow()
	default:
		return Workflow{}, fmt.Errorf("unknown template %q", template)
	}
	if name == "" {
		name = "Untitled workflow"
	}
	if len([]rune(name)) > maxNameLen {
		name = string([]rune(name)[:maxNameLen])
	}
	w.ID = newID()
	w.Name = name
	w.Updated = now()
	w.normalize()
	if err := w.validate(); err != nil {
		return Workflow{}, err
	}
	s.data.Workflows = append(s.data.Workflows, w)
	if err := s.persistLocked(); err != nil {
		s.data.Workflows = s.data.Workflows[:len(s.data.Workflows)-1]
		return Workflow{}, err
	}
	return clone(w), nil
}

// Replace saves the client's version of a workflow. Outputs stay as the server
// has them, minus outputs of steps that no longer exist.
func (s *Store) Replace(id string, in Workflow) (Workflow, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return Workflow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return Workflow{}, errNotFound
	}
	keep := map[string]Output{}
	for _, g := range in.Steps {
		for _, n := range g {
			if o, ok := s.data.Workflows[i].Outputs[n.ID]; ok {
				keep[n.ID] = o
			}
		}
	}
	in.ID = id
	in.Outputs = keep
	in.Updated = now()
	old := s.data.Workflows[i]
	s.data.Workflows[i] = in
	if err := s.persistLocked(); err != nil {
		s.data.Workflows[i] = old
		return Workflow{}, err
	}
	return clone(in), nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return errNotFound
	}
	if len(s.data.Workflows) == 1 {
		return errLastWorkflow
	}
	old := s.data.Workflows
	s.data.Workflows = append(append([]Workflow{}, old[:i]...), old[i+1:]...)
	if err := s.persistLocked(); err != nil {
		s.data.Workflows = old
		return err
	}
	return nil
}

func (s *Store) SetOutput(id, nodeID string, o Output) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return errNotFound
	}
	s.data.Workflows[i].Outputs[nodeID] = o
	return s.persistLocked()
}

func (s *Store) ResetOutputs(id string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.indexLocked(id)
	if i < 0 {
		return Workflow{}, errNotFound
	}
	s.data.Workflows[i].Outputs = map[string]Output{}
	s.data.Workflows[i].Progress = 0
	if err := s.persistLocked(); err != nil {
		return Workflow{}, err
	}
	return clone(s.data.Workflows[i]), nil
}

func (s *Store) Workspace() Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := clone(s.data.Workspace)
	ws.normalize()
	return ws
}

func (s *Store) SetWorkspace(ws Workspace) (Workspace, error) {
	ws.normalize()
	if err := ws.validate(); err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Workspace
	s.data.Workspace = ws
	if err := s.persistLocked(); err != nil {
		s.data.Workspace = old
		return Workspace{}, err
	}
	return clone(ws), nil
}

func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---------- starting points ----------

func welcomeMessage() Message {
	return Message{
		Author: "Claude", Kind: "agent", Time: time.Now().Format("15:04"),
		Text: "Add agents and people from the left. Mention an agent by name here to add your message to its instructions.",
	}
}

func blankWorkflow() Workflow {
	return Workflow{
		NextID: 1,
		Steps: [][]Node{
			{{ID: "n1", Kind: "trigger", Title: "Start manually", Desc: "Starts the workflow when you press Run.", Who: "You", Tools: []string{}}},
		},
		Messages: []Message{welcomeMessage()},
	}
}

// exampleWorkflow is the weekly digest a fresh install starts with.
func exampleWorkflow() Workflow {
	return Workflow{
		ID:       newID(),
		Name:     "Weekly customer digest",
		Progress: 4,
		NextID:   1,
		Updated:  now(),
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

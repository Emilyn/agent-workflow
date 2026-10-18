package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Server struct {
	store     *Store
	claude    *Claude
	credsPath string // where a user-connected API key is stored
	envKey    bool   // true when the key comes from ANTHROPIC_API_KEY and cannot be changed in the app
}

func (s *Server) Routes(staticDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)

	mux.HandleFunc("GET /api/claude", s.getClaude)
	mux.HandleFunc("PUT /api/claude/key", s.putClaudeKey)
	mux.HandleFunc("DELETE /api/claude/key", s.deleteClaudeKey)

	mux.HandleFunc("GET /api/workspace", s.getWorkspace)
	mux.HandleFunc("PUT /api/workspace", s.putWorkspace)

	mux.HandleFunc("GET /api/workflows", s.listWorkflows)
	mux.HandleFunc("POST /api/workflows", s.createWorkflow)
	mux.HandleFunc("GET /api/workflows/{id}", s.getWorkflow)
	mux.HandleFunc("PUT /api/workflows/{id}", s.putWorkflow)
	mux.HandleFunc("DELETE /api/workflows/{id}", s.deleteWorkflow)
	mux.HandleFunc("POST /api/workflows/{id}/run/step", s.runStep)
	mux.HandleFunc("POST /api/workflows/{id}/run/reset", s.runReset)
	mux.HandleFunc("POST /api/workflows/{id}/chat", s.chat)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.Handle("/", spa(staticDir))
	return logRequests(mux)
}

// ---------- plumbing ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// storeError maps store errors to HTTP statuses.
func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errLastWorkflow):
		writeError(w, http.StatusConflict, "Keep at least one workflow. Create another before deleting this one.")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

// spa serves the built frontend and falls back to index.html for client routes.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(index); err != nil {
			writeError(w, http.StatusNotFound, "frontend is not built yet: run `npm run build` in frontend/ or use the Vite dev server")
			return
		}
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "claude": s.claude.Enabled(), "model": s.claude.Model})
}

// ---------- workspace (shared by all workflows) ----------

func (s *Server) getWorkspace(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Workspace())
}

func (s *Server) putWorkspace(w http.ResponseWriter, r *http.Request) {
	var in Workspace
	if !decode(w, r, &in) {
		return
	}
	out, err := s.store.SetWorkspace(in)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- workflows ----------

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.List())
}

type createRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"` // blank | example
	CopyOf   string `json:"copyOf"`
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}
	wf, err := s.store.Create(req.Name, req.Template, req.CopyOf)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wf)
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	wf, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

func (s *Server) putWorkflow(w http.ResponseWriter, r *http.Request) {
	var in Workflow
	if !decode(w, r, &in) {
		return
	}
	wf, err := s.store.Replace(r.PathValue("id"), in)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.List())
}

func (s *Server) runReset(w http.ResponseWriter, r *http.Request) {
	wf, err := s.store.ResetOutputs(r.PathValue("id"))
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

// ---------- running a step ----------

type runRequest struct {
	StepID string `json:"stepId"`
}

type runResponse struct {
	StepID    string `json:"stepId"`
	Text      string `json:"text"`
	Simulated bool   `json:"simulated"`
}

func locate(steps [][]Node, id string) (int, *Node) {
	for i, g := range steps {
		for j := range g {
			if g[j].ID == id {
				return i, &steps[i][j]
			}
		}
	}
	return -1, nil
}

// previousInput gathers the outputs of the nearest earlier row that has any.
func previousInput(wf Workflow, row int) string {
	for i := row - 1; i >= 0; i-- {
		var parts []string
		for _, n := range wf.Steps[i] {
			if o, ok := wf.Outputs[n.ID]; ok && strings.TrimSpace(o.Text) != "" {
				parts = append(parts, fmt.Sprintf("From %s:\n%s", n.Title, o.Text))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}
	return ""
}

func stepPrompts(wf Workflow, n *Node, input string) (system, user string) {
	system = fmt.Sprintf(
		"You are %q, one agent step in a team workflow called %q. Do only your own step.\n\nYour instructions:\n%s\n\n"+
			"You cannot call external tools in this version, so work from the material you are given and say plainly when something you would need is missing. "+
			"Write the output the next step or person will use, with no preamble.",
		n.Title, wf.Name, n.Instruction)
	if input == "" {
		user = "There is no output from an earlier step. Do your step from your instructions alone."
	} else {
		user = "Output from the earlier step:\n\n" + input + "\n\nDo your step now."
	}
	return system, user
}

func (s *Server) runStep(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if !decode(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	wf, err := s.store.Get(id)
	if err != nil {
		storeError(w, err)
		return
	}
	row, n := locate(wf.Steps, req.StepID)
	if n == nil {
		writeError(w, http.StatusNotFound, "no such step")
		return
	}
	if n.Kind != "agent" {
		writeError(w, http.StatusConflict, "only agent steps run automatically; people finish their own steps")
		return
	}
	system, user := stepPrompts(wf, n, previousInput(wf, row))
	fallback := fmt.Sprintf("Simulated output. Connect an Anthropic API key (Integrations tab) to run %s with Claude.\n\nIt would follow: %s", n.Title, n.Instruction)
	text, simulated, err := s.generate(r.Context(), system, user, fallback, 1500)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.store.SetOutput(id, n.ID, Output{Text: text, Simulated: simulated}); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runResponse{StepID: n.ID, Text: text, Simulated: simulated})
}

// generate calls Claude when a key is connected, otherwise returns the fallback.
func (s *Server) generate(ctx context.Context, system, user, fallback string, maxTokens int) (string, bool, error) {
	if !s.claude.Enabled() {
		return fallback, true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 85*time.Second)
	defer cancel()
	text, err := s.claude.Complete(ctx, system, user, maxTokens)
	if err != nil {
		return "", false, err
	}
	return text, false, nil
}

// ---------- chat ----------

type chatRequest struct {
	AgentName   string `json:"agentName"`
	Instruction string `json:"instruction"`
	Message     string `json:"message"`
}

type chatResponse struct {
	Reply     string `json:"reply"`
	Simulated bool   `json:"simulated"`
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if !decode(w, r, &req) {
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is empty")
		return
	}
	if len(req.Message) > 4000 {
		writeError(w, http.StatusBadRequest, "message is too long")
		return
	}
	wf, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		storeError(w, err)
		return
	}
	var outline []string
	for i, g := range wf.Steps {
		var titles []string
		for _, n := range g {
			titles = append(titles, n.Title)
		}
		outline = append(outline, fmt.Sprintf("%d. %s", i+1, strings.Join(titles, " + ")))
	}

	var system, fallback string
	if req.AgentName != "" {
		system = fmt.Sprintf(
			"You are %q, an agent in a team workflow called %q. Your standing instructions:\n%s\n\n"+
				"A teammate has just messaged you in the team chat. Their message has been added to your instructions for the next run. "+
				"Reply in one or two plain sentences: confirm what you will do differently, or ask one short question if it is unclear. Do not use lists.",
			req.AgentName, wf.Name, req.Instruction)
		fallback = "Understood. I have added that to my instructions and will follow it on the next run."
	} else {
		system = fmt.Sprintf(
			"You are Claude, helping a team build a workflow called %q that mixes agents and people. The steps are:\n%s\n\n"+
				"Answer the teammate in at most three plain sentences. Suggest a concrete change to the workflow when it helps. Do not use lists.",
			wf.Name, strings.Join(outline, "\n"))
		fallback = "Noted. Connect an Anthropic API key in the Integrations tab and I will be able to answer properly."
	}
	reply, simulated, err := s.generate(r.Context(), system, req.Message, fallback, 400)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chatResponse{Reply: reply, Simulated: simulated})
}

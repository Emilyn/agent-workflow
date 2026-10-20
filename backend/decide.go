package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// A person's decision on the step a run is paused at: approve it, which lets the run go on, or send it
// back with a note, which re-runs the agent step before it with that note added to its instructions.

const maxDecisionNote = 1000

var (
	errNotAssignee = errors.New("only the person this step is assigned to, or an admin, can decide it")
	errNotWaiting  = errors.New("this step is no longer waiting. Someone may have decided it already")
)

type decision struct {
	Decision string `json:"decision"` // approve | send_back
	Note     string `json:"note"`
}

// DecideStep applies a decision to the paused step stepID of workflow id, in one transaction.
// It returns the updated workflow and a short line for the workflow's chat.
func (r *Repo) DecideStep(ctx context.Context, wsID, id, stepID string, user User, role string, d decision) (Workflow, string, error) {
	var line string
	err := r.tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanWorkflow(tx.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, id, wsID))
		if err != nil {
			return err
		}
		if cur.Progress >= len(cur.Steps) {
			return errConflict
		}
		var node *Node
		for i := range cur.Steps[cur.Progress] {
			if n := &cur.Steps[cur.Progress][i]; n.ID == stepID && n.Kind == "human" {
				node = n
			}
		}
		if node == nil {
			return errNotWaiting
		}
		if node.Assignee != user.Name && roleRank[role] < roleRank["admin"] {
			return errNotAssignee
		}
		progress := cur.Progress + 1
		switch d.Decision {
		case "approve":
			line = fmt.Sprintf("%s approved “%s”.", user.Name, node.Title)
			if d.Note != "" {
				line += " Note: " + d.Note
			}
		case "send_back":
			back := -1
			for j := cur.Progress - 1; j >= 0 && back < 0; j-- {
				for _, n := range cur.Steps[j] {
					if n.Kind == "agent" {
						back = j
					}
				}
			}
			if back < 0 {
				return validationError{"there is no agent step before this one to send it back to"}
			}
			var names []string
			for i := range cur.Steps[back] {
				if n := &cur.Steps[back][i]; n.Kind == "agent" {
					n.Instruction += "\n\nTeam note from " + user.Name + ": " + d.Note
					names = append(names, n.Title)
				}
			}
			for j := back; j < len(cur.Steps); j++ {
				for _, n := range cur.Steps[j] {
					delete(cur.Outputs, n.ID)
				}
			}
			progress = back
			line = fmt.Sprintf("%s sent “%s” back to %s: %s", user.Name, node.Title, strings.Join(names, ", "), d.Note)
		default:
			return validationError{"decision must be approve or send_back"}
		}
		steps, _ := json.Marshal(cur.Steps)
		outputs, _ := json.Marshal(cur.Outputs)
		_, err = tx.ExecContext(ctx, `UPDATE workflows SET steps=$3, progress=$4, outputs=$5, version=version+1, updated_at=now() WHERE id=$1 AND workspace_id=$2`, id, wsID, steps, progress, outputs)
		return err
	})
	if err != nil {
		return Workflow{}, "", err
	}
	wf, err := r.GetWorkflow(ctx, wsID, id)
	return wf, line, err
}

func (s *Server) decideStep(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var d decision
	if !decode(w, r, &d) {
		return
	}
	d.Note = clip(d.Note, maxDecisionNote)
	if d.Decision == "send_back" && d.Note == "" {
		writeError(w, http.StatusBadRequest, "Say what should change, so the agent knows what to fix.")
		return
	}
	wf, line, err := s.repo.DecideStep(r.Context(), c.WS, r.PathValue("id"), r.PathValue("step"), c.User, c.Role, d)
	switch {
	case errors.Is(err, errNotAssignee):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case errors.Is(err, errNotWaiting), errors.Is(err, errConflict):
		writeError(w, http.StatusConflict, errNotWaiting.Error())
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	if _, err := s.repo.AddMessage(r.Context(), c.WS, wf.ID, "", "Claude", "agent", line); err != nil {
		s.log.Warn("could not note the decision in chat", "err", err)
	}
	if ok, err := s.repo.ResumeStart(r.Context(), wf.ID); err == nil && ok {
		s.wakeWorker()
	}
	writeJSON(w, http.StatusOK, summarize(wf))
}

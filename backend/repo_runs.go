package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"
)

// ---------- types ----------

// changeEvent describes the pull request or merge request a run is about.
type changeEvent struct {
	Provider    string `json:"provider"`
	Project     string `json:"project"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Author      string `json:"author"`
	HeadSHA     string `json:"headSha"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	Description string `json:"description"`
}

type reviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

type Trigger struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	WorkflowID  string `json:"workflowId"`
	Provider    string `json:"provider"`
	Project     string `json:"project"`
	ReviewMode  string `json:"reviewMode"`
	OnPush      bool   `json:"onPush"`
	Enabled     bool   `json:"enabled"`
	HookID      string `json:"-"`
	HookStatus  string `json:"hookStatus"` // registered | manual
	HookError   string `json:"hookError,omitempty"`
	CreatedAt   string `json:"createdAt"`
	SecretEnc   []byte `json:"-"`
}

type RunStep struct {
	StepID    string   `json:"stepId"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	Used      []string `json:"used,omitempty"`
	Simulated bool     `json:"simulated,omitempty"`
}

type ReviewAction struct {
	ID        string          `json:"id"`
	RunID     string          `json:"runId"`
	Provider  string          `json:"provider"`
	Project   string          `json:"project"`
	Number    int             `json:"number"`
	Summary   string          `json:"summary"`
	Comments  []reviewComment `json:"comments"`
	Status    string          `json:"status"` // pending | posting | posted | discarded | failed
	Result    string          `json:"result,omitempty"`
	CreatedAt string          `json:"createdAt"`
}

type Run struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"-"`
	WorkflowID  string         `json:"workflowId"`
	TriggerID   string         `json:"triggerId,omitempty"`
	Source      string         `json:"source"`
	ReviewMode  string         `json:"reviewMode"`
	Status      string         `json:"status"`
	Input       changeEvent    `json:"input"`
	Steps       []RunStep      `json:"steps"`
	Error       string         `json:"error,omitempty"`
	Attempts    int            `json:"-"`
	CreatedAt   string         `json:"createdAt"`
	StartedAt   string         `json:"startedAt,omitempty"`
	FinishedAt  string         `json:"finishedAt,omitempty"`
	Actions     []ReviewAction `json:"actions"`
}

// ---------- triggers ----------

const triggerCols = `id, workspace_id, workflow_id, provider, project, review_mode, on_push, enabled, hook_id, hook_status, hook_error, created_at, secret_enc`

func scanTrigger(sc interface{ Scan(...any) error }) (Trigger, error) {
	var t Trigger
	var at time.Time
	err := sc.Scan(&t.ID, &t.WorkspaceID, &t.WorkflowID, &t.Provider, &t.Project, &t.ReviewMode, &t.OnPush, &t.Enabled, &t.HookID, &t.HookStatus, &t.HookError, &at, &t.SecretEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNotFound
	}
	t.CreatedAt = at.UTC().Format(rfc)
	return t, err
}

func (r *Repo) CreateTrigger(ctx context.Context, t Trigger, userID string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO triggers (id, workspace_id, workflow_id, provider, project, secret_enc, review_mode, on_push, hook_id, hook_status, hook_error, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, t.ID, t.WorkspaceID, t.WorkflowID, t.Provider, t.Project, t.SecretEnc, t.ReviewMode, t.OnPush, t.HookID, t.HookStatus, t.HookError, userID)
	return err
}

func (r *Repo) ListTriggers(ctx context.Context, wsID string) ([]Trigger, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+triggerCols+` FROM triggers WHERE workspace_id = $1 ORDER BY created_at`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Trigger{}
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) GetTrigger(ctx context.Context, wsID, id string) (Trigger, error) {
	return scanTrigger(r.db.QueryRowContext(ctx, `SELECT `+triggerCols+` FROM triggers WHERE id = $1 AND workspace_id = $2`, id, wsID))
}

// TriggerByID is for the public webhook endpoint: the unguessable id is the address.
func (r *Repo) TriggerByID(ctx context.Context, id string) (Trigger, error) {
	return scanTrigger(r.db.QueryRowContext(ctx, `SELECT `+triggerCols+` FROM triggers WHERE id = $1`, id))
}

func (r *Repo) UpdateTrigger(ctx context.Context, wsID, id string, enabled *bool, mode *string, onPush *bool) error {
	res, err := r.db.ExecContext(ctx, `UPDATE triggers SET enabled = COALESCE($3, enabled), review_mode = COALESCE($4, review_mode), on_push = COALESCE($5, on_push)
		WHERE id = $1 AND workspace_id = $2`, id, wsID, enabled, mode, onPush)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (r *Repo) DeleteTrigger(ctx context.Context, wsID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM triggers WHERE id = $1 AND workspace_id = $2`, id, wsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// ---------- runs ----------

const runCols = `id, workspace_id, workflow_id, COALESCE(trigger_id,''), source, review_mode, status, input, steps, error, attempts, created_at, started_at, finished_at`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var run Run
	var in, steps []byte
	var created time.Time
	var started, finished sql.NullTime
	err := sc.Scan(&run.ID, &run.WorkspaceID, &run.WorkflowID, &run.TriggerID, &run.Source, &run.ReviewMode, &run.Status, &in, &steps, &run.Error, &run.Attempts, &created, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return run, errNotFound
	}
	if err != nil {
		return run, err
	}
	_ = json.Unmarshal(in, &run.Input)
	_ = json.Unmarshal(steps, &run.Steps)
	if run.Steps == nil {
		run.Steps = []RunStep{}
	}
	run.Actions = []ReviewAction{}
	run.CreatedAt = created.UTC().Format(rfc)
	if started.Valid {
		run.StartedAt = started.Time.UTC().Format(rfc)
	}
	if finished.Valid {
		run.FinishedAt = finished.Time.UTC().Format(rfc)
	}
	return run, nil
}

// ActiveRuns counts queued and running runs of a workspace, to refuse floods.
func (r *Repo) ActiveRuns(ctx context.Context, wsID string) (n int, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE workspace_id = $1 AND status IN ('queued','running')`, wsID).Scan(&n)
	return n, err
}

// EnqueueRun adds a run. It returns false when the same event was already queued (dedupeKey).
func (r *Repo) EnqueueRun(ctx context.Context, run Run, dedupeKey, userID string) (bool, error) {
	in, _ := json.Marshal(run.Input)
	var dk, trig, by any
	if dedupeKey != "" {
		dk = dedupeKey
	}
	if run.TriggerID != "" {
		trig = run.TriggerID
	}
	if userID != "" {
		by = userID
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO runs (id, workspace_id, workflow_id, trigger_id, source, review_mode, input, dedupe_key, started_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, run.ID, run.WorkspaceID, run.WorkflowID, trig, run.Source, run.ReviewMode, in, dk, by)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ClaimRun takes the oldest runnable run. A run whose worker died (lease over) is taken again, up to three attempts.
func (r *Repo) ClaimRun(ctx context.Context) (Run, bool, error) {
	run, err := scanRun(r.db.QueryRowContext(ctx, `UPDATE runs SET status = 'running', attempts = attempts + 1, started_at = COALESCE(started_at, now()), locked_until = now() + interval '10 minutes', steps = '[]'
		WHERE id = (SELECT id FROM runs WHERE run_after <= now() AND attempts < 3 AND (status = 'queued' OR (status = 'running' AND locked_until < now()))
		             ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+runCols))
	if errors.Is(err, errNotFound) {
		return Run{}, false, nil
	}
	return run, err == nil, err
}

func (r *Repo) SaveRunSteps(ctx context.Context, id string, steps []RunStep) error {
	raw, _ := json.Marshal(steps)
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET steps = $2, locked_until = now() + interval '10 minutes' WHERE id = $1`, id, raw)
	return err
}

func (r *Repo) FinishRun(ctx context.Context, id, status, errText string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET status = $2, error = $3, finished_at = now(), locked_until = NULL WHERE id = $1`, id, status, errText)
	return err
}

// RequeueRun puts a run back for later without counting the attempt (the Claude rate limit was hit).
func (r *Repo) RequeueRun(ctx context.Context, id string, delay time.Duration) error {
	_, err := r.db.ExecContext(ctx, `UPDATE runs SET status = 'queued', attempts = GREATEST(attempts - 1, 0), run_after = now() + $2 * interval '1 second', locked_until = NULL WHERE id = $1`, id, int(delay.Seconds()))
	return err
}

func (r *Repo) GetRun(ctx context.Context, wsID, id string) (Run, error) {
	run, err := scanRun(r.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = $1 AND workspace_id = $2`, id, wsID))
	if err != nil {
		return run, err
	}
	acts, err := r.ActionsForRuns(ctx, wsID, []string{id})
	run.Actions = acts[id]
	return run, err
}

func (r *Repo) ListRuns(ctx context.Context, wsID, workflowID string, limit int) ([]Run, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+runCols+` FROM runs WHERE workspace_id = $1 AND workflow_id = $2 ORDER BY created_at DESC LIMIT $3`, wsID, workflowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	var ids []string
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
		ids = append(ids, run.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	acts, err := r.ActionsForRuns(ctx, wsID, ids)
	for i := range out {
		if a := acts[out[i].ID]; a != nil {
			out[i].Actions = a
		}
	}
	return out, err
}

// ---------- review actions ----------

const actionCols = `id, run_id, provider, project, number, summary, comments, status, result, created_at`

func scanAction(sc interface{ Scan(...any) error }) (ReviewAction, error) {
	var a ReviewAction
	var cm []byte
	var at time.Time
	err := sc.Scan(&a.ID, &a.RunID, &a.Provider, &a.Project, &a.Number, &a.Summary, &cm, &a.Status, &a.Result, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return a, errNotFound
	}
	_ = json.Unmarshal(cm, &a.Comments)
	if a.Comments == nil {
		a.Comments = []reviewComment{}
	}
	a.CreatedAt = at.UTC().Format(rfc)
	return a, err
}

func (r *Repo) AddAction(ctx context.Context, wsID string, a ReviewAction) error {
	cm, _ := json.Marshal(a.Comments)
	_, err := r.db.ExecContext(ctx, `INSERT INTO review_actions (id, workspace_id, run_id, provider, project, number, summary, comments, status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		a.ID, wsID, a.RunID, a.Provider, a.Project, a.Number, a.Summary, cm, a.Status)
	return err
}

func (r *Repo) ActionsForRuns(ctx context.Context, wsID string, runIDs []string) (map[string][]ReviewAction, error) {
	out := map[string][]ReviewAction{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+actionCols+` FROM review_actions WHERE workspace_id = $1 AND run_id = ANY($2) ORDER BY created_at`, wsID, pq.Array(runIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out[a.RunID] = append(out[a.RunID], a)
	}
	return out, rows.Err()
}

func (r *Repo) GetAction(ctx context.Context, wsID, id string) (ReviewAction, error) {
	return scanAction(r.db.QueryRowContext(ctx, `SELECT `+actionCols+` FROM review_actions WHERE id = $1 AND workspace_id = $2`, id, wsID))
}

// ClaimAction moves a pending draft to the given status exactly once (approve or discard).
func (r *Repo) ClaimAction(ctx context.Context, wsID, id, status, userID string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE review_actions SET status = $3, decided_by = $4, decided_at = now() WHERE id = $1 AND workspace_id = $2 AND status = 'pending'`, id, wsID, status, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (r *Repo) FinishAction(ctx context.Context, id, status, result string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE review_actions SET status = $2, result = $3 WHERE id = $1`, id, status, result)
	return err
}

// PostedAction returns the review already posted for a run, so a retried run does not post twice.
func (r *Repo) PostedAction(ctx context.Context, runID string) (string, error) {
	var res string
	err := r.db.QueryRowContext(ctx, `SELECT result FROM review_actions WHERE run_id = $1 AND status = 'posted' LIMIT 1`, runID).Scan(&res)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return res, err
}

func (r *Repo) CountActions(ctx context.Context, runID string) (n int, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT count(*) FROM review_actions WHERE run_id = $1`, runID).Scan(&n)
	return n, err
}

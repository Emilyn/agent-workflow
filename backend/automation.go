package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Automatic starts of a workflow: schedules, a secret webhook address, and a manual "run in the
// background". Each start waits in a queue until its workflow is free, then a server-side engine
// runs the agent steps, pauses at a step for a person, and carries on after their decision.

const (
	maxQueuedStarts = 5  // per workflow; more are recorded as skipped
	maxSchedules    = 10 // per workflow
	maxHookBody     = 64 << 10
	maxHookInput    = 8000
)

// ---------- schedules ----------

type Schedule struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflowId"`
	Freq       string `json:"freq"` // daily | weekly | monthly
	AtTime     string `json:"atTime"`
	Weekdays   int    `json:"weekdays"`
	DayOfMonth int    `json:"dayOfMonth"`
	Timezone   string `json:"timezone"`
	Enabled    bool   `json:"enabled"`
	NextRunAt  string `json:"nextRunAt,omitempty"`
	LastRunAt  string `json:"lastRunAt,omitempty"`
}

var clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

func (sc *Schedule) validate() error {
	if sc.Freq != "daily" && sc.Freq != "weekly" && sc.Freq != "monthly" {
		return errors.New("choose daily, weekly or monthly")
	}
	if !clockPattern.MatchString(sc.AtTime) {
		return errors.New("time must look like 09:30")
	}
	if _, err := time.LoadLocation(sc.Timezone); err != nil || sc.Timezone == "" || sc.Timezone == "Local" {
		return errors.New("that time zone is not known")
	}
	switch sc.Freq {
	case "weekly":
		if sc.Weekdays <= 0 || sc.Weekdays > 127 {
			return errors.New("choose at least one day of the week")
		}
	case "monthly":
		if sc.DayOfMonth < 1 || sc.DayOfMonth > 31 {
			return errors.New("day of the month must be from 1 to 31")
		}
	}
	return nil
}

// nextOccurrence is the first time after `after` that the schedule fires, in UTC. A monthly day that a
// month does not have (the 31st in June) fires on that month's last day. Times that do not exist at a
// daylight saving change move forward, as time.Date does.
func nextOccurrence(sc Schedule, after time.Time) time.Time {
	loc, err := time.LoadLocation(sc.Timezone)
	if err != nil {
		loc = time.UTC
	}
	var hh, mm int
	fmt.Sscanf(sc.AtTime, "%d:%d", &hh, &mm)
	local := after.In(loc)
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, hh, mm, 0, 0, loc) }
	switch sc.Freq {
	case "weekly":
		for i := 0; i <= 7; i++ {
			d := local.AddDate(0, 0, i)
			if sc.Weekdays&(1<<uint(d.Weekday())) == 0 {
				continue
			}
			if c := at(d.Year(), d.Month(), d.Day()); c.After(after) {
				return c.UTC()
			}
		}
	case "monthly":
		for i := 0; i <= 13; i++ {
			first := time.Date(local.Year(), local.Month()+time.Month(i), 1, 0, 0, 0, 0, loc)
			last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, loc).Day()
			day := min(sc.DayOfMonth, last)
			if c := at(first.Year(), first.Month(), day); c.After(after) {
				return c.UTC()
			}
		}
	}
	// daily, and the fallback
	for i := 0; i <= 2; i++ {
		d := local.AddDate(0, 0, i)
		if c := at(d.Year(), d.Month(), d.Day()); c.After(after) {
			return c.UTC()
		}
	}
	return after.Add(24 * time.Hour)
}

const scheduleCols = `id, workflow_id, freq, at_time, weekdays, day_of_month, timezone, enabled, next_run_at, last_run_at`

func scanSchedule(sc interface{ Scan(...any) error }) (Schedule, error) {
	var s Schedule
	var next, last sql.NullTime
	if err := sc.Scan(&s.ID, &s.WorkflowID, &s.Freq, &s.AtTime, &s.Weekdays, &s.DayOfMonth, &s.Timezone, &s.Enabled, &next, &last); err != nil {
		return s, err
	}
	if next.Valid {
		s.NextRunAt = next.Time.UTC().Format(rfc)
	}
	if last.Valid {
		s.LastRunAt = last.Time.UTC().Format(rfc)
	}
	return s, nil
}

func (r *Repo) ListSchedules(ctx context.Context, wsID, wfID string) ([]Schedule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE workspace_id = $1 AND workflow_id = $2 ORDER BY created_at, id`, wsID, wfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) GetSchedule(ctx context.Context, wsID, id string) (Schedule, error) {
	s, err := scanSchedule(r.db.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = $1 AND workspace_id = $2`, id, wsID))
	if errors.Is(err, sql.ErrNoRows) {
		return s, errNotFound
	}
	return s, err
}

func (r *Repo) CreateSchedule(ctx context.Context, wsID, userID string, s Schedule) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schedules WHERE workflow_id = $1 AND workspace_id = $2`, s.WorkflowID, wsID).Scan(&n); err != nil {
			return err
		}
		if n >= maxSchedules {
			return validationError{fmt.Sprintf("a workflow can have at most %d schedules", maxSchedules)}
		}
		next := nextOccurrence(s, time.Now())
		res, err := tx.ExecContext(ctx, `INSERT INTO schedules (id, workspace_id, workflow_id, freq, at_time, weekdays, day_of_month, timezone, enabled, next_run_at, created_by)
			SELECT $1, $2, w.id, $4, $5, $6, $7, $8, true, $9, $10 FROM workflows w WHERE w.id = $3 AND w.workspace_id = $2`,
			s.ID, wsID, s.WorkflowID, s.Freq, s.AtTime, s.Weekdays, s.DayOfMonth, s.Timezone, next, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound
		}
		return nil
	})
}

// SetScheduleEnabled turns a schedule on or off. Turning it on looks forward from now, so it never
// fires for a time that passed while it was off.
func (r *Repo) SetScheduleEnabled(ctx context.Context, wsID, id string, on bool) error {
	s, err := r.GetSchedule(ctx, wsID, id)
	if err != nil {
		return err
	}
	var next any
	if on {
		next = nextOccurrence(s, time.Now())
	}
	_, err = r.db.ExecContext(ctx, `UPDATE schedules SET enabled = $3, next_run_at = $4 WHERE id = $1 AND workspace_id = $2`, id, wsID, on, next)
	return err
}

func (r *Repo) DeleteSchedule(ctx context.Context, wsID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = $1 AND workspace_id = $2`, id, wsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

type dueSchedule struct {
	Schedule
	WorkspaceID string
	Due         time.Time
}

func (r *Repo) dueSchedules(ctx context.Context, limit int) ([]dueSchedule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+scheduleCols+`, workspace_id FROM schedules WHERE enabled AND next_run_at <= now() ORDER BY next_run_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dueSchedule
	for rows.Next() {
		var d dueSchedule
		var next, last sql.NullTime
		if err := rows.Scan(&d.ID, &d.WorkflowID, &d.Freq, &d.AtTime, &d.Weekdays, &d.DayOfMonth, &d.Timezone, &d.Enabled, &next, &last, &d.WorkspaceID); err != nil {
			return nil, err
		}
		d.Due = next.Time
		out = append(out, d)
	}
	return out, rows.Err()
}

// fireSchedule moves a due schedule to its next time and queues one start for the tick, in one
// statement's worth of atomicity: whoever advances next_run_at first (this server or another) wins.
// Several missed ticks (the server was down) become one start.
func (r *Repo) fireSchedule(ctx context.Context, d dueSchedule, now time.Time) (started bool, err error) {
	next := nextOccurrence(d.Schedule, now)
	err = r.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE schedules SET next_run_at = $3, last_run_at = $4 WHERE id = $1 AND next_run_at = $2 AND enabled`, d.ID, d.Due, next, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // someone else fired it
		}
		var queued int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_starts WHERE workflow_id = $1 AND status = 'queued'`, d.WorkflowID).Scan(&queued); err != nil {
			return err
		}
		status, msg := "queued", ""
		if queued >= maxQueuedStarts {
			status, msg = "skipped", "The queue was full."
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO workflow_starts (id, workspace_id, workflow_id, source, source_ref, status, error, scheduled_for, finished_at)
			VALUES ($1,$2,$3,'schedule',$4,$5,$6,$7, CASE WHEN $5 = 'skipped' THEN now() END) ON CONFLICT DO NOTHING`,
			newID("st"), d.WorkspaceID, d.WorkflowID, d.ID, status, msg, d.Due)
		started = status == "queued"
		return err
	})
	return started, err
}

// ---------- the secret webhook address ----------

func hashToken(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }

type Hook struct {
	Enabled bool
	TokenEn []byte
}

func (r *Repo) GetHook(ctx context.Context, wsID, wfID string) (*Hook, error) {
	var h Hook
	err := r.db.QueryRowContext(ctx, `SELECT enabled, token_enc FROM workflow_hooks WHERE workflow_id = $1 AND workspace_id = $2`, wfID, wsID).Scan(&h.Enabled, &h.TokenEn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &h, err
}

// SetHook creates the address, or replaces its token (the old address stops working).
func (r *Repo) SetHook(ctx context.Context, wsID, wfID string, hash, enc []byte) error {
	res, err := r.db.ExecContext(ctx, `INSERT INTO workflow_hooks (workflow_id, workspace_id, token_hash, token_enc)
		SELECT w.id, w.workspace_id, $3, $4 FROM workflows w WHERE w.id = $1 AND w.workspace_id = $2
		ON CONFLICT (workflow_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, token_enc = EXCLUDED.token_enc, enabled = true, created_at = now()`, wfID, wsID, hash, enc)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (r *Repo) SetHookEnabled(ctx context.Context, wsID, wfID string, on bool) error {
	res, err := r.db.ExecContext(ctx, `UPDATE workflow_hooks SET enabled = $3 WHERE workflow_id = $1 AND workspace_id = $2`, wfID, wsID, on)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (r *Repo) DeleteHook(ctx context.Context, wsID, wfID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workflow_hooks WHERE workflow_id = $1 AND workspace_id = $2`, wfID, wsID)
	return err
}

func (r *Repo) HookByToken(ctx context.Context, token string) (wsID, wfID string, enabled bool, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT workspace_id, workflow_id, enabled FROM workflow_hooks WHERE token_hash = $1`, hashToken(token)).Scan(&wsID, &wfID, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		err = errNotFound
	}
	return
}

// ---------- starts: the queue and the history ----------

type Start struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	WorkflowID  string `json:"workflowId"`
	Source      string `json:"source"` // schedule | webhook | manual
	SourceRef   string `json:"sourceRef,omitempty"`
	Input       string `json:"-"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Attempts    int    `json:"-"`
	CreatedAt   string `json:"createdAt"`
	StartedAt   string `json:"startedAt,omitempty"`
	FinishedAt  string `json:"finishedAt,omitempty"`
}

const startCols = `id, workspace_id, workflow_id, source, source_ref, input, status, error, attempts, created_at, started_at, finished_at`

func scanStart(sc interface{ Scan(...any) error }) (Start, error) {
	var s Start
	var created time.Time
	var started, finished sql.NullTime
	if err := sc.Scan(&s.ID, &s.WorkspaceID, &s.WorkflowID, &s.Source, &s.SourceRef, &s.Input, &s.Status, &s.Error, &s.Attempts, &created, &started, &finished); err != nil {
		return s, err
	}
	s.CreatedAt = created.UTC().Format(rfc)
	if started.Valid {
		s.StartedAt = started.Time.UTC().Format(rfc)
	}
	if finished.Valid {
		s.FinishedAt = finished.Time.UTC().Format(rfc)
	}
	return s, nil
}

var errQueueFull = errors.New("too many runs are already waiting for this workflow")

// QueueStart adds a start to the workflow's queue.
func (r *Repo) QueueStart(ctx context.Context, st Start, userID string) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		var queued int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_starts WHERE workflow_id = $1 AND workspace_id = $2 AND status = 'queued'`, st.WorkflowID, st.WorkspaceID).Scan(&queued); err != nil {
			return err
		}
		if queued >= maxQueuedStarts {
			return errQueueFull
		}
		var by any
		if userID != "" {
			by = userID
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO workflow_starts (id, workspace_id, workflow_id, source, source_ref, input, started_by)
			SELECT $1, w.workspace_id, w.id, $4, $5, $6, $7 FROM workflows w WHERE w.id = $3 AND w.workspace_id = $2`,
			st.ID, st.WorkspaceID, st.WorkflowID, st.Source, st.SourceRef, st.Input, by)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound
		}
		return nil
	})
}

func (r *Repo) ListStarts(ctx context.Context, wsID, wfID string, limit int) ([]Start, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+startCols+` FROM workflow_starts WHERE workspace_id = $1 AND ($2 = '' OR workflow_id = $2) ORDER BY created_at DESC LIMIT $3`, wsID, wfID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Start{}
	for rows.Next() {
		s, err := scanStart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ActiveStart is the start that currently owns a workflow's run (running, or waiting on a person), if any.
func (r *Repo) ActiveStart(ctx context.Context, wfID string) (*Start, error) {
	s, err := scanStart(r.db.QueryRowContext(ctx, `SELECT `+startCols+` FROM workflow_starts WHERE workflow_id = $1 AND status IN ('running', 'waiting') ORDER BY created_at LIMIT 1`, wfID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

func (r *Repo) CountQueued(ctx context.Context, wfID string) (n int, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT count(*) FROM workflow_starts WHERE workflow_id = $1 AND status = 'queued'`, wfID).Scan(&n)
	return
}

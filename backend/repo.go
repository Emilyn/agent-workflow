package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Repo is the Postgres data layer. Every query that touches workspace data
// takes the workspace id and filters on it, so one workspace can never read or
// change another's rows even if a caller passes a foreign id.
type Repo struct{ db *sql.DB }

func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

var (
	errEmailTaken  = errors.New("an account with that email already exists")
	errLastOwner   = errors.New("a workspace needs at least one owner")
	errBadInvite   = errors.New("this invite link is invalid or has expired")
	errAlreadyIn   = errors.New("you are already a member of that workspace")
	errTooMany     = errors.New("limit reached")
	errNoSuchRole  = errors.New("unknown role")
	rfc            = time.RFC3339
	maxWorkspaces  = 20
	maxMembers     = 200
	inviteLifetime = 7 * 24 * time.Hour
)

func isUnique(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}

func (r *Repo) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (r *Repo) Ping(ctx context.Context) error { return r.db.PingContext(ctx) }

// ---------- users and sessions ----------

type User struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	passwordHash string
}

func (r *Repo) CreateUser(ctx context.Context, email, name, hash string) (User, error) {
	u := User{ID: newID("u"), Email: email, Name: name}
	_, err := r.db.ExecContext(ctx, `INSERT INTO users (id, email, name, password_hash) VALUES ($1, $2, $3, $4)`, u.ID, email, name, hash)
	if isUnique(err) {
		return User{}, errEmailTaken
	}
	return u, err
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := r.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errNotFound
	}
	return u, err
}

func (r *Repo) CreateSession(ctx context.Context, userID string, hash []byte, expires time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, hash, userID, expires)
	return err
}

func (r *Repo) SessionUser(ctx context.Context, hash []byte) (User, error) {
	var u User
	err := r.db.QueryRowContext(ctx, `SELECT u.id, u.email, u.name FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, hash).Scan(&u.ID, &u.Email, &u.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errNotFound
	}
	return u, err
}

func (r *Repo) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}

func (r *Repo) PurgeSessions(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------- workspaces and members ----------

type WorkspaceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// CreateWorkspace makes a workspace with the user as owner and an example workflow to start from.
func (r *Repo) CreateWorkspace(ctx context.Context, userID, name string) (WorkspaceInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxNameLen {
		return WorkspaceInfo{}, fmt.Errorf("give the workspace a name of up to %d characters", maxNameLen)
	}
	ws := WorkspaceInfo{ID: newID("s"), Name: name, Role: "owner"}
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM memberships WHERE user_id = $1 AND role = 'owner'`, userID).Scan(&n); err != nil {
			return err
		}
		if n >= maxWorkspaces {
			return fmt.Errorf("you can own at most %d workspaces", maxWorkspaces)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, $2)`, ws.ID, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, ws.ID, userID); err != nil {
			return err
		}
		_, err := insertWorkflow(ctx, tx, ws.ID, userID, "Weekly customer digest", exampleWorkflow())
		return err
	})
	return ws, err
}

func (r *Repo) WorkspacesFor(ctx context.Context, userID string) ([]WorkspaceInfo, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT w.id, w.name, m.role FROM memberships m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY m.created_at, w.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceInfo{}
	for rows.Next() {
		var w WorkspaceInfo
		if err := rows.Scan(&w.ID, &w.Name, &w.Role); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Role returns the user's role in the workspace, or errNotFound when they are not a member.
func (r *Repo) Role(ctx context.Context, wsID, userID string) (string, error) {
	var role string
	err := r.db.QueryRowContext(ctx, `SELECT role FROM memberships WHERE workspace_id = $1 AND user_id = $2`, wsID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	return role, err
}

func (r *Repo) RenameWorkspace(ctx context.Context, wsID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxNameLen {
		return fmt.Errorf("give the workspace a name of up to %d characters", maxNameLen)
	}
	_, err := r.db.ExecContext(ctx, `UPDATE workspaces SET name = $2 WHERE id = $1`, wsID, name)
	return err
}

func (r *Repo) DeleteWorkspace(ctx context.Context, wsID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, wsID)
	return err
}

type Member struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Since  string `json:"since"`
}

func (r *Repo) Members(ctx context.Context, wsID string) ([]Member, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT u.id, u.name, u.email, m.role, m.created_at FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, lower(u.name), u.id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var t time.Time
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &t); err != nil {
			return nil, err
		}
		m.Since = t.UTC().Format(rfc)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetRole changes a member's role. It refuses to leave a workspace without an owner.
func (r *Repo) SetRole(ctx context.Context, wsID, userID, role string) error {
	if role != "owner" && role != "admin" && role != "member" {
		return errNoSuchRole
	}
	return r.tx(ctx, func(tx *sql.Tx) error {
		cur, err := lockMember(ctx, tx, wsID, userID)
		if err != nil {
			return err
		}
		if cur == "owner" && role != "owner" {
			if err := requireOtherOwner(ctx, tx, wsID, userID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE memberships SET role = $3 WHERE workspace_id = $1 AND user_id = $2`, wsID, userID, role)
		return err
	})
}

func (r *Repo) RemoveMember(ctx context.Context, wsID, userID string) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		cur, err := lockMember(ctx, tx, wsID, userID)
		if err != nil {
			return err
		}
		if cur == "owner" {
			if err := requireOtherOwner(ctx, tx, wsID, userID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM memberships WHERE workspace_id = $1 AND user_id = $2`, wsID, userID)
		return err
	})
}

func lockMember(ctx context.Context, tx *sql.Tx, wsID, userID string) (string, error) {
	var role string
	err := tx.QueryRowContext(ctx, `SELECT role FROM memberships WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`, wsID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	return role, err
}

// requireOtherOwner locks the owner rows so two simultaneous demotions cannot both succeed.
func requireOtherOwner(ctx context.Context, tx *sql.Tx, wsID, userID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM memberships WHERE workspace_id = $1 AND role = 'owner' FOR UPDATE`, wsID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if id != userID {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return errLastOwner
}

// ---------- invites ----------

type Invite struct {
	ID        string `json:"id"`
	Note      string `json:"note"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

// CreateInvite returns the invite and the one-time token. Only the token's hash is stored.
func (r *Repo) CreateInvite(ctx context.Context, wsID, userID, note, role string) (Invite, string, error) {
	if role != "admin" && role != "member" {
		return Invite{}, "", errNoSuchRole
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 120 {
		return Invite{}, "", errors.New("keep the note under 120 characters")
	}
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM memberships WHERE workspace_id = $1) + (SELECT count(*) FROM invites WHERE workspace_id = $1 AND expires_at > now())`, wsID).Scan(&n); err != nil {
		return Invite{}, "", err
	}
	if n >= maxMembers {
		return Invite{}, "", fmt.Errorf("a workspace can have at most %d members and open invites", maxMembers)
	}
	token := randomToken()
	inv := Invite{ID: newID("i"), Note: note, Role: role}
	exp := time.Now().Add(inviteLifetime)
	_, err := r.db.ExecContext(ctx, `INSERT INTO invites (id, workspace_id, token_hash, note, role, created_by, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		inv.ID, wsID, tokenHash(token), note, role, userID, exp)
	inv.CreatedAt = time.Now().UTC().Format(rfc)
	inv.ExpiresAt = exp.UTC().Format(rfc)
	return inv, token, err
}

func (r *Repo) Invites(ctx context.Context, wsID string) ([]Invite, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, note, role, created_at, expires_at FROM invites WHERE workspace_id = $1 AND expires_at > now() ORDER BY created_at DESC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var i Invite
		var c, e time.Time
		if err := rows.Scan(&i.ID, &i.Note, &i.Role, &c, &e); err != nil {
			return nil, err
		}
		i.CreatedAt, i.ExpiresAt = c.UTC().Format(rfc), e.UTC().Format(rfc)
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteInvite(ctx context.Context, wsID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM invites WHERE id = $1 AND workspace_id = $2`, id, wsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// InvitePreview names the workspace behind a token without using it up.
func (r *Repo) InvitePreview(ctx context.Context, token string) (WorkspaceInfo, error) {
	var w WorkspaceInfo
	err := r.db.QueryRowContext(ctx, `SELECT w.id, w.name, i.role FROM invites i JOIN workspaces w ON w.id = i.workspace_id
		WHERE i.token_hash = $1 AND i.expires_at > now()`, tokenHash(token)).Scan(&w.ID, &w.Name, &w.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceInfo{}, errBadInvite
	}
	return w, err
}

// AcceptInvite uses the token up (it works once) and adds the user to the workspace.
func (r *Repo) AcceptInvite(ctx context.Context, token, userID string) (WorkspaceInfo, error) {
	var out WorkspaceInfo
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var inviteID, role string
		err := tx.QueryRowContext(ctx, `SELECT i.id, i.workspace_id, i.role, w.name FROM invites i JOIN workspaces w ON w.id = i.workspace_id
			WHERE i.token_hash = $1 AND i.expires_at > now() FOR UPDATE OF i`, tokenHash(token)).Scan(&inviteID, &out.ID, &role, &out.Name)
		if errors.Is(err, sql.ErrNoRows) {
			return errBadInvite
		}
		if err != nil {
			return err
		}
		out.Role = role
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE workspace_id = $1 AND user_id = $2)`, out.ID, userID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errAlreadyIn
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, $3)`, out.ID, userID, role); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM invites WHERE id = $1`, inviteID)
		return err
	})
	return out, err
}

// ---------- shared workspace settings ----------

func (r *Repo) Settings(ctx context.Context, wsID string) (Workspace, error) {
	var ws Workspace
	var connected, agents []byte
	err := r.db.QueryRowContext(ctx, `SELECT connected, git_provider, custom_agents FROM workspaces WHERE id = $1`, wsID).Scan(&connected, &ws.GitProvider, &agents)
	if errors.Is(err, sql.ErrNoRows) {
		return ws, errNotFound
	}
	if err != nil {
		return ws, err
	}
	_ = json.Unmarshal(connected, &ws.Connected)
	_ = json.Unmarshal(agents, &ws.CustomAgents)
	ws.normalize()
	return ws, nil
}

func (r *Repo) SetSettings(ctx context.Context, wsID string, ws Workspace) (Workspace, error) {
	ws.normalize()
	if err := ws.validate(); err != nil {
		return Workspace{}, err
	}
	c, _ := json.Marshal(ws.Connected)
	a, _ := json.Marshal(ws.CustomAgents)
	res, err := r.db.ExecContext(ctx, `UPDATE workspaces SET connected = $2, git_provider = $3, custom_agents = $4 WHERE id = $1`, wsID, c, ws.GitProvider, a)
	if err != nil {
		return Workspace{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Workspace{}, errNotFound
	}
	return ws, nil
}

// ---------- API key (encrypted) ----------

func (r *Repo) SetKey(ctx context.Context, wsID, userID string, enc []byte, hint string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO workspace_secrets (workspace_id, anthropic_key_enc, key_hint, updated_by) VALUES ($1,$2,$3,$4)
		ON CONFLICT (workspace_id) DO UPDATE SET anthropic_key_enc = EXCLUDED.anthropic_key_enc, key_hint = EXCLUDED.key_hint, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		wsID, enc, hint, userID)
	return err
}

// Key returns the encrypted key and its hint; enc is nil when none is stored.
func (r *Repo) Key(ctx context.Context, wsID string) (enc []byte, hint string, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT anthropic_key_enc, key_hint FROM workspace_secrets WHERE workspace_id = $1`, wsID).Scan(&enc, &hint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	return enc, hint, err
}

func (r *Repo) DeleteKey(ctx context.Context, wsID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspace_secrets WHERE workspace_id = $1`, wsID)
	return err
}

// AgentMode is how a workspace runs its agents: "api" (default) or "claude_code".
func (r *Repo) AgentMode(ctx context.Context, wsID string) (string, error) {
	var mode string
	err := r.db.QueryRowContext(ctx, `SELECT mode FROM workspace_agent WHERE workspace_id = $1`, wsID).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "api", nil
	}
	return mode, err
}

func (r *Repo) SetAgentMode(ctx context.Context, wsID, userID, mode string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO workspace_agent (workspace_id, mode, updated_by) VALUES ($1,$2,$3)
		ON CONFLICT (workspace_id) DO UPDATE SET mode = EXCLUDED.mode, updated_by = EXCLUDED.updated_by, updated_at = now()`, wsID, mode, userID)
	return err
}

// ---------- workflows ----------

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

func (r *Repo) ListWorkflows(ctx context.Context, wsID string) ([]Summary, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, steps, progress, updated_at FROM workflows WHERE workspace_id = $1 ORDER BY created_at, id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var w Workflow
		var steps []byte
		var t time.Time
		if err := rows.Scan(&w.ID, &w.Name, &steps, &w.Progress, &t); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(steps, &w.Steps)
		w.Updated = t.UTC().Format(rfc)
		out = append(out, summarize(w))
	}
	return out, rows.Err()
}

const workflowCols = `id, name, steps, progress, next_id, outputs, version, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanWorkflow(row rowScanner) (Workflow, error) {
	var w Workflow
	var steps, outputs []byte
	var t time.Time
	if err := row.Scan(&w.ID, &w.Name, &steps, &w.Progress, &w.NextID, &outputs, &w.Version, &t); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return w, errNotFound
		}
		return w, err
	}
	_ = json.Unmarshal(steps, &w.Steps)
	_ = json.Unmarshal(outputs, &w.Outputs)
	w.Updated = t.UTC().Format(rfc)
	w.normalize()
	return w, nil
}

// GetWorkflow loads a workflow with its most recent messages.
func (r *Repo) GetWorkflow(ctx context.Context, wsID, id string) (Workflow, error) {
	w, err := scanWorkflow(r.db.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE id = $1 AND workspace_id = $2`, id, wsID))
	if err != nil {
		return w, err
	}
	w.Messages, err = r.RecentMessages(ctx, id, maxMessages)
	return w, err
}

func insertWorkflow(ctx context.Context, tx *sql.Tx, wsID, userID, name string, w Workflow) (string, error) {
	w.ID = newID("w")
	w.Name = name
	w.normalize()
	if err := w.validate(); err != nil {
		return "", err
	}
	steps, _ := json.Marshal(w.Steps)
	outputs, _ := json.Marshal(w.Outputs)
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflows WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
		return "", err
	}
	if n >= maxWorkflows {
		return "", fmt.Errorf("at most %d workflows per workspace", maxWorkflows)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflows (id, workspace_id, name, steps, progress, next_id, outputs, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		w.ID, wsID, w.Name, steps, w.Progress, w.NextID, outputs, userID); err != nil {
		return "", err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO messages (workflow_id, author, kind, text) VALUES ($1, 'Claude', 'agent', $2)`, w.ID, welcomeText)
	return w.ID, err
}

// CreateWorkflow adds a workflow from a template, or copies copyOf (steps only, no results or chat).
func (r *Repo) CreateWorkflow(ctx context.Context, wsID, userID, name, template, copyOf string) (Workflow, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > maxNameLen {
		return Workflow{}, fmt.Errorf("the name is longer than %d characters", maxNameLen)
	}
	var id string
	err := r.tx(ctx, func(tx *sql.Tx) error {
		var w Workflow
		switch {
		case copyOf != "":
			src, err := scanWorkflow(tx.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE id = $1 AND workspace_id = $2`, copyOf, wsID))
			if err != nil {
				return err
			}
			w = Workflow{Steps: src.Steps, NextID: src.NextID}
			if name == "" {
				name = "Copy of " + src.Name
			}
		case template == "example":
			w = exampleWorkflow()
		case template == "review":
			w = reviewWorkflow()
		case template == "" || template == "blank":
			w = blankWorkflow()
		default:
			return fmt.Errorf("unknown template %q", template)
		}
		if name == "" {
			name = "Untitled workflow"
		}
		if len([]rune(name)) > maxNameLen {
			name = string([]rune(name)[:maxNameLen])
		}
		var err error
		id, err = insertWorkflow(ctx, tx, wsID, userID, name, w)
		return err
	})
	if err != nil {
		return Workflow{}, err
	}
	return r.GetWorkflow(ctx, wsID, id)
}

// ReplaceWorkflow saves the client's version if it was based on the stored version.
// Outputs stay as the server has them, minus outputs of steps that no longer exist.
func (r *Repo) ReplaceWorkflow(ctx context.Context, wsID, id string, in Workflow) (Workflow, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return Workflow{}, err
	}
	err := r.tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanWorkflow(tx.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, id, wsID))
		if err != nil {
			return err
		}
		if in.Version != cur.Version {
			return errConflict
		}
		keep := map[string]Output{}
		for _, g := range in.Steps {
			for _, n := range g {
				if o, ok := cur.Outputs[n.ID]; ok {
					keep[n.ID] = o
				}
			}
		}
		steps, _ := json.Marshal(in.Steps)
		outputs, _ := json.Marshal(keep)
		_, err = tx.ExecContext(ctx, `UPDATE workflows SET name=$3, steps=$4, progress=$5, next_id=$6, outputs=$7, version=version+1, updated_at=now() WHERE id=$1 AND workspace_id=$2`,
			id, wsID, in.Name, steps, in.Progress, in.NextID, outputs)
		return err
	})
	if err != nil {
		return Workflow{}, err
	}
	return r.GetWorkflow(ctx, wsID, id)
}

func (r *Repo) DeleteWorkflow(ctx context.Context, wsID, id string) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		// Lock the workspace row so two concurrent deletes cannot both remove the last-but-one.
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, wsID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflows WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1 AND workspace_id = $2)`, id, wsID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errNotFound
		}
		if n <= 1 {
			return errLastWorkflow
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM workflows WHERE id = $1 AND workspace_id = $2`, id, wsID)
		return err
	})
}

func (r *Repo) SetOutput(ctx context.Context, wsID, id, nodeID string, o Output) error {
	raw, _ := json.Marshal(o)
	res, err := r.db.ExecContext(ctx, `UPDATE workflows SET outputs = jsonb_set(outputs, ARRAY[$3::text], $4::jsonb, true), updated_at = now()
		WHERE id = $1 AND workspace_id = $2`, id, wsID, nodeID, raw)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// ResetRun clears the results and starts the run over. It bumps the version so other open copies reload.
func (r *Repo) ResetRun(ctx context.Context, wsID, id string) (Workflow, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE workflows SET outputs = '{}', progress = 0, version = version + 1, updated_at = now() WHERE id = $1 AND workspace_id = $2`, id, wsID)
	if err != nil {
		return Workflow{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Workflow{}, errNotFound
	}
	return r.GetWorkflow(ctx, wsID, id)
}

// ---------- chat ----------

func (r *Repo) RecentMessages(ctx context.Context, workflowID string, limit int) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, author, kind, text, created_at FROM (
		SELECT id, author, kind, text, created_at FROM messages WHERE workflow_id = $1 ORDER BY id DESC LIMIT $2) t ORDER BY id`, workflowID, limit)
	return scanMessages(rows, err)
}

func (r *Repo) MessagesAfter(ctx context.Context, workflowID string, after int64, limit int) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, author, kind, text, created_at FROM messages WHERE workflow_id = $1 AND id > $2 ORDER BY id LIMIT $3`, workflowID, after, limit)
	return scanMessages(rows, err)
}

func scanMessages(rows *sql.Rows, err error) ([]Message, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var t time.Time
		if err := rows.Scan(&m.ID, &m.Author, &m.Kind, &m.Text, &t); err != nil {
			return nil, err
		}
		m.CreatedAt = t.UTC().Format(rfc)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMessage appends to a workflow's chat. The workspace id is checked so a message cannot land in a foreign workflow.
func (r *Repo) AddMessage(ctx context.Context, wsID, workflowID, userID, author, kind, text string) (Message, error) {
	var m Message
	var t time.Time
	var uid any
	if userID != "" {
		uid = userID
	}
	err := r.db.QueryRowContext(ctx, `INSERT INTO messages (workflow_id, user_id, author, kind, text)
		SELECT w.id, $3, $4, $5, $6 FROM workflows w WHERE w.id = $1 AND w.workspace_id = $2
		RETURNING id, author, kind, text, created_at`, workflowID, wsID, uid, author, kind, text).Scan(&m.ID, &m.Author, &m.Kind, &m.Text, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return m, errNotFound
	}
	m.CreatedAt = t.UTC().Format(rfc)
	return m, err
}

func isDBError(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ---------- connected tools (OAuth tokens, encrypted) ----------

type IntegrationInfo struct {
	Account     string `json:"account"`
	Scopes      string `json:"scopes"`
	ConnectedAt string `json:"connectedAt"`
}

func (r *Repo) SetIntegration(ctx context.Context, wsID, provider, userID, account, scopes string, enc []byte) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO integration_tokens (workspace_id, provider, token_enc, account, scopes, connected_by) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (workspace_id, provider) DO UPDATE SET token_enc = EXCLUDED.token_enc, account = EXCLUDED.account, scopes = EXCLUDED.scopes,
		connected_by = EXCLUDED.connected_by, created_at = now()`, wsID, provider, enc, account, scopes, userID)
	return err
}

// Integration returns what is stored for a provider; enc is nil when it is not connected.
func (r *Repo) Integration(ctx context.Context, wsID, provider string) (info IntegrationInfo, enc []byte, err error) {
	var t time.Time
	err = r.db.QueryRowContext(ctx, `SELECT token_enc, account, scopes, created_at FROM integration_tokens WHERE workspace_id = $1 AND provider = $2`, wsID, provider).
		Scan(&enc, &info.Account, &info.Scopes, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil, nil
	}
	info.ConnectedAt = t.UTC().Format(rfc)
	return info, enc, err
}

func (r *Repo) DeleteIntegration(ctx context.Context, wsID, provider string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM integration_tokens WHERE workspace_id = $1 AND provider = $2`, wsID, provider)
	return err
}

// LockIntegration runs fn with the stored token row locked, so two requests (or two server
// instances) never refresh the same rotating refresh token at once. fn gets the current
// ciphertext and returns replacement ciphertext, or nil to keep it. It returns what is stored afterwards.
func (r *Repo) LockIntegration(ctx context.Context, wsID, provider string, fn func(enc []byte) ([]byte, error)) ([]byte, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var enc []byte
	err = tx.QueryRowContext(ctx, `SELECT token_enc FROM integration_tokens WHERE workspace_id = $1 AND provider = $2 FOR UPDATE`, wsID, provider).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	next, err := fn(enc)
	if err != nil {
		return nil, err
	}
	if next != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE integration_tokens SET token_enc = $3 WHERE workspace_id = $1 AND provider = $2`, wsID, provider, next); err != nil {
			return nil, err
		}
		enc = next
	}
	return enc, tx.Commit()
}

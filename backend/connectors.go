package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Connectors: remote MCP servers an admin adds to a workspace, so agents can read from sources this app has
// no built-in support for. An agent gets a connector's tools when a step lists the connector's name, and
// only the tools an admin switched on. Only tools the server itself marks read-only can be switched on.

const maxConnectors = 10

type Connector struct {
	ID         string
	Name       string
	URL        string
	HeaderName string
	SecretEnc  []byte
	ServerName string
	Tools      []mcpTool
	Enabled    map[string]string // tool name -> hash it had when switched on
	Error      string
	Refreshed  time.Time
}

func (c Connector) enabledTools() []mcpTool {
	var out []mcpTool
	for _, t := range c.Tools {
		if h, ok := c.Enabled[t.Name]; ok && h == t.Hash && t.ReadOnly {
			out = append(out, t)
		}
	}
	return out
}

const connectorCols = `id, name, url, header_name, secret_enc, server_name, tools, enabled, error, refreshed_at`

func scanConnector(sc interface{ Scan(...any) error }) (Connector, error) {
	var c Connector
	var tools, enabled []byte
	var refreshed sql.NullTime
	if err := sc.Scan(&c.ID, &c.Name, &c.URL, &c.HeaderName, &c.SecretEnc, &c.ServerName, &tools, &enabled, &c.Error, &refreshed); err != nil {
		return c, err
	}
	_ = json.Unmarshal(tools, &c.Tools)
	_ = json.Unmarshal(enabled, &c.Enabled)
	if c.Enabled == nil {
		c.Enabled = map[string]string{}
	}
	c.Refreshed = refreshed.Time
	return c, nil
}

func (r *Repo) ListConnectors(ctx context.Context, wsID string) ([]Connector, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+connectorCols+` FROM mcp_connectors WHERE workspace_id = $1 ORDER BY created_at, id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connector{}
	for rows.Next() {
		c, err := scanConnector(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) GetConnector(ctx context.Context, wsID, id string) (Connector, error) {
	c, err := scanConnector(r.db.QueryRowContext(ctx, `SELECT `+connectorCols+` FROM mcp_connectors WHERE id = $1 AND workspace_id = $2`, id, wsID))
	if errors.Is(err, sql.ErrNoRows) {
		return c, errNotFound
	}
	return c, err
}

var errNameTaken = validationError{"a connector with that name already exists"}

func (r *Repo) CreateConnector(ctx context.Context, wsID, userID string, c Connector) error {
	return r.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mcp_connectors WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
			return err
		}
		if n >= maxConnectors {
			return validationError{fmt.Sprintf("a workspace can have at most %d connectors", maxConnectors)}
		}
		tools, _ := json.Marshal(c.Tools)
		enabled, _ := json.Marshal(c.Enabled)
		_, err := tx.ExecContext(ctx, `INSERT INTO mcp_connectors (id, workspace_id, name, url, header_name, secret_enc, server_name, tools, enabled, error, refreshed_at, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now(), $11)`, c.ID, wsID, c.Name, c.URL, c.HeaderName, c.SecretEnc, c.ServerName, tools, enabled, c.Error, userID)
		if isUniqueViolation(err) {
			return errNameTaken
		}
		return err
	})
}

func (r *Repo) SaveConnectorTools(ctx context.Context, wsID, id, server string, tools []mcpTool, enabled map[string]string, errText string) error {
	tj, _ := json.Marshal(tools)
	ej, _ := json.Marshal(enabled)
	_, err := r.db.ExecContext(ctx, `UPDATE mcp_connectors SET server_name = $3, tools = $4, enabled = $5, error = $6, refreshed_at = now() WHERE id = $1 AND workspace_id = $2`, id, wsID, server, tj, ej, errText)
	return err
}

func (r *Repo) SetConnectorEnabled(ctx context.Context, wsID, id string, enabled map[string]string) error {
	ej, _ := json.Marshal(enabled)
	_, err := r.db.ExecContext(ctx, `UPDATE mcp_connectors SET enabled = $3 WHERE id = $1 AND workspace_id = $2`, id, wsID, ej)
	return err
}

func (r *Repo) DeleteConnector(ctx context.Context, wsID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM mcp_connectors WHERE id = $1 AND workspace_id = $2`, id, wsID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}

// ---------- talking to the server ----------

var (
	connectorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,39}$`)
	headerNameRe  = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	forbiddenHdrs = map[string]bool{"host": true, "content-length": true, "content-type": true, "accept": true, "transfer-encoding": true, "connection": true, "mcp-session-id": true, "mcp-protocol-version": true, "user-agent": true, "cookie": true}
)

// reservedLabel reports whether a connector name would clash with a built-in tool name.
func reservedLabel(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case strings.ToLower(webLabel), "docs", "sheets", "slack", "email":
		return true
	}
	for _, p := range providers {
		if strings.ToLower(p.Label) == n {
			return true
		}
	}
	return false
}

// authHeaders is how the stored token is sent: as a bearer token, or as the value of a header the admin named.
func authHeaders(headerName, token string) map[string]string {
	if token == "" {
		return nil
	}
	if headerName == "" {
		return map[string]string{"Authorization": "Bearer " + token}
	}
	return map[string]string{headerName: token}
}

func (s *Server) connectorClient(c Connector, wsID string) (*mcpClient, error) {
	token := ""
	if len(c.SecretEnc) > 0 {
		t, err := s.sealer.Open(c.SecretEnc, "mcp|"+wsID+"|"+c.ID)
		if err != nil {
			return nil, errors.New("the stored token cannot be read; add the connector again")
		}
		token = t
	}
	return newMCPClient(s.mcpGuard, c.URL, authHeaders(c.HeaderName, token)), nil
}

// ---------- API ----------

type connectorToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"readOnly"`
	Enabled     bool   `json:"enabled"`
	Changed     bool   `json:"changed,omitempty"`
}

type connectorView struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	URL        string              `json:"url,omitempty"` // admins only: it may carry a key
	Host       string              `json:"host"`
	HasToken   bool                `json:"hasToken"`
	HeaderName string              `json:"headerName,omitempty"`
	ServerName string              `json:"serverName,omitempty"`
	Error      string              `json:"error,omitempty"`
	Tools      []connectorToolView `json:"tools"`
	Enabled    int                 `json:"enabledCount"`
	Refreshed  string              `json:"refreshedAt,omitempty"`
}

type connectorsView struct {
	Web struct {
		Fetch  bool `json:"fetch"`
		Search bool `json:"search"`
	} `json:"web"`
	MCP []connectorView `json:"mcp"`
}

func (s *Server) viewOf(c Connector, admin bool) connectorView {
	v := connectorView{ID: c.ID, Name: c.Name, HasToken: len(c.SecretEnc) > 0, HeaderName: c.HeaderName, ServerName: c.ServerName, Error: c.Error, Tools: []connectorToolView{}}
	if u, err := url.Parse(c.URL); err == nil {
		v.Host = u.Host
	}
	if admin {
		v.URL = c.URL
	}
	if !c.Refreshed.IsZero() {
		v.Refreshed = c.Refreshed.UTC().Format(rfc)
	}
	for _, t := range c.Tools {
		on := false
		if h, ok := c.Enabled[t.Name]; ok && h == t.Hash && t.ReadOnly {
			on = true
			v.Enabled++
		}
		v.Tools = append(v.Tools, connectorToolView{Name: t.Name, Description: t.Description, ReadOnly: t.ReadOnly, Enabled: on, Changed: t.Changed})
	}
	return v
}

func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request, c wsCtx) {
	list, err := s.repo.ListConnectors(r.Context(), c.WS)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out connectorsView
	out.Web.Fetch, out.Web.Search = true, s.cfg.WebSearch.Enabled()
	out.MCP = []connectorView{}
	admin := roleRank[c.Role] >= roleRank["admin"]
	for _, x := range list {
		out.MCP = append(out.MCP, s.viewOf(x, admin))
	}
	writeJSON(w, http.StatusOK, out)
}

// probe connects and reads the tool list, for adding a connector or refreshing it.
func (s *Server) probeConnector(ctx context.Context, cl *mcpClient) (string, []mcpTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return cl.ListTools(ctx)
}

func (s *Server) createConnector(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Name       string `json:"name"`
		URL        string `json:"url"`
		Token      string `json:"token"`
		HeaderName string `json:"headerName"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Name, req.Token, req.HeaderName = strings.TrimSpace(req.Name), strings.TrimSpace(req.Token), strings.TrimSpace(req.HeaderName)
	if !connectorName.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "Give the connector a short name of letters, numbers, spaces, - or _.")
		return
	}
	if reservedLabel(req.Name) {
		writeError(w, http.StatusBadRequest, "That name is already used by a built-in tool. Pick another.")
		return
	}
	u, err := parseWebURL(req.URL, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if u.Scheme != "https" && !s.cfg.AllowPrivateConnectors {
		writeError(w, http.StatusBadRequest, "Use an https:// address.")
		return
	}
	if len(req.Token) > 2000 || strings.ContainsAny(req.Token, "\r\n") {
		writeError(w, http.StatusBadRequest, "That token does not look right.")
		return
	}
	if req.HeaderName != "" && (!headerNameRe.MatchString(req.HeaderName) || forbiddenHdrs[strings.ToLower(req.HeaderName)]) {
		writeError(w, http.StatusBadRequest, "That header name cannot be used.")
		return
	}
	conn := Connector{ID: newID("mc"), Name: req.Name, URL: u.String(), HeaderName: req.HeaderName, Enabled: map[string]string{}}
	if req.Token != "" {
		conn.SecretEnc = s.sealer.Seal(req.Token, "mcp|"+c.WS+"|"+conn.ID)
	}
	cl, err := s.connectorClient(conn, c.WS)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	server, tools, err := s.probeConnector(r.Context(), cl)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not connect: "+err.Error())
		return
	}
	conn.ServerName, conn.Tools = server, tools
	if err := s.repo.CreateConnector(r.Context(), c.WS, c.User.ID, conn); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listConnectors(w, r, c)
}

// refreshConnector reads the tool list again. A tool that was switched on and has changed (or is no longer
// marked read-only) is switched off until an admin looks at it again.
func (s *Server) refreshConnector(w http.ResponseWriter, r *http.Request, c wsCtx) {
	conn, err := s.repo.GetConnector(r.Context(), c.WS, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cl, err := s.connectorClient(conn, c.WS)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	server, tools, err := s.probeConnector(r.Context(), cl)
	if err != nil {
		_ = s.repo.SaveConnectorTools(r.Context(), c.WS, conn.ID, conn.ServerName, conn.Tools, conn.Enabled, cut(err.Error(), 300))
		writeError(w, http.StatusBadGateway, "Could not connect: "+err.Error())
		return
	}
	enabled := map[string]string{}
	for i := range tools {
		if h, was := conn.Enabled[tools[i].Name]; was {
			if h == tools[i].Hash && tools[i].ReadOnly {
				enabled[tools[i].Name] = h
			} else {
				tools[i].Changed = true
			}
		}
	}
	if err := s.repo.SaveConnectorTools(r.Context(), c.WS, conn.ID, server, tools, enabled, ""); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listConnectors(w, r, c)
}

func (s *Server) setConnectorTools(w http.ResponseWriter, r *http.Request, c wsCtx) {
	var req struct {
		Enabled []string `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	conn, err := s.repo.GetConnector(r.Context(), c.WS, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byName := map[string]mcpTool{}
	for _, t := range conn.Tools {
		byName[t.Name] = t
	}
	enabled := map[string]string{}
	for _, name := range req.Enabled {
		t, ok := byName[name]
		switch {
		case !ok:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("The server has no tool called %q.", name))
			return
		case !t.ReadOnly:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not marked read-only by the server, so agents cannot use it.", name))
			return
		}
		enabled[name] = t.Hash
	}
	if err := s.repo.SetConnectorEnabled(r.Context(), c.WS, conn.ID, enabled); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listConnectors(w, r, c)
}

func (s *Server) deleteConnector(w http.ResponseWriter, r *http.Request, c wsCtx) {
	if err := s.repo.DeleteConnector(r.Context(), c.WS, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listConnectors(w, r, c)
}

// ---------- offering the tools to an agent ----------

var nonToolChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// exposedName is the tool name an agent sees: unique across connectors, at most 64 characters of [A-Za-z0-9_-].
func exposedName(connector, tool string, taken map[string]bool) string {
	slug := strings.Trim(nonToolChars.ReplaceAllString(strings.ToLower(connector), "_"), "_")
	if len(slug) > 12 {
		slug = slug[:12]
	}
	base := "mcp_" + slug + "_" + nonToolChars.ReplaceAllString(tool, "_")
	sum := sha256.Sum256([]byte(connector + "\x00" + tool))
	if len(base) > 64 {
		base = base[:55] + "_" + hex.EncodeToString(sum[:2])
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s_%d", base[:min(len(base), 60)], i)
	}
	taken[name] = true
	return name
}

// mcpTools adds the tools of the connectors a step lists. One connection per connector is shared by
// the step's tool calls.
func (s *Server) mcpTools(ctx context.Context, wsID string, stepTools []string, defs *[]ToolDef, handlers map[string]func(context.Context, json.RawMessage) (string, error), labels *[]string) error {
	list, err := s.repo.ListConnectors(ctx, wsID)
	if err != nil {
		return err
	}
	taken := map[string]bool{}
	for _, d := range *defs {
		taken[d.Name] = true
	}
	for _, conn := range list {
		if !hasTool(stepTools, conn.Name) {
			continue
		}
		tools := conn.enabledTools()
		if len(tools) == 0 {
			continue
		}
		cl, err := s.connectorClient(conn, wsID)
		if err != nil {
			s.log.Warn("connector unusable", "connector", conn.Name, "err", err)
			continue
		}
		*labels = append(*labels, conn.Name)
		for _, t := range tools {
			name := exposedName(conn.Name, t.Name, taken)
			orig := t.Name
			desc := fmt.Sprintf("[%s] %s", conn.Name, t.Description)
			*defs = append(*defs, ToolDef{Name: name, Description: cut(desc, maxMCPDesc+60), InputSchema: t.InputSchema})
			handlers[name] = func(ctx context.Context, in json.RawMessage) (string, error) {
				if len(in) > maxMCPToolInput {
					return "", errors.New("the tool input is too large")
				}
				ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()
				return cl.CallTool(ctx, orig, in)
			}
		}
	}
	return nil
}

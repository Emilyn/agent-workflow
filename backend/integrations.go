package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	oauthCookiePrefix = "aw_oauth_"
	stateLifetime     = 10 * time.Minute
)

// tokenSet is what is stored (encrypted) for a connected provider.
type tokenSet struct {
	Access  string            `json:"a"`
	Refresh string            `json:"r,omitempty"`
	Expiry  int64             `json:"e,omitempty"` // unix seconds; 0 = does not expire
	Extra   map[string]string `json:"x,omitempty"` // provider specifics, e.g. the Jira cloud id
}

func (t tokenSet) expiring() bool { return t.Expiry > 0 && time.Now().Unix() >= t.Expiry-60 }

// provider describes how to connect to one service and which tools it gives agents.
type provider struct {
	ID, Label string
	authorize func(app OAuthApp, q url.Values) string
	exchange  func(ctx context.Context, app OAuthApp, redirect, code string) (tokenResponse, error)
	refresh   func(ctx context.Context, app OAuthApp, ts tokenSet) (tokenResponse, error) // nil: tokens do not expire
	identify  func(ctx context.Context, app OAuthApp, ts *tokenSet) (account string, err error)
	revoke    func(app OAuthApp, ts tokenSet) // best effort, may be nil
	tools     []ToolDef
	run       func(ctx context.Context, app OAuthApp, ts tokenSet, name string, in json.RawMessage) (string, error)

	// Code review (GitHub and GitLab). All nil for the other providers.
	validProject func(project string) bool
	fetchChange  func(ctx context.Context, app OAuthApp, ts tokenSet, project string, number int) (changeEvent, error)
	postReview   func(ctx context.Context, app OAuthApp, ts tokenSet, ev changeEvent, summary string, comments []reviewComment) (string, error)
	createHook   func(ctx context.Context, app OAuthApp, ts tokenSet, project, hookURL, secret string) (string, error)
	deleteHook   func(ctx context.Context, app OAuthApp, ts tokenSet, project, hookID string) error
	parseHook    func(h http.Header, body []byte, secret string, onPush bool) (ev changeEvent, ignore string, err error)
}

type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
	Scope        string  `json:"scope"`
	Error        string  `json:"error"`
}

var providerOrder = []string{"github", "gitlab", "jira", "figma"}

var providers = map[string]*provider{}

func init() {
	for _, p := range []*provider{githubProvider(), gitlabProvider(), jiraProvider(), figmaProvider()} {
		providers[p.ID] = p
	}
}

func hasTool(tools []string, want string) bool {
	for _, t := range tools {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// ---------- small shared helpers ----------

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "… [cut]"
}

func compact(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func objSchema(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func (t tokenResponse) set() tokenSet {
	ts := tokenSet{Access: t.AccessToken, Refresh: t.RefreshToken}
	if t.ExpiresIn > 0 {
		ts.Expiry = time.Now().Unix() + int64(t.ExpiresIn)
	}
	return ts
}

// postToken sends a token request (form or JSON) and reads the standard answer.
func postToken(ctx context.Context, endpoint string, form url.Values, asJSON bool, basicID, basicSecret string) (tokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body io.Reader
	ct := "application/x-www-form-urlencoded"
	if asJSON {
		m := map[string]string{}
		for k := range form {
			m[k] = form.Get(k)
		}
		raw, _ := json.Marshal(m)
		body, ct = bytes.NewReader(raw), "application/json"
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Accept", "application/json")
	if basicID != "" {
		req.SetBasicAuth(basicID, basicSecret)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return tokenResponse{}, errors.New("could not reach the provider")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out tokenResponse
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 != 2 || out.AccessToken == "" {
		return out, fmt.Errorf("token endpoint answered %d %s", resp.StatusCode, out.Error)
	}
	return out, nil
}

// apiGet calls a provider's REST API. Errors are short and safe to show to the model.
func apiGet(ctx context.Context, label, base, path string, q url.Values, token string, hdr map[string]string) ([]byte, error) {
	return apiDo(ctx, label, http.MethodGet, base, path, q, nil, token, hdr)
}

// apiPost sends a JSON body.
func apiPost(ctx context.Context, label, base, path string, body any, token string, hdr map[string]string) ([]byte, error) {
	return apiDo(ctx, label, http.MethodPost, base, path, nil, body, token, hdr)
}

func apiDo(ctx context.Context, label, method, base, path string, q url.Values, body any, token string, hdr map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s", label)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 401:
		return nil, fmt.Errorf("%s rejected the stored token; an admin needs to reconnect %s", label, label)
	case resp.StatusCode == 403 || resp.StatusCode == 429:
		return nil, fmt.Errorf("%s refused the request (permissions or rate limit)", label)
	case resp.StatusCode == 404:
		return nil, errors.New("not found, or the connected account cannot see it")
	case resp.StatusCode == 400 || resp.StatusCode == 422:
		return nil, fmt.Errorf("%s did not accept the request: %s", label, cut(string(raw), 200))
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("%s returned %d", label, resp.StatusCode)
	}
	return raw, nil
}

// ---------- status ----------

type integrationStatus struct {
	Configured  bool   `json:"configured"`
	Connected   bool   `json:"connected"`
	Account     string `json:"account,omitempty"`
	Scopes      string `json:"scopes,omitempty"`
	ConnectedAt string `json:"connectedAt,omitempty"`
}

func (s *Server) statusOf(ctx context.Context, wsID string, p *provider) (integrationStatus, error) {
	st := integrationStatus{Configured: s.cfg.Apps[p.ID].Enabled()}
	info, enc, err := s.repo.Integration(ctx, wsID, p.ID)
	if err != nil {
		return st, err
	}
	if enc != nil {
		st.Connected, st.Account, st.Scopes, st.ConnectedAt = true, info.Account, info.Scopes, info.ConnectedAt
	}
	return st, nil
}

func (s *Server) integrations(w http.ResponseWriter, r *http.Request, c wsCtx) {
	out := map[string]integrationStatus{}
	for _, id := range providerOrder {
		st, err := s.statusOf(r.Context(), c.WS, providers[id])
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out[id] = st
	}
	writeJSON(w, http.StatusOK, out)
}

// providerOr404 finds the provider named in the path.
func providerOr404(w http.ResponseWriter, r *http.Request) *provider {
	p := providers[r.PathValue("provider")]
	if p == nil {
		writeError(w, http.StatusNotFound, "no such integration")
	}
	return p
}

// ---------- OAuth state ----------

func (s *Server) stateKey() []byte {
	h := sha256.Sum256(append([]byte("aw-oauth-state:"), s.cfg.Secret...))
	return h[:]
}

// signState binds an authorisation round trip to a provider, a workspace, a user and a browser (the nonce is also a cookie).
func (s *Server) signState(provider, ws, uid, nonce string, exp time.Time) string {
	payload := strings.Join([]string{provider, ws, uid, nonce, strconv.FormatInt(exp.Unix(), 10)}, "|")
	mac := hmac.New(sha256.New, s.stateKey())
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var (
	errBadState     = errors.New("bad state")
	errExpiredState = errors.New("expired state")
)

func (s *Server) verifyState(state string) (provider, ws, uid, nonce string, err error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", "", "", "", errBadState
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return "", "", "", "", errBadState
	}
	mac := hmac.New(sha256.New, s.stateKey())
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", "", "", "", errBadState
	}
	f := strings.Split(string(payload), "|")
	if len(f) != 5 {
		return "", "", "", "", errBadState
	}
	exp, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", "", "", errExpiredState
	}
	return f[0], f[1], f[2], f[3], nil
}

func (s *Server) callbackURL(p *provider) string {
	return s.cfg.PublicURL + "/api/integrations/" + p.ID + "/callback"
}

func (s *Server) cookieFor(p *provider, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: oauthCookiePrefix + p.ID, Value: value, Path: "/api/integrations/" + p.ID, MaxAge: maxAge,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
}

// ---------- connect, callback, disconnect ----------

// integrationStart returns the provider page the browser should go to. Only admins can connect.
func (s *Server) integrationStart(w http.ResponseWriter, r *http.Request, c wsCtx) {
	p := providerOr404(w, r)
	if p == nil {
		return
	}
	app := s.cfg.Apps[p.ID]
	if !app.Enabled() {
		up := strings.ToUpper(p.ID)
		writeError(w, http.StatusConflict, fmt.Sprintf("This server has no %s OAuth app configured. Set %s_CLIENT_ID and %s_CLIENT_SECRET.", p.Label, up, up))
		return
	}
	nonce := randomToken()
	http.SetCookie(w, s.cookieFor(p, nonce, int(stateLifetime.Seconds())))
	q := url.Values{}
	q.Set("client_id", app.ClientID)
	q.Set("redirect_uri", s.callbackURL(p))
	q.Set("scope", app.Scopes)
	q.Set("state", s.signState(p.ID, c.WS, c.User.ID, nonce, time.Now().Add(stateLifetime)))
	writeJSON(w, http.StatusOK, map[string]string{"url": p.authorize(app, q)})
}

// integrationCallback is where the provider sends the browser back. Failures redirect to the app
// with a short reason code, never the provider's own text.
func (s *Server) integrationCallback(w http.ResponseWriter, r *http.Request) {
	p := providerOr404(w, r)
	if p == nil {
		return
	}
	fail := func(reason, ws string) {
		dest := "/?integration=" + p.ID + "&result=error&reason=" + reason
		if ws != "" {
			dest += "#/s/" + url.PathEscape(ws)
		}
		http.Redirect(w, r, dest, http.StatusSeeOther)
	}
	q := r.URL.Query()
	stProv, ws, uid, nonce, err := s.verifyState(q.Get("state"))
	if err != nil {
		if errors.Is(err, errExpiredState) {
			fail("expired", "")
		} else {
			fail("state", "")
		}
		return
	}
	ck, cerr := r.Cookie(oauthCookiePrefix + p.ID)
	if stProv != p.ID || cerr != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(nonce)) != 1 {
		fail("state", ws)
		return
	}
	http.SetCookie(w, s.cookieFor(p, "", -1))

	u, err := s.currentUser(r)
	if err != nil || u.ID != uid {
		fail("session", "")
		return
	}
	if role, err := s.repo.Role(r.Context(), ws, u.ID); err != nil || roleRank[role] < roleRank["admin"] {
		fail("forbidden", "")
		return
	}
	app := s.cfg.Apps[p.ID]
	if !app.Enabled() {
		fail("failed", ws)
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		fail("denied", ws)
		return
	}
	tr, err := p.exchange(r.Context(), app, s.callbackURL(p), q.Get("code"))
	if err != nil {
		s.log.Warn("token exchange failed", "provider", p.ID, "err", err)
		fail("exchange", ws)
		return
	}
	ts := tr.set()
	account, err := p.identify(r.Context(), app, &ts)
	if err != nil {
		s.log.Warn("account lookup failed", "provider", p.ID, "err", err)
		fail("exchange", ws)
		return
	}
	raw, _ := json.Marshal(ts)
	scopes := tr.Scope
	if scopes == "" {
		scopes = app.Scopes
	}
	if err := s.repo.SetIntegration(r.Context(), ws, p.ID, u.ID, account, scopes, s.sealer.Seal(string(raw), ws+"|"+p.ID)); err != nil {
		s.log.Error("store token", "provider", p.ID, "err", err)
		fail("failed", ws)
		return
	}
	http.Redirect(w, r, "/?integration="+p.ID+"&result=connected#/s/"+url.PathEscape(ws), http.StatusSeeOther)
}

func (s *Server) integrationDisconnect(w http.ResponseWriter, r *http.Request, c wsCtx) {
	p := providerOr404(w, r)
	if p == nil {
		return
	}
	_, enc, err := s.repo.Integration(r.Context(), c.WS, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.repo.DeleteIntegration(r.Context(), c.WS, p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if enc != nil && p.revoke != nil { // best effort: also revoke the grant at the provider
		if plain, err := s.sealer.Open(enc, c.WS+"|"+p.ID); err == nil {
			var ts tokenSet
			if json.Unmarshal([]byte(plain), &ts) == nil {
				app := s.cfg.Apps[p.ID]
				go p.revoke(app, ts)
			}
		}
	}
	s.integrations(w, r, c)
}

// ---------- using a stored token ----------

var errNotConnected = errors.New("not connected")

func (s *Server) openTokens(enc []byte, wsID string, p *provider) (tokenSet, error) {
	plain, err := s.sealer.Open(enc, wsID+"|"+p.ID)
	if err != nil {
		return tokenSet{}, err
	}
	var ts tokenSet
	if err := json.Unmarshal([]byte(plain), &ts); err != nil || ts.Access == "" {
		return tokenSet{}, errors.New("stored token is unreadable; reconnect")
	}
	return ts, nil
}

// tokensFor returns a usable token, refreshing it first if it is about to expire. The refresh
// runs under a row lock because some providers rotate the refresh token on every use.
func (s *Server) tokensFor(ctx context.Context, wsID string, p *provider) (tokenSet, error) {
	_, enc, err := s.repo.Integration(ctx, wsID, p.ID)
	if err != nil {
		return tokenSet{}, err
	}
	if enc == nil {
		return tokenSet{}, errNotConnected
	}
	ts, err := s.openTokens(enc, wsID, p)
	if err != nil {
		return tokenSet{}, err
	}
	if !ts.expiring() {
		return ts, nil
	}
	reconnect := fmt.Errorf("the %s connection has expired; an admin needs to reconnect %s", p.Label, p.Label)
	if p.refresh == nil || ts.Refresh == "" {
		return tokenSet{}, reconnect
	}
	app := s.cfg.Apps[p.ID]
	final, err := s.repo.LockIntegration(ctx, wsID, p.ID, func(cur []byte) ([]byte, error) {
		now, err := s.openTokens(cur, wsID, p)
		if err != nil {
			return nil, err
		}
		if !now.expiring() { // another request already refreshed it
			return nil, nil
		}
		tr, err := p.refresh(ctx, app, now)
		if err != nil {
			s.log.Warn("token refresh failed", "provider", p.ID, "err", err)
			return nil, reconnect
		}
		next := tr.set()
		if next.Refresh == "" {
			next.Refresh = now.Refresh // some providers keep the refresh token
		}
		next.Extra = now.Extra
		raw, _ := json.Marshal(next)
		return s.sealer.Seal(string(raw), wsID+"|"+p.ID), nil
	})
	if err != nil {
		return tokenSet{}, err
	}
	if final == nil {
		return tokenSet{}, errNotConnected
	}
	return s.openTokens(final, wsID, p)
}

// toolbox collects the tools an agent step may use: only for providers the step lists as a tool
// and that the workspace has connected. It returns the labels of those providers for the prompt.
// Inside a run started by a pull or merge request (rc != nil) the step also gets post_review,
// which can only ever write to that one change.
func (s *Server) toolbox(ctx context.Context, wsID string, stepTools []string, rc *runCtx) (defs []ToolDef, run ToolRunner, labels []string, err error) {
	handlers := map[string]func(ctx context.Context, in json.RawMessage) (string, error){}
	for _, id := range providerOrder {
		p := providers[id]
		if !hasTool(stepTools, p.Label) {
			continue
		}
		_, enc, err := s.repo.Integration(ctx, wsID, p.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		if enc == nil {
			continue
		}
		labels = append(labels, p.Label)
		for _, t := range p.tools {
			defs = append(defs, t)
			name := t.Name
			handlers[name] = func(ctx context.Context, in json.RawMessage) (string, error) {
				ts, err := s.tokensFor(ctx, wsID, p)
				if err != nil {
					return "", s.tokenError(p, err)
				}
				return p.run(ctx, s.cfg.Apps[p.ID], ts, name, in)
			}
		}
		if rc != nil && rc.Ev.Provider == p.ID && p.postReview != nil {
			defs = append(defs, postReviewTool(p))
			handlers["post_review"] = func(ctx context.Context, in json.RawMessage) (string, error) {
				return s.postReviewTool(ctx, rc, p, in)
			}
		}
	}
	if hasTool(stepTools, webLabel) {
		labels = append(labels, webLabel)
		for _, t := range webTools(s.cfg.WebSearch) {
			defs = append(defs, t)
			handlers[t.Name] = func(ctx context.Context, in json.RawMessage) (string, error) { return s.runWeb(ctx, t.Name, in) }
		}
	}
	if len(defs) == 0 {
		return nil, nil, nil, nil
	}
	run = func(ctx context.Context, name string, in json.RawMessage) (string, error) {
		h := handlers[name]
		if h == nil {
			return "", fmt.Errorf("unknown tool %q", name)
		}
		return h(ctx, in)
	}
	return defs, run, labels, nil
}

func (s *Server) tokenError(p *provider, err error) error {
	if errors.Is(err, errNotConnected) {
		return fmt.Errorf("%s is no longer connected", p.Label)
	}
	s.log.Warn("token unavailable", "provider", p.ID, "err", err)
	return errors.New(cut(err.Error(), 200))
}

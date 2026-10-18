package main

import (
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
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	githubProvider = "github"
	oauthCookie    = "aw_oauth"
	stateLifetime  = 10 * time.Minute
)

// ---------- connecting (OAuth) ----------

type githubStatus struct {
	Configured  bool   `json:"configured"`
	Connected   bool   `json:"connected"`
	Account     string `json:"account,omitempty"`
	Scopes      string `json:"scopes,omitempty"`
	ConnectedAt string `json:"connectedAt,omitempty"`
}

func (s *Server) githubStatusFor(ctx context.Context, wsID string) (githubStatus, error) {
	st := githubStatus{Configured: s.cfg.GitHub.Enabled()}
	info, enc, err := s.repo.Integration(ctx, wsID, githubProvider)
	if err != nil {
		return st, err
	}
	if enc != nil {
		st.Connected, st.Account, st.Scopes, st.ConnectedAt = true, info.Account, info.Scopes, info.ConnectedAt
	}
	return st, nil
}

func (s *Server) integrations(w http.ResponseWriter, r *http.Request, c wsCtx) {
	gh, err := s.githubStatusFor(r.Context(), c.WS)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"github": gh})
}

func (s *Server) stateKey() []byte {
	h := sha256.Sum256(append([]byte("aw-oauth-state:"), s.cfg.Secret...))
	return h[:]
}

// signState binds an authorisation round trip to one workspace, one user and one browser (the nonce is also set as a cookie).
func (s *Server) signState(ws, uid, nonce string, exp time.Time) string {
	payload := strings.Join([]string{ws, uid, nonce, strconv.FormatInt(exp.Unix(), 10)}, "|")
	mac := hmac.New(sha256.New, s.stateKey())
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

var errBadState = errors.New("bad state")

func (s *Server) verifyState(state string) (ws, uid, nonce string, err error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", "", "", errBadState
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return "", "", "", errBadState
	}
	mac := hmac.New(sha256.New, s.stateKey())
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", "", "", errBadState
	}
	f := strings.Split(string(payload), "|")
	if len(f) != 4 {
		return "", "", "", errBadState
	}
	exp, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", "", errExpiredState
	}
	return f[0], f[1], f[2], nil
}

var errExpiredState = errors.New("expired state")

func (s *Server) callbackURL() string { return s.cfg.PublicURL + "/api/integrations/github/callback" }

// githubStart returns the GitHub page the browser should go to. Only admins can connect.
func (s *Server) githubStart(w http.ResponseWriter, r *http.Request, c wsCtx) {
	g := s.cfg.GitHub
	if !g.Enabled() {
		writeError(w, http.StatusConflict, "This server has no GitHub OAuth app configured. Set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET.")
		return
	}
	nonce := randomToken()
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookie, Value: nonce, Path: "/api/integrations/github", MaxAge: int(stateLifetime.Seconds()),
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{}
	q.Set("client_id", g.ClientID)
	q.Set("redirect_uri", s.callbackURL())
	q.Set("scope", g.Scopes)
	q.Set("state", s.signState(c.WS, c.User.ID, nonce, time.Now().Add(stateLifetime)))
	q.Set("allow_signup", "false")
	writeJSON(w, http.StatusOK, map[string]string{"url": g.OAuthURL + "/login/oauth/authorize?" + q.Encode()})
}

// githubCallback is where GitHub sends the browser back. Failures redirect to the app with a short reason code, never GitHub's text.
func (s *Server) githubCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(reason string, ws string) {
		dest := "/?github=error&reason=" + reason
		if ws != "" {
			dest += "#/s/" + url.PathEscape(ws)
		}
		http.Redirect(w, r, dest, http.StatusSeeOther)
	}
	q := r.URL.Query()
	ws, uid, nonce, err := s.verifyState(q.Get("state"))
	if err != nil {
		if errors.Is(err, errExpiredState) {
			fail("expired", "")
		} else {
			fail("state", "")
		}
		return
	}
	ck, cerr := r.Cookie(oauthCookie)
	if cerr != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(nonce)) != 1 {
		fail("state", ws)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: "", Path: "/api/integrations/github", MaxAge: -1, HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})

	u, err := s.currentUser(r)
	if err != nil || u.ID != uid {
		fail("session", "")
		return
	}
	if role, err := s.repo.Role(r.Context(), ws, u.ID); err != nil || roleRank[role] < roleRank["admin"] {
		fail("forbidden", "")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		fail("denied", ws)
		return
	}
	token, scopes, err := s.githubExchange(r.Context(), q.Get("code"))
	if err != nil {
		s.log.Warn("github token exchange failed", "err", err)
		fail("exchange", ws)
		return
	}
	login, err := newGitHub(s.cfg.GitHub, token).login(r.Context())
	if err != nil {
		s.log.Warn("github user lookup failed", "err", err)
		fail("exchange", ws)
		return
	}
	if err := s.repo.SetIntegration(r.Context(), ws, githubProvider, u.ID, login, scopes, s.sealer.Seal(token, ws+"|"+githubProvider)); err != nil {
		s.log.Error("store github token", "err", err)
		fail("failed", ws)
		return
	}
	http.Redirect(w, r, "/?github=connected#/s/"+url.PathEscape(ws), http.StatusSeeOther)
}

func (s *Server) githubExchange(ctx context.Context, code string) (token, scopes string, err error) {
	g := s.cfg.GitHub
	form := url.Values{"client_id": {g.ClientID}, "client_secret": {g.ClientSecret}, "code": {code}, "redirect_uri": {s.callbackURL()}}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.OAuthURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != 200 || out.AccessToken == "" {
		return "", "", fmt.Errorf("github answered %d %s", resp.StatusCode, out.Error)
	}
	return out.AccessToken, out.Scope, nil
}

func (s *Server) githubDisconnect(w http.ResponseWriter, r *http.Request, c wsCtx) {
	_, enc, err := s.repo.Integration(r.Context(), c.WS, githubProvider)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.repo.DeleteIntegration(r.Context(), c.WS, githubProvider); err != nil {
		s.fail(w, r, err)
		return
	}
	if enc != nil { // best effort: also revoke the grant at GitHub
		if tok, err := s.sealer.Open(enc, c.WS+"|"+githubProvider); err == nil {
			go s.githubRevoke(tok)
		}
	}
	s.integrations(w, r, c)
}

func (s *Server) githubRevoke(token string) {
	g := s.cfg.GitHub
	if !g.Enabled() {
		return
	}
	body, _ := json.Marshal(map[string]string{"access_token": token})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, g.APIURL+"/applications/"+url.PathEscape(g.ClientID)+"/grant", strings.NewReader(string(body)))
	req.SetBasicAuth(g.ClientID, g.ClientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agent-workflow")
	if resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req); err == nil {
		resp.Body.Close()
	}
}

// ---------- GitHub API (read only) ----------

type gitHub struct {
	cfg   GitHubConfig
	token string
	http  *http.Client
}

func newGitHub(cfg GitHubConfig, token string) *gitHub {
	return &gitHub{cfg: cfg, token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

func (g *gitHub) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := g.cfg.APIURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agent-workflow")
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, errors.New("could not reach GitHub")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 401:
		return nil, errors.New("GitHub rejected the stored token; an admin needs to reconnect GitHub")
	case resp.StatusCode == 403 || resp.StatusCode == 429:
		return nil, errors.New("GitHub refused the request (permissions or rate limit)")
	case resp.StatusCode == 404:
		return nil, errors.New("not found, or the connected account cannot see it")
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}
	return raw, nil
}

func (g *gitHub) login(ctx context.Context) (string, error) {
	raw, err := g.get(ctx, "/user", nil)
	if err != nil {
		return "", err
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(raw, &u); err != nil || u.Login == "" {
		return "", errors.New("GitHub did not say who this is")
	}
	return u.Login, nil
}

var (
	ghName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	ghRef  = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,200}$`)
)

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "… [cut]"
}

type toolInput struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	State  string `json:"state"`
	Number int    `json:"number"`
	Limit  int    `json:"limit"`
	Path   string `json:"path"`
	Ref    string `json:"ref"`
}

func (in toolInput) repoPath() (string, error) {
	if !ghName.MatchString(in.Owner) || !ghName.MatchString(in.Repo) || in.Owner == "." || in.Owner == ".." || in.Repo == "." || in.Repo == ".." {
		return "", errors.New("owner and repo must be plain names like \"octocat\" and \"hello-world\"")
	}
	return "/repos/" + in.Owner + "/" + in.Repo, nil
}

func (in toolInput) limit(def int) string {
	if in.Limit < 1 {
		return strconv.Itoa(def)
	}
	if in.Limit > 30 {
		return "30"
	}
	return strconv.Itoa(in.Limit)
}

func (in toolInput) state() (string, error) {
	switch in.State {
	case "", "open":
		return "open", nil
	case "closed", "all":
		return in.State, nil
	}
	return "", errors.New("state must be open, closed or all")
}

func compact(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func names(items []struct {
	Name string `json:"name"`
}) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.Name)
	}
	return out
}

var ownerRepoSchema = map[string]any{
	"owner": map[string]any{"type": "string", "description": "Account or organisation that owns the repository"},
	"repo":  map[string]any{"type": "string", "description": "Repository name"},
}

func schema(extra map[string]any, required ...string) map[string]any {
	props := map[string]any{}
	for k, v := range ownerRepoSchema {
		props[k] = v
	}
	for k, v := range extra {
		props[k] = v
	}
	return map[string]any{"type": "object", "properties": props, "required": append([]string{"owner", "repo"}, required...)}
}

var stateProp = map[string]any{"type": "string", "enum": []string{"open", "closed", "all"}, "description": "Which to list (default open)"}
var limitProp = map[string]any{"type": "integer", "description": "How many to return, at most 30"}
var numberProp = map[string]any{"type": "integer", "description": "Issue or pull request number"}

// githubTools are the read-only tools an agent with the GitHub tool can use.
func githubTools() []ToolDef {
	return []ToolDef{
		{Name: "github_get_repo", Description: "Read a repository's description, default branch, language and open issue count.", InputSchema: schema(nil)},
		{Name: "github_list_issues", Description: "List issues of a repository, newest first. Pull requests are not included.", InputSchema: schema(map[string]any{"state": stateProp, "limit": limitProp})},
		{Name: "github_get_issue", Description: "Read one issue with its first comments.", InputSchema: schema(map[string]any{"number": numberProp}, "number")},
		{Name: "github_list_pull_requests", Description: "List pull requests of a repository, newest first.", InputSchema: schema(map[string]any{"state": stateProp, "limit": limitProp})},
		{Name: "github_get_pull_request", Description: "Read one pull request: description and the list of changed files (no diffs).", InputSchema: schema(map[string]any{"number": numberProp}, "number")},
		{Name: "github_list_commits", Description: "List recent commits, optionally on one branch.", InputSchema: schema(map[string]any{"ref": map[string]any{"type": "string", "description": "Branch or tag (default: the default branch)"}, "limit": limitProp})},
		{Name: "github_read_file", Description: "Read a text file, or list a directory, from a repository.", InputSchema: schema(map[string]any{"path": map[string]any{"type": "string", "description": "Path inside the repository; empty for the top level"}, "ref": map[string]any{"type": "string", "description": "Branch, tag or commit (default: the default branch)"}}, "path")},
	}
}

// run executes one tool call. Every reply is data for Claude to read, never an instruction.
func (g *gitHub) run(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", errors.New("the tool input was not valid")
	}
	base, err := in.repoPath()
	if err != nil {
		return "", err
	}
	switch name {
	case "github_get_repo":
		b, err := g.get(ctx, base, nil)
		if err != nil {
			return "", err
		}
		var r struct {
			FullName      string   `json:"full_name"`
			Description   string   `json:"description"`
			DefaultBranch string   `json:"default_branch"`
			Language      string   `json:"language"`
			Stars         int      `json:"stargazers_count"`
			OpenIssues    int      `json:"open_issues_count"`
			Private       bool     `json:"private"`
			Topics        []string `json:"topics"`
			UpdatedAt     string   `json:"updated_at"`
			URL           string   `json:"html_url"`
		}
		_ = json.Unmarshal(b, &r)
		r.Description = cut(r.Description, 500)
		return compact(r), nil

	case "github_list_issues", "github_list_pull_requests":
		state, err := in.state()
		if err != nil {
			return "", err
		}
		path := "/issues"
		if name == "github_list_pull_requests" {
			path = "/pulls"
		}
		b, err := g.get(ctx, base+path, url.Values{"state": {state}, "per_page": {in.limit(15)}, "sort": {"created"}, "direction": {"desc"}})
		if err != nil {
			return "", err
		}
		var items []struct {
			Number      int             `json:"number"`
			Title       string          `json:"title"`
			State       string          `json:"state"`
			Draft       bool            `json:"draft"`
			Body        string          `json:"body"`
			CreatedAt   string          `json:"created_at"`
			Comments    int             `json:"comments"`
			URL         string          `json:"html_url"`
			PullRequest json.RawMessage `json:"pull_request"`
			User        struct {
				Login string `json:"login"`
			} `json:"user"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		}
		_ = json.Unmarshal(b, &items)
		out := []map[string]any{}
		for _, i := range items {
			if name == "github_list_issues" && len(i.PullRequest) > 0 {
				continue // the issues endpoint also lists pull requests
			}
			out = append(out, map[string]any{"number": i.Number, "title": cut(i.Title, 200), "state": i.State, "author": i.User.Login,
				"labels": names(i.Labels), "created": i.CreatedAt, "comments": i.Comments, "draft": i.Draft, "url": i.URL, "body": cut(i.Body, 400)})
		}
		return compact(out), nil

	case "github_get_issue", "github_get_pull_request":
		if in.Number < 1 || in.Number > 10_000_000 {
			return "", errors.New("number must be a positive integer")
		}
		n := strconv.Itoa(in.Number)
		kind := "/issues/"
		if name == "github_get_pull_request" {
			kind = "/pulls/"
		}
		b, err := g.get(ctx, base+kind+n, nil)
		if err != nil {
			return "", err
		}
		var it struct {
			Number    int    `json:"number"`
			Title     string `json:"title"`
			State     string `json:"state"`
			Body      string `json:"body"`
			CreatedAt string `json:"created_at"`
			Merged    bool   `json:"merged"`
			URL       string `json:"html_url"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			Head struct {
				Ref string `json:"ref"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		_ = json.Unmarshal(b, &it)
		out := map[string]any{"number": it.Number, "title": cut(it.Title, 300), "state": it.State, "author": it.User.Login, "created": it.CreatedAt, "url": it.URL, "body": cut(it.Body, 3000)}
		if name == "github_get_pull_request" {
			out["merged"], out["from"], out["into"] = it.Merged, it.Head.Ref, it.Base.Ref
			if fb, err := g.get(ctx, base+"/pulls/"+n+"/files", url.Values{"per_page": {"50"}}); err == nil {
				var files []struct {
					Filename  string `json:"filename"`
					Status    string `json:"status"`
					Additions int    `json:"additions"`
					Deletions int    `json:"deletions"`
				}
				_ = json.Unmarshal(fb, &files)
				out["files"] = files
			}
		} else if cb, err := g.get(ctx, base+"/issues/"+n+"/comments", url.Values{"per_page": {"20"}}); err == nil {
			var cs []struct {
				Body string `json:"body"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}
			_ = json.Unmarshal(cb, &cs)
			comments := []map[string]string{}
			for _, c := range cs {
				comments = append(comments, map[string]string{"author": c.User.Login, "body": cut(c.Body, 800)})
			}
			out["comments"] = comments
		}
		return compact(out), nil

	case "github_list_commits":
		q := url.Values{"per_page": {in.limit(15)}}
		if in.Ref != "" {
			if !ghRef.MatchString(in.Ref) || strings.Contains(in.Ref, "..") {
				return "", errors.New("ref is not valid")
			}
			q.Set("sha", in.Ref)
		}
		b, err := g.get(ctx, base+"/commits", q)
		if err != nil {
			return "", err
		}
		var cs []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		}
		_ = json.Unmarshal(b, &cs)
		out := []map[string]string{}
		for _, c := range cs {
			sha := c.SHA
			if len(sha) > 10 {
				sha = sha[:10]
			}
			out = append(out, map[string]string{"sha": sha, "author": c.Commit.Author.Name, "date": c.Commit.Author.Date, "message": cut(c.Commit.Message, 300)})
		}
		return compact(out), nil

	case "github_read_file":
		segs := []string{}
		for _, p := range strings.Split(strings.Trim(in.Path, "/"), "/") {
			if p == "" {
				continue
			}
			if p == "." || p == ".." {
				return "", errors.New("path must not contain . or ..")
			}
			segs = append(segs, url.PathEscape(p))
		}
		q := url.Values{}
		if in.Ref != "" {
			if !ghRef.MatchString(in.Ref) || strings.Contains(in.Ref, "..") {
				return "", errors.New("ref is not valid")
			}
			q.Set("ref", in.Ref)
		}
		b, err := g.get(ctx, base+"/contents/"+strings.Join(segs, "/"), q)
		if err != nil {
			return "", err
		}
		if len(b) > 0 && b[0] == '[' {
			var entries []struct {
				Name string `json:"name"`
				Type string `json:"type"`
				Size int    `json:"size"`
			}
			_ = json.Unmarshal(b, &entries)
			return compact(map[string]any{"directory": entries}), nil
		}
		var f struct {
			Type     string `json:"type"`
			Size     int    `json:"size"`
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
		}
		_ = json.Unmarshal(b, &f)
		if f.Type != "file" || f.Encoding != "base64" {
			return "", errors.New("that path is not a readable file")
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
		if err != nil {
			return "", errors.New("could not decode the file")
		}
		for _, c := range data[:min(len(data), 2000)] {
			if c == 0 {
				return compact(map[string]any{"binary": true, "size": f.Size}), nil
			}
		}
		return compact(map[string]any{"size": f.Size, "content": cut(string(data), 20000)}), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func hasTool(tools []string, want string) bool {
	for _, t := range tools {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// githubFor returns a client for the workspace's stored token, or nil when GitHub is not connected.
func (s *Server) githubFor(ctx context.Context, wsID string) (*gitHub, error) {
	_, enc, err := s.repo.Integration(ctx, wsID, githubProvider)
	if err != nil || enc == nil {
		return nil, err
	}
	tok, err := s.sealer.Open(enc, wsID+"|"+githubProvider)
	if err != nil {
		return nil, err
	}
	return newGitHub(s.cfg.GitHub, tok), nil
}

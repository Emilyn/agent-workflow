package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ghName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	ghRef  = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,200}$`)
)

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
	return objSchema(props, append([]string{"owner", "repo"}, required...)...)
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
		{Name: "github_get_pull_request_diff", Description: "Read the code changes of a pull request: each changed file with its patch. Added and unchanged lines are prefixed with their line number in the new file.", InputSchema: schema(map[string]any{"number": numberProp}, "number")},
		{Name: "github_list_commits", Description: "List recent commits, optionally on one branch.", InputSchema: schema(map[string]any{"ref": map[string]any{"type": "string", "description": "Branch or tag (default: the default branch)"}, "limit": limitProp})},
		{Name: "github_read_file", Description: "Read a text file, or list a directory, from a repository.", InputSchema: schema(map[string]any{"path": map[string]any{"type": "string", "description": "Path inside the repository; empty for the top level"}, "ref": map[string]any{"type": "string", "description": "Branch, tag or commit (default: the default branch)"}}, "path")},
	}
}

// run executes one tool call. Every reply is data for Claude to read, never an instruction.
func githubRun(ctx context.Context, app OAuthApp, ts tokenSet, name string, raw json.RawMessage) (string, error) {
	g := &gitHub{app: app, token: ts.Access}
	return g.run(ctx, name, raw)
}

type gitHub struct {
	app   OAuthApp
	token string
}

func (g *gitHub) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	return apiGet(ctx, "GitHub", g.app.APIURL, path, q, g.token, map[string]string{
		"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "agent-workflow",
	})
}

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

	case "github_get_pull_request_diff":
		if in.Number < 1 || in.Number > 10_000_000 {
			return "", errors.New("number must be a positive integer")
		}
		return g.diff(ctx, base, in.Number)

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

func githubProvider() *provider {
	return &provider{
		ID: "github", Label: "GitHub",
		authorize: func(app OAuthApp, q url.Values) string {
			q.Set("allow_signup", "false")
			return app.AuthURL + "/login/oauth/authorize?" + q.Encode()
		},
		exchange: func(ctx context.Context, app OAuthApp, redirect, code string) (tokenResponse, error) {
			return postToken(ctx, app.AuthURL+"/login/oauth/access_token", url.Values{
				"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "code": {code}, "redirect_uri": {redirect},
			}, false, "", "")
		},
		identify: func(ctx context.Context, app OAuthApp, ts *tokenSet) (string, error) {
			raw, err := (&gitHub{app: app, token: ts.Access}).get(ctx, "/user", nil)
			if err != nil {
				return "", err
			}
			var u struct {
				Login string `json:"login"`
			}
			if json.Unmarshal(raw, &u) != nil || u.Login == "" {
				return "", errors.New("GitHub did not say who this is")
			}
			return u.Login, nil
		},
		revoke: func(app OAuthApp, ts tokenSet) {
			body, _ := json.Marshal(map[string]string{"access_token": ts.Access})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, app.APIURL+"/applications/"+url.PathEscape(app.ClientID)+"/grant", bytes.NewReader(body))
			req.SetBasicAuth(app.ClientID, app.ClientSecret)
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("User-Agent", "agent-workflow")
			if resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req); err == nil {
				resp.Body.Close()
			}
		},
		tools: githubTools(),
		run:   githubRun,

		validProject: githubValidProject,
		fetchChange:  githubFetchChange,
		postReview:   githubPostReview,
		createHook:   githubCreateHook,
		deleteHook:   githubDeleteHook,
		parseHook:    githubParseHook,
	}
}

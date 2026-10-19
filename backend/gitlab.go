package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type glInput struct {
	Project string `json:"project"`
	State   string `json:"state"`
	Number  int    `json:"number"`
	Limit   int    `json:"limit"`
	Path    string `json:"path"`
	Ref     string `json:"ref"`
}

// projectID turns "group/sub/name" (or a numeric id) into the escaped form GitLab expects.
func (in glInput) projectID() (string, error) {
	p := strings.Trim(in.Project, "/")
	segs := strings.Split(p, "/")
	if p == "" || len(segs) > 10 {
		return "", errors.New("project must be a path like \"group/name\" or a numeric id")
	}
	for _, sgm := range segs {
		if !ghName.MatchString(sgm) || sgm == "." || sgm == ".." {
			return "", errors.New("project must be a path like \"group/name\" or a numeric id")
		}
	}
	return "/projects/" + url.PathEscape(p), nil
}

func (in glInput) per(def int) string {
	if in.Limit < 1 {
		return strconv.Itoa(def)
	}
	return strconv.Itoa(min(in.Limit, 30))
}

func (in glInput) mapState(allowed ...string) (string, error) {
	s := in.State
	if s == "" || s == "open" {
		s = "opened"
	}
	for _, a := range allowed {
		if s == a {
			return s, nil
		}
	}
	return "", errors.New("state is not valid for this tool (use open, closed or all; merge requests also allow merged)")
}

func gitlabTools() []ToolDef {
	proj := map[string]any{"project": map[string]any{"type": "string", "description": "Project path like \"group/name\" (subgroups allowed) or its numeric id"}}
	with := func(extra map[string]any, required ...string) map[string]any {
		props := map[string]any{"project": proj["project"]}
		for k, v := range extra {
			props[k] = v
		}
		return objSchema(props, append([]string{"project"}, required...)...)
	}
	state := map[string]any{"type": "string", "enum": []string{"open", "closed", "all"}, "description": "Which to list (default open)"}
	mrState := map[string]any{"type": "string", "enum": []string{"open", "closed", "merged", "all"}, "description": "Which to list (default open)"}
	number := map[string]any{"type": "integer", "description": "The issue or merge request number (the !/# number shown in GitLab, its iid)"}
	return []ToolDef{
		{Name: "gitlab_get_project", Description: "Read a GitLab project's description, default branch and open issue count.", InputSchema: with(nil)},
		{Name: "gitlab_list_issues", Description: "List issues of a GitLab project, newest first.", InputSchema: with(map[string]any{"state": state, "limit": limitProp})},
		{Name: "gitlab_get_issue", Description: "Read one GitLab issue with its first comments.", InputSchema: with(map[string]any{"number": number}, "number")},
		{Name: "gitlab_list_merge_requests", Description: "List merge requests of a GitLab project, newest first.", InputSchema: with(map[string]any{"state": mrState, "limit": limitProp})},
		{Name: "gitlab_get_merge_request", Description: "Read one merge request: description and changed file names (no diffs).", InputSchema: with(map[string]any{"number": number}, "number")},
		{Name: "gitlab_get_merge_request_diff", Description: "Read the code changes of a merge request: each changed file with its patch. Added and unchanged lines are prefixed with their line number in the new file.", InputSchema: with(map[string]any{"number": number}, "number")},
		{Name: "gitlab_list_commits", Description: "List recent commits, optionally on one branch.", InputSchema: with(map[string]any{"ref": map[string]any{"type": "string", "description": "Branch or tag (default: the default branch)"}, "limit": limitProp})},
		{Name: "gitlab_read_file", Description: "Read a text file, or list a directory, from a GitLab project.", InputSchema: with(map[string]any{"path": map[string]any{"type": "string", "description": "Path inside the repository; empty for the top level"}, "ref": map[string]any{"type": "string", "description": "Branch, tag or commit (default: the default branch)"}}, "path")},
	}
}

func gitlabRun(ctx context.Context, app OAuthApp, ts tokenSet, name string, raw json.RawMessage) (string, error) {
	var in glInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", errors.New("the tool input was not valid")
	}
	base, err := in.projectID()
	if err != nil {
		return "", err
	}
	get := func(path string, q url.Values) ([]byte, error) {
		return apiGet(ctx, "GitLab", app.APIURL, path, q, ts.Access, nil)
	}
	switch name {
	case "gitlab_get_project":
		b, err := get(base, nil)
		if err != nil {
			return "", err
		}
		var r struct {
			Path        string `json:"path_with_namespace"`
			Description string `json:"description"`
			Default     string `json:"default_branch"`
			OpenIssues  int    `json:"open_issues_count"`
			Visibility  string `json:"visibility"`
			Activity    string `json:"last_activity_at"`
			URL         string `json:"web_url"`
		}
		_ = json.Unmarshal(b, &r)
		r.Description = cut(r.Description, 500)
		return compact(r), nil

	case "gitlab_list_issues", "gitlab_list_merge_requests":
		var state string
		path := "/issues"
		if name == "gitlab_list_merge_requests" {
			path = "/merge_requests"
			state, err = in.mapState("opened", "closed", "merged", "all")
		} else {
			state, err = in.mapState("opened", "closed", "all")
		}
		if err != nil {
			return "", err
		}
		b, err := get(base+path, url.Values{"state": {state}, "per_page": {in.per(15)}, "order_by": {"created_at"}, "sort": {"desc"}})
		if err != nil {
			return "", err
		}
		var items []struct {
			IID         int      `json:"iid"`
			Title       string   `json:"title"`
			State       string   `json:"state"`
			Description string   `json:"description"`
			CreatedAt   string   `json:"created_at"`
			Labels      []string `json:"labels"`
			Draft       bool     `json:"draft"`
			URL         string   `json:"web_url"`
			Author      struct {
				Username string `json:"username"`
			} `json:"author"`
		}
		_ = json.Unmarshal(b, &items)
		out := []map[string]any{}
		for _, i := range items {
			out = append(out, map[string]any{"number": i.IID, "title": cut(i.Title, 200), "state": i.State, "author": i.Author.Username,
				"labels": i.Labels, "created": i.CreatedAt, "draft": i.Draft, "url": i.URL, "body": cut(i.Description, 400)})
		}
		return compact(out), nil

	case "gitlab_get_issue", "gitlab_get_merge_request":
		if in.Number < 1 || in.Number > 10_000_000 {
			return "", errors.New("number must be a positive integer")
		}
		n := strconv.Itoa(in.Number)
		kind := "/issues/"
		if name == "gitlab_get_merge_request" {
			kind = "/merge_requests/"
		}
		b, err := get(base+kind+n, nil)
		if err != nil {
			return "", err
		}
		var it struct {
			IID         int    `json:"iid"`
			Title       string `json:"title"`
			State       string `json:"state"`
			Description string `json:"description"`
			CreatedAt   string `json:"created_at"`
			URL         string `json:"web_url"`
			Source      string `json:"source_branch"`
			Target      string `json:"target_branch"`
			Author      struct {
				Username string `json:"username"`
			} `json:"author"`
		}
		_ = json.Unmarshal(b, &it)
		out := map[string]any{"number": it.IID, "title": cut(it.Title, 300), "state": it.State, "author": it.Author.Username, "created": it.CreatedAt, "url": it.URL, "body": cut(it.Description, 3000)}
		if name == "gitlab_get_merge_request" {
			out["from"], out["into"] = it.Source, it.Target
			if fb, err := get(base+kind+n+"/diffs", url.Values{"per_page": {"50"}}); err == nil {
				var files []struct {
					Old     string `json:"old_path"`
					New     string `json:"new_path"`
					Added   bool   `json:"new_file"`
					Deleted bool   `json:"deleted_file"`
					Renamed bool   `json:"renamed_file"`
				}
				_ = json.Unmarshal(fb, &files)
				out["files"] = files
			}
		} else if nb, err := get(base+kind+n+"/notes", url.Values{"per_page": {"20"}, "sort": {"asc"}}); err == nil {
			var notes []struct {
				Body   string `json:"body"`
				System bool   `json:"system"`
				Author struct {
					Username string `json:"username"`
				} `json:"author"`
			}
			_ = json.Unmarshal(nb, &notes)
			comments := []map[string]string{}
			for _, c := range notes {
				if !c.System {
					comments = append(comments, map[string]string{"author": c.Author.Username, "body": cut(c.Body, 800)})
				}
			}
			out["comments"] = comments
		}
		return compact(out), nil

	case "gitlab_get_merge_request_diff":
		if in.Number < 1 || in.Number > 10_000_000 {
			return "", errors.New("number must be a positive integer")
		}
		return glDiff(ctx, app, ts, base, in.Number)

	case "gitlab_list_commits":
		q := url.Values{"per_page": {in.per(15)}}
		if in.Ref != "" {
			if !ghRef.MatchString(in.Ref) || strings.Contains(in.Ref, "..") {
				return "", errors.New("ref is not valid")
			}
			q.Set("ref_name", in.Ref)
		}
		b, err := get(base+"/repository/commits", q)
		if err != nil {
			return "", err
		}
		var cs []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Author  string `json:"author_name"`
			Created string `json:"created_at"`
		}
		_ = json.Unmarshal(b, &cs)
		out := []map[string]string{}
		for _, c := range cs {
			out = append(out, map[string]string{"sha": c.ID[:min(len(c.ID), 10)], "author": c.Author, "date": c.Created, "message": cut(c.Title, 300)})
		}
		return compact(out), nil

	case "gitlab_read_file":
		if in.Ref != "" && (!ghRef.MatchString(in.Ref) || strings.Contains(in.Ref, "..")) {
			return "", errors.New("ref is not valid")
		}
		path := strings.Trim(in.Path, "/")
		var segs []string
		for _, p := range strings.Split(path, "/") {
			if p == "" {
				continue
			}
			if p == "." || p == ".." {
				return "", errors.New("path must not contain . or ..")
			}
			segs = append(segs, p)
		}
		ref := in.Ref
		if ref == "" { // the files API needs a ref: use the default branch
			pb, err := get(base, nil)
			if err != nil {
				return "", err
			}
			var pr struct {
				Default string `json:"default_branch"`
			}
			_ = json.Unmarshal(pb, &pr)
			if pr.Default == "" {
				return "", errors.New("could not find the default branch")
			}
			ref = pr.Default
		}
		tree := func() (string, error) {
			b, err := get(base+"/repository/tree", url.Values{"path": {strings.Join(segs, "/")}, "ref": {ref}, "per_page": {"100"}})
			if err != nil {
				return "", err
			}
			var entries []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			}
			_ = json.Unmarshal(b, &entries)
			return compact(map[string]any{"directory": entries}), nil
		}
		if len(segs) == 0 {
			return tree()
		}
		b, err := get(base+"/repository/files/"+url.PathEscape(strings.Join(segs, "/")), url.Values{"ref": {ref}})
		if err != nil {
			if strings.HasPrefix(err.Error(), "not found") {
				return tree() // maybe it is a directory
			}
			return "", err
		}
		var f struct {
			Size     int    `json:"size"`
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
		}
		_ = json.Unmarshal(b, &f)
		data, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil || f.Encoding != "base64" {
			return "", errors.New("could not decode the file")
		}
		for _, c := range data[:min(len(data), 2000)] {
			if c == 0 {
				return compact(map[string]any{"binary": true, "size": f.Size}), nil
			}
		}
		return compact(map[string]any{"size": f.Size, "content": cut(string(data), 20000)}), nil
	}
	return "", errors.New("unknown tool")
}

func gitlabProvider() *provider {
	return &provider{
		ID: "gitlab", Label: "GitLab",
		authorize: func(app OAuthApp, q url.Values) string {
			q.Set("response_type", "code")
			return app.AuthURL + "/oauth/authorize?" + q.Encode()
		},
		exchange: func(ctx context.Context, app OAuthApp, redirect, code string) (tokenResponse, error) {
			return postToken(ctx, app.AuthURL+"/oauth/token", url.Values{
				"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "code": {code}, "redirect_uri": {redirect}, "grant_type": {"authorization_code"},
			}, false, "", "")
		},
		refresh: func(ctx context.Context, app OAuthApp, ts tokenSet) (tokenResponse, error) {
			return postToken(ctx, app.AuthURL+"/oauth/token", url.Values{
				"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "refresh_token": {ts.Refresh}, "grant_type": {"refresh_token"},
			}, false, "", "")
		},
		identify: func(ctx context.Context, app OAuthApp, ts *tokenSet) (string, error) {
			raw, err := apiGet(ctx, "GitLab", app.APIURL, "/user", nil, ts.Access, nil)
			if err != nil {
				return "", err
			}
			var u struct {
				Username string `json:"username"`
			}
			if json.Unmarshal(raw, &u) != nil || u.Username == "" {
				return "", errors.New("GitLab did not say who this is")
			}
			return u.Username, nil
		},
		revoke: func(app OAuthApp, ts tokenSet) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = postToken(ctx, app.AuthURL+"/oauth/revoke", url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "token": {ts.Access}}, false, "", "")
		},
		tools: gitlabTools(),
		run:   gitlabRun,

		validProject: gitlabValidProject,
		fetchChange:  gitlabFetchChange,
		postReview:   gitlabPostReview,
		createHook:   gitlabCreateHook,
		deleteHook:   gitlabDeleteHook,
		parseHook:    gitlabParseHook,
	}
}

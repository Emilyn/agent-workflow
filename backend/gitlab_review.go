package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func gitlabValidProject(p string) bool {
	_, err := glInput{Project: p}.projectID()
	return err == nil && !strings.HasPrefix(p, "/") && strings.Contains(p, "/")
}

type glMR struct {
	IID         int    `json:"iid"`
	Title       string `json:"title"`
	URL         string `json:"web_url"`
	Description string `json:"description"`
	Draft       bool   `json:"draft"`
	State       string `json:"state"`
	SHA         string `json:"sha"`
	Source      string `json:"source_branch"`
	Target      string `json:"target_branch"`
	Author      struct {
		Username string `json:"username"`
	} `json:"author"`
	DiffRefs struct {
		Base  string `json:"base_sha"`
		Start string `json:"start_sha"`
		Head  string `json:"head_sha"`
	} `json:"diff_refs"`
}

func (m glMR) event(project string) changeEvent {
	return changeEvent{Provider: "gitlab", Project: project, Number: m.IID, Title: cut(m.Title, 300), URL: m.URL, Author: m.Author.Username,
		HeadSHA: m.SHA, Source: m.Source, Target: m.Target, Description: cut(m.Description, 4000)}
}

func glGet(ctx context.Context, app OAuthApp, ts tokenSet, path string, q url.Values) ([]byte, error) {
	return apiGet(ctx, "GitLab", app.APIURL, path, q, ts.Access, nil)
}

func gitlabFetchChange(ctx context.Context, app OAuthApp, ts tokenSet, project string, number int) (changeEvent, error) {
	base, err := glInput{Project: project}.projectID()
	if err != nil {
		return changeEvent{}, err
	}
	b, err := glGet(ctx, app, ts, base+"/merge_requests/"+strconv.Itoa(number), nil)
	if err != nil {
		return changeEvent{}, err
	}
	var m glMR
	if json.Unmarshal(b, &m) != nil || m.IID == 0 {
		return changeEvent{}, errors.New("GitLab did not return that merge request")
	}
	return m.event(project), nil
}

// gitlabPostReview adds inline discussions where the line is part of the diff, then one summary note.
func gitlabPostReview(ctx context.Context, app OAuthApp, ts tokenSet, ev changeEvent, summary string, comments []reviewComment) (string, error) {
	base, err := glInput{Project: ev.Project}.projectID()
	if err != nil {
		return "", err
	}
	mr := base + "/merge_requests/" + strconv.Itoa(ev.Number)
	b, err := glGet(ctx, app, ts, mr, nil)
	if err != nil {
		return "", err
	}
	var m glMR
	_ = json.Unmarshal(b, &m)
	var failed []reviewComment
	for _, c := range comments {
		if m.DiffRefs.Head == "" {
			failed = append(failed, c)
			continue
		}
		_, err := apiPost(ctx, "GitLab", app.APIURL, mr+"/discussions", map[string]any{
			"body": c.Body,
			"position": map[string]any{"position_type": "text", "base_sha": m.DiffRefs.Base, "start_sha": m.DiffRefs.Start, "head_sha": m.DiffRefs.Head,
				"new_path": c.Path, "old_path": c.Path, "new_line": c.Line},
		}, ts.Access, nil)
		if err != nil {
			failed = append(failed, c)
		}
	}
	out, err := apiPost(ctx, "GitLab", app.APIURL, mr+"/notes", map[string]any{"body": foldComments(summary, failed) + reviewFooter}, ts.Access, nil)
	if err != nil {
		return "", err
	}
	var n struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(out, &n)
	link := m.URL
	if link == "" {
		link = ev.URL
	}
	if n.ID != 0 {
		link += "#note_" + strconv.FormatInt(n.ID, 10)
	}
	return link, nil
}

func gitlabCreateHook(ctx context.Context, app OAuthApp, ts tokenSet, project, hookURL, secret string) (string, error) {
	base, err := glInput{Project: project}.projectID()
	if err != nil {
		return "", err
	}
	out, err := apiPost(ctx, "GitLab", app.APIURL, base+"/hooks", map[string]any{
		"url": hookURL, "token": secret, "merge_requests_events": true, "push_events": false, "enable_ssl_verification": true,
	}, ts.Access, nil)
	if err != nil {
		return "", err
	}
	var r struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(out, &r) != nil || r.ID == 0 {
		return "", errors.New("GitLab did not return the webhook id")
	}
	return strconv.FormatInt(r.ID, 10), nil
}

func gitlabDeleteHook(ctx context.Context, app OAuthApp, ts tokenSet, project, hookID string) error {
	base, err := glInput{Project: project}.projectID()
	if err != nil {
		return err
	}
	if _, err := strconv.ParseInt(hookID, 10, 64); err != nil {
		return errors.New("bad hook id")
	}
	_, err = apiDo(ctx, "GitLab", http.MethodDelete, app.APIURL, base+"/hooks/"+hookID, nil, nil, ts.Access, nil)
	return err
}

func gitlabParseHook(h http.Header, body []byte, secret string, onPush bool) (changeEvent, string, error) {
	if subtle.ConstantTimeCompare([]byte(h.Get("X-Gitlab-Token")), []byte(secret)) != 1 {
		return changeEvent{}, "", errBadSignature
	}
	var p struct {
		Kind    string `json:"object_kind"`
		Project struct {
			Path string `json:"path_with_namespace"`
		} `json:"project"`
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Attrs struct {
			IID         int    `json:"iid"`
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Action      string `json:"action"`
			State       string `json:"state"`
			Draft       bool   `json:"draft"`
			WIP         bool   `json:"work_in_progress"`
			OldRev      string `json:"oldrev"`
			Source      string `json:"source_branch"`
			Target      string `json:"target_branch"`
			LastCommit  struct {
				ID string `json:"id"`
			} `json:"last_commit"`
		} `json:"object_attributes"`
		Changes struct {
			Draft *struct {
				Previous bool `json:"previous"`
				Current  bool `json:"current"`
			} `json:"draft"`
		} `json:"changes"`
	}
	if json.Unmarshal(body, &p) != nil || p.Kind != "merge_request" || p.Attrs.IID == 0 {
		return changeEvent{}, "not a merge request event", nil
	}
	a := p.Attrs
	ready := p.Changes.Draft != nil && p.Changes.Draft.Previous && !p.Changes.Draft.Current
	switch a.Action {
	case "open", "reopen":
	case "update":
		if !ready && !(a.OldRev != "" && onPush) {
			return changeEvent{}, "update without a new push is not reviewed", nil
		}
	default:
		return changeEvent{}, "action " + a.Action + " is not used", nil
	}
	if (a.Draft || a.WIP) && !ready {
		return changeEvent{}, "draft merge request", nil
	}
	if a.State != "" && a.State != "opened" {
		return changeEvent{}, "merge request is not open", nil
	}
	return changeEvent{Provider: "gitlab", Project: p.Project.Path, Number: a.IID, Title: cut(a.Title, 300), URL: a.URL, Author: p.User.Username,
		HeadSHA: a.LastCommit.ID, Source: a.Source, Target: a.Target, Description: cut(a.Description, 4000)}, "", nil
}

func glDiff(ctx context.Context, app OAuthApp, ts tokenSet, base string, number int) (string, error) {
	b, err := glGet(ctx, app, ts, base+"/merge_requests/"+strconv.Itoa(number)+"/diffs", url.Values{"per_page": {"100"}})
	if err != nil {
		return "", err
	}
	var fs []struct {
		Old     string `json:"old_path"`
		New     string `json:"new_path"`
		Diff    string `json:"diff"`
		Added   bool   `json:"new_file"`
		Deleted bool   `json:"deleted_file"`
		Renamed bool   `json:"renamed_file"`
	}
	_ = json.Unmarshal(b, &fs)
	files := make([]diffFile, 0, len(fs))
	for _, f := range fs {
		status := "modified"
		switch {
		case f.Added:
			status = "added"
		case f.Deleted:
			status = "deleted"
		case f.Renamed:
			status = "renamed from " + f.Old
		}
		add, del := 0, 0
		for _, l := range strings.Split(f.Diff, "\n") {
			if strings.HasPrefix(l, "+") {
				add++
			} else if strings.HasPrefix(l, "-") {
				del++
			}
		}
		files = append(files, diffFile{f.New, status, add, del, f.Diff})
	}
	if len(files) == 0 {
		return "", errors.New("the merge request has no changed files")
	}
	return renderDiff(files, len(files)), nil
}

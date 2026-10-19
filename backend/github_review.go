package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var errBadSignature = errors.New("bad webhook signature")

func githubValidProject(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) != 2 {
		return false
	}
	for _, x := range parts {
		if !ghName.MatchString(x) || x == "." || x == ".." {
			return false
		}
	}
	return true
}

type ghPull struct {
	Number int                    `json:"number"`
	Title  string                 `json:"title"`
	URL    string                 `json:"html_url"`
	Body   string                 `json:"body"`
	Draft  bool                   `json:"draft"`
	State  string                 `json:"state"`
	User   struct{ Login string } `json:"user"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (p ghPull) event(project string) changeEvent {
	return changeEvent{Provider: "github", Project: project, Number: p.Number, Title: cut(p.Title, 300), URL: p.URL, Author: p.User.Login,
		HeadSHA: p.Head.SHA, Source: p.Head.Ref, Target: p.Base.Ref, Description: cut(p.Body, 4000)}
}

func githubFetchChange(ctx context.Context, app OAuthApp, ts tokenSet, project string, number int) (changeEvent, error) {
	g := &gitHub{app: app, token: ts.Access}
	b, err := g.get(ctx, "/repos/"+project+"/pulls/"+strconv.Itoa(number), nil)
	if err != nil {
		return changeEvent{}, err
	}
	var p ghPull
	if json.Unmarshal(b, &p) != nil || p.Number == 0 {
		return changeEvent{}, errors.New("GitHub did not return that pull request")
	}
	return p.event(project), nil
}

// githubPostReview submits one review. Comments GitHub rejects (a line outside the diff) are folded into the summary.
func githubPostReview(ctx context.Context, app OAuthApp, ts tokenSet, ev changeEvent, summary string, comments []reviewComment) (string, error) {
	g := &gitHub{app: app, token: ts.Access}
	base := "/repos/" + ev.Project + "/pulls/" + strconv.Itoa(ev.Number)
	b, err := g.get(ctx, base, nil)
	if err != nil {
		return "", err
	}
	var p ghPull
	_ = json.Unmarshal(b, &p)
	if p.Head.SHA == "" {
		return "", errors.New("could not read the pull request's latest commit")
	}
	hdr := map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "agent-workflow"}
	send := func(body string, cs []reviewComment) ([]byte, error) {
		inline := []map[string]any{}
		for _, c := range cs {
			inline = append(inline, map[string]any{"path": c.Path, "line": c.Line, "side": "RIGHT", "body": c.Body})
		}
		return apiPost(ctx, "GitHub", app.APIURL, base+"/reviews", map[string]any{"commit_id": p.Head.SHA, "body": body + reviewFooter, "event": "COMMENT", "comments": inline}, ts.Access, hdr)
	}
	out, err := send(summary, comments)
	if err != nil && len(comments) > 0 && strings.Contains(err.Error(), "did not accept") {
		out, err = send(foldComments(summary, comments), nil) // a line was not part of the diff
	}
	if err != nil {
		return "", err
	}
	var r struct {
		URL string `json:"html_url"`
	}
	_ = json.Unmarshal(out, &r)
	if r.URL == "" {
		r.URL = ev.URL
	}
	return r.URL, nil
}

func githubCreateHook(ctx context.Context, app OAuthApp, ts tokenSet, project, hookURL, secret string) (string, error) {
	hdr := map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "agent-workflow"}
	out, err := apiPost(ctx, "GitHub", app.APIURL, "/repos/"+project+"/hooks", map[string]any{
		"name": "web", "active": true, "events": []string{"pull_request"},
		"config": map[string]string{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"},
	}, ts.Access, hdr)
	if err != nil {
		return "", err
	}
	var r struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(out, &r) != nil || r.ID == 0 {
		return "", errors.New("GitHub did not return the webhook id")
	}
	return strconv.FormatInt(r.ID, 10), nil
}

func githubDeleteHook(ctx context.Context, app OAuthApp, ts tokenSet, project, hookID string) error {
	if _, err := strconv.ParseInt(hookID, 10, 64); err != nil {
		return errors.New("bad hook id")
	}
	_, err := apiDo(ctx, "GitHub", http.MethodDelete, app.APIURL, "/repos/"+project+"/hooks/"+hookID, nil, nil, ts.Access,
		map[string]string{"Accept": "application/vnd.github+json", "User-Agent": "agent-workflow"})
	return err
}

// githubParseHook checks the HMAC signature and decides whether a delivery should start a review.
func githubParseHook(h http.Header, body []byte, secret string, onPush bool) (changeEvent, string, error) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(h.Get("X-Hub-Signature-256"))) {
		return changeEvent{}, "", errBadSignature
	}
	switch h.Get("X-GitHub-Event") {
	case "ping":
		return changeEvent{}, "ping", nil
	case "pull_request":
	default:
		return changeEvent{}, "event " + h.Get("X-GitHub-Event") + " is not used", nil
	}
	var p struct {
		Action      string `json:"action"`
		PullRequest ghPull `json:"pull_request"`
		Repository  struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &p) != nil || p.PullRequest.Number == 0 {
		return changeEvent{}, "unreadable payload", nil
	}
	switch p.Action {
	case "opened", "reopened", "ready_for_review":
	case "synchronize":
		if !onPush {
			return changeEvent{}, "new pushes are not reviewed", nil
		}
	default:
		return changeEvent{}, "action " + p.Action + " is not used", nil
	}
	if p.PullRequest.Draft && p.Action != "ready_for_review" {
		return changeEvent{}, "draft pull request", nil
	}
	if p.PullRequest.State != "" && p.PullRequest.State != "open" {
		return changeEvent{}, "pull request is not open", nil
	}
	return p.PullRequest.event(p.Repository.FullName), "", nil
}

// githubDiff reads the changed files of a pull request with numbered patches.
func (g *gitHub) diff(ctx context.Context, base string, number int) (string, error) {
	b, err := g.get(ctx, base+"/pulls/"+strconv.Itoa(number)+"/files", url.Values{"per_page": {"100"}})
	if err != nil {
		return "", err
	}
	var fs []struct {
		Filename  string `json:"filename"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Patch     string `json:"patch"`
	}
	_ = json.Unmarshal(b, &fs)
	files := make([]diffFile, 0, len(fs))
	for _, f := range fs {
		files = append(files, diffFile{f.Filename, f.Status, f.Additions, f.Deletions, f.Patch})
	}
	if len(files) == 0 {
		return "", errors.New("the pull request has no changed files")
	}
	return renderDiff(files, len(files)), nil
}

var _ = fmt.Sprintf

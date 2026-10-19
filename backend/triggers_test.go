package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNumberPatch(t *testing.T) {
	got := numberPatch("@@ -10,3 +10,4 @@ func x()\n a\n-b\n+c\n+d")
	want := "@@ -10,3 +10,4 @@ func x()\n   10: a\n     :-b\n   11:+c\n   12:+d"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(numberPatch("@@ -1 +5 @@\n+x"), "    5:+x") {
		t.Error("hunk without counts")
	}
}

func TestCleanComments(t *testing.T) {
	in := []reviewComment{{"a.go", 3, "ok"}, {"../x", 1, "no"}, {"/abs", 1, "no"}, {"a//b", 1, "no"}, {"a.go", 0, "no"}, {"a.go", 2, " "}, {"b.go", 7, strings.Repeat("x", 5000)}}
	out := cleanComments(in)
	if len(out) != 2 || out[0].Path != "a.go" || len([]rune(out[1].Body)) > 4010 {
		t.Errorf("%+v", out)
	}
}

// ---------- helpers ----------

type reviewEnv struct {
	*env
	f     *fakeProviders
	an    *scriptedAnthropic
	admin *person
	wf    string
}

// reviewScript is what the fake model does: read the diff, post a review, then say it is done.
func reviewScript(diffTool string, diffInput map[string]any, comments []map[string]any) []map[string]any {
	return []map[string]any{
		toolUse("t1", diffTool, diffInput),
		toolUse("t2", "post_review", map[string]any{"summary": "Mostly fine. One thing to check.", "comments": comments}),
		finalText("Review submitted."),
	}
}

var ghDiff = []any{"github_get_pull_request_diff", map[string]any{"owner": "acme", "repo": "app", "number": 5}}
var glDiffCall = []any{"gitlab_get_merge_request_diff", map[string]any{"project": "acme/group/app", "number": 3}}

func newReviewEnv(t *testing.T, provider string, comments ...map[string]any) *reviewEnv {
	t.Helper()
	f := newFakeProviders(t)
	call := ghDiff
	if provider == "gitlab" {
		call = glDiffCall
	}
	if len(comments) == 0 {
		path := "main.go"
		if provider == "gitlab" {
			path = "a.go"
		}
		comments = []map[string]any{{"path": path, "line": 11, "body": "Check for nil here."}}
	}
	an := newScriptedAnthropic(t, reviewScript(call[0].(string), call[1].(map[string]any), comments)...)
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	admin := e.signup("Ann", "ann@example.com")
	connect(t, admin, provider)
	var wf Workflow
	admin.mustCall(201, "POST", admin.path("/workflows"), map[string]string{"name": "Review", "template": "review"}, &wf)
	return &reviewEnv{env: e, f: f, an: an, admin: admin, wf: wf.ID}
}

func projectOf(provider string) string {
	if provider == "gitlab" {
		return "acme/group/app"
	}
	return "acme/app"
}

func (r *reviewEnv) addTrigger(provider, mode string, onPush bool) triggerView {
	r.t.Helper()
	var v triggerView
	r.admin.mustCall(201, "POST", r.admin.path("/triggers"), map[string]any{"workflowId": r.wf, "provider": provider, "project": projectOf(provider), "reviewMode": mode, "onPush": onPush}, &v)
	return v
}

func (r *reviewEnv) runs() []Run {
	r.t.Helper()
	var out []Run
	r.admin.mustCall(200, "GET", r.admin.path("/workflows/"+r.wf+"/runs"), nil, &out)
	return out
}

// waitRun waits until the newest run has finished.
func (r *reviewEnv) waitRun() Run {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rs := r.runs(); len(rs) > 0 && (rs[0].Status == "done" || rs[0].Status == "failed") {
			return rs[0]
		}
		time.Sleep(30 * time.Millisecond)
	}
	r.t.Fatalf("run did not finish: %+v", r.runs())
	return Run{}
}

func deliver(t *testing.T, url string, headers map[string]string, body []byte) (int, map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func ghHeaders(secret, event string, body []byte) map[string]string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return map[string]string{"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(m.Sum(nil)), "X-GitHub-Event": event}
}

func ghPayload(action string, draft bool, sha string) []byte {
	b, _ := json.Marshal(map[string]any{"action": action, "pull_request": map[string]any{"number": 5, "title": "Add feature", "html_url": "https://github.example/acme/app/pull/5",
		"body": "Please review", "draft": draft, "state": "open", "user": map[string]string{"login": "dev1"}, "head": map[string]string{"sha": sha, "ref": "feature"}, "base": map[string]string{"ref": "main"}},
		"repository": map[string]string{"full_name": "acme/app"}})
	return b
}

func glPayload(action string, extra map[string]any) []byte {
	attrs := map[string]any{"iid": 3, "title": "Fix bug", "url": "https://gitlab.example/acme/group/app/-/merge_requests/3", "description": "d", "action": action, "state": "opened",
		"source_branch": "fix", "target_branch": "main", "last_commit": map[string]string{"id": "def456"}}
	payload := map[string]any{"object_kind": "merge_request", "project": map[string]string{"path_with_namespace": "acme/group/app"}, "user": map[string]string{"username": "dev2"}, "object_attributes": attrs}
	for k, v := range extra {
		if k == "changes" {
			payload[k] = v
		} else {
			attrs[k] = v
		}
	}
	b, _ := json.Marshal(payload)
	return b
}

// ---------- the main scenario, for both providers ----------

func TestWebhookReviewDraftThenApprove(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			r := newReviewEnv(t, provider)
			tr := r.addTrigger(provider, "draft", false)
			if tr.HookStatus != "registered" || tr.Secret == "" || !strings.HasSuffix(tr.WebhookURL, "/api/hooks/"+tr.ID) {
				t.Fatalf("trigger %+v", tr)
			}
			hookCalls := r.f.bodies(provider, "POST ")
			found := false
			for _, b := range hookCalls {
				if strings.Contains(b, tr.Secret) && strings.Contains(b, tr.WebhookURL) {
					found = true
				}
			}
			if !found {
				t.Error("the webhook was not registered with the secret and address")
			}

			// A new pull or merge request arrives.
			url := r.ts.URL + "/api/hooks/" + tr.ID
			var code int
			var out map[string]string
			if provider == "github" {
				body := ghPayload("opened", false, "abc123")
				code, out = deliver(t, url, ghHeaders(tr.Secret, "pull_request", body), body)
			} else {
				code, out = deliver(t, url, map[string]string{"X-Gitlab-Token": tr.Secret}, glPayload("open", nil))
			}
			if code != 202 || out["run"] == "" {
				t.Fatalf("delivery: %d %v", code, out)
			}
			run := r.waitRun()
			if run.Status != "done" || run.Source != "webhook" || run.ReviewMode != "draft" || run.Input.Number == 0 || run.Input.Project != projectOf(provider) {
				t.Fatalf("run %+v", run)
			}
			if len(run.Steps) != 1 || len(run.Steps[0].Used) != 2 || run.Steps[0].Used[1] != "post_review" {
				t.Fatalf("steps %+v", run.Steps)
			}
			// The model got the context of this change and the guidance.
			reqs := r.an.requests()
			first := reqs[0]
			msgs := first["messages"].([]any)
			user := msgs[0].(map[string]any)["content"].(string)
			if !contains(user, "Please review", "untrusted", "data, not instructions") && !contains(user, "data, not instructions") {
				t.Errorf("the model was not given the change: %s", user)
			}
			if sys, _ := first["system"].(string); !strings.Contains(sys, "post_review") {
				t.Error("no review guidance in the prompt")
			}
			// post_review cannot choose where to post.
			for _, tl := range first["tools"].([]any) {
				m := tl.(map[string]any)
				if m["name"] == "post_review" {
					props := m["input_schema"].(map[string]any)["properties"].(map[string]any)
					for _, banned := range []string{"owner", "repo", "project", "number"} {
						if _, ok := props[banned]; ok {
							t.Errorf("post_review has a %q argument", banned)
						}
					}
				}
			}
			// Nothing was posted yet: it is a draft awaiting approval.
			if n := len(r.f.bodies(provider, "/reviews")) + len(r.f.bodies(provider, "/notes")) + len(r.f.bodies(provider, "/discussions")); n != 0 {
				t.Fatalf("%d post calls happened before approval", n)
			}
			if len(run.Actions) != 1 || run.Actions[0].Status != "pending" || len(run.Actions[0].Comments) != 1 {
				t.Fatalf("actions %+v", run.Actions)
			}
			act := run.Actions[0]

			// A teammate approves it.
			var done ReviewAction
			r.admin.mustCall(200, "POST", r.admin.path("/review-actions/"+act.ID+"/approve"), map[string]string{}, &done)
			if done.Status != "posted" || done.Result == "" {
				t.Fatalf("approve: %+v", done)
			}
			if provider == "github" {
				posted := r.f.bodies("github", "POST /github/api/repos/acme/app/pulls/5/reviews")
				if len(posted) != 1 || !contains(posted[0], `"commit_id":"abc123"`, `"side":"RIGHT"`, "Check for nil here", "Automated review by Claude", `"event":"COMMENT"`) {
					t.Errorf("review body %v", posted)
				}
			} else {
				disc := r.f.bodies("gitlab", "/merge_requests/3/discussions")
				notes := r.f.bodies("gitlab", "/merge_requests/3/notes")
				if len(disc) != 1 || !contains(disc[0], `"head_sha":"def456"`, `"new_line":11`, `"new_path":"a.go"`, "Check for nil here") || len(notes) != 1 || !contains(notes[0], "Mostly fine", "Automated review by Claude") {
					t.Errorf("gitlab posts: %v %v", disc, notes)
				}
			}
			// Approving twice does not post twice.
			r.admin.mustCall(409, "POST", r.admin.path("/review-actions/"+act.ID+"/approve"), map[string]string{}, nil)
			r.admin.mustCall(409, "POST", r.admin.path("/review-actions/"+act.ID+"/discard"), map[string]string{}, nil)
			again := r.runs()[0].Actions[0]
			if again.Status != "posted" {
				t.Errorf("status %s", again.Status)
			}
		})
	}
}

func TestPostModePostsDirectly(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			r := newReviewEnv(t, provider)
			tr := r.addTrigger(provider, "post", false)
			url := r.ts.URL + "/api/hooks/" + tr.ID
			if provider == "github" {
				body := ghPayload("opened", false, "abc123")
				deliver(t, url, ghHeaders(tr.Secret, "pull_request", body), body)
			} else {
				deliver(t, url, map[string]string{"X-Gitlab-Token": tr.Secret}, glPayload("open", nil))
			}
			run := r.waitRun()
			if run.Status != "done" || len(run.Actions) != 1 || run.Actions[0].Status != "posted" || run.Actions[0].Result == "" {
				t.Fatalf("%+v", run)
			}
			posts := len(r.f.bodies(provider, "/reviews")) + len(r.f.bodies(provider, "/notes"))
			if posts != 1 {
				t.Errorf("%d posts", posts)
			}
		})
	}
}

func TestLinesOutsideTheDiffAreFolded(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			path := map[string]string{"github": "main.go", "gitlab": "a.go"}[provider]
			r := newReviewEnv(t, provider, map[string]any{"path": path, "line": 999, "body": "Off-diff remark"}, map[string]any{"path": path, "line": 11, "body": "On-diff remark"})
			tr := r.addTrigger(provider, "post", false)
			url := r.ts.URL + "/api/hooks/" + tr.ID
			if provider == "github" {
				body := ghPayload("opened", false, "abc123")
				deliver(t, url, ghHeaders(tr.Secret, "pull_request", body), body)
			} else {
				deliver(t, url, map[string]string{"X-Gitlab-Token": tr.Secret}, glPayload("open", nil))
			}
			run := r.waitRun()
			if run.Status != "done" || run.Actions[0].Status != "posted" {
				t.Fatalf("%+v", run)
			}
			var summaries []string
			if provider == "github" {
				summaries = r.f.bodies("github", "/reviews")[1:] // second attempt carries the folded text
			} else {
				summaries = r.f.bodies("gitlab", "/notes")
			}
			if len(summaries) != 1 || !contains(summaries[0], "could not be attached to a line", "Off-diff remark") {
				t.Errorf("summary %v", summaries)
			}
			if provider == "github" && !contains(summaries[0], "On-diff remark") {
				t.Error("github retry should fold all comments")
			}
		})
	}
}

func TestWebhookRejectionsAndFiltering(t *testing.T) {
	r := newReviewEnv(t, "github")
	tr := r.addTrigger("github", "draft", false)
	url := r.ts.URL + "/api/hooks/" + tr.ID
	good := ghPayload("opened", false, "abc123")

	if code, _ := deliver(t, url, ghHeaders("wrong-secret", "pull_request", good), good); code != 401 {
		t.Errorf("bad signature: %d", code)
	}
	if code, _ := deliver(t, url, map[string]string{"X-GitHub-Event": "pull_request"}, good); code != 401 {
		t.Errorf("missing signature: %d", code)
	}
	if code, _ := deliver(t, r.ts.URL+"/api/hooks/trnotreal", ghHeaders(tr.Secret, "pull_request", good), good); code != 404 {
		t.Errorf("unknown trigger: %d", code)
	}
	ignored := func(why string, headers map[string]string, body []byte) {
		t.Helper()
		code, out := deliver(t, url, headers, body)
		if code != 200 || !strings.Contains(out["ignored"], why) {
			t.Errorf("want ignored %q, got %d %v", why, code, out)
		}
	}
	ping := []byte(`{"zen":"x"}`)
	ignored("ping", ghHeaders(tr.Secret, "ping", ping), ping)
	ignored("not used", ghHeaders(tr.Secret, "push", ping), ping)
	draft := ghPayload("opened", true, "abc123")
	ignored("draft", ghHeaders(tr.Secret, "pull_request", draft), draft)
	closed := ghPayload("closed", false, "abc123")
	ignored("not used", ghHeaders(tr.Secret, "pull_request", closed), closed)
	push := ghPayload("synchronize", false, "new1")
	ignored("new pushes", ghHeaders(tr.Secret, "pull_request", push), push)
	other := bytes.ReplaceAll(good, []byte("acme/app"), []byte("evil/app"))
	ignored("different project", ghHeaders(tr.Secret, "pull_request", other), other)
	if n := len(r.runs()); n != 0 {
		t.Fatalf("%d runs were started by ignored deliveries", n)
	}

	// A real one, delivered twice (GitHub retries): one run.
	if code, _ := deliver(t, url, ghHeaders(tr.Secret, "pull_request", good), good); code != 202 {
		t.Fatalf("good delivery: %d", code)
	}
	ignored("already reviewed", ghHeaders(tr.Secret, "pull_request", good), good)
	r.waitRun()
	if n := len(r.runs()); n != 1 {
		t.Errorf("%d runs after a duplicate delivery", n)
	}
	// Ready for review of a draft starts a review (new commit or not, the same sha was reviewed: deduped).
	ready := ghPayload("ready_for_review", false, "abc123")
	ignored("already reviewed", ghHeaders(tr.Secret, "pull_request", ready), ready)

	// Switching the trigger off, and on pushes on.
	r.admin.mustCall(200, "PATCH", r.admin.path("/triggers/"+tr.ID), map[string]any{"enabled": false}, nil)
	fresh := ghPayload("opened", false, "zzz999")
	ignored("switched off", ghHeaders(tr.Secret, "pull_request", fresh), fresh)
	r.admin.mustCall(200, "PATCH", r.admin.path("/triggers/"+tr.ID), map[string]any{"enabled": true, "onPush": true}, nil)
	if code, _ := deliver(t, url, ghHeaders(tr.Secret, "pull_request", push), push); code != 202 {
		t.Errorf("a push should start a review when onPush is on: %d", code)
	}
}

func TestGitLabWebhookFiltering(t *testing.T) {
	r := newReviewEnv(t, "gitlab")
	tr := r.addTrigger("gitlab", "draft", false)
	url := r.ts.URL + "/api/hooks/" + tr.ID
	hdr := map[string]string{"X-Gitlab-Token": tr.Secret}
	if code, _ := deliver(t, url, map[string]string{"X-Gitlab-Token": "nope"}, glPayload("open", nil)); code != 401 {
		t.Errorf("bad token: %d", code)
	}
	ignored := func(why string, body []byte) {
		t.Helper()
		code, out := deliver(t, url, hdr, body)
		if code != 200 || !strings.Contains(out["ignored"], why) {
			t.Errorf("want ignored %q, got %d %v", why, code, out)
		}
	}
	ignored("draft", glPayload("open", map[string]any{"draft": true}))
	ignored("not used", glPayload("merge", nil))
	ignored("without a new push", glPayload("update", nil))
	ignored("without a new push", glPayload("update", map[string]any{"oldrev": "old1"})) // onPush is off
	if n := len(r.runs()); n != 0 {
		t.Fatalf("%d runs from ignored deliveries", n)
	}
	// A draft that becomes ready is reviewed.
	ready := glPayload("update", map[string]any{"draft": false, "changes": map[string]any{"draft": map[string]bool{"previous": true, "current": false}}})
	if code, _ := deliver(t, url, hdr, ready); code != 202 {
		t.Errorf("ready transition: %d", code)
	}
	r.waitRun()
}

func TestHookRegistrationFailureFallsBackToManual(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			r := newReviewEnv(t, provider)
			r.f.mu.Lock()
			r.f.failHook = true
			r.f.mu.Unlock()
			tr := r.addTrigger(provider, "draft", false)
			if tr.HookStatus != "manual" || tr.HookError == "" || tr.Secret == "" || tr.WebhookURL == "" {
				t.Fatalf("%+v", tr)
			}
			// It still works when the admin sets the webhook up by hand.
			if provider == "github" {
				body := ghPayload("opened", false, "abc123")
				if code, _ := deliver(t, tr.WebhookURL2(r), ghHeaders(tr.Secret, "pull_request", body), body); code != 202 {
					t.Errorf("manual webhook: %d", code)
				}
			} else if code, _ := deliver(t, tr.WebhookURL2(r), map[string]string{"X-Gitlab-Token": tr.Secret}, glPayload("open", nil)); code != 202 {
				t.Errorf("manual webhook: %d", code)
			}
		})
	}
}

// WebhookURL2 points the test client at the test server instead of the public address.
func (v triggerView) WebhookURL2(r *reviewEnv) string { return r.ts.URL + "/api/hooks/" + v.ID }

func TestDeletingATriggerRemovesTheHook(t *testing.T) {
	r := newReviewEnv(t, "github")
	tr := r.addTrigger("github", "draft", false)
	r.admin.mustCall(204, "DELETE", r.admin.path("/triggers/"+tr.ID), nil, nil)
	if r.f.count("github", "DELETE /github/api/repos/acme/app/hooks/77") != 1 {
		t.Error("the webhook was left at GitHub")
	}
	body := ghPayload("opened", false, "abc123")
	if code, _ := deliver(t, tr.WebhookURL2(r), ghHeaders(tr.Secret, "pull_request", body), body); code != 404 {
		t.Errorf("deleted trigger still answers: %d", code)
	}
}

func TestTriggerPermissionsAndIsolation(t *testing.T) {
	r := newReviewEnv(t, "github")
	member := r.signup("Mo", "mo@example.com")
	outsider := r.signup("Out", "out@example.com")
	r.join(r.admin, member, "member")
	tr := r.addTrigger("github", "draft", false)

	member.mustCall(403, "POST", r.admin.path("/triggers"), map[string]any{"workflowId": r.wf, "provider": "github", "project": "acme/app"}, nil)
	member.mustCall(403, "PATCH", r.admin.path("/triggers/"+tr.ID), map[string]any{"enabled": false}, nil)
	member.mustCall(403, "DELETE", r.admin.path("/triggers/"+tr.ID), nil, nil)
	var list []triggerView
	member.mustCall(200, "GET", r.admin.path("/triggers"), nil, &list)
	if len(list) != 1 || list[0].Secret != "" || list[0].WebhookURL != "" {
		t.Errorf("a member can see the webhook secret: %+v", list)
	}
	r.admin.mustCall(200, "GET", r.admin.path("/triggers"), nil, &list)
	if list[0].Secret == "" {
		t.Error("admins should see the secret")
	}
	for _, c := range []struct {
		method, path string
	}{
		{"GET", "/triggers"}, {"POST", "/triggers"}, {"GET", "/workflows/" + r.wf + "/runs"}, {"POST", "/workflows/" + r.wf + "/runs"},
		{"PATCH", "/triggers/" + tr.ID}, {"DELETE", "/triggers/" + tr.ID}, {"POST", "/review-actions/x/approve"},
	} {
		outsider.mustCall(404, c.method, r.admin.path(c.path), map[string]any{}, nil)
	}

	// The secret is stored encrypted, bound to the trigger.
	var enc []byte
	if err := r.db.QueryRow(`SELECT secret_enc FROM triggers WHERE id=$1`, tr.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte(list[0].Secret)) {
		t.Error("webhook secret stored in the clear")
	}
	// A workspace cannot approve another workspace's draft.
	body := ghPayload("opened", false, "abc123")
	deliver(t, tr.WebhookURL2(r), ghHeaders(tr.Secret, "pull_request", body), body)
	run := r.waitRun()
	other := r.signup("Eve", "eve@example.com")
	connect(t, other, "github")
	other.mustCall(404, "POST", other.path("/review-actions/"+run.Actions[0].ID+"/approve"), map[string]string{}, nil)
	// Members may approve.
	member.mustCall(200, "POST", r.admin.path("/review-actions/"+run.Actions[0].ID+"/discard"), map[string]string{}, nil)
}

func TestTriggerValidation(t *testing.T) {
	r := newReviewEnv(t, "github")
	post := func(code int, body map[string]any) {
		t.Helper()
		r.admin.mustCall(code, "POST", r.admin.path("/triggers"), body, nil)
	}
	post(400, map[string]any{"workflowId": r.wf, "provider": "jira", "project": "acme/app"})
	post(400, map[string]any{"workflowId": r.wf, "provider": "github", "project": "acme"})
	post(400, map[string]any{"workflowId": r.wf, "provider": "github", "project": "acme/../app"})
	post(400, map[string]any{"workflowId": r.wf, "provider": "github", "project": "acme/app", "reviewMode": "yolo"})
	post(404, map[string]any{"workflowId": "wnope", "provider": "github", "project": "acme/app"})
	post(409, map[string]any{"workflowId": r.wf, "provider": "gitlab", "project": "acme/group/app"}) // GitLab is not connected
	post(201, map[string]any{"workflowId": r.wf, "provider": "github", "project": "acme/app"})
	post(409, map[string]any{"workflowId": r.wf, "provider": "github", "project": "ACME/App"}) // same project, other case
	post(409, map[string]any{"workflowId": r.wf, "provider": "github", "project": "acme/app"})
	if r.f.count("github", "DELETE /github/api/repos/acme/app/hooks/77") != 1 {
		t.Error("the duplicate trigger left its webhook behind")
	}
}

func TestManualRunAndRerun(t *testing.T) {
	r := newReviewEnv(t, "github")
	member := r.signup("Mo", "mo@example.com")
	r.join(r.admin, member, "member")
	var run Run
	member.mustCall(202, "POST", r.admin.path("/workflows/"+r.wf+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, &run)
	if run.Source != "manual" || run.ReviewMode != "draft" || run.Input.Title != "Add feature" {
		t.Fatalf("%+v", run)
	}
	done := r.waitRun()
	if done.Status != "done" || len(done.Actions) != 1 || done.Actions[0].Status != "pending" {
		t.Fatalf("%+v", done)
	}
	r.admin.mustCall(409, "POST", r.admin.path("/workflows/"+r.wf+"/runs"), map[string]any{"provider": "gitlab", "project": "acme/group/app", "number": 3}, nil)
	r.admin.mustCall(502, "POST", r.admin.path("/workflows/"+r.wf+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 404}, nil)
	r.admin.mustCall(400, "POST", r.admin.path("/workflows/"+r.wf+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 0}, nil)

	var again Run
	r.admin.mustCall(202, "POST", r.admin.path("/runs/"+done.ID+"/rerun"), map[string]string{}, &again)
	if again.ID == done.ID || again.Input.Number != 5 {
		t.Fatalf("%+v", again)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(r.runs()) < 2 || r.runs()[0].Status != "done" {
		if time.Now().After(deadline) {
			t.Fatal("rerun did not finish")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestPostReviewOnlyForTheTriggeringProviderAndOnce(t *testing.T) {
	f := newFakeProviders(t)
	// The model tries to post twice, and to post with a step that lacks the provider tool.
	an := newScriptedAnthropic(t,
		toolUse("a", "post_review", map[string]any{"summary": "first"}),
		toolUse("b", "post_review", map[string]any{"summary": "second"}),
		finalText("done"))
	e := newEnv(t, envOpts{providersURL: f.URL, anthropicURL: an.URL, serverKey: "k"})
	admin := e.signup("Ann", "ann@example.com")
	connect(t, admin, "github")
	r := &reviewEnv{env: e, f: f, an: an, admin: admin}
	var wf Workflow
	admin.mustCall(201, "POST", admin.path("/workflows"), map[string]string{"name": "R", "template": "review"}, &wf)
	r.wf = wf.ID
	r.admin.mustCall(202, "POST", r.admin.path("/workflows/"+r.wf+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, nil)
	run := r.waitRun()
	if run.Status != "done" || len(run.Actions) != 1 || run.Actions[0].Summary != "first" {
		t.Fatalf("%+v", run)
	}
	res := an.lastToolResults()
	if len(res) != 1 {
		t.Fatalf("results %v", res)
	}

	// A step without the GitHub tool never sees post_review.
	for i := range wf.Steps {
		for j := range wf.Steps[i] {
			if wf.Steps[i][j].ID == "n2" {
				wf.Steps[i][j].Tools = []string{"Docs"}
			}
		}
	}
	admin.mustCall(200, "PUT", admin.path("/workflows/"+wf.ID), wf, nil)
	before := len(an.requests())
	admin.mustCall(202, "POST", admin.path("/workflows/"+wf.ID+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, nil)
	deadline := time.Now().Add(10 * time.Second)
	for len(an.requests()) == before && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	if _, has := an.requests()[before]["tools"]; has {
		t.Error("tools offered to a step that does not list the provider")
	}
}

func TestRunFailsClearlyWithoutClaude(t *testing.T) {
	f := newFakeProviders(t)
	e := newEnv(t, envOpts{providersURL: f.URL}) // no Anthropic key anywhere
	admin := e.signup("Ann", "ann@example.com")
	connect(t, admin, "github")
	r := &reviewEnv{env: e, f: f, admin: admin}
	var wf Workflow
	admin.mustCall(201, "POST", admin.path("/workflows"), map[string]string{"name": "R", "template": "review"}, &wf)
	r.wf = wf.ID
	admin.mustCall(202, "POST", admin.path("/workflows/"+wf.ID+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, nil)
	run := r.waitRun()
	if run.Status != "failed" || !strings.Contains(run.Error, "Claude is not connected") {
		t.Fatalf("%+v", run)
	}
}

func TestCrashedRunIsPickedUpAgain(t *testing.T) {
	r := newReviewEnv(t, "github")
	r.admin.mustCall(202, "POST", r.admin.path("/workflows/"+r.wf+"/runs"), map[string]any{"provider": "github", "project": "acme/app", "number": 5}, nil)
	first := r.waitRun()
	// Pretend the worker died mid-run: running, lease over.
	if _, err := r.db.Exec(`UPDATE runs SET status='running', finished_at=NULL, locked_until=now()-interval '1 minute', steps='[]', attempts=1 WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	r.srv.wakeWorker()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := r.runs()[0]
		if got.Status == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not recovered: %+v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

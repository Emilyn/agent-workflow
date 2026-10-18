package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Everything in workspace A must be invisible and untouchable from workspace B.
func TestWorkspacesAreIsolated(t *testing.T) {
	api := newFakeAnthropic(t, "ok", "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa")
	e := newEnv(t, envOpts{anthropicURL: api.URL})
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	wfA := a.firstWorkflow()
	a.mustCall(200, "PUT", a.path("/claude/key"), map[string]string{"apiKey": "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa"}, nil)

	// B knocking on A's workspace: every route says 404, never 403 or data.
	aw := "/api/workspaces/" + a.WS
	for _, c := range []struct{ method, path string }{
		{"GET", aw + "/workflows"}, {"POST", aw + "/workflows"},
		{"GET", aw + "/workflows/" + wfA.ID}, {"PUT", aw + "/workflows/" + wfA.ID},
		{"DELETE", aw + "/workflows/" + wfA.ID}, {"GET", aw + "/workflows/" + wfA.ID + "/sync"},
		{"POST", aw + "/workflows/" + wfA.ID + "/chat"}, {"POST", aw + "/workflows/" + wfA.ID + "/messages"},
		{"POST", aw + "/workflows/" + wfA.ID + "/run/step"}, {"POST", aw + "/workflows/" + wfA.ID + "/run/reset"},
		{"GET", aw + "/settings"}, {"PUT", aw + "/settings"}, {"GET", aw + "/claude"},
		{"PUT", aw + "/claude/key"}, {"DELETE", aw + "/claude/key"}, {"GET", aw + "/members"},
		{"GET", aw + "/invites"}, {"POST", aw + "/invites"}, {"PATCH", aw}, {"DELETE", aw},
	} {
		var body any
		if c.method != "GET" && c.method != "DELETE" {
			body = map[string]string{"name": "x", "message": "hi", "text": "hi", "stepId": "n2", "apiKey": "sk-ant-api03-bbbbbbbbbbbbbbbbbbbb"}
		}
		if got := b.call(c.method, c.path, body, nil); got != 404 {
			t.Errorf("%s %s as outsider: status %d, want 404", c.method, c.path, got)
		}
	}
	// Signed out: 401.
	if got := e.client().call("GET", aw+"/workflows", nil, nil); got != 401 {
		t.Errorf("anonymous: %d", got)
	}

	// B using A's workflow id inside B's own workspace finds nothing.
	for _, c := range []struct{ method, rest string }{
		{"GET", "/workflows/" + wfA.ID}, {"DELETE", "/workflows/" + wfA.ID}, {"GET", "/workflows/" + wfA.ID + "/sync"},
		{"POST", "/workflows/" + wfA.ID + "/run/reset"},
	} {
		if got := b.call(c.method, b.path(c.rest), nil, nil); got != 404 {
			t.Errorf("%s %s: status %d, want 404", c.method, c.rest, got)
		}
	}
	if got := b.call("POST", b.path("/workflows/"+wfA.ID+"/messages"), map[string]string{"text": "planted"}, nil); got != 404 {
		t.Errorf("posting into A's chat via B's workspace: %d", got)
	}
	if got := b.call("POST", b.path("/workflows"), map[string]string{"copyOf": wfA.ID}, nil); got != 404 {
		t.Errorf("copying A's workflow into B: %d", got)
	}
	var wf Workflow
	b.mustCall(200, "GET", b.path("/workflows/"+b.firstWorkflow().ID), nil, &wf)
	wf.ID = wfA.ID // an id in the body must not redirect the write
	b.call("PUT", b.path("/workflows/"+wfA.ID), wf, nil)

	// A's data is intact.
	var after Workflow
	a.mustCall(200, "GET", a.path("/workflows/"+wfA.ID), nil, &after)
	if after.Version != 1 {
		t.Errorf("A's workflow was modified: version %d", after.Version)
	}
	for _, m := range after.Messages {
		if m.Text == "planted" {
			t.Error("B planted a message in A's chat")
		}
	}
	// B has no key; A's key is not used for B's runs.
	var st claudeStatus
	b.mustCall(200, "GET", b.path("/claude"), nil, &st)
	if st.Connected {
		t.Error("B appears connected through A's key")
	}
	var run runResponse
	b.mustCall(200, "POST", b.path("/workflows/"+b.firstWorkflow().ID+"/run/step"), map[string]string{"stepId": "n2"}, &run)
	if !run.Simulated {
		t.Error("B's run used a key")
	}
}

func TestRolesAndPermissions(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Olga", "olga@example.com")
	admin := e.signup("Adam", "adam@example.com")
	member := e.signup("Mia", "mia@example.com")
	e.join(owner, admin, "admin")
	e.join(owner, member, "member")
	// Each person's path into the shared workspace:
	admin.WS, member.WS = owner.WS, owner.WS

	settings := Workspace{GitProvider: "gitlab", CustomAgents: []Agent{{ID: "c1", Name: "Release agent"}}}
	if got := member.call("PUT", member.path("/settings"), settings, nil); got != 403 {
		t.Errorf("member changing the connected tools: %d", got)
	}
	if got := admin.call("PUT", admin.path("/settings"), settings, nil); got != 200 {
		t.Errorf("admin editing settings: %d", got)
	}
	var got Workspace
	member.mustCall(200, "GET", member.path("/settings"), nil, &got)
	if got.GitProvider != "gitlab" || len(got.CustomAgents) != 1 {
		t.Errorf("member can't read shared settings: %+v", got)
	}
	// Members can still create agents, as long as the tool connections are left as they are.
	got.CustomAgents = append(got.CustomAgents, Agent{ID: "c2", Name: "Member agent"})
	var saved Workspace
	member.mustCall(200, "PUT", member.path("/settings"), got, &saved)
	if len(saved.CustomAgents) != 2 {
		t.Errorf("member creating an agent: %+v", saved)
	}
	got.Connected["jira"] = true
	if code := member.call("PUT", member.path("/settings"), got, nil); code != 403 {
		t.Errorf("member connecting Jira: %d", code)
	}

	if got := member.call("PUT", member.path("/claude/key"), map[string]string{"apiKey": "sk-ant-api03-cccccccccccccccccccc"}, nil); got != 403 {
		t.Errorf("member setting the key: %d", got)
	}
	if got := member.call("POST", member.path("/invites"), map[string]string{}, nil); got != 403 {
		t.Errorf("member inviting: %d", got)
	}
	if got := member.call("GET", member.path("/invites"), nil, nil); got != 403 {
		t.Errorf("member listing invites: %d", got)
	}
	if got := admin.call("POST", admin.path("/invites"), map[string]string{"role": "admin"}, nil); got != 403 {
		t.Errorf("admin inviting an admin: %d", got)
	}
	if got := admin.call("POST", admin.path("/invites"), map[string]string{"role": "owner"}, nil); got != 400 {
		t.Errorf("inviting an owner: %d", got)
	}
	if got := member.call("PATCH", member.path(""), map[string]string{"name": "Mine now"}, nil); got != 403 {
		t.Errorf("member renaming: %d", got)
	}
	if got := admin.call("DELETE", admin.path(""), nil, nil); got != 403 {
		t.Errorf("admin deleting the workspace: %d", got)
	}
	// Members still do the everyday work.
	if got := member.call("POST", member.path("/workflows"), map[string]string{"name": "Mine"}, nil); got != 201 {
		t.Errorf("member creating a workflow: %d", got)
	}

	// Role changes.
	if got := member.call("PATCH", member.path("/members/"+member.User.ID), map[string]string{"role": "owner"}, nil); got != 403 {
		t.Errorf("member promoting self: %d", got)
	}
	if got := admin.call("PATCH", admin.path("/members/"+admin.User.ID), map[string]string{"role": "owner"}, nil); got != 403 {
		t.Errorf("admin promoting self to owner: %d", got)
	}
	if got := admin.call("PATCH", admin.path("/members/"+owner.User.ID), map[string]string{"role": "member"}, nil); got != 403 {
		t.Errorf("admin demoting the owner: %d", got)
	}
	if got := admin.call("PATCH", admin.path("/members/"+member.User.ID), map[string]string{"role": "admin"}, nil); got != 200 {
		t.Errorf("admin promoting a member: %d", got)
	}
	if got := owner.call("PATCH", owner.path("/members/"+member.User.ID), map[string]string{"role": "wizard"}, nil); got != 400 {
		t.Errorf("unknown role: %d", got)
	}
	if got := owner.call("PATCH", owner.path("/members/"+member.User.ID), map[string]string{"role": "member"}, nil); got != 200 {
		t.Errorf("owner demoting: %d", got)
	}

	// The last owner can't be demoted or removed, even by themselves.
	if got := owner.call("PATCH", owner.path("/members/"+owner.User.ID), map[string]string{"role": "admin"}, nil); got != 409 {
		t.Errorf("demoting the last owner: %d", got)
	}
	if got := owner.call("DELETE", owner.path("/members/"+owner.User.ID), nil, nil); got != 409 {
		t.Errorf("last owner leaving: %d", got)
	}
	// With a second owner the first can step down.
	owner.mustCall(200, "PATCH", owner.path("/members/"+admin.User.ID), map[string]string{"role": "owner"}, nil)
	owner.mustCall(200, "PATCH", owner.path("/members/"+owner.User.ID), map[string]string{"role": "member"}, nil)

	// Removing people: a member can't remove others, can leave.
	if got := owner.call("DELETE", owner.path("/members/"+member.User.ID), nil, nil); got != 403 {
		t.Errorf("a plain member removing another: %d", got)
	}
	member.mustCall(200, "DELETE", member.path("/members/"+member.User.ID), nil, nil)
	if got := member.call("GET", member.path("/workflows"), nil, nil); got != 404 {
		t.Errorf("after leaving, the workspace is still reachable: %d", got)
	}
}

func TestMembersListHidesEmailsFromPlainMembers(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Olga", "olga@example.com")
	member := e.signup("Mia", "mia@example.com")
	e.join(owner, member, "member")
	member.WS = owner.WS

	var asOwner, asMember []Member
	owner.mustCall(200, "GET", owner.path("/members"), nil, &asOwner)
	member.mustCall(200, "GET", member.path("/members"), nil, &asMember)
	if len(asOwner) != 2 || asOwner[0].Role != "owner" || asOwner[0].Email == "" {
		t.Errorf("owner view: %+v", asOwner)
	}
	for _, m := range asMember {
		if m.UserID != member.User.ID && m.Email != "" {
			t.Errorf("a member can see %s's email", m.Name)
		}
	}
}

func TestInviteLinks(t *testing.T) {
	e := newEnv(t)
	owner := e.signup("Olga", "olga@example.com")
	guest := e.signup("Gus", "gus@example.com")

	var created struct {
		Invite Invite `json:"invite"`
		Token  string `json:"token"`
	}
	owner.mustCall(201, "POST", owner.path("/invites"), map[string]string{"note": "for Gus", "role": "member"}, &created)
	if len(created.Token) < 30 {
		t.Fatalf("weak token %q", created.Token)
	}
	// The listing never shows the token again, and the database holds only its hash.
	var raw []map[string]any
	owner.mustCall(200, "GET", owner.path("/invites"), nil, &raw)
	if len(raw) != 1 || raw[0]["token"] != nil || strings.Contains(fmt.Sprint(raw), created.Token) {
		t.Errorf("invite listing leaks the token: %v", raw)
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM invites WHERE token_hash = $1`, []byte(created.Token)).Scan(&n)
	if n != 0 {
		t.Error("raw token stored")
	}

	// Anyone with the link can preview which workspace it is for (signed out too), and nothing more.
	var prev map[string]string
	e.client().mustCall(200, "GET", "/api/invites/preview?token="+created.Token, nil, &prev)
	if prev["workspace"] == "" || len(prev) != 2 {
		t.Errorf("preview: %v", prev)
	}
	if got := e.client().call("GET", "/api/invites/preview?token=nope", nil, nil); got != 400 {
		t.Errorf("bad token preview: %d", got)
	}
	// Accepting needs an account.
	if got := e.client().call("POST", "/api/invites/accept", map[string]string{"token": created.Token}, nil); got != 401 {
		t.Errorf("anonymous accept: %d", got)
	}
	var ws WorkspaceInfo
	guest.mustCall(200, "POST", "/api/invites/accept", map[string]string{"token": created.Token}, &ws)
	if ws.ID != owner.WS || ws.Role != "member" {
		t.Errorf("accepted: %+v", ws)
	}
	var me meResponse
	guest.mustCall(200, "GET", "/api/me", nil, &me)
	if len(me.Workspaces) != 2 {
		t.Errorf("guest sees %d workspaces", len(me.Workspaces))
	}
	// One use only.
	third := e.signup("Tess", "tess@example.com")
	if got := third.call("POST", "/api/invites/accept", map[string]string{"token": created.Token}, nil); got != 400 {
		t.Errorf("reusing a link: %d", got)
	}
	// Already a member -> clear error; link not consumed by the failure.
	owner.mustCall(201, "POST", owner.path("/invites"), map[string]string{}, &created)
	if got := guest.call("POST", "/api/invites/accept", map[string]string{"token": created.Token}, nil); got != 400 && got != 409 {
		t.Errorf("joining twice: %d", got)
	}
	third.mustCall(200, "POST", "/api/invites/accept", map[string]string{"token": created.Token}, nil)

	// Expired and revoked links.
	owner.mustCall(201, "POST", owner.path("/invites"), map[string]string{}, &created)
	_, _ = e.db.Exec(`UPDATE invites SET expires_at = now() - interval '1 hour'`)
	fourth := e.signup("Fay", "fay@example.com")
	if got := fourth.call("POST", "/api/invites/accept", map[string]string{"token": created.Token}, nil); got != 400 {
		t.Errorf("expired link: %d", got)
	}
	owner.mustCall(201, "POST", owner.path("/invites"), map[string]string{}, &created)
	owner.mustCall(200, "DELETE", owner.path("/invites/"+created.Invite.ID), nil, nil)
	if got := fourth.call("POST", "/api/invites/accept", map[string]string{"token": created.Token}, nil); got != 400 {
		t.Errorf("revoked link: %d", got)
	}
}

func TestInviteCannotBeUsedInAnotherWorkspaceToRevoke(t *testing.T) {
	e := newEnv(t)
	a := e.signup("Amara", "amara@example.com")
	b := e.signup("Bruno", "bruno@example.com")
	var created struct {
		Invite Invite `json:"invite"`
	}
	a.mustCall(201, "POST", a.path("/invites"), map[string]string{}, &created)
	if got := b.call("DELETE", b.path("/invites/"+created.Invite.ID), nil, nil); got != 404 {
		t.Errorf("revoking another workspace's invite: %d", got)
	}
}

func TestWorkspaceLifecycle(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	var ws WorkspaceInfo
	p.mustCall(201, "POST", "/api/workspaces", map[string]string{"name": "Product team"}, &ws)
	if ws.Role != "owner" {
		t.Errorf("creator role %q", ws.Role)
	}
	if got := p.call("POST", "/api/workspaces", map[string]string{"name": "  "}, nil); got != 400 {
		t.Errorf("blank name: %d", got)
	}
	var list []Summary
	p.mustCall(200, "GET", "/api/workspaces/"+ws.ID+"/workflows", nil, &list)
	if len(list) != 1 {
		t.Errorf("new workspace should start with the example: %d", len(list))
	}
	p.mustCall(200, "PATCH", "/api/workspaces/"+ws.ID, map[string]string{"name": "Platform team"}, nil)
	var me meResponse
	p.mustCall(200, "GET", "/api/me", nil, &me)
	found := false
	for _, w := range me.Workspaces {
		found = found || (w.ID == ws.ID && w.Name == "Platform team")
	}
	if !found {
		t.Errorf("rename not visible: %+v", me.Workspaces)
	}
	p.mustCall(200, "DELETE", "/api/workspaces/"+ws.ID, nil, nil)
	if got := p.call("GET", "/api/workspaces/"+ws.ID+"/workflows", nil, nil); got != 404 {
		t.Errorf("deleted workspace: %d", got)
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM workflows WHERE workspace_id = $1`, ws.ID).Scan(&n)
	if n != 0 {
		t.Error("workflows left behind")
	}
}

func TestConcurrentSavesOnlyOneWins(t *testing.T) {
	e := newEnv(t)
	p := e.signup("Amara", "amara@example.com")
	id := p.firstWorkflow().ID
	var base Workflow
	p.mustCall(200, "GET", p.path("/workflows/"+id), nil, &base)

	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := base
			w.Name = fmt.Sprintf("Edit %d", i)
			codes[i] = p.call("PUT", p.path("/workflows/"+id), w, nil)
		}()
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			conflict++
		}
	}
	if ok != 1 || conflict != 7 {
		t.Errorf("want exactly one winner, got ok=%d conflict=%d (%v)", ok, conflict, codes)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	e := newEnv(t)
	if err := migrate(t.Context(), e.db); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != 1 {
		t.Errorf("%d migrations recorded", n)
	}
}

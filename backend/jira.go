package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	jiraKey   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,30}-[0-9]{1,9}$`)
	jiraCloud = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// adfText flattens Atlassian Document Format (Jira's rich text) to plain text.
func adfText(v any) string {
	var b strings.Builder
	var walk func(n any, depth int)
	walk = func(n any, depth int) {
		if depth > 30 || b.Len() > 20000 {
			return
		}
		switch t := n.(type) {
		case string:
			b.WriteString(t) // very old-style plain text bodies
		case map[string]any:
			typ, _ := t["type"].(string)
			if typ == "text" {
				s, _ := t["text"].(string)
				b.WriteString(s)
			}
			if typ == "hardBreak" {
				b.WriteString("\n")
			}
			if typ == "mention" {
				if a, ok := t["attrs"].(map[string]any); ok {
					if s, _ := a["text"].(string); s != "" {
						b.WriteString(s)
					}
				}
			}
			if c, ok := t["content"].([]any); ok {
				for _, x := range c {
					walk(x, depth+1)
				}
				switch typ {
				case "paragraph", "heading", "listItem", "codeBlock", "blockquote":
					b.WriteString("\n")
				}
			}
		}
	}
	walk(v, 0)
	return strings.TrimSpace(b.String())
}

type jiraInput struct {
	Key   string `json:"key"`
	JQL   string `json:"jql"`
	Limit int    `json:"limit"`
}

func jiraTools() []ToolDef {
	limit := map[string]any{"type": "integer", "description": "How many to return, at most 25"}
	return []ToolDef{
		{Name: "jira_list_projects", Description: "List the Jira projects the connected account can see.", InputSchema: objSchema(map[string]any{"limit": limit})},
		{Name: "jira_search_issues", Description: "Search Jira issues with a JQL query, e.g. \"project = ABC AND statusCategory != Done ORDER BY updated DESC\". Queries must be bounded (restrict by project or date). Returns summaries only.",
			InputSchema: objSchema(map[string]any{"jql": map[string]any{"type": "string", "description": "A JQL query, at most 500 characters"}, "limit": limit}, "jql")},
		{Name: "jira_get_issue", Description: "Read one Jira issue by key (like ABC-123): description and first comments.",
			InputSchema: objSchema(map[string]any{"key": map[string]any{"type": "string", "description": "Issue key such as ABC-123"}}, "key")},
	}
}

func jiraRun(ctx context.Context, app OAuthApp, ts tokenSet, name string, raw json.RawMessage) (string, error) {
	var in jiraInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", errors.New("the tool input was not valid")
	}
	cloud := ts.Extra["cloud"]
	if !jiraCloud.MatchString(cloud) {
		return "", errors.New("no Jira site is connected; an admin needs to reconnect Jira")
	}
	get := func(path string, q url.Values) ([]byte, error) {
		return apiGet(ctx, "Jira", app.APIURL+"/ex/jira/"+cloud, path, q, ts.Access, nil)
	}
	limit := "15"
	if in.Limit > 0 {
		limit = strconv.Itoa(min(in.Limit, 25))
	}
	name2 := func(m map[string]any, k string) string {
		if o, ok := m[k].(map[string]any); ok {
			s, _ := o["name"].(string)
			return s
		}
		return ""
	}
	person := func(m map[string]any, k string) string {
		if o, ok := m[k].(map[string]any); ok {
			s, _ := o["displayName"].(string)
			return s
		}
		return ""
	}
	switch name {
	case "jira_list_projects":
		b, err := get("/rest/api/3/project/search", url.Values{"maxResults": {limit}, "orderBy": {"key"}})
		if err != nil {
			return "", err
		}
		var r struct {
			Values []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
				Type string `json:"projectTypeKey"`
			} `json:"values"`
		}
		_ = json.Unmarshal(b, &r)
		return compact(r.Values), nil

	case "jira_search_issues":
		jql := strings.TrimSpace(in.JQL)
		if jql == "" || len([]rune(jql)) > 500 {
			return "", errors.New("jql must be 1 to 500 characters")
		}
		b, err := get("/rest/api/3/search/jql", url.Values{"jql": {jql}, "maxResults": {limit}, "fields": {"summary,status,assignee,issuetype,priority,updated"}})
		if err != nil {
			return "", err
		}
		var r struct {
			Issues []struct {
				Key    string         `json:"key"`
				Fields map[string]any `json:"fields"`
			} `json:"issues"`
		}
		_ = json.Unmarshal(b, &r)
		out := []map[string]string{}
		for _, i := range r.Issues {
			summary, _ := i.Fields["summary"].(string)
			updated, _ := i.Fields["updated"].(string)
			out = append(out, map[string]string{"key": i.Key, "summary": cut(summary, 200), "status": name2(i.Fields, "status"), "type": name2(i.Fields, "issuetype"),
				"priority": name2(i.Fields, "priority"), "assignee": person(i.Fields, "assignee"), "updated": updated})
		}
		return compact(out), nil

	case "jira_get_issue":
		if !jiraKey.MatchString(in.Key) {
			return "", errors.New("key must look like ABC-123")
		}
		b, err := get("/rest/api/3/issue/"+in.Key, url.Values{"fields": {"summary,status,description,assignee,reporter,issuetype,priority,labels,created,updated,comment"}})
		if err != nil {
			return "", err
		}
		var r struct {
			Key    string         `json:"key"`
			Fields map[string]any `json:"fields"`
		}
		_ = json.Unmarshal(b, &r)
		f := r.Fields
		summary, _ := f["summary"].(string)
		out := map[string]any{"key": r.Key, "summary": cut(summary, 300), "status": name2(f, "status"), "type": name2(f, "issuetype"), "priority": name2(f, "priority"),
			"assignee": person(f, "assignee"), "reporter": person(f, "reporter"), "labels": f["labels"], "created": f["created"], "updated": f["updated"],
			"description": cut(adfText(f["description"]), 3000)}
		if c, ok := f["comment"].(map[string]any); ok {
			comments := []map[string]string{}
			list, _ := c["comments"].([]any)
			for _, x := range list {
				if len(comments) >= 10 {
					break
				}
				if m, ok := x.(map[string]any); ok {
					comments = append(comments, map[string]string{"author": person(m, "author"), "body": cut(adfText(m["body"]), 800)})
				}
			}
			out["comments"] = comments
		}
		return compact(out), nil
	}
	return "", errors.New("unknown tool")
}

func jiraProvider() *provider {
	return &provider{
		ID: "jira", Label: "Jira",
		authorize: func(app OAuthApp, q url.Values) string {
			q.Set("audience", "api.atlassian.com")
			q.Set("response_type", "code")
			q.Set("prompt", "consent")
			return app.AuthURL + "/authorize?" + q.Encode()
		},
		exchange: func(ctx context.Context, app OAuthApp, redirect, code string) (tokenResponse, error) {
			return postToken(ctx, app.AuthURL+"/oauth/token", url.Values{
				"grant_type": {"authorization_code"}, "client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "code": {code}, "redirect_uri": {redirect},
			}, true, "", "")
		},
		refresh: func(ctx context.Context, app OAuthApp, ts tokenSet) (tokenResponse, error) { // the refresh token rotates
			return postToken(ctx, app.AuthURL+"/oauth/token", url.Values{
				"grant_type": {"refresh_token"}, "client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "refresh_token": {ts.Refresh},
			}, true, "", "")
		},
		identify: func(ctx context.Context, app OAuthApp, ts *tokenSet) (string, error) {
			raw, err := apiGet(ctx, "Jira", app.APIURL, "/oauth/token/accessible-resources", nil, ts.Access, nil)
			if err != nil {
				return "", err
			}
			var sites []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				URL  string `json:"url"`
			}
			if json.Unmarshal(raw, &sites) != nil || len(sites) == 0 || !jiraCloud.MatchString(sites[0].ID) {
				return "", errors.New("the account has no Jira site")
			}
			// The first site is used. Accounts with several sites connect the first one Atlassian lists.
			ts.Extra = map[string]string{"cloud": sites[0].ID}
			return cut(sites[0].Name, 100), nil
		},
		tools: jiraTools(),
		run:   jiraRun,
	}
}

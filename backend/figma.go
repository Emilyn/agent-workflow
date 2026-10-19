package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
)

var (
	figmaKey  = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	figmaNode = regexp.MustCompile(`^[A-Za-z0-9:;_-]{1,120}$`)
)

type figmaInput struct {
	FileKey string `json:"file_key"`
	NodeID  string `json:"node_id"`
	Limit   int    `json:"limit"`
}

func figmaTools() []ToolDef {
	key := map[string]any{"type": "string", "description": "The file key: the part after /design/ or /file/ in a Figma URL"}
	return []ToolDef{
		{Name: "figma_get_file", Description: "Read a Figma file's name, last change, pages and the top-level frames on each page (names and node ids).",
			InputSchema: objSchema(map[string]any{"file_key": key}, "file_key")},
		{Name: "figma_get_node_text", Description: "Read one frame or page: its layer outline and all text content. Use a node id from figma_get_file, such as 12:34.",
			InputSchema: objSchema(map[string]any{"file_key": key, "node_id": map[string]any{"type": "string", "description": "Node id like 12:34"}}, "file_key", "node_id")},
		{Name: "figma_get_comments", Description: "Read the comments on a Figma file.",
			InputSchema: objSchema(map[string]any{"file_key": key, "limit": map[string]any{"type": "integer", "description": "At most 50"}}, "file_key")},
	}
}

type figNode struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Characters string    `json:"characters"`
	Children   []figNode `json:"children"`
}

func figmaRun(ctx context.Context, app OAuthApp, ts tokenSet, name string, raw json.RawMessage) (string, error) {
	var in figmaInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", errors.New("the tool input was not valid")
	}
	if !figmaKey.MatchString(in.FileKey) {
		return "", errors.New("file_key is not valid")
	}
	get := func(path string, q url.Values) ([]byte, error) {
		return apiGet(ctx, "Figma", app.APIURL, path, q, ts.Access, nil)
	}
	switch name {
	case "figma_get_file":
		b, err := get("/v1/files/"+in.FileKey, url.Values{"depth": {"2"}})
		if err != nil {
			return "", err
		}
		var f struct {
			Name     string  `json:"name"`
			Modified string  `json:"lastModified"`
			Version  string  `json:"version"`
			Document figNode `json:"document"`
		}
		_ = json.Unmarshal(b, &f)
		type frame struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		type page struct {
			ID     string  `json:"id"`
			Name   string  `json:"name"`
			Frames []frame `json:"frames"`
		}
		pages := []page{}
		for _, p := range f.Document.Children[:min(len(f.Document.Children), 20)] {
			pg := page{ID: p.ID, Name: cut(p.Name, 100), Frames: []frame{}}
			for _, c := range p.Children[:min(len(p.Children), 30)] {
				pg.Frames = append(pg.Frames, frame{ID: c.ID, Name: cut(c.Name, 100), Type: c.Type})
			}
			pages = append(pages, pg)
		}
		return compact(map[string]any{"name": cut(f.Name, 200), "lastModified": f.Modified, "version": f.Version, "pages": pages}), nil

	case "figma_get_node_text":
		if !figmaNode.MatchString(in.NodeID) {
			return "", errors.New("node_id is not valid")
		}
		b, err := get("/v1/files/"+in.FileKey+"/nodes", url.Values{"ids": {in.NodeID}, "depth": {"6"}})
		if err != nil {
			return "", err
		}
		var r struct {
			Nodes map[string]struct {
				Document figNode `json:"document"`
			} `json:"nodes"`
		}
		_ = json.Unmarshal(b, &r)
		var outline, texts []string
		var walk func(n figNode, depth int)
		walk = func(n figNode, depth int) {
			if depth > 6 || len(outline) >= 150 {
				return
			}
			line := ""
			for i := 0; i < depth; i++ {
				line += "  "
			}
			outline = append(outline, line+n.Type+": "+cut(n.Name, 80))
			if n.Type == "TEXT" && n.Characters != "" && len(texts) < 150 {
				texts = append(texts, cut(n.Characters, 400))
			}
			for _, c := range n.Children {
				walk(c, depth+1)
			}
		}
		found := false
		for _, n := range r.Nodes {
			walk(n.Document, 0)
			found = true
		}
		if !found {
			return "", errors.New("that node was not found in the file")
		}
		return compact(map[string]any{"outline": outline, "text": texts}), nil

	case "figma_get_comments":
		b, err := get("/v1/files/"+in.FileKey+"/comments", nil)
		if err != nil {
			return "", err
		}
		var r struct {
			Comments []struct {
				ID       string `json:"id"`
				Message  string `json:"message"`
				Created  string `json:"created_at"`
				Resolved string `json:"resolved_at"`
				Parent   string `json:"parent_id"`
				User     struct {
					Handle string `json:"handle"`
				} `json:"user"`
			} `json:"comments"`
		}
		_ = json.Unmarshal(b, &r)
		limit := 50
		if in.Limit > 0 {
			limit = min(in.Limit, 50)
		}
		out := []map[string]any{}
		for _, c := range r.Comments {
			if len(out) >= limit {
				break
			}
			out = append(out, map[string]any{"id": c.ID, "author": c.User.Handle, "message": cut(c.Message, 500), "created": c.Created, "resolved": c.Resolved != "", "replyTo": c.Parent})
		}
		return compact(out), nil
	}
	return "", errors.New("unknown tool")
}

func figmaProvider() *provider {
	return &provider{
		ID: "figma", Label: "Figma",
		authorize: func(app OAuthApp, q url.Values) string {
			q.Set("response_type", "code")
			return app.AuthURL + "/oauth?" + q.Encode()
		},
		exchange: func(ctx context.Context, app OAuthApp, redirect, code string) (tokenResponse, error) {
			return postToken(ctx, app.APIURL+"/v1/oauth/token", url.Values{"redirect_uri": {redirect}, "code": {code}, "grant_type": {"authorization_code"}}, false, app.ClientID, app.ClientSecret)
		},
		refresh: func(ctx context.Context, app OAuthApp, ts tokenSet) (tokenResponse, error) { // keeps the same refresh token
			return postToken(ctx, app.APIURL+"/v1/oauth/refresh", url.Values{"refresh_token": {ts.Refresh}}, false, app.ClientID, app.ClientSecret)
		},
		identify: func(ctx context.Context, app OAuthApp, ts *tokenSet) (string, error) {
			raw, err := apiGet(ctx, "Figma", app.APIURL, "/v1/me", nil, ts.Access, nil)
			if err != nil {
				return "", err
			}
			var u struct {
				Handle string `json:"handle"`
				Email  string `json:"email"`
			}
			if json.Unmarshal(raw, &u) != nil || (u.Handle == "" && u.Email == "") {
				return "", errors.New("Figma did not say who this is")
			}
			if u.Handle != "" {
				return cut(u.Handle, 100), nil
			}
			return cut(u.Email, 100), nil
		},
		tools: figmaTools(),
		run:   figmaRun,
	}
}

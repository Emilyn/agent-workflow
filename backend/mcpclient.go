package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A small client for remote MCP servers over the streamable HTTP transport: initialize, list tools, call a
// tool. It does not run anything a server asks of the client (sampling, roots): it only reads.

const (
	mcpProtocol     = "2025-03-26"
	maxMCPResponse  = 4 << 20
	maxMCPTools     = 200
	maxMCPSchema    = 20 << 10
	maxMCPDesc      = 600
	maxMCPToolInput = 20 << 10
	maxMCPOutput    = 12000
)

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	ReadOnly    bool           `json:"readOnly"`
	Hash        string         `json:"hash"`
	Changed     bool           `json:"changed,omitempty"` // was switched on, then the server changed it
}

// hashTool identifies what a person looked at when switching a tool on: if the server later changes the
// description or the inputs, the tool is switched off until someone looks again.
func hashTool(t mcpTool) string {
	schema, _ := json.Marshal(t.InputSchema)
	h := sha256.Sum256([]byte(t.Name + "\x00" + t.Description + "\x00" + string(schema)))
	return hex.EncodeToString(h[:8])
}

type mcpClient struct {
	http    *http.Client
	url     string
	headers map[string]string

	mu      sync.Mutex
	session string
	ready   bool
	nextID  int
	server  string
	// refresh gets new credentials after the server refuses the current ones (OAuth). It runs at most once per request.
	refresh func(ctx context.Context) (map[string]string, error)
}

func newMCPClient(g guard, url string, headers map[string]string) *mcpClient {
	return &mcpClient{http: g.client(30 * time.Second), url: url, headers: headers}
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *mcpClient) post(ctx context.Context, body []byte) (*http.Response, error) {
	resp, err := c.send(ctx, body)
	if err != nil && errors.Is(err, errUnauthorized) && c.refresh != nil {
		hdrs, rerr := c.refresh(ctx)
		if rerr != nil {
			return nil, rerr
		}
		c.headers = hdrs
		resp, err = c.send(ctx, body)
	}
	if errors.Is(err, errUnauthorized) {
		if c.refresh != nil {
			return nil, errNeedsSignIn
		}
		return nil, errors.New("the server refused the sign-in. Check the token")
	}
	return resp, err
}

var errUnauthorized = errors.New("unauthorized")

func (c *mcpClient) send(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("that is not a valid address")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "AgentWorkflow/1 (+mcp)")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
		req.Header.Set("MCP-Protocol-Version", mcpProtocol)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), errBlockedAddress.Error()) {
			return nil, errBlockedAddress
		}
		if ctx.Err() != nil {
			return nil, errors.New("the server took too long to answer")
		}
		return nil, errors.New("could not reach the server")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		resp.Body.Close()
		return nil, errUnauthorized
	}
	return resp, nil
}

// rpc sends one request and returns its result. The caller holds c.mu.
func (c *mcpClient) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("the server answered with status %d", resp.StatusCode)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && c.session == "" {
		c.session = sid
	}
	want := fmt.Sprint(id)
	match := func(raw []byte) (json.RawMessage, bool, error) {
		var m rpcMessage
		if err := json.Unmarshal(raw, &m); err != nil || len(m.ID) == 0 {
			return nil, false, nil // a notification or something we do not use
		}
		if strings.Trim(string(m.ID), `"`) != want {
			return nil, false, nil
		}
		if m.Error != nil {
			return nil, true, fmt.Errorf("the server reported an error: %s", cut(m.Error.Message, 200))
		}
		return m.Result, true, nil
	}
	limited := io.LimitReader(resp.Body, maxMCPResponse)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		sc := bufio.NewScanner(limited)
		sc.Buffer(make([]byte, 64<<10), maxMCPResponse)
		var data []string
		flush := func() (json.RawMessage, bool, error) {
			if len(data) == 0 {
				return nil, false, nil
			}
			raw := []byte(strings.Join(data, "\n"))
			data = nil
			return match(raw)
		}
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if r, ok, err := flush(); ok {
					return r, err
				}
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if r, ok, err := flush(); ok {
			return r, err
		}
		return nil, errors.New("the server ended its answer without a result")
	}
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("the server's answer was cut off")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' { // a batch
		var batch []json.RawMessage
		if json.Unmarshal(raw, &batch) == nil {
			for _, m := range batch {
				if r, ok, err := match(m); ok {
					return r, err
				}
			}
		}
		return nil, errors.New("the server answered with something unexpected")
	}
	if r, ok, err := match(raw); ok {
		return r, err
	}
	return nil, errors.New("the server answered with something unexpected")
}

func (c *mcpClient) init(ctx context.Context) error {
	if c.ready {
		return nil
	}
	res, err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agent-workflow", "version": "1"},
	})
	if err != nil {
		return err
	}
	var info struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	_ = json.Unmarshal(res, &info)
	c.server = cut(info.ServerInfo.Name, 80)
	if body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err == nil {
		if resp, err := c.post(ctx, body); err == nil {
			resp.Body.Close()
		}
	}
	c.ready = true
	return nil
}

// ListTools connects and returns the server's name and its tools.
func (c *mcpClient) ListTools(ctx context.Context) (server string, tools []mcpTool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.init(ctx); err != nil {
		return "", nil, err
	}
	cursor := ""
	for page := 0; page < 10 && len(tools) < maxMCPTools; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		res, err := c.rpc(ctx, "tools/list", params)
		if err != nil {
			return "", nil, err
		}
		var out struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
				Annotations struct {
					ReadOnlyHint *bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(res, &out); err != nil {
			return "", nil, errors.New("the server's tool list was not understood")
		}
		for _, t := range out.Tools {
			if t.Name == "" || len(tools) >= maxMCPTools {
				continue
			}
			schema := t.InputSchema
			if raw, _ := json.Marshal(schema); schema == nil || len(raw) > maxMCPSchema || schema["type"] != "object" {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			mt := mcpTool{Name: cut(t.Name, 100), Description: cut(strings.TrimSpace(t.Description), maxMCPDesc), InputSchema: schema, ReadOnly: t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint}
			mt.Hash = hashTool(mt)
			tools = append(tools, mt)
		}
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	return c.server, tools, nil
}

// CallTool runs one tool and returns its text. A tool-level error comes back as an error with the server's message.
func (c *mcpClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.init(ctx); err != nil {
		return "", err
	}
	var arguments any = map[string]any{}
	if len(bytes.TrimSpace(args)) > 0 {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return "", errors.New("the tool input was not valid")
		}
	}
	res, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Resource *struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", errors.New("the tool's answer was not understood")
	}
	var parts []string
	for _, p := range out.Content {
		switch {
		case p.Type == "text":
			parts = append(parts, p.Text)
		case p.Type == "resource" && p.Resource != nil && p.Resource.Text != "":
			parts = append(parts, p.Resource.Text)
		default:
			parts = append(parts, "["+p.Type+" content left out]")
		}
	}
	text := cut(strings.TrimSpace(strings.Join(parts, "\n")), maxMCPOutput)
	if out.IsError {
		if text == "" {
			text = "the tool reported an error"
		}
		return "", errors.New(text)
	}
	if text == "" {
		text = "(no output)"
	}
	return text, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Claude is a minimal client for the Anthropic Messages API. It holds no key:
// each call is given the key of the workspace it runs for.
type Claude struct {
	Model   string
	BaseURL string
	HTTP    *http.Client
}

var errKeyRejected = errors.New("anthropic rejected the API key")

func NewClaude(model, baseURL string) *Claude {
	return &Claude{
		Model:   model,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 90 * time.Second},
	}
}

type messagesRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []messagesTurn `json:"messages"`
}

type messagesTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Claude) newRequest(ctx context.Context, method, path, key string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	return req, nil
}

// Verify checks a key with a cheap, read-only call to the Models API.
// It returns errKeyRejected when Anthropic says the key is not valid.
func (c *Claude) Verify(ctx context.Context, key string) error {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/models?limit=1", key, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Anthropic: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errKeyRejected
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("anthropic returned %d while checking the key", resp.StatusCode)
	}
	return nil
}

// postMessages sends one Messages API request and returns the raw reply body.
func (c *Claude) postMessages(ctx context.Context, key string, payload any) ([]byte, error) {
	if key == "" {
		return nil, errors.New("Claude is not connected: add an Anthropic API key")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/messages", key, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		var out messagesResponse
		_ = json.Unmarshal(raw, &out)
		msg := strings.TrimSpace(string(raw))
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("claude returned %d: %s", resp.StatusCode, msg)
	}
	return raw, nil
}

// Complete sends a single user turn and returns the text of the reply.
func (c *Claude) Complete(ctx context.Context, key, system, user string, maxTokens int) (string, error) {
	raw, err := c.postMessages(ctx, key, messagesRequest{
		Model:     c.Model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []messagesTurn{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	var out messagesResponse
	_ = json.Unmarshal(raw, &out)
	var sb strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			sb.WriteString(part.Text)
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", errors.New("claude returned an empty reply")
	}
	return text, nil
}

// ---------- tool use ----------

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolRunner runs one tool call. Its text (or error) goes back to Claude as the tool result.
type ToolRunner func(ctx context.Context, name string, input json.RawMessage) (string, error)

const (
	maxToolTurns = 8  // model calls in one run
	maxToolCalls = 12 // tool executions in one run
)

type toolResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

// CompleteWithTools lets Claude call tools until it has an answer. On the last
// turn, or once the call budget is spent, it is told to answer without more tools.
func (c *Claude) CompleteWithTools(ctx context.Context, key, system, user string, tools []ToolDef, run ToolRunner, maxTokens int) (string, error) {
	messages := []map[string]any{{"role": "user", "content": user}}
	calls := 0
	for turn := 0; turn < maxToolTurns; turn++ {
		payload := map[string]any{
			"model": c.Model, "max_tokens": maxTokens, "system": system, "messages": messages, "tools": tools,
		}
		if turn == maxToolTurns-1 || calls >= maxToolCalls {
			payload["tool_choice"] = map[string]string{"type": "none"}
		}
		raw, err := c.postMessages(ctx, key, payload)
		if err != nil {
			return "", err
		}
		var out toolResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", errors.New("claude sent a reply this app could not read")
		}
		var rawContent struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw, &rawContent)

		if out.StopReason != "tool_use" {
			var sb strings.Builder
			for _, p := range out.Content {
				if p.Type == "text" {
					sb.WriteString(p.Text)
				}
			}
			text := strings.TrimSpace(sb.String())
			if text == "" {
				return "", errors.New("claude returned an empty reply")
			}
			return text, nil
		}

		results := []map[string]any{}
		for _, p := range out.Content {
			if p.Type != "tool_use" {
				continue
			}
			res := map[string]any{"type": "tool_result", "tool_use_id": p.ID}
			if calls >= maxToolCalls {
				res["content"], res["is_error"] = "Tool call limit reached. Answer with what you have.", true
			} else {
				calls++
				text, err := run(ctx, p.Name, p.Input)
				if err != nil {
					text, res["is_error"] = err.Error(), true
				}
				res["content"] = text
			}
			results = append(results, res)
		}
		messages = append(messages,
			map[string]any{"role": "assistant", "content": rawContent.Content},
			map[string]any{"role": "user", "content": results})
	}
	return "", errors.New("claude kept calling tools without answering")
}

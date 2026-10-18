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

// Complete sends a single user turn and returns the text of the reply.
func (c *Claude) Complete(ctx context.Context, key, system, user string, maxTokens int) (string, error) {
	if key == "" {
		return "", errors.New("Claude is not connected: add an Anthropic API key")
	}
	body, err := json.Marshal(messagesRequest{
		Model:     c.Model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []messagesTurn{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/messages", key, body)
	if err != nil {
		return "", err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var out messagesResponse
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("claude returned %d: %s", resp.StatusCode, msg)
	}
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

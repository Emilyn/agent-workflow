package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Claude is a minimal client for the Anthropic Messages API.
type Claude struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

func NewClaudeFromEnv() *Claude {
	return &Claude{
		APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		Model:   envOr("ANTHROPIC_MODEL", "claude-sonnet-5-5"),
		BaseURL: strings.TrimRight(envOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com"), "/"),
		HTTP:    &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Claude) Enabled() bool { return c != nil && c.APIKey != "" }

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

// Complete sends a single user turn and returns the text of the reply.
func (c *Claude) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	if !c.Enabled() {
		return "", errors.New("ANTHROPIC_API_KEY is not set")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

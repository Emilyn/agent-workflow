package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The API key a user connects is kept in a separate file from the workflow,
// readable only by the server's user. It is never sent back to the browser.

type credentials struct {
	APIKey string `json:"apiKey"`
}

func loadCredentials(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var c credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", err
	}
	return c.APIKey, nil
}

func saveCredentials(path, key string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(credentials{APIKey: key})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func deleteCredentials(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type claudeStatus struct {
	Connected bool   `json:"connected"`
	Source    string `json:"source"` // "app" | "environment" | ""
	KeyHint   string `json:"keyHint,omitempty"`
	Model     string `json:"model"`
}

func (s *Server) claudeStatus() claudeStatus {
	k := s.claude.key()
	st := claudeStatus{Connected: k != "", Model: s.claude.Model}
	if k != "" {
		st.Source = "app"
		if s.envKey {
			st.Source = "environment"
		}
		if len(k) >= 12 {
			st.KeyHint = "…" + k[len(k)-4:]
		}
	}
	return st
}

func (s *Server) getClaude(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.claudeStatus())
}

func (s *Server) putClaudeKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"apiKey"`
	}
	if !decode(w, r, &req) {
		return
	}
	if s.envKey {
		writeError(w, http.StatusConflict, "The key is set by the server's ANTHROPIC_API_KEY environment variable. Change it there.")
		return
	}
	key := strings.TrimSpace(req.APIKey)
	switch {
	case strings.HasPrefix(key, "sk-ant-oat"):
		writeError(w, http.StatusBadRequest, "That looks like a Claude subscription token. Those cannot be used here. Create an API key in the Claude Console instead.")
		return
	case !strings.HasPrefix(key, "sk-ant-") || len(key) < 20 || len(key) > 400 || strings.ContainsAny(key, " \t\r\n"):
		writeError(w, http.StatusBadRequest, "That does not look like an Anthropic API key. Keys start with sk-ant-.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.claude.Verify(ctx, key); err != nil {
		if errors.Is(err, errKeyRejected) {
			writeError(w, http.StatusBadRequest, "Anthropic rejected that key. Check it and try again.")
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	if err := saveCredentials(s.credsPath, key); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not store the key on the server.")
		return
	}
	s.claude.SetKey(key)
	writeJSON(w, http.StatusOK, s.claudeStatus())
}

func (s *Server) deleteClaudeKey(w http.ResponseWriter, r *http.Request) {
	if s.envKey {
		writeError(w, http.StatusConflict, "The key is set by the server's ANTHROPIC_API_KEY environment variable. Change it there.")
		return
	}
	if err := deleteCredentials(s.credsPath); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not remove the stored key.")
		return
	}
	s.claude.SetKey("")
	writeJSON(w, http.StatusOK, s.claudeStatus())
}

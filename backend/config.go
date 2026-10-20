package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is read from environment variables once at startup.
type Config struct {
	Port         string
	DatabaseURL  string
	DBMaxConns   int
	StaticDir    string
	Secret       []byte // 32 bytes, encrypts stored API keys
	DevSecret    bool   // true when SECRET_KEY was missing and a fixed development key is used
	Model        string
	AnthropicURL string
	ServerKey    string // optional ANTHROPIC_API_KEY used by workspaces that have not connected their own
	CookieSecure bool
	TrustProxy   bool
	AllowSignup  bool
	PublicURL    string              // where browsers reach this app; OAuth callbacks are built from it
	Apps         map[string]OAuthApp // keyed by provider id: github, gitlab, jira, figma
	ClaudeCode   ClaudeCodeConfig
}

// ClaudeCodeConfig turns on the "run on this machine's Claude Code" mode. It starts the claude
// program that is installed on the server, signed in with the login already used there.
type ClaudeCodeConfig struct {
	Enabled      bool
	Path         string        // the claude program; "claude" is looked up on PATH
	Model        string        // empty: the server's ANTHROPIC_MODEL
	Effort       string        // low, medium, high, xhigh or max; empty: Claude Code's default
	MaxTurns     int           // model turns in one run
	MaxBudgetUSD float64       // spending cap for one run
	Timeout      time.Duration // longest a background run may take
	CallbackURL  string        // where claude reaches this server; empty: http://127.0.0.1:PORT
}

// OAuthApp is the server's OAuth app for one provider. Leave ClientID empty to turn the provider off.
type OAuthApp struct {
	ClientID     string
	ClientSecret string
	Scopes       string
	AuthURL      string // where browsers authorise and tokens are exchanged (provider specific paths are added)
	APIURL       string // base of the provider's REST API
}

func (a OAuthApp) Enabled() bool { return a.ClientID != "" && a.ClientSecret != "" }

func loadApp(prefix, scopes, authURL, apiURL string) OAuthApp {
	return OAuthApp{
		ClientID:     os.Getenv(prefix + "_CLIENT_ID"),
		ClientSecret: os.Getenv(prefix + "_CLIENT_SECRET"),
		Scopes:       envOr(prefix+"_SCOPES", scopes),
		AuthURL:      strings.TrimRight(authURL, "/"),
		APIURL:       strings.TrimRight(apiURL, "/"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	return v == "1" || v == "true" || v == "yes"
}

// A fixed key only for local development: anything encrypted with it is as good as plain text.
const devSecret = "0000000000000000000000000000000000000000000000000000000000000000"

func LoadConfig() (Config, error) {
	c := Config{
		Port:         envOr("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		StaticDir:    envOr("STATIC_DIR", "../frontend/dist"),
		Model:        envOr("ANTHROPIC_MODEL", "claude-sonnet-5-5"),
		AnthropicURL: envOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		ServerKey:    os.Getenv("ANTHROPIC_API_KEY"),
		CookieSecure: envBool("COOKIE_SECURE", false),
		TrustProxy:   envBool("TRUST_PROXY", false),
		AllowSignup:  envBool("ALLOW_SIGNUP", true),
		DBMaxConns:   20,
		PublicURL:    strings.TrimRight(envOr("PUBLIC_URL", "http://localhost:"+envOr("PORT", "8080")), "/"),
		Apps: map[string]OAuthApp{
			"github": loadApp("GITHUB", "public_repo read:user write:repo_hook", envOr("GITHUB_OAUTH_URL", "https://github.com"), envOr("GITHUB_API_URL", "https://api.github.com")),
			"gitlab": loadApp("GITLAB", "api read_user", envOr("GITLAB_URL", "https://gitlab.com"), strings.TrimRight(envOr("GITLAB_URL", "https://gitlab.com"), "/")+"/api/v4"),
			"jira":   loadApp("JIRA", "read:jira-work read:jira-user offline_access", envOr("JIRA_AUTH_URL", "https://auth.atlassian.com"), envOr("JIRA_API_URL", "https://api.atlassian.com")),
			"figma":  loadApp("FIGMA", "current_user:read file_content:read file_comments:read", envOr("FIGMA_AUTH_URL", "https://www.figma.com"), envOr("FIGMA_API_URL", "https://api.figma.com")),
		},
	}
	cc, err := loadClaudeCode(c.Model)
	if err != nil {
		return c, err
	}
	c.ClaudeCode = cc
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required, e.g. postgres://user:pass@localhost:5432/agentworkflow?sslmode=disable")
	}
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 2 || n > 500 {
			return c, fmt.Errorf("DB_MAX_CONNS must be a number from 2 to 500")
		}
		c.DBMaxConns = n
	}
	raw := os.Getenv("SECRET_KEY")
	if raw == "" {
		if envBool("ALLOW_DEV_SECRET", false) {
			raw = devSecret
			c.DevSecret = true
		} else {
			return c, errors.New("SECRET_KEY is required to encrypt API keys (generate one with: openssl rand -base64 32). For local development only, set ALLOW_DEV_SECRET=true")
		}
	}
	key, err := ParseSecretKey(raw)
	if err != nil {
		return c, err
	}
	c.Secret = key
	return c, nil
}

func loadClaudeCode(serverModel string) (ClaudeCodeConfig, error) {
	cc := ClaudeCodeConfig{
		Enabled:      envBool("CLAUDE_CODE_ENABLED", false),
		Path:         envOr("CLAUDE_CODE_PATH", "claude"),
		Model:        envOr("CLAUDE_CODE_MODEL", serverModel),
		Effort:       strings.ToLower(os.Getenv("CLAUDE_CODE_EFFORT")),
		MaxTurns:     15,
		MaxBudgetUSD: 1,
		Timeout:      5 * time.Minute,
		CallbackURL:  strings.TrimRight(os.Getenv("CLAUDE_CODE_CALLBACK_URL"), "/"),
	}
	if !validEffort(cc.Effort) {
		return cc, errors.New("CLAUDE_CODE_EFFORT must be low, medium, high, xhigh or max")
	}
	if v := os.Getenv("CLAUDE_CODE_MAX_TURNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || !validMaxTurns(n) {
			return cc, errors.New("CLAUDE_CODE_MAX_TURNS must be a number from 1 to 100")
		}
		cc.MaxTurns = n
	}
	if v := os.Getenv("CLAUDE_CODE_MAX_BUDGET_USD"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || !validBudget(f) {
			return cc, errors.New("CLAUDE_CODE_MAX_BUDGET_USD must be a positive number of dollars")
		}
		cc.MaxBudgetUSD = f
	}
	if v := os.Getenv("CLAUDE_CODE_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || !validTimeout(n) {
			return cc, errors.New("CLAUDE_CODE_TIMEOUT_SECONDS must be from 30 to 3600")
		}
		cc.Timeout = time.Duration(n) * time.Second
	}
	return cc, nil
}

// The limits below apply to the server's config and to what a workspace admin sets in Settings.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\[\]-]{0,99}$`)

func validModel(m string) bool   { return modelName.MatchString(m) }
func validMaxTurns(n int) bool   { return n >= 1 && n <= 100 }
func validBudget(f float64) bool { return f > 0 && f <= 1000 }
func validTimeout(n int) bool    { return n >= 30 && n <= 3600 }
func validEffort(e string) bool {
	switch e {
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Sign-in with OAuth for MCP servers (the MCP authorization flow): find the server's authorization
// server from its protected-resource metadata, register this app as a client automatically (or use a client
// an admin typed in), then authorization code with PKCE, and refresh tokens. The tokens belong to the
// connector, so the whole workspace uses the account the admin signed in with.

const (
	mcpOAuthCookie  = "aw_oauth_mcp"
	oauthCallback   = "/api/connectors/oauth/callback"
	maxOAuthDoc     = 256 << 10
	accessTokenSkew = 60 * time.Second
)

var errNeedsSignIn = errors.New("the sign-in has expired. An admin needs to sign in again in Settings")

// oauthMeta is what discovery and registration found. Nothing in it is secret.
type oauthMeta struct {
	Issuer     string `json:"issuer"`
	AuthURL    string `json:"authUrl"`
	TokenURL   string `json:"tokenUrl"`
	Resource   string `json:"resource,omitempty"`
	Scopes     string `json:"scopes,omitempty"`
	ClientID   string `json:"clientId"`
	AuthMethod string `json:"authMethod"` // none | client_secret_post | client_secret_basic
}

// oauthSecret is encrypted in the database.
type oauthSecret struct {
	ClientSecret string `json:"cs,omitempty"`
	Access       string `json:"a,omitempty"`
	Refresh      string `json:"r,omitempty"`
	Expiry       int64  `json:"e,omitempty"`
	Nonce        string `json:"n,omitempty"` // a sign-in in progress
	Verifier     string `json:"v,omitempty"`
	PendingUntil int64  `json:"p,omitempty"`
}

func (s *Server) openOAuth(c Connector, wsID string) (oauthSecret, error) {
	var sec oauthSecret
	if len(c.OAuthEnc) == 0 {
		return sec, nil
	}
	plain, err := s.sealer.Open(c.OAuthEnc, "mcpoauth|"+wsID+"|"+c.ID)
	if err != nil {
		return sec, errors.New("the stored sign-in cannot be read; sign in again")
	}
	return sec, json.Unmarshal([]byte(plain), &sec)
}

func (s *Server) sealOAuth(sec oauthSecret, wsID, id string) []byte {
	raw, _ := json.Marshal(sec)
	return s.sealer.Seal(string(raw), "mcpoauth|"+wsID+"|"+id)
}

func (r *Repo) SaveOAuth(ctx context.Context, wsID, id string, meta *oauthMeta, enc []byte) error {
	var m any
	if meta != nil {
		b, _ := json.Marshal(meta)
		m = string(b)
	}
	_, err := r.db.ExecContext(ctx, `UPDATE mcp_connectors SET oauth = $3, oauth_enc = $4 WHERE id = $1 AND workspace_id = $2`, id, wsID, m, enc)
	return err
}

// LockOAuth runs fn with the connector's sign-in row locked, so two requests never refresh a rotating
// refresh token at once. fn returns replacement ciphertext, or nil to keep what is stored.
func (r *Repo) LockOAuth(ctx context.Context, wsID, id string, fn func(c Connector) ([]byte, error)) (Connector, error) {
	var out Connector
	err := r.tx(ctx, func(tx *sql.Tx) error {
		c, err := scanConnector(tx.QueryRowContext(ctx, `SELECT `+connectorCols+` FROM mcp_connectors WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, id, wsID))
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		next, err := fn(c)
		if err != nil {
			return err
		}
		if next != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE mcp_connectors SET oauth_enc = $3 WHERE id = $1 AND workspace_id = $2`, id, wsID, next); err != nil {
				return err
			}
			c.OAuthEnc = next
		}
		out = c
		return nil
	})
	return out, err
}

// ---------- fetching what the server tells us ----------

// checkEndpoint accepts an address from a server's metadata: https (http only where private connectors are
// allowed), a host, no credentials.
func (s *Server) checkEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("the server gave an address that cannot be used")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && s.cfg.AllowPrivateConnectors) {
		return "", errors.New("the server gave an address that is not https")
	}
	return u.String(), nil
}

func (s *Server) fetchDoc(ctx context.Context, method, raw string, body io.Reader, hdr http.Header) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return nil, 0, errors.New("that address cannot be used")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AgentWorkflow/1 (+mcp-oauth)")
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := s.mcpGuard.client(15 * time.Second).Do(req)
	if err != nil {
		if strings.Contains(err.Error(), errBlockedAddress.Error()) {
			return nil, 0, errBlockedAddress
		}
		return nil, 0, errors.New("could not reach the sign-in service")
	}
	defer resp.Body.Close()
	raw2, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthDoc))
	if err != nil {
		return nil, resp.StatusCode, errors.New("the sign-in service's answer was cut off")
	}
	return raw2, resp.StatusCode, nil
}

var reResourceMeta = regexp.MustCompile(`resource_metadata="([^"]+)"`)

type discovered struct {
	Meta         oauthMeta
	Registration string
	Methods      []string
}

func trimSlash(s string) string { return strings.TrimRight(s, "/") }

// discoverOAuth finds how to sign in to an MCP server: protected-resource metadata (RFC 9728), then the
// authorization server's metadata (RFC 8414 or OpenID discovery).
func (s *Server) discoverOAuth(ctx context.Context, mcpURL string) (discovered, error) {
	var d discovered
	u, err := url.Parse(mcpURL)
	if err != nil {
		return d, errors.New("that is not a valid address")
	}
	origin := u.Scheme + "://" + u.Host

	// The server's own 401 may name where its metadata is.
	var cands []string
	if body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "agent-workflow", "version": "1"}}}); err == nil {
		if resp, err := s.probeUnauthenticated(ctx, mcpURL, body); err == nil && resp != "" {
			if m := reResourceMeta.FindStringSubmatch(resp); m != nil {
				if ep, err := s.checkEndpoint(m[1]); err == nil {
					cands = append(cands, ep)
				}
			}
		} else if errors.Is(err, errBlockedAddress) {
			return d, err
		}
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" {
		cands = append(cands, origin+"/.well-known/oauth-protected-resource"+p)
	}
	cands = append(cands, origin+"/.well-known/oauth-protected-resource")

	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	for _, c := range cands {
		raw, code, err := s.fetchDoc(ctx, http.MethodGet, c, nil, nil)
		if errors.Is(err, errBlockedAddress) {
			return d, err
		}
		if err == nil && code == 200 && json.Unmarshal(raw, &prm) == nil && len(prm.AuthorizationServers) > 0 {
			break
		}
		prm.AuthorizationServers = nil
	}
	as := origin // older servers act as their own authorization server
	if len(prm.AuthorizationServers) > 0 {
		as = prm.AuthorizationServers[0]
	}
	as, err = s.checkEndpoint(as)
	if err != nil {
		return d, err
	}
	asU, _ := url.Parse(as)
	asOrigin := asU.Scheme + "://" + asU.Host
	asPath := strings.TrimRight(asU.Path, "/")
	var meta struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
		AuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
	}
	found := false
	for _, c := range []string{
		asOrigin + "/.well-known/oauth-authorization-server" + asPath,
		asOrigin + "/.well-known/openid-configuration" + asPath,
		as + "/.well-known/openid-configuration",
	} {
		raw, code, err := s.fetchDoc(ctx, http.MethodGet, c, nil, nil)
		if errors.Is(err, errBlockedAddress) {
			return d, err
		}
		if err == nil && code == 200 && json.Unmarshal(raw, &meta) == nil && meta.AuthorizationEndpoint != "" && meta.TokenEndpoint != "" {
			found = true
			break
		}
	}
	if !found {
		return d, errors.New("this server does not offer OAuth sign-in. Use a token instead")
	}
	if trimSlash(meta.Issuer) != trimSlash(as) {
		return d, errors.New("the sign-in service's metadata does not match its address")
	}
	if len(meta.CodeChallengeMethods) > 0 {
		ok := false
		for _, m := range meta.CodeChallengeMethods {
			ok = ok || m == "S256"
		}
		if !ok {
			return d, errors.New("the sign-in service does not support the secure (S256) code exchange")
		}
	}
	d.Meta.Issuer = trimSlash(as)
	if d.Meta.AuthURL, err = s.checkEndpoint(meta.AuthorizationEndpoint); err != nil {
		return d, err
	}
	if d.Meta.TokenURL, err = s.checkEndpoint(meta.TokenEndpoint); err != nil {
		return d, err
	}
	if meta.RegistrationEndpoint != "" {
		if d.Registration, err = s.checkEndpoint(meta.RegistrationEndpoint); err != nil {
			return d, err
		}
	}
	d.Methods = meta.AuthMethods
	d.Meta.Resource = mcpURL
	if prm.Resource != "" {
		if ru, err := url.Parse(prm.Resource); err == nil && ru.Host == u.Host { // only a resource on the same host
			d.Meta.Resource = prm.Resource
		}
	}
	return d, nil
}

// probeUnauthenticated posts to the MCP server without credentials and returns its WWW-Authenticate header.
func (s *Server) probeUnauthenticated(ctx context.Context, mcpURL string, body []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := s.mcpGuard.client(15 * time.Second).Do(req)
	if err != nil {
		if strings.Contains(err.Error(), errBlockedAddress.Error()) {
			return "", errBlockedAddress
		}
		return "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.Header.Get("WWW-Authenticate"), nil
}

func (s *Server) callbackAddress() string { return s.cfg.PublicURL + oauthCallback }

// registerClient registers this app with the authorization server (RFC 7591).
func (s *Server) registerClient(ctx context.Context, d discovered) (id, secret, method string, err error) {
	if d.Registration == "" {
		return "", "", "", errors.New("this server does not allow automatic registration. Create an OAuth client with it and enter the client ID under Advanced")
	}
	method = "none"
	if len(d.Methods) > 0 {
		has := func(m string) bool {
			for _, x := range d.Methods {
				if x == m {
					return true
				}
			}
			return false
		}
		switch {
		case has("none"):
		case has("client_secret_post"):
			method = "client_secret_post"
		case has("client_secret_basic"):
			method = "client_secret_basic"
		}
	}
	body, _ := json.Marshal(map[string]any{
		"client_name": "Agent Workflow", "redirect_uris": []string{s.callbackAddress()},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": method, "application_type": "web",
	})
	raw, code, err := s.fetchDoc(ctx, http.MethodPost, d.Registration, bytes.NewReader(body), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		return "", "", "", err
	}
	if code/100 != 2 {
		return "", "", "", fmt.Errorf("the server refused to register this app (status %d)", code)
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthMethod   string `json:"token_endpoint_auth_method"`
	}
	if json.Unmarshal(raw, &out) != nil || out.ClientID == "" {
		return "", "", "", errors.New("the server's registration answer was not understood")
	}
	if out.AuthMethod != "" {
		method = out.AuthMethod
	}
	if out.ClientSecret == "" {
		method = "none"
	}
	return out.ClientID, out.ClientSecret, method, nil
}

// ---------- signing in ----------

func pkcePair() (verifier, challenge string) {
	verifier = randomToken() // 43 characters of base64url
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

var scopePattern = regexp.MustCompile(`^[A-Za-z0-9._:/ -]{0,300}$`)

// beginSignIn records a sign-in in progress and returns the address the admin's browser goes to.
func (s *Server) beginSignIn(ctx context.Context, wsID, userID string, c Connector) (authorizeURL, nonce string, err error) {
	if c.OAuth == nil {
		return "", "", errors.New("this connector does not use OAuth")
	}
	sec, err := s.openOAuth(c, wsID)
	if err != nil {
		return "", "", err
	}
	nonce = randomToken()
	verifier, challenge := pkcePair()
	sec.Nonce, sec.Verifier, sec.PendingUntil = nonce, verifier, time.Now().Add(stateLifetime).Unix()
	if err := s.repo.SaveOAuth(ctx, wsID, c.ID, c.OAuth, s.sealOAuth(sec, wsID, c.ID)); err != nil {
		return "", "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.OAuth.ClientID)
	q.Set("redirect_uri", s.callbackAddress())
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", s.signState("mcp:"+c.ID, wsID, userID, nonce, time.Now().Add(stateLifetime)))
	if c.OAuth.Resource != "" {
		q.Set("resource", c.OAuth.Resource)
	}
	if c.OAuth.Scopes != "" {
		q.Set("scope", c.OAuth.Scopes)
	}
	sep := "?"
	if strings.Contains(c.OAuth.AuthURL, "?") {
		sep = "&"
	}
	return c.OAuth.AuthURL + sep + q.Encode(), nonce, nil
}

func (s *Server) mcpCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: mcpOAuthCookie, Value: value, Path: "/api/connectors/oauth", MaxAge: maxAge, HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
}

type tokenReply struct {
	Access  string  `json:"access_token"`
	Refresh string  `json:"refresh_token"`
	Type    string  `json:"token_type"`
	Expires float64 `json:"expires_in"`
	Error   string  `json:"error"`
}

// tokenRequest posts to the token endpoint with the client's credentials.
func (s *Server) tokenRequest(ctx context.Context, meta *oauthMeta, clientSecret string, form url.Values) (tokenReply, error) {
	var tr tokenReply
	form.Set("client_id", meta.ClientID)
	if meta.Resource != "" {
		form.Set("resource", meta.Resource)
	}
	hdr := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	switch {
	case meta.AuthMethod == "client_secret_basic" && clientSecret != "":
		hdr.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(meta.ClientID)+":"+url.QueryEscape(clientSecret))))
		form.Del("client_id")
	case meta.AuthMethod == "client_secret_post" && clientSecret != "":
		form.Set("client_secret", clientSecret)
	}
	raw, code, err := s.fetchDoc(ctx, http.MethodPost, meta.TokenURL, strings.NewReader(form.Encode()), hdr)
	if err != nil {
		return tr, err
	}
	_ = json.Unmarshal(raw, &tr)
	if code/100 != 2 || tr.Access == "" {
		if tr.Error == "invalid_grant" {
			return tr, errNeedsSignIn
		}
		return tr, fmt.Errorf("the sign-in service did not accept the request (status %d)", code)
	}
	if tr.Type != "" && !strings.EqualFold(tr.Type, "bearer") {
		return tr, errors.New("the sign-in service issued a token of a type that is not supported")
	}
	return tr, nil
}

func (sec *oauthSecret) apply(tr tokenReply) {
	sec.Access = tr.Access
	if tr.Refresh != "" {
		sec.Refresh = tr.Refresh // some services keep the old one
	}
	sec.Expiry = 0
	if tr.Expires > 0 {
		sec.Expiry = time.Now().Unix() + int64(tr.Expires)
	}
}

// finishSignIn is where the authorization service sends the browser back: it swaps the code for tokens.
func (s *Server) mcpOAuthCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(reason, ws string) {
		dest := "/?connector=error&reason=" + reason
		if ws != "" {
			dest += "#/s/" + url.PathEscape(ws) + "/settings/integrations"
		}
		http.Redirect(w, r, dest, http.StatusSeeOther)
	}
	q := r.URL.Query()
	provider, ws, uid, nonce, err := s.verifyState(q.Get("state"))
	if err != nil || !strings.HasPrefix(provider, "mcp:") {
		if errors.Is(err, errExpiredState) {
			fail("expired", "")
		} else {
			fail("state", "")
		}
		return
	}
	id := strings.TrimPrefix(provider, "mcp:")
	ck, cerr := r.Cookie(mcpOAuthCookie)
	if cerr != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(nonce)) != 1 {
		fail("state", ws)
		return
	}
	http.SetCookie(w, s.mcpCookie("", -1))
	u, err := s.currentUser(r)
	if err != nil || u.ID != uid {
		fail("session", "")
		return
	}
	if role, err := s.repo.Role(r.Context(), ws, u.ID); err != nil || roleRank[role] < roleRank["admin"] {
		fail("forbidden", "")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		fail("denied", ws)
		return
	}
	code := q.Get("code")
	var tokensErr error
	conn, err := s.repo.LockOAuth(r.Context(), ws, id, func(c Connector) ([]byte, error) {
		sec, err := s.openOAuth(c, ws)
		if err != nil || c.OAuth == nil {
			return nil, errors.New("not ready")
		}
		if sec.Nonce == "" || subtle.ConstantTimeCompare([]byte(sec.Nonce), []byte(nonce)) != 1 || time.Now().Unix() > sec.PendingUntil {
			return nil, errBadState
		}
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.callbackAddress()}, "code_verifier": {sec.Verifier}}
		tr, err := s.tokenRequest(r.Context(), c.OAuth, sec.ClientSecret, form)
		if err != nil {
			tokensErr = err
			return nil, err
		}
		sec.apply(tr)
		sec.Nonce, sec.Verifier, sec.PendingUntil = "", "", 0
		return s.sealOAuth(sec, ws, c.ID), nil
	})
	if err != nil {
		s.log.Warn("mcp sign-in failed", "connector", id, "err", err, "tokens", tokensErr)
		if errors.Is(err, errBadState) {
			fail("state", ws)
		} else if errors.Is(err, errNotFound) {
			fail("failed", ws)
		} else {
			fail("exchange", ws)
		}
		return
	}
	// Read the server's tools with the new sign-in.
	if err := s.refreshTools(r.Context(), ws, conn); err != nil {
		s.log.Warn("mcp tools after sign-in", "connector", id, "err", err)
	}
	http.Redirect(w, r, "/?connector="+url.QueryEscape(conn.Name)+"&result=connected#/s/"+url.PathEscape(ws)+"/settings/integrations", http.StatusSeeOther)
}

// mcpAccessToken returns a usable access token, refreshing it first when it is about to expire (or when force
// is set because the server just refused it).
func (s *Server) mcpAccessToken(ctx context.Context, wsID string, c Connector, force bool) (string, error) {
	sec, err := s.openOAuth(c, wsID)
	if err != nil {
		return "", err
	}
	if sec.Access == "" {
		return "", errNeedsSignIn
	}
	if !force && (sec.Expiry == 0 || time.Now().Unix() < sec.Expiry-int64(accessTokenSkew.Seconds())) {
		return sec.Access, nil
	}
	if sec.Refresh == "" || c.OAuth == nil {
		if !force && sec.Expiry != 0 && time.Now().Unix() < sec.Expiry {
			return sec.Access, nil
		}
		return "", errNeedsSignIn
	}
	stale := sec.Access
	var token string
	_, err = s.repo.LockOAuth(ctx, wsID, c.ID, func(cur Connector) ([]byte, error) {
		now, err := s.openOAuth(cur, wsID)
		if err != nil {
			return nil, err
		}
		if now.Access != stale && now.Access != "" { // someone else already refreshed it
			token = now.Access
			return nil, nil
		}
		tr, err := s.tokenRequest(ctx, cur.OAuth, now.ClientSecret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {now.Refresh}})
		if err != nil {
			return nil, err
		}
		now.apply(tr)
		token = now.Access
		return s.sealOAuth(now, wsID, cur.ID), nil
	})
	if err != nil {
		s.log.Warn("mcp token refresh failed", "connector", c.Name, "err", err)
		return "", errNeedsSignIn
	}
	return token, nil
}

// refreshTools reads the server's tool list and stores it, switching off tools that changed.
func (s *Server) refreshTools(ctx context.Context, wsID string, conn Connector) error {
	cl, err := s.connectorClient(ctx, conn, wsID)
	if err != nil {
		return err
	}
	server, tools, err := s.probeConnector(ctx, cl)
	if err != nil {
		_ = s.repo.SaveConnectorTools(ctx, wsID, conn.ID, conn.ServerName, conn.Tools, conn.Enabled, cut(err.Error(), 300))
		return err
	}
	enabled := map[string]string{}
	for i := range tools {
		if h, was := conn.Enabled[tools[i].Name]; was {
			if h == tools[i].Hash && tools[i].ReadOnly {
				enabled[tools[i].Name] = h
			} else {
				tools[i].Changed = true
			}
		}
	}
	return s.repo.SaveConnectorTools(ctx, wsID, conn.ID, server, tools, enabled, "")
}

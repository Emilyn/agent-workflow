-- MCP connectors that sign in with OAuth: the endpoints and client found by discovery (not secret), and
-- one encrypted blob with the client secret, the tokens and the sign-in in progress.
ALTER TABLE mcp_connectors
    ADD COLUMN auth      text NOT NULL DEFAULT 'none' CHECK (auth IN ('none', 'token', 'oauth')),
    ADD COLUMN oauth     jsonb,
    ADD COLUMN oauth_enc bytea;
UPDATE mcp_connectors SET auth = 'token' WHERE secret_enc IS NOT NULL;

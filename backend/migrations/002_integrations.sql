-- OAuth tokens of connected tools, one per workspace and provider, encrypted like the API key.
CREATE TABLE integration_tokens (
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    provider     text NOT NULL CHECK (provider IN ('github')),
    token_enc    bytea NOT NULL,
    account      text NOT NULL,
    scopes       text NOT NULL DEFAULT '',
    connected_by text REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, provider)
);

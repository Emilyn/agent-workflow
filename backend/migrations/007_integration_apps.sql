-- A workspace's own OAuth app for a tool (client id, encrypted client secret, scopes), set by an admin in
-- Settings. A workspace without one uses the server's app from the environment, if there is one.
CREATE TABLE integration_apps (
    workspace_id  text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    provider      text NOT NULL,
    client_id     text NOT NULL,
    secret_enc    bytea NOT NULL,
    scopes        text NOT NULL DEFAULT '',
    updated_by    text,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, provider)
);

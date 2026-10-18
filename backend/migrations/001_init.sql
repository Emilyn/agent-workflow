CREATE TABLE users (
    id            text PRIMARY KEY,
    email         text NOT NULL,
    name          text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Only a hash of the session token is stored, so a database leak does not leak sessions.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE workspaces (
    id            text PRIMARY KEY,
    name          text NOT NULL,
    connected     jsonb NOT NULL DEFAULT '{}',
    git_provider  text NOT NULL DEFAULT 'github',
    custom_agents jsonb NOT NULL DEFAULT '[]',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id);

-- One-time join links. Only the token hash is stored.
CREATE TABLE invites (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    note         text NOT NULL DEFAULT '',
    role         text NOT NULL CHECK (role IN ('admin', 'member')),
    created_by   text REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX invites_workspace_idx ON invites (workspace_id);

-- The Anthropic API key of a workspace, encrypted with AES-256-GCM (see crypto.go).
CREATE TABLE workspace_secrets (
    workspace_id text PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    anthropic_key_enc bytea NOT NULL,
    key_hint     text NOT NULL,
    updated_by   text REFERENCES users (id) ON DELETE SET NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workflows (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    steps        jsonb NOT NULL,
    progress     integer NOT NULL DEFAULT 0,
    next_id      integer NOT NULL DEFAULT 1,
    outputs      jsonb NOT NULL DEFAULT '{}',
    version      integer NOT NULL DEFAULT 1,
    created_by   text REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workflows_workspace_idx ON workflows (workspace_id, updated_at DESC);

-- Chat is append-only so teammates never overwrite each other's messages.
CREATE TABLE messages (
    id          bigserial PRIMARY KEY,
    workflow_id text NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    user_id     text REFERENCES users (id) ON DELETE SET NULL,
    author      text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('human', 'agent')),
    text        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_workflow_idx ON messages (workflow_id, id);

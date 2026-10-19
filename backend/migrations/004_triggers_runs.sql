-- Event triggers (webhooks from GitHub and GitLab), the runs they start, and the review comments
-- an agent wants to post (held as drafts until a person approves, unless the trigger posts directly).
CREATE TABLE triggers (
    id          text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    workflow_id text NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    provider    text NOT NULL CHECK (provider IN ('github', 'gitlab')),
    project     text NOT NULL,
    secret_enc  bytea NOT NULL,
    review_mode text NOT NULL DEFAULT 'draft' CHECK (review_mode IN ('draft', 'post')),
    on_push     boolean NOT NULL DEFAULT false,
    enabled     boolean NOT NULL DEFAULT true,
    hook_id     text NOT NULL DEFAULT '',
    hook_status text NOT NULL DEFAULT 'manual',
    hook_error  text NOT NULL DEFAULT '',
    created_by  text REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX triggers_unique ON triggers (workspace_id, workflow_id, provider, lower(project));
CREATE INDEX triggers_workspace ON triggers (workspace_id);

CREATE TABLE runs (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    workflow_id  text NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    trigger_id   text REFERENCES triggers (id) ON DELETE SET NULL,
    source       text NOT NULL CHECK (source IN ('webhook', 'manual')),
    review_mode  text NOT NULL CHECK (review_mode IN ('draft', 'post')),
    status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    input        jsonb NOT NULL,
    steps        jsonb NOT NULL DEFAULT '[]',
    error        text NOT NULL DEFAULT '',
    dedupe_key   text,
    attempts     int NOT NULL DEFAULT 0,
    run_after    timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    started_by   text REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    finished_at  timestamptz
);
-- A delivery that GitHub or GitLab sends twice must not start two runs.
CREATE UNIQUE INDEX runs_dedupe ON runs (trigger_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX runs_queue ON runs (status, run_after);
CREATE INDEX runs_workflow ON runs (workflow_id, created_at DESC);

CREATE TABLE review_actions (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    run_id       text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    provider     text NOT NULL,
    project      text NOT NULL,
    number       int  NOT NULL,
    summary      text NOT NULL,
    comments     jsonb NOT NULL DEFAULT '[]',
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'posting', 'posted', 'discarded', 'failed')),
    result       text NOT NULL DEFAULT '',
    decided_by   text REFERENCES users (id) ON DELETE SET NULL,
    decided_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX review_actions_run ON review_actions (run_id);

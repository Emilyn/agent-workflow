-- Automatic starts: schedules, a secret incoming-webhook address per workflow, and the queue of
-- starts waiting for a workflow to be free (which also serves as the history of automatic runs).
CREATE TABLE schedules (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    workflow_id  text NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    freq         text NOT NULL CHECK (freq IN ('daily', 'weekly', 'monthly')),
    at_time      text NOT NULL,                -- 'HH:MM' in the schedule's timezone
    weekdays     integer NOT NULL DEFAULT 0,   -- weekly: bit 0 is Sunday, bit 6 is Saturday
    day_of_month integer NOT NULL DEFAULT 1,   -- monthly: 1 to 31; short months use their last day
    timezone     text NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    next_run_at  timestamptz,
    last_run_at  timestamptz,
    created_by   text REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due ON schedules (next_run_at) WHERE enabled;
CREATE INDEX schedules_workflow ON schedules (workflow_id);

CREATE TABLE workflow_hooks (
    workflow_id  text PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    token_enc    bytea NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workflow_starts (
    id            text PRIMARY KEY,
    workspace_id  text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    workflow_id   text NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    source        text NOT NULL CHECK (source IN ('schedule', 'webhook', 'manual')),
    source_ref    text NOT NULL DEFAULT '',
    input         text NOT NULL DEFAULT '',
    status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'waiting', 'done', 'failed', 'skipped')),
    error         text NOT NULL DEFAULT '',
    scheduled_for timestamptz,
    started_by    text REFERENCES users (id) ON DELETE SET NULL,
    attempts      integer NOT NULL DEFAULT 0,
    run_after     timestamptz NOT NULL DEFAULT now(),
    locked_until  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);
-- A schedule tick must never start two runs, even with several servers.
CREATE UNIQUE INDEX starts_tick ON workflow_starts (source_ref, scheduled_for) WHERE source = 'schedule';
CREATE INDEX starts_workflow ON workflow_starts (workflow_id, created_at DESC);
CREATE INDEX starts_active ON workflow_starts (status) WHERE status IN ('queued', 'running', 'waiting');

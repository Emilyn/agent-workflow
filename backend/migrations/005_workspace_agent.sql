-- How a workspace runs its agents: with an Anthropic API key (default), or with the Claude Code
-- installed on the server (when the operator has turned that on).
CREATE TABLE workspace_agent (
    workspace_id text PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    mode         text NOT NULL DEFAULT 'api' CHECK (mode IN ('api', 'claude_code')),
    updated_by   text,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

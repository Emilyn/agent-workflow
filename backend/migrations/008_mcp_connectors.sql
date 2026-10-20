-- Remote MCP servers an admin connected to a workspace. Agents get the tools an admin switched on
-- (only tools the server itself marks read-only can be switched on).
CREATE TABLE mcp_connectors (
    id           text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    url          text NOT NULL,
    header_name  text NOT NULL DEFAULT '',
    secret_enc   bytea,
    server_name  text NOT NULL DEFAULT '',
    tools        jsonb NOT NULL DEFAULT '[]',
    enabled      jsonb NOT NULL DEFAULT '{}',   -- tool name -> hash of its description and schema when it was switched on
    error        text NOT NULL DEFAULT '',
    refreshed_at timestamptz,
    created_by   text REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mcp_connectors_name ON mcp_connectors (workspace_id, lower(name));

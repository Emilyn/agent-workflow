-- Per-workspace limits for runs on the server's Claude Code. NULL means "use the server's default".
ALTER TABLE workspace_agent
    ADD COLUMN code_model           text,
    ADD COLUMN code_effort          text,
    ADD COLUMN code_max_turns       integer,
    ADD COLUMN code_max_budget_usd  double precision,
    ADD COLUMN code_timeout_seconds integer;

# Agent Workflow

Build workflows out of AI agents and people, and talk to the team (and the agents) while you build. Teams sign in, share workspaces, and every workspace has its own workflows, chat, agents and Claude API key.

- **Accounts and teams:** email and password sign-up, workspaces with owner / admin / member roles, one-time invite links.
- **Settings page:** Claude key, tool connections, review triggers and the team live on a page of their own (Settings in the top bar), so the side panel stays for chat, step settings and runs.
- **Multiple workflows** per workspace: create (blank, example or a copy), rename, duplicate, delete. The open one is in the URL.
- **Canvas:** each step is an agent, a person or a trigger. People steps are assigned to real teammates.
- **Create agents:** name, role, instructions, tools, model. Add one with a click, or `@mention` it in chat.
- **Run it:** each agent step is sent to Claude with the output of the step before it. People finish their own steps.
- **Team chat:** one thread per workflow, stored on the server. Teammates see new messages within a few seconds. Mentioning an agent adds your message to its instructions.
- **Claude per workspace:** an admin pastes an Anthropic API key. It is encrypted in the database and never sent back to the browser. Or, on a server you run, the workspace can use the Claude Code installed there instead.
- **GitHub, GitLab, Jira and Figma per workspace:** an admin signs in once (OAuth). Agents whose tools include one of them can then read from it while they run: repositories, issues, merge requests and files; Jira issues and projects; Figma frames, text and comments. Read only.
- **Code review on pull and merge requests:** a GitHub or GitLab webhook starts a workflow for every new pull or merge request; the agent reads the diff and writes a review that a teammate approves before it is posted (or posts it directly). See [Code review](#code-review-on-pull-and-merge-requests-github-and-gitlab).

## Stack

| Part | Tech |
| --- | --- |
| Frontend | React 18, Vite, plain CSS (`frontend/`) |
| Backend | Go 1.24, `net/http`, `database/sql` with `lib/pq` (`backend/`) |
| Database | PostgreSQL 16, SQL migrations embedded in the binary (`backend/migrations/`) |
| Model | Anthropic Messages API, called from the Go server with the workspace's key |
| Packaging | One Docker image (Go binary plus the built frontend), `docker-compose.yml`, GitHub Actions CI |

## Run it

### With Docker (everything, including Postgres)

```sh
cp .env.example .env
# put a random value in SECRET_KEY:  openssl rand -base64 32
docker compose up --build
```

Open http://localhost:8080 and create an account. The image was written but not built in the environment this was developed in (no Docker there); CI builds it on every push.

### For development

You need Go 1.24+, Node 20+ and a PostgreSQL 16 database.

```sh
# 1. Backend (terminal 1)
cd backend
export DATABASE_URL='postgres://user:pass@localhost:5432/agentworkflow?sslmode=disable'
export ALLOW_DEV_SECRET=true      # development only: uses a fixed, insecure encryption key
go run .                          # http://localhost:8080, runs the migrations on start

# 2. Frontend with hot reload (terminal 2)
cd frontend
npm install
npm run dev                       # http://localhost:5173, proxies /api to :8080
```

### Configuration (environment variables)

| Variable | Default | |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres connection string. |
| `SECRET_KEY` | required | 32 random bytes (base64 or hex) that encrypt stored API keys. `ALLOW_DEV_SECRET=true` substitutes a fixed key for local work only. |
| `COOKIE_SECURE` | `false` | Set `true` behind HTTPS so the session cookie is only sent securely. |
| `TRUST_PROXY` | `false` | Set `true` behind a reverse proxy that sets `X-Forwarded-For`, so rate limits use the real client address. |
| `ALLOW_SIGNUP` | `true` | `false` closes sign-up. |
| `ANTHROPIC_API_KEY` | unset | Optional key used by workspaces that haven't connected their own. Everyone on the server then shares it. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5-5` | Model for agent steps and chat. |
| `PUBLIC_URL` | `http://localhost:<PORT>` | The address people use in the browser. Providers send them back to `PUBLIC_URL/api/integrations/<provider>/callback`. |
| `GITHUB_`, `GITLAB_`, `JIRA_`, `FIGMA_` `CLIENT_ID` / `CLIENT_SECRET` | unset | An optional shared OAuth app for each service. Workspaces can instead add their own in Settings → Integrations; a service with neither shows "no OAuth app yet". |
| `…_SCOPES` | see below | What each OAuth app asks for. |
| `GITHUB_OAUTH_URL`, `GITHUB_API_URL` | `https://github.com`, `https://api.github.com` | Change for GitHub Enterprise Server. |
| `GITLAB_URL` | `https://gitlab.com` | Change for a self-managed GitLab. |
| `JIRA_AUTH_URL`, `JIRA_API_URL`, `FIGMA_AUTH_URL`, `FIGMA_API_URL` | Atlassian and Figma's public addresses | Rarely changed. |
| `PORT` | `8080` | |
| `DB_MAX_CONNS` | `20` | Connection pool size per instance. |
| `STATIC_DIR` | `../frontend/dist` | Built frontend to serve (`/app/public` in the image). |

`GET /api/health` says the process is up; `GET /api/ready` also checks the database. The server logs JSON, handles SIGTERM by finishing in-flight requests, and the image has a `healthcheck` subcommand.

## Run agents on your own Claude Code (optional)

By default agents run on an Anthropic API key that an admin adds under **Settings → Claude**. If you run this server yourself, you can instead let a workspace run its agents on the **Claude Code installed on that server**, with the login you already use there. No API key is needed for those workspaces.

1. Install Claude Code on the machine that runs the backend and sign in once: run `claude`, then `/login`.
2. Start the backend with `CLAUDE_CODE_ENABLED=true` (see the table below).
3. An admin opens **Settings → Claude** and picks **Claude Code on this machine**. Under it, the admin can change the model, effort, turns, spending cap and time limit for that workspace; empty boxes use the defaults from the table below. The card shows which account the agent runs as and the limits. If Claude Code is missing or signed out, it says so and the workspace stays on its API key.

How it works: each run starts `claude -p` in an empty temporary folder. Its own tools (files, shell, web) and your personal Claude Code settings are switched off. It is given only this app's tools (GitHub, GitLab, Jira, Figma and, for reviews, the single bound `post_review`), through a small MCP endpoint on this server at an unguessable, per-run address that stops working when the run ends. They run here with the same checks as before. `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` are removed from the environment Claude Code sees, so it uses the login. Background review runs may take up to `CLAUDE_CODE_TIMEOUT_SECONDS`; steps run from the browser stop after about 100 seconds.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CLAUDE_CODE_ENABLED` | `false` | Offer the mode to workspace admins. Server-only, for the same reason. |
| `CLAUDE_CODE_PATH` | `claude` | The Claude Code program (a name on `PATH`, or a full path). Server-only: not settable in the app, so no workspace admin can choose which program the server runs. |
| `CLAUDE_CODE_MODEL` | `ANTHROPIC_MODEL` | Model for these runs (an alias or a full name). |
| `CLAUDE_CODE_EFFORT` | Claude Code's default | `low`, `medium`, `high`, `xhigh` or `max`. |
| `CLAUDE_CODE_MAX_TURNS` | `15` | Model turns in one run. |
| `CLAUDE_CODE_MAX_BUDGET_USD` | `1` | Spending cap for one run. |
| `CLAUDE_CODE_TIMEOUT_SECONDS` | `300` | Longest a background run may take. |
| `CLAUDE_CODE_CALLBACK_URL` | `http://127.0.0.1:PORT` | Where Claude Code reaches this server's tool endpoint. |

Things to know:

- **Whose login:** every workspace on the server that picks this mode uses the same login, the one on the server. Use it for your own work on a machine you run. Do not use it to hand a personal plan to other people, and check your plan's terms before sharing it with a team. For a shared or hosted app, use API keys.
- **Docker:** the image does not contain Claude Code. Install it in your own image, or run the backend on the host, so that `claude` exists and is signed in where the server runs. A container needs the login (the `~/.claude` folder) mounted in.
- Runs are sequential processes (a few seconds of start-up each). Plan for a handful at a time, not hundreds.
- Cost and rate limits count against the signed-in plan, not against an API key.

## Connecting tools (GitHub, GitLab, Jira, Figma)

1. Create an OAuth app at the service and set its callback URL to `PUBLIC_URL/api/integrations/<provider>/callback` (`github`, `gitlab`, `jira` or `figma`).
2. Paste its client ID and secret (and, if you like, the scopes) under **Settings → Integrations** in the app. The secret is stored encrypted and never shown again. Each workspace can use its own OAuth app. Or, to share one app across every workspace on the server, put them in `…_CLIENT_ID` and `…_CLIENT_SECRET` and set `PUBLIC_URL`; a workspace's own app overrides it. Changing the client ID disconnects the current sign-in. The service addresses (`GITLAB_URL`, `GITHUB_API_URL` and so on) stay server-only.
3. An admin opens **Settings → Integrations** (the Settings button in the top bar), chooses **Connect …** and approves on the service's own page. The sign-in is encrypted like the Claude key (bound to the workspace) and never sent to the browser.
4. Give a step the tool: select the step, then **Step settings → Tools and sources**. When it runs, Claude can call that service's read-only tools, and the step's output lists which were used.

| Service | Default scopes | Where to create the app | Read-only tools |
| --- | --- | --- | --- |
| GitHub | `public_repo read:user write:repo_hook` (use `repo read:user write:repo_hook` for private repositories) | Settings → Developer settings → OAuth Apps | repo info, issues, pull requests (with changed file names), pull request diff, commits, file or directory contents |
| GitLab | `api read_user` (GitLab has no narrower scope that can post review comments) | User settings → Applications (or the group or admin area). Tick "Confidential" | project info, issues, merge requests (with changed file names), merge request diff, commits, file or directory contents |
| Jira | `read:jira-work read:jira-user offline_access` | developer.atlassian.com → OAuth 2.0 (3LO) app. Add the Jira API permissions | list projects, search issues with JQL, read one issue with comments |
| Figma | `current_user:read file_content:read file_comments:read` | figma.com/developers/apps | file pages and frames, text and layer outline of a frame, comments |

How it is kept safe:

- The sign-in `state` is signed, expires after 10 minutes, and is tied to the provider, the admin, the workspace and the browser (a cookie). The callback re-checks that the user is still an admin.
- GitLab, Jira and Figma tokens expire. They are refreshed automatically before use, under a database row lock, so two requests or two server instances never spend the same rotating refresh token twice. If a refresh fails, the tool tells the agent that an admin must reconnect.
- Tool inputs are validated and escaped (repository and project names, issue keys, file keys, node ids, paths and refs), so an agent cannot reach other endpoints. Results are truncated. Each run is limited to 8 model turns and 12 tool calls. The system prompt tells Claude that whatever the tools return is untrusted data, not instructions.
- Tokens carry only the scopes you configure. Anyone in the workspace who can run an agent can use them through that agent, so connect an account whose access you are happy to share with the team.
- Disconnecting deletes the token. GitHub and GitLab grants are also revoked at the service (Jira and Figma do not offer a revoke call for this flow, so remove the app's access in their settings if you need to).
- Jira connects the first site Atlassian lists for the account.

## Code review on pull and merge requests (GitHub and GitLab)

A workflow can start by itself for every new pull request (GitHub) or merge request (GitLab), read the changes, and write a review.

1. Connect the service under **Settings → Integrations** (see above) and set `PUBLIC_URL` to an address the service can reach. On a laptop, use a tunnel (for example `cloudflared tunnel` or `ngrok`) and set `PUBLIC_URL` to its address.
2. Create a workflow from the **Code review** template, or give any agent step the GitHub or GitLab tool.
3. Open **Settings → Review triggers** and **Add a trigger**: choose the workflow to start, the service, the project (`owner/repo` or `group/project`), and what happens with the review.
4. The app tries to register the webhook itself. If that is refused (for example, the token lacks rights), the trigger shows the payload URL and secret to add by hand: GitHub → Settings → Webhooks (JSON, "Pull requests" events); GitLab → Settings → Webhooks (secret token, "Merge request events").
5. **Review one request now** in the workflow's **Runs** tab starts a run on any pull or merge request without waiting for a webhook, which is the easiest way to test.

How a run works:

- A background worker picks up queued runs (so a restart or a slow model does not lose them), runs the agent steps in order, and skips steps assigned to a person. Failed runs are retried up to 3 times; a busy Claude is retried after 30 seconds.
- The agent reads the diff with the diff tool and finishes with the `post_review` tool: a summary plus up to 20 inline comments on changed lines. It cannot choose where to post: the tool is bound to the one pull or merge request that started the run, and works once per run.
- **Draft mode (default):** the review is saved and a teammate sees it in the workflow's Runs tab, then picks **Approve and post** or **Discard**. **Post mode** (admin choice per trigger) posts straight away. Manual runs always make drafts.
- GitHub gets one review with the inline comments. GitLab gets one discussion per inline comment plus a summary note. Comments that cannot be placed on the diff are folded into the summary instead of being lost.
- Reviews are posted as the account that connected the service, with a footer saying the review is automatic. Use a dedicated bot account for that.

Safety:

- The webhook address contains an unguessable id and every delivery is verified (GitHub: HMAC-SHA256 signature; GitLab: secret token, compared in constant time). The secret is stored encrypted and shown only to admins.
- Deliveries are de-duplicated per pull or merge request and commit, so a redelivery does not review twice. Drafts are skipped. At most 50 runs can be active per workspace.
- The pull request text and code are untrusted: the prompt says so, and the agent's only write is the single bound review.
- Large diffs are truncated (about 60,000 characters, 6,000 per file), and the agent is told when that happens.

## Roles

| | Member | Admin | Owner |
| --- | :-: | :-: | :-: |
| See and edit workflows, chat, run steps, create agents | yes | yes | yes |
| See runs, start a review on a pull or merge request, approve or discard a draft review | yes | yes | yes |
| See member names | yes | yes | yes |
| See member emails, create and revoke invite links for new members | | yes | yes |
| Connect or remove the Claude API key and the tool connections, add, change or remove review triggers (and see their webhook secret), rename the workspace | | yes | yes |
| Invite admins, make or change owners, delete the workspace | | | yes |

Anyone can leave a workspace. A workspace always keeps at least one owner. People who aren't in a workspace get a 404 for everything in it.

## Joining a team

An admin creates an invite link on the **Team** tab and sends it themselves. The link works once, expires after 7 days, and only a hash of it is stored. Whoever opens it signs in or creates an account and joins. The app does not send email.

## How your data is protected

- Passwords: PBKDF2-HMAC-SHA256 with 600,000 iterations and a random salt. Sign-in answers the same way for unknown emails and wrong passwords, and is rate limited.
- Sessions: a random token in an `HttpOnly`, `SameSite=Lax` cookie. Only its SHA-256 hash is stored, so a database leak doesn't leak sessions. Logging out deletes it on the server.
- API keys: AES-256-GCM with a random nonce, bound to the workspace id, using `SECRET_KEY`. Losing `SECRET_KEY` means people reconnect their keys; changing it needs a re-encryption step that isn't built.
- Workspace isolation: every query that touches workspace data filters on the workspace id. Tests try every route from an outsider's account.
- Cross-site writes are refused (`Origin` must match), JSON bodies are required, and responses carry a CSP and other security headers.
- Claude calls are limited to 60 a minute per workspace.

## API

All routes except health, `auth/*` and `invites/preview` need a signed-in session. `{ws}` is a workspace id.

| | |
| --- | --- |
| `POST /api/auth/signup`, `/login`, `/logout`; `GET /api/me` | Account and session. |
| `GET /api/invites/preview?token=`, `POST /api/invites/accept` | Look at, then use, an invite link. |
| `POST /api/workspaces`; `PATCH`, `DELETE /api/workspaces/{ws}` | Create, rename, delete. |
| `GET /api/workspaces/{ws}/members`; `PATCH`, `DELETE …/members/{userId}` | List, change role, remove or leave. |
| `GET`, `POST …/invites`; `DELETE …/invites/{id}` | Invite links. The token is returned once, on creation. |
| `GET …/claude`; `PUT`, `DELETE …/claude/key`; `PUT …/claude/mode` | Whether Claude is connected, the stored key, and whether the workspace runs on its key (`api`) or the server's Claude Code (`claude_code`). |
| `GET …/integrations`; `POST …/integrations/{provider}/start`; `DELETE …/integrations/{provider}` | Connection status of every tool, the sign-in address (admins), and disconnect (admins). `{provider}` is `github`, `gitlab`, `jira` or `figma`. |
| `GET /api/integrations/{provider}/callback` | Where the service sends the browser back. Redirects to the app with `?integration=…&result=connected` or `result=error&reason=…`. |
| `GET`, `POST …/triggers`; `PATCH`, `DELETE …/triggers/{id}` | Review triggers (admins create, change, delete; members see them without the secret). |
| `POST /api/hooks/{id}` | Public webhook receiver for GitHub and GitLab. Verified by signature or token. |
| `GET`, `POST …/workflows/{id}/runs`; `POST …/runs/{id}/rerun` | Run history, start a run on one pull or merge request, run again. |
| `POST …/review-actions/{id}/approve`, `…/discard` | Post or throw away a draft review (any member who can run agents). |
| `GET`, `PUT …/settings` | Custom agents and integration flags. |
| `GET`, `POST …/workflows`; `GET`, `PUT`, `DELETE …/workflows/{id}` | Workflows. `PUT` needs the `version` you loaded and returns 409 with the current copy if someone saved since. |
| `GET …/workflows/{id}/sync?after=&version=` | Polled by open screens: new messages, results, and the workflow if it changed. |
| `POST …/workflows/{id}/messages` | Add a chat message or a short note. The author comes from the session. |
| `POST …/workflows/{id}/chat` | Store the message and the reply from Claude or a mentioned agent. |
| `POST …/workflows/{id}/run/step`, `…/run/reset` | Run one agent step, or clear results. |

## Tests

```sh
cd backend
TEST_DATABASE_URL='postgres://postgres@localhost:5432/aw_test?sslmode=disable' go test -race ./...
```

The tests use a real Postgres (each test gets its own schema) and a fake Anthropic server. They cover the review triggers (webhook checks, runs, drafts, approval and posting for both services, retries and crash recovery), sign-up and sign-in, rate limits, cross-site requests, isolation between workspaces, roles, invites, key encryption, conflicting saves and chat. Without a database the database tests skip; set `REQUIRE_DB=1` to fail instead (CI does).

## Scaling notes

The server keeps no state in memory except rate-limit counters, so you can run several copies behind a load balancer against one Postgres. What to expect and change as you grow:

- **Rate limits are per instance.** With N instances the effective limit is N times higher. Move the counters to Postgres or Redis for exact global limits.
- **Teammates sync by polling** (every 4 seconds per open workflow, plus a membership refresh every 15). That is a few cheap indexed queries each. Replace it with Postgres `LISTEN/NOTIFY` and server-sent events or WebSockets for instant updates and lower load.
- **Chat and manual step runs call Claude inside the request** (up to 85 seconds). Review runs already use a durable queue: every instance runs 2 workers that claim runs with `FOR UPDATE SKIP LOCKED` and a 10-minute lease, so several instances share the work safely. Move chat runs to the queue too if they get long.
- **Workspace settings are saved as one document**, so two people creating agents at the same moment can overwrite each other. Give agents their own endpoints and rows when that matters.
- **Chat grows without bound.** Add retention or archiving.
- **Database:** back it up (Postgres `pg_dump` or your host's snapshots). Migrations run at startup under an advisory lock, so rolling deploys are safe.

## What is real and what is a placeholder

- **Real:** accounts, teams, roles, invites, Postgres storage, encrypted per-workspace API keys, workflows, running agent steps through Claude, shared chat, conflict detection on saves. Tested against a fake Anthropic server, plus a manual check that Anthropic rejects a bad key.
- **Real, GitHub, GitLab, Jira, Figma:** OAuth connect per workspace, encrypted tokens with automatic refresh, read-only tools that Claude calls during a step. Tested against fake services and a scripted fake Anthropic (including token expiry, rotation, concurrent refresh and input validation), plus a browser run of connecting all four and running a step. **Not tried against the real services** (no OAuth apps were available), so expect to fix small differences on the first real sign-in, especially Figma's scope names and Jira's search endpoint.
- **Real, review triggers:** webhooks, durable run queue and worker, diff reading, draft-then-approve reviews, and direct posting, for GitHub and GitLab. Tested against fake services and a scripted Claude, in Go and in the browser. **Not tried against real GitHub or GitLab**, so expect small differences (for example how a position is accepted for an inline comment).
- **Placeholder, other tools on an agent** (Web search, Docs, Sheets, Slack, Email): recorded but not called.
- **Not built for the connected tools:** writing other than the review (comments elsewhere, tickets, pull requests), triggers for Jira or Figma, per-repository or per-project limits, choosing among several Jira sites, one connection per user instead of per workspace.
- **Not built:** password reset, email verification and email delivery, sign-in with Google or SSO, two-factor, audit log, billing, Branch/Parallel/Wait steps, scheduled triggers (the example's "Every Monday" is a label).

## Why API keys, and what Claude Code mode changes

A Claude Pro or Max plan covers Claude.ai and is billed separately from the API. Anthropic's terms don't allow third-party apps to offer Claude.ai login or to use Free, Pro or Max credentials on behalf of users ([Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), [why the API is billed separately](https://support.claude.com/en/articles/9876003-i-have-a-paid-claude-subscription-pro-max-team-or-enterprise-plans-why-do-i-have-to-pay-separately-to-use-the-claude-api-and-console)). So the app only accepts API keys from the [Claude Console](https://platform.claude.com/), and refuses subscription tokens (`sk-ant-oat…`).

The optional Claude Code mode is a different arrangement: this app never touches a Claude login. It starts the Claude Code that the person running the server installed and signed in themselves, and only that person's own use is covered by their plan. If you host this for other people, use API keys.

## Next steps

1. Write actions with approval (comment on an issue, open a ticket) and more tools such as Slack.
2. Password reset and email verification (needs an email provider), then SSO.
3. Real-time updates, scheduled triggers, and webhook triggers for Jira and Figma.
4. Move the UI onto shadcn/ui. The init (`npx shadcn@latest init --preset b7PaZOyoUt --template vite`) needs `ui.shadcn.com` to be reachable.

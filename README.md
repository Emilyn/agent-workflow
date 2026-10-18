# Agent Workflow

Build workflows out of AI agents and people, and talk to the team (and the agents) while you build. Teams sign in, share workspaces, and every workspace has its own workflows, chat, agents and Claude API key.

- **Accounts and teams:** email and password sign-up, workspaces with owner / admin / member roles, one-time invite links.
- **Multiple workflows** per workspace: create (blank, example or a copy), rename, duplicate, delete. The open one is in the URL.
- **Canvas:** each step is an agent, a person or a trigger. People steps are assigned to real teammates.
- **Create agents:** name, role, instructions, tools, model. Add one with a click, or `@mention` it in chat.
- **Run it:** each agent step is sent to Claude with the output of the step before it. People finish their own steps.
- **Team chat:** one thread per workflow, stored on the server. Teammates see new messages within a few seconds. Mentioning an agent adds your message to its instructions.
- **Claude per workspace:** an admin pastes an Anthropic API key. It is encrypted in the database and never sent back to the browser.
- **GitHub per workspace:** an admin signs in with GitHub (OAuth). Agents whose tools include GitHub can then read repositories, issues, pull requests, commits and files while they run. Read only.

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
| `PUBLIC_URL` | `http://localhost:<PORT>` | The address people use in the browser. GitHub sends them back to `PUBLIC_URL/api/integrations/github/callback`. |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | unset | A GitHub OAuth app. Without them the GitHub card says it is not configured. |
| `GITHUB_SCOPES` | `public_repo read:user` | What the OAuth app asks for. Use `repo read:user` to let agents read private repositories. |
| `GITHUB_OAUTH_URL`, `GITHUB_API_URL` | `https://github.com`, `https://api.github.com` | Change for GitHub Enterprise Server. |
| `PORT` | `8080` | |
| `DB_MAX_CONNS` | `20` | Connection pool size per instance. |
| `STATIC_DIR` | `../frontend/dist` | Built frontend to serve (`/app/public` in the image). |

`GET /api/health` says the process is up; `GET /api/ready` also checks the database. The server logs JSON, handles SIGTERM by finishing in-flight requests, and the image has a `healthcheck` subcommand.

## Connecting GitHub

1. On GitHub: Settings, Developer settings, OAuth Apps, New OAuth App. Set the callback URL to `PUBLIC_URL/api/integrations/github/callback`.
2. Put the client id and secret in `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`, and set `PUBLIC_URL`.
3. An admin opens **Integrations**, chooses **Connect GitHub** and approves on GitHub. The access token is encrypted like the Claude key (bound to the workspace) and is never sent to the browser.
4. Give an agent the GitHub tool. When it runs, Claude can call seven read-only tools: repository info, issues, pull requests (with changed file names), commits and file contents. The step's output shows which tools were used.

How it is kept safe: the sign-in `state` is signed, expires after 10 minutes, and is tied to the admin, the workspace and the browser (a cookie); the callback re-checks that the user is still an admin. Tool inputs are validated (owner, repo, ref and path are checked and escaped, so an agent cannot reach other GitHub endpoints), results are truncated, each run is limited to 8 model turns and 12 tool calls, and the system prompt tells Claude that issue and file text is untrusted data, not instructions. The token has only the scopes you configured, and anyone in the workspace who can run an agent can use it through that agent, so connect an account whose access you are happy to share with the team. Disconnecting deletes the token and revokes the grant at GitHub.

## Roles

| | Member | Admin | Owner |
| --- | :-: | :-: | :-: |
| See and edit workflows, chat, run steps, create agents | yes | yes | yes |
| See member names | yes | yes | yes |
| See member emails, create and revoke invite links for new members | | yes | yes |
| Connect or remove the Claude API key and GitHub, change connected tools, rename the workspace | | yes | yes |
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
| `GET …/claude`; `PUT`, `DELETE …/claude/key` | Whether Claude is connected, and the stored key. |
| `GET …/integrations`; `POST …/integrations/github/start`; `DELETE …/integrations/github` | GitHub status, the GitHub sign-in address (admins), and disconnect (admins). |
| `GET /api/integrations/github/callback` | Where GitHub sends the browser back. Redirects to the app with `?github=connected` or `?github=error&reason=…`. |
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

The tests use a real Postgres (each test gets its own schema) and a fake Anthropic server. They cover sign-up and sign-in, rate limits, cross-site requests, isolation between workspaces, roles, invites, key encryption, conflicting saves and chat. Without a database the database tests skip; set `REQUIRE_DB=1` to fail instead (CI does).

## Scaling notes

The server keeps no state in memory except rate-limit counters, so you can run several copies behind a load balancer against one Postgres. What to expect and change as you grow:

- **Rate limits are per instance.** With N instances the effective limit is N times higher. Move the counters to Postgres or Redis for exact global limits.
- **Teammates sync by polling** (every 4 seconds per open workflow, plus a membership refresh every 15). That is a few cheap indexed queries each. Replace it with Postgres `LISTEN/NOTIFY` and server-sent events or WebSockets for instant updates and lower load.
- **Claude calls run inside the request** (up to 85 seconds). For long runs or many at once, move them to a background job queue and stream progress.
- **Workspace settings are saved as one document**, so two people creating agents at the same moment can overwrite each other. Give agents their own endpoints and rows when that matters.
- **Chat grows without bound.** Add retention or archiving.
- **Database:** back it up (Postgres `pg_dump` or your host's snapshots). Migrations run at startup under an advisory lock, so rolling deploys are safe.

## What is real and what is a placeholder

- **Real:** accounts, teams, roles, invites, Postgres storage, encrypted per-workspace API keys, workflows, running agent steps through Claude, shared chat, conflict detection on saves. Tested against a fake Anthropic server, plus a manual check that Anthropic rejects a bad key.
- **Real, GitHub:** OAuth connect per workspace, encrypted token, seven read-only tools that Claude calls during a step. Tested against a fake GitHub and a scripted fake Anthropic, plus a browser run of the whole flow. Not tried against github.com itself (no OAuth app was available), so check the first real sign-in.
- **Placeholder, GitLab, Jira, Figma:** "Connect" only toggles a flag. Nothing signs in and agents can't use them. They can follow the GitHub pattern (`github.go`, `integration_tokens`).
- **Placeholder, other tools on an agent** (Web search, Docs, Sheets, Slack, Email): recorded but not called.
- **Not built for GitHub:** writing (comments, PRs), GitHub Apps with per-repository installs, token refresh, one connection per user instead of per workspace.
- **Not built:** password reset, email verification and email delivery, sign-in with Google or SSO, two-factor, audit log, billing, Branch/Parallel/Wait steps, scheduled triggers (the example's "Every Monday" is a label).

## Why API keys and not "sign in with Claude Pro"

A Claude Pro or Max plan covers Claude.ai and is billed separately from the API. Anthropic's terms don't allow third-party apps to offer Claude.ai login or to use Free, Pro or Max credentials on behalf of users ([Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), [why the API is billed separately](https://support.claude.com/en/articles/9876003-i-have-a-paid-claude-subscription-pro-max-team-or-enterprise-plans-why-do-i-have-to-pay-separately-to-use-the-claude-api-and-console)). So the app only accepts API keys from the [Claude Console](https://platform.claude.com/), and refuses subscription tokens (`sk-ant-oat…`).

## Next steps

1. The same OAuth and tool pattern for GitLab, Jira and Figma; a step editor so tools can be chosen on an existing step.
2. Password reset and email verification (needs an email provider), then SSO.
3. Real-time updates, background runs, and scheduled triggers.
4. Move the UI onto shadcn/ui. The init (`npx shadcn@latest init --preset b7PaZOyoUt --template vite`) needs `ui.shadcn.com` to be reachable.

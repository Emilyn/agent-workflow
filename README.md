# Agent Workflow

Build workflows out of AI agents and people, and talk to the team (and the agents) while you build. Teams sign in, share workspaces, and every workspace has its own workflows, chat, agents and Claude API key.

- **Accounts and teams:** email and password sign-up, workspaces with owner / admin / member roles, one-time invite links.
- **Multiple workflows** per workspace: create (blank, example or a copy), rename, duplicate, delete. The open one is in the URL.
- **Canvas:** each step is an agent, a person or a trigger. People steps are assigned to real teammates.
- **Create agents:** name, role, instructions, tools, model. Add one with a click, or `@mention` it in chat.
- **Run it:** each agent step is sent to Claude with the output of the step before it. People finish their own steps.
- **Team chat:** one thread per workflow, stored on the server. Teammates see new messages within a few seconds. Mentioning an agent adds your message to its instructions.
- **Claude per workspace:** an admin pastes an Anthropic API key. It is encrypted in the database and never sent back to the browser.

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
| `PORT` | `8080` | |
| `DB_MAX_CONNS` | `20` | Connection pool size per instance. |
| `STATIC_DIR` | `../frontend/dist` | Built frontend to serve (`/app/public` in the image). |

`GET /api/health` says the process is up; `GET /api/ready` also checks the database. The server logs JSON, handles SIGTERM by finishing in-flight requests, and the image has a `healthcheck` subcommand.

## Roles

| | Member | Admin | Owner |
| --- | :-: | :-: | :-: |
| See and edit workflows, chat, run steps, create agents | yes | yes | yes |
| See member names | yes | yes | yes |
| See member emails, create and revoke invite links for new members | | yes | yes |
| Connect or remove the Claude API key, change connected tools, rename the workspace | | yes | yes |
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
- **Placeholder, Integrations:** "Connect" for GitHub, GitLab, Jira and Figma only toggles a flag. Nothing signs in to them and agents can't use them. Real connections need OAuth and per-workspace token storage (the encryption in `crypto.go` is built for that).
- **Placeholder, tools on an agent:** recorded but not called. Agents work from the text they are given.
- **Not built:** password reset, email verification and email delivery, sign-in with Google or SSO, two-factor, audit log, billing, Branch/Parallel/Wait steps, scheduled triggers (the example's "Every Monday" is a label).

## Why API keys and not "sign in with Claude Pro"

A Claude Pro or Max plan covers Claude.ai and is billed separately from the API. Anthropic's terms don't allow third-party apps to offer Claude.ai login or to use Free, Pro or Max credentials on behalf of users ([Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), [why the API is billed separately](https://support.claude.com/en/articles/9876003-i-have-a-paid-claude-subscription-pro-max-team-or-enterprise-plans-why-do-i-have-to-pay-separately-to-use-the-claude-api-and-console)). So the app only accepts API keys from the [Claude Console](https://platform.claude.com/), and refuses subscription tokens (`sk-ant-oat…`).

## Next steps

1. OAuth for GitHub or GitLab, Jira and Figma, plus tool calling so agents can use them.
2. Password reset and email verification (needs an email provider), then SSO.
3. Real-time updates, background runs, and scheduled triggers.
4. Move the UI onto shadcn/ui. The init (`npx shadcn@latest init --preset b7PaZOyoUt --template vite`) needs `ui.shadcn.com` to be reachable.

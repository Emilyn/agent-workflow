# Agent Workflow

Build workflows out of AI agents and people, and talk to the team (and the agents) while you build.

- **Canvas:** a step-by-step flow where each step is an agent, a person or a trigger. Select a step to edit its instructions or assignee, or remove it.
- **Create agents:** name, one-line role, instructions, tools and a model. Add one to the workflow with a click, or mention it in chat with `@Name`.
- **People steps:** review, approval, ask for input and hand-off, assignable to a teammate.
- **Run it:** each agent step is sent to Claude with the output of the step before it. People finish their own steps.
- **Team chat:** a shared thread. A message that mentions an agent is added to that agent's instructions, so the next run follows it.
- **Integrations:** connect cards for GitHub or GitLab, Jira and Figma (placeholders, see below).

## Stack

| Part | Tech |
| --- | --- |
| Frontend | React 18, Vite, plain CSS (`frontend/`) |
| Backend | Go 1.22, standard library only (`backend/`) |
| Storage | One JSON file written by the Go server (`backend/data/state.json`) |
| Model | Anthropic Messages API, called from the Go server |

## Run it

You need Go 1.22+ and Node 20+.

```sh
# 1. Backend (terminal 1)
cd backend
export ANTHROPIC_API_KEY=sk-ant-...   # optional, see below
go run .                              # http://localhost:8080

# 2. Frontend with hot reload (terminal 2)
cd frontend
npm install
npm run dev                           # http://localhost:5173, proxies /api to :8080
```

Production style, one process: `cd frontend && npm install && npm run build`, then `cd ../backend && go run .` and open http://localhost:8080. The Go server serves the built frontend.

### Connecting Claude

Open **Integrations** and paste an Anthropic API key from the [Claude Console](https://platform.claude.com/). The server checks it with Anthropic, stores it, and agent steps and chat replies then run on Claude, billed to that key's owner. Until a key is connected everything is clearly marked simulated.

**Why not "sign in with Claude Pro"?** A Claude Pro or Max plan covers Claude.ai and is billed separately from the API. Anthropic's terms also don't allow third-party apps to offer Claude.ai login or to use Free, Pro or Max credentials on behalf of users, or to collect or store Claude.ai credentials ([Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), [why the API is billed separately](https://support.claude.com/en/articles/9876003-i-have-a-paid-claude-subscription-pro-max-team-or-enterprise-plans-why-do-i-have-to-pay-separately-to-use-the-claude-api-and-console)). So the app only accepts API keys, and it refuses subscription tokens (`sk-ant-oat…`).

How the key is handled:

- It goes from the browser to your Go server only, and is never sent back. The app shows just the last four characters.
- It is saved in `data/credentials.json` (mode `0600`) as plain text, so keep that folder private. A key set through `ANTHROPIC_API_KEY` is used instead and can't be changed in the app.
- Use HTTPS if the server is not on `localhost`, so the key isn't sent in clear text.
- There is one key per server, shared by everyone who opens it. Per-user keys need user accounts first.

### Configuration (environment variables, backend)

| Variable | Default | |
| --- | --- | --- |
| `ANTHROPIC_API_KEY` | unset | Optional. Sets the key at startup instead of connecting one in the app. |
| `CREDENTIALS_FILE` | `data/credentials.json` | Where a key connected in the app is stored. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5-5` | Model used for every agent step and chat reply. |
| `PORT` | `8080` | |
| `DATA_FILE` | `data/state.json` | Where the workflow is stored. |
| `STATIC_DIR` | `../frontend/dist` | Built frontend to serve. |

## API

| | |
| --- | --- |
| `GET /api/state`, `PUT /api/state` | Load and save the workflow. Step outputs belong to the server and are ignored on save. |
| `POST /api/run/step` `{stepId}` | Run one agent step. Its prompt is the step's instructions plus the nearest earlier outputs. |
| `POST /api/run/reset` | Clear outputs and start the run over. |
| `POST /api/chat` `{agentName, instruction, message}` | Reply as the mentioned agent, or as Claude. |
| `GET /api/claude` | Whether Claude is connected, where the key came from and its last four characters. |
| `PUT /api/claude/key` `{apiKey}` | Check a key with Anthropic and store it. Rejects bad formats and subscription tokens. |
| `DELETE /api/claude/key` | Disconnect and delete the stored key. |
| `GET /api/health` | Liveness and whether a key is configured. |

## Tests

```sh
cd backend && go test ./...
```

The Go tests cover the API and run steps against a fake Anthropic server, including error handling and that earlier outputs reach the prompt.

## What is real and what is a placeholder

- **Real:** the Go API, saving, connecting an API key, running agent steps through Claude, agent chat replies, and chat notes changing agent instructions. Tested against a fake Anthropic server, not a live key.
- **Placeholder, Integrations:** "Connect" only toggles a flag. Nothing signs in to GitHub, GitLab, Jira or Figma, and agents cannot use those tools. Real connections need OAuth and a secure place on the server to keep tokens.
- **Placeholder, tools on an agent:** they are recorded but not called. Agents work from the text they are given.
- **Not built yet:** Branch, Parallel and Wait steps; sign-in and per-user workspaces; live sync between teammates (today there is one shared workflow per server).

## Next steps

1. OAuth for GitHub or GitLab, Jira and Figma, plus tool calling so agents can use them.
2. User accounts, a database instead of one JSON file, and live updates between teammates.
3. Branching and parallel steps.
4. Move the UI onto shadcn/ui. The init (`yarn dlx shadcn@latest init --preset b7PaZOyoUt --template vite`) needs `ui.shadcn.com` to be reachable, and `yarn dlx` needs Yarn 2 or later.

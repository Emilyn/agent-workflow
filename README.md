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

### Configuration (environment variables, backend)

| Variable | Default | |
| --- | --- | --- |
| `ANTHROPIC_API_KEY` | unset | Without it, agent steps and chat replies are clearly marked simulated. |
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
| `GET /api/health` | Whether a Claude key is configured. |

## Tests

```sh
cd backend && go test ./...
```

The Go tests cover the API and run steps against a fake Anthropic server, including error handling and that earlier outputs reach the prompt.

## What is real and what is a placeholder

- **Real:** the Go API, saving, running agent steps through Claude (with a key), agent chat replies, and chat notes changing agent instructions.
- **Placeholder, Integrations:** "Connect" only toggles a flag. Nothing signs in to GitHub, GitLab, Jira or Figma, and agents cannot use those tools. Real connections need OAuth and a secure place on the server to keep tokens.
- **Placeholder, tools on an agent:** they are recorded but not called. Agents work from the text they are given.
- **Not built yet:** Branch, Parallel and Wait steps; sign-in and per-user workspaces; live sync between teammates (today there is one shared workflow per server).

## Next steps

1. OAuth for GitHub or GitLab, Jira and Figma, plus tool calling so agents can use them.
2. User accounts, a database instead of one JSON file, and live updates between teammates.
3. Branching and parallel steps.
4. Move the UI onto shadcn/ui. The init (`yarn dlx shadcn@latest init --preset b7PaZOyoUt --template vite`) needs `ui.shadcn.com` to be reachable, and `yarn dlx` needs Yarn 2 or later.

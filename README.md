# Agent Workflow

A browser app for building workflows out of AI agents and people, and talking to the team (and the agents) while you build.

- **Canvas:** a step-by-step flow where each step is an agent, a person, or a trigger. Select a step to edit its instructions, assignee or tools, or remove it.
- **Create agents:** give an agent a name, a one-line role, instructions, tools and a model. Add it to the workflow with one click, or mention it in chat with `@Name`.
- **People steps:** review, approval, ask for input and hand-off, assignable to a teammate.
- **Team chat:** a shared thread with @mentions for people and agents.
- **Integrations:** connect cards for GitHub or GitLab, Jira and Figma.

## Run it

No build step.

```sh
python3 -m http.server 8000
# then open http://localhost:8000
```

Or open `index.html` directly in a browser.

## What is real and what is a placeholder

This is a front-end prototype. Everything is stored in your browser (`localStorage`), so workflows and agents you create stay on your device.

- **Integrations are simulated.** "Connect" toggles a state; it does not sign in to GitHub, GitLab, Jira or Figma. Real connections need OAuth and a backend that holds the tokens.
- **Runs are simulated.** "Advance run" moves through the step statuses. Nothing calls a model yet.
- **Agent replies in chat are canned text**, not generated.
- **Logic steps** (Branch, Parallel, Wait) are placeholders.

## Next steps

1. A small backend to run each agent step against the Claude API, passing the previous step's output.
2. OAuth for the integrations, with per-agent permission scopes.
3. Shared workflows and chat between teammates (a database plus realtime updates).
4. Branch and parallel steps.

## Files

- `index.html`: page shell
- `styles.css`: styles
- `app.js`: state, rendering and behaviour

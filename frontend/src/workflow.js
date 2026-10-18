// Pure helpers for reading and changing the workflow document.

// People get a stable colour and initials from their name.
const SWATCHES = [
  { bg: '#EBD9CE', fg: '#5A2E18' },
  { bg: '#D5E3E0', fg: '#1C4A45' },
  { bg: '#E4E1F2', fg: '#2F2F66' },
  { bg: '#F3E6B8', fg: '#5E4A00' },
  { bg: '#DDE8D2', fg: '#2F5018' },
  { bg: '#F0D5DD', fg: '#6A2038' },
];
export function personStyle(name) {
  const n = (name || '?').trim();
  let h = 0;
  for (const ch of n) h = (h * 31 + ch.codePointAt(0)) >>> 0;
  const words = n.split(/\s+/).filter(Boolean);
  const initials = ((words[0]?.[0] || '?') + (words.length > 1 ? words[words.length - 1][0] : words[0]?.[1] || '')).toUpperCase();
  return { initials, ...SWATCHES[h % SWATCHES.length] };
}

export const ROLE_RANK = { member: 1, admin: 2, owner: 3 };
export const ROLE_LABEL = { owner: 'Owner', admin: 'Admin', member: 'Member' };

export const LIBRARY = [
  { id: 'research', name: 'Research agent', role: 'Searches the web and your files', instruction: 'Search the sources you are given, collect the relevant facts with links, and flag anything you could not verify.', tools: ['Web search', 'Docs'], model: 'Claude Sonnet' },
  { id: 'writer', name: 'Writer agent', role: 'Drafts and rewrites', instruction: 'Draft clear, concise copy in the team’s voice from the material provided. Keep to the length and structure requested.', tools: ['Docs'], model: 'Claude Sonnet' },
  { id: 'analyst', name: 'Analyst agent', role: 'Works through data', instruction: 'Look for patterns and changes in the data you receive, compare with earlier periods, and explain what matters.', tools: ['Sheets'], model: 'Claude Sonnet' },
  { id: 'reviewer', name: 'Reviewer agent', role: 'Checks work against criteria', instruction: 'Check the previous step’s output against the criteria you are given and list what needs fixing before it moves on.', tools: ['Docs'], model: 'Claude Sonnet' },
];

export const PERSON_STEPS = [
  { key: 'review', name: 'Review and edit', hint: 'A person improves the output', desc: 'Check the work, fix wording, confirm the details.', instruction: 'Read the previous output, edit anything unclear and confirm it is ready to move on.' },
  { key: 'approval', name: 'Approval', hint: 'A yes or no gate', desc: 'A yes or no before anything is shared.', instruction: 'Approve the work, or send it back with a note for the agent before it.' },
  { key: 'input', name: 'Ask for input', hint: 'A question or short form', desc: 'Pause the run and ask for a decision or missing detail.', instruction: 'Answer the question the workflow needs before it can continue.' },
  { key: 'handoff', name: 'Hand off', hint: 'Assign to a teammate', desc: 'Hand the work to a teammate to finish.', instruction: 'Take over from here and finish the remaining work.' },
];

export const INTEGRATIONS = {
  git: { desc: 'Let agents read repositories, open pull requests and respond to reviews. Pick GitHub or GitLab.', can: ['Read code and diffs', 'Open pull requests', 'Comment on reviews'], note: 'read code and open pull requests', icon: 'git' },
  jira: { name: 'Jira', desc: 'Let agents read issues and sprints, and move tickets forward when a step finishes.', can: ['Read issues and sprints', 'Create and update tickets', 'Comment on tickets'], note: 'read and update tickets', icon: 'jira' },
  figma: { name: 'Figma', desc: 'Let agents read frames and comments, and attach designs to a step.', can: ['Read files and frames', 'Read comments', 'Export images'], note: 'read frames and comments', icon: 'figma' },
};

export const KIND_LABEL = { trigger: 'Trigger', agent: 'Agent step', human: 'Person step' };

export const blankForm = () => ({ name: '', role: '', instructions: '', model: 'Claude Sonnet', tools: {} });

export const gitName = (doc) => (doc.gitProvider === 'gitlab' ? 'GitLab' : 'GitHub');
export const integrationName = (doc, key) => (key === 'git' ? gitName(doc) : INTEGRATIONS[key].name);
export const allAgents = (doc) => [...LIBRARY, ...doc.customAgents];
export const fullName = (name) => {
  const n = (name || '').trim();
  return /agent$/i.test(n) ? n : `${n} agent`;
};
export const clock = (iso) => (iso ? new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '');

// Adds messages that are not there yet (by id), oldest first.
export function mergeMessages(have, incoming) {
  const seen = new Set(have.map((m) => m.id));
  const fresh = incoming.filter((m) => !seen.has(m.id));
  if (!fresh.length) return have;
  return [...have, ...fresh].sort((a, b) => a.id - b.id).slice(-200);
}

export const rowOf = (steps, id) => steps.findIndex((g) => g.some((n) => n.id === id));
export const findNode = (steps, id) => {
  for (const g of steps) for (const n of g) if (n.id === id) return n;
  return null;
};

export function statusFor(progress, row, node) {
  if (row < progress) return 'done';
  if (row === progress) return node.kind === 'human' ? 'waiting' : 'running';
  return 'queued';
}

export function runSummary(doc) {
  const total = doc.steps.length;
  if (doc.progress >= total) return { done: true, waiting: null, total };
  const human = doc.steps[doc.progress].find((n) => n.kind === 'human');
  return { done: false, waiting: human ? human.assignee || 'an assignee' : null, total };
}

// Inserts a one-step row after the selected step, or after the current step if that is later.
export function insertStep(doc, selectedId, node) {
  const sel = rowOf(doc.steps, selectedId);
  const idx = Math.min(doc.steps.length, Math.max(sel, doc.progress) + 1);
  const steps = [...doc.steps];
  steps.splice(idx, 0, [node]);
  return { doc: { ...doc, steps, nextId: doc.nextId + 1 }, idx };
}

export function removeStep(doc, id) {
  let progress = doc.progress;
  const steps = [];
  doc.steps.forEach((group, i) => {
    const keep = group.filter((n) => n.id !== id);
    if (keep.length) steps.push(keep);
    else if (i < progress) progress -= 1;
  });
  const outputs = { ...doc.outputs };
  delete outputs[id];
  return { ...doc, steps, progress: Math.min(progress, steps.length), outputs };
}

export function patchNode(doc, id, patch) {
  return { ...doc, steps: doc.steps.map((g) => g.map((n) => (n.id === id ? { ...n, ...patch } : n))) };
}

export function agentNode(doc, agent) {
  return {
    id: `u${doc.nextId}`,
    agentId: agent.id,
    kind: 'agent',
    title: agent.name,
    desc: agent.role || agent.instruction,
    who: agent.model,
    instruction: agent.instruction,
    tools: [...agent.tools],
  };
}

export function personNode(doc, step, assignee) {
  return {
    id: `u${doc.nextId}`,
    kind: 'human',
    title: step.name,
    desc: step.desc,
    who: assignee,
    assignee,
    instruction: step.instruction,
    tools: [],
  };
}

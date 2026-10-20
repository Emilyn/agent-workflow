import { useMemo, useState } from 'react';

const FILTERS = [
  ['all', 'All'],
  ['active', 'In progress'],
  ['done', 'Done'],
  ['new', 'Not run yet'],
];

export const stateOf = (w) => (w.steps && w.progress >= w.steps ? 'done' : w.progress > 0 ? 'active' : 'new');
const STATE_LABEL = (w) => (w.waitingOn ? `Waiting on ${w.waitingOn}` : { done: 'Done', active: `Step ${w.progress + 1} of ${w.steps}`, new: 'Not run yet' }[stateOf(w)]);
const pillOf = (w) => (w.waitingOn ? 'waiting' : { done: 'done', active: 'running', new: 'queued' }[stateOf(w)]);

export function ago(iso) {
  if (!iso) return '';
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  if (s < 86400 * 7) return `${Math.floor(s / 86400)} d ago`;
  return new Date(iso).toLocaleDateString([], { day: 'numeric', month: 'short' });
}

const key = (ws) => `aw:checklist:${ws}`;
const dismissed = (ws) => { try { return window.localStorage.getItem(key(ws)) === '1'; } catch { return false; } };

// The page you land on: every workflow in the workspace, and, until the basics are set up, a short checklist.
export default function HomePage({ ws, list, claude, ints, members, canManage, onNew, onOpen, onGo, onInvite }) {
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState('all');
  const [hidden, setHidden] = useState(() => dismissed(ws.id));

  const counts = useMemo(() => {
    const c = { all: list.length, active: 0, done: 0, new: 0 };
    list.forEach((w) => { c[stateOf(w)] += 1; });
    return c;
  }, [list]);
  const shown = list
    .filter((w) => (filter === 'all' || stateOf(w) === filter) && w.name.toLowerCase().includes(q.trim().toLowerCase()))
    .sort((a, b) => (b.updated || '').localeCompare(a.updated || ''));

  const toolConnected = ints && Object.values(ints).some((s) => s.connected);
  const tasks = [
    { id: 'claude', title: 'Connect Claude', desc: 'Agents need Claude to run for real. Until then their steps are simulated.', done: !!claude?.connected, cta: 'Connect Claude', go: () => onGo('claude'), admin: true },
    { id: 'tool', title: 'Connect a tool', desc: 'Let agents read from GitHub, GitLab, Jira or Figma. Then sign in once.', done: !!toolConnected, cta: 'Connect a tool', go: () => onGo('integrations'), admin: true },
    { id: 'flow', title: 'Build and run a workflow', desc: 'Start from a template, then run it. Agents do some steps and people do others.', done: list.some((w) => w.progress > 0), cta: 'New workflow', go: onNew },
    { id: 'team', title: 'Invite a teammate', desc: 'People can approve steps, edit output and chat on a workflow.', done: members.length > 1, cta: 'Invite', go: onInvite },
  ].filter((t) => canManage || !t.admin);
  const left = tasks.filter((t) => !t.done).length;
  const next = tasks.find((t) => !t.done);
  const showList = !hidden && left > 0;
  const dismiss = () => {
    setHidden(true);
    try { window.localStorage.setItem(key(ws.id), '1'); } catch { /* the choice just is not remembered */ }
  };

  return (
    <main className="page" aria-labelledby="home-title">
      <div className="page-head">
        <div className="col g6">
          <h1 id="home-title" className="serif" style={{ fontSize: 40, margin: 0 }}>Workflows</h1>
          <span className="muted small">Agents and people working through the same steps. {list.length} in {ws.name}.</span>
        </div>
        <button type="button" className="btn primary" onClick={onNew}>+ New workflow</button>
      </div>

      {showList && (
        <section className="checklist" aria-labelledby="cl-title">
          <div className="row g12" style={{ justifyContent: 'space-between', flexWrap: 'wrap' }}>
            <div className="col g6">
              <h2 id="cl-title" style={{ margin: 0, fontSize: 18 }}>Get set up</h2>
              <span className="small muted">{tasks.length - left} of {tasks.length} done</span>
            </div>
            <button type="button" className="link-btn" onClick={dismiss}>Hide</button>
          </div>
          <div className="progress" role="progressbar" aria-valuemin={0} aria-valuemax={tasks.length} aria-valuenow={tasks.length - left} aria-label="Setup progress"><div style={{ width: `${((tasks.length - left) / tasks.length) * 100}%` }} /></div>
          <ol className="tasks">
            {tasks.map((t, i) => (
              <li key={t.id} className={`task${t.done ? ' done' : t === next ? ' now' : ''}`}>
                <span className="task-mark" aria-hidden="true">{t.done ? '✓' : i + 1}</span>
                <span className="col g6" style={{ flex: 1, minWidth: 0 }}>
                  <strong>{t.title}</strong>
                  <span className="small" style={{ color: 'var(--ink-2)' }}>{t.desc}</span>
                </span>
                {t.done ? <span className="ok">Done</span> : <button type="button" className={`btn${t === next ? ' outline' : ''}`} onClick={t.go}>{t.cta}</button>}
              </li>
            ))}
          </ol>
        </section>
      )}

      <div className="row g8 wrap">
        <label htmlFor="wf-search" className="sr-only">Search workflows</label>
        <input id="wf-search" className="field" style={{ width: 280 }} type="search" placeholder="Search workflows" value={q} onChange={(e) => setQ(e.target.value)} />
        <div className="row g8 wrap" role="group" aria-label="Filter workflows">
          {FILTERS.map(([id, label]) => (
            <button key={id} type="button" className={`filter${filter === id ? ' on' : ''}`} aria-pressed={filter === id} onClick={() => setFilter(id)}>{label} {counts[id]}</button>
          ))}
        </div>
      </div>

      {shown.length === 0 ? (
        <div className="empty">
          <span>{list.length === 0 ? 'No workflows yet.' : 'No workflow matches.'}</span>
          {list.length === 0 && <button type="button" className="btn primary" onClick={onNew}>Create the first one</button>}
        </div>
      ) : (
        <ul className="wf-table">
          <li className="wf-row head" aria-hidden="true"><span>Workflow</span><span>Status</span><span>Who</span><span>Updated</span></li>
          {shown.map((w) => (
            <li key={w.id}>
              <button type="button" className="wf-row" onClick={() => onOpen(w.id)}>
                <span className="col g6" style={{ minWidth: 0 }}>
                  <strong className="wf-row-name">{w.name}</strong>
                  <span className="small muted">{w.steps} steps</span>
                </span>
                <span><span className={`pill ${pillOf(w)}`}>{STATE_LABEL(w)}</span></span>
                <span className="small">{w.agents} agent{w.agents === 1 ? '' : 's'} · {w.people} {w.people === 1 ? 'person' : 'people'}</span>
                <span className="small muted">{ago(w.updated)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}

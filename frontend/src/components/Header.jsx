import { Play } from './Icons.jsx';
import WorkflowMenu from './WorkflowMenu.jsx';
import { personStyle } from '../workflow.js';

const SAVE_LABEL = { saved: 'Saved', saving: 'Saving…', error: 'Not saved' };
const MAX_AVATARS = 4;

// The bar above one workflow: where you are, who is here, and the Run button.
export default function Header({ current, workflowMenu, members, saveState, runLabel, busy, onHome, onAdvance, onShare }) {
  const shown = members.slice(0, MAX_AVATARS);
  const extra = members.length - shown.length;
  return (
    <header className="topbar">
      <div className="col" style={{ minWidth: 0, flex: '1 1 320px' }}>
        <nav aria-label="Breadcrumb" className="small muted">
          <button type="button" className="crumb" onClick={onHome}>Workflows</button> <span aria-hidden="true">/</span>
        </nav>
        <div className="row g8" style={{ minWidth: 0 }}>
          <h1 className="serif title crumb-title">{current.name}</h1>
          <WorkflowMenu current={current} {...workflowMenu} />
          <span className="small muted">{SAVE_LABEL[saveState]}</span>
        </div>
      </div>
      <div className="row wrap g12">
        <div className="row" role="group" aria-label={`Team: ${members.map((m) => m.name).join(', ')}`}>
          {shown.map((m) => {
            const p = personStyle(m.name);
            return <span key={m.userId} className="avatar" style={{ background: p.bg, color: p.fg }} title={m.name}>{p.initials}</span>;
          })}
          {extra > 0 && <span className="avatar" style={{ background: 'var(--chip)', color: 'var(--ink-2)' }} title={`${extra} more`}>+{extra}</span>}
        </div>
        <button className="btn" type="button" onClick={onShare}>Invite</button>
        <button className="btn primary" type="button" onClick={onAdvance} disabled={busy}>
          <Play /> <span>{runLabel}</span>
        </button>
      </div>
    </header>
  );
}

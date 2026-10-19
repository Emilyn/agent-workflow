import { Play, Spark } from './Icons.jsx';
import Switcher from './Switcher.jsx';
import WorkspaceMenu from './WorkspaceMenu.jsx';
import { personStyle } from '../workflow.js';

const SAVE_LABEL = { saved: 'Saved', saving: 'Saving…', error: 'Not saved' };
const MAX_AVATARS = 4;

export default function Header({ current, switcher, workspaceMenu, members, saveState, runLabel, busy, onAdvance, onShare, onSettings }) {
  const shown = members.slice(0, MAX_AVATARS);
  const extra = members.length - shown.length;
  return (
    <header className="topbar">
      <div className="row g12" style={{ minWidth: 0, flex: '1 1 360px' }}>
        <div className="logo"><Spark size={20} /></div>
        <div className="col" style={{ minWidth: 0, flex: 1 }}>
          <div className="row g8 wrap">
            <WorkspaceMenu {...workspaceMenu} />
            <span className="small muted">{SAVE_LABEL[saveState]}</span>
          </div>
          <Switcher current={current} {...switcher} />
        </div>
      </div>
      <div className="row wrap g12">
        <div className="row" role="group" aria-label={`Team: ${members.map((m) => m.name).join(', ')}`}>
          {shown.map((m) => {
            const p = personStyle(m.name);
            return <span key={m.userId} className="avatar" style={{ background: p.bg, color: p.fg }} title={m.name}>{p.initials}</span>;
          })}
          {extra > 0 && <span className="avatar" style={{ background: 'var(--chip)', color: 'var(--ink-2)' }} title={`${extra} more`}>+{extra}</span>}
          <span className="avatar agent" title="Claude"><Spark size={16} /></span>
        </div>
        <button className="btn" type="button" onClick={onShare}>Invite</button>
        <button className="btn" type="button" onClick={onSettings}>Settings</button>
        <button className="btn primary" type="button" onClick={onAdvance} disabled={busy}>
          <Play /> <span>{runLabel}</span>
        </button>
      </div>
    </header>
  );
}

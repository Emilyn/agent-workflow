import { useEffect, useRef, useState } from 'react';
import { ROLE_LABEL } from '../workflow.js';

// The workspace name in the header: switch workspace, create one, open the team tab, sign out.
export default function WorkspaceMenu({ current, workspaces, user, disabled, onSwitch, onCreate, onTeam, onSettings, onSignOut }) {
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [err, setErr] = useState('');
  const [working, setWorking] = useState(false);
  const box = useRef(null);
  const btn = useRef(null);

  const close = () => { setOpen(false); setAdding(false); setErr(''); };

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => { if (e.key === 'Escape') { close(); btn.current?.focus(); } };
    const onDown = (e) => { if (box.current && !box.current.contains(e.target)) close(); };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => { document.removeEventListener('keydown', onKey); document.removeEventListener('mousedown', onDown); };
  }, [open]);

  const run = async (fn) => {
    setWorking(true);
    setErr('');
    try { await fn(); close(); } catch (e) { setErr(e.message); } finally { setWorking(false); }
  };

  return (
    <div className="switcher" ref={box}>
      <button ref={btn} type="button" className="ws-btn" aria-haspopup="true" aria-expanded={open} disabled={disabled} onClick={() => (open ? close() : setOpen(true))}>
        <span className="ws-name">{current.name}</span>
        <span aria-hidden="true" className="caret">▾</span>
      </button>
      {open && (
        <div className="menu" role="group" aria-label="Workspaces">
          <div className="eyebrow menu-head">Workspaces</div>
          <ul className="wf-list">
            {workspaces.map((w) => (
              <li key={w.id}>
                <button type="button" className={`wf-item${w.id === current.id ? ' on' : ''}`} aria-current={w.id === current.id ? 'true' : undefined}
                  onClick={() => (w.id === current.id ? close() : run(() => onSwitch(w.id)))}>
                  <span className="wf-name">{w.name}</span>
                  <span className="small muted">{ROLE_LABEL[w.role]}</span>
                </button>
              </li>
            ))}
          </ul>
          {adding ? (
            <form className="col g8" onSubmit={(e) => { e.preventDefault(); run(() => onCreate(name.trim())); }}>
              <label className="lbl" htmlFor="new-ws">Workspace name</label>
              <input id="new-ws" className="field" value={name} maxLength={80} autoFocus onChange={(e) => setName(e.target.value)} />
              <div className="row g8">
                <button type="submit" className="btn primary" disabled={working || !name.trim()}>Create</button>
                <button type="button" className="btn ghost" onClick={() => setAdding(false)}>Back</button>
              </div>
            </form>
          ) : (
            <div className="menu-actions">
              <button type="button" className="btn" onClick={() => { setAdding(true); setName(''); }}>New workspace</button>
              <button type="button" className="btn" onClick={() => { close(); onTeam(); }}>Team and members</button>
              <button type="button" className="btn" onClick={() => { close(); onSettings(); }}>Settings</button>
            </div>
          )}
          <div className="menu-foot small">
            <span className="muted">Signed in as <strong style={{ color: 'var(--ink)' }}>{user.name}</strong><br />{user.email}</span>
            <button type="button" className="btn" disabled={working} onClick={() => run(onSignOut)}>Sign out</button>
          </div>
          {err && <div role="alert" className="menu-err small">{err}</div>}
        </div>
      )}
    </div>
  );
}

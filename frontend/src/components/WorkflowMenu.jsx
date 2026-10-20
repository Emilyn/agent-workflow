import { useEffect, useRef, useState } from 'react';

// The "⋯" next to the workflow name: rename, duplicate, delete.
export default function WorkflowMenu({ current, canDelete, disabled, onRename, onDuplicate, onDelete }) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState('list'); // list | rename | delete
  const [name, setName] = useState('');
  const [err, setErr] = useState('');
  const [working, setWorking] = useState(false);
  const box = useRef(null);
  const btn = useRef(null);

  const close = () => { setOpen(false); setMode('list'); setErr(''); };

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
      <button ref={btn} type="button" className="btn icon-btn" aria-label="Workflow actions" aria-haspopup="true" aria-expanded={open} disabled={disabled} onClick={() => (open ? close() : setOpen(true))}>
        <svg aria-hidden="true" width="18" height="18" viewBox="0 0 18 18" fill="currentColor"><circle cx="3.5" cy="9" r="1.5" /><circle cx="9" cy="9" r="1.5" /><circle cx="14.5" cy="9" r="1.5" /></svg>
      </button>
      {open && (
        <div className="menu menu-right" role="group" aria-label="Workflow actions">
          {mode === 'list' && (
            <div className="col g8">
              <button type="button" className="btn" onClick={() => { setMode('rename'); setName(current.name); setErr(''); }}>Rename</button>
              <button type="button" className="btn" disabled={working} onClick={() => run(onDuplicate)}>Duplicate</button>
              <button type="button" className="btn danger" disabled={!canDelete} title={canDelete ? '' : 'You need at least one workflow'} onClick={() => { setMode('delete'); setErr(''); }}>Delete</button>
            </div>
          )}
          {mode === 'rename' && (
            <form className="col g12" onSubmit={(e) => { e.preventDefault(); if (!name.trim()) return setErr('Give the workflow a name.'); return run(() => onRename(name.trim())); }}>
              <div className="eyebrow">Rename workflow</div>
              <input className="field" aria-label="Workflow name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} autoFocus />
              <div className="row g8">
                <button type="submit" className="btn primary" disabled={working}>Save name</button>
                <button type="button" className="btn ghost" onClick={() => setMode('list')}>Back</button>
              </div>
            </form>
          )}
          {mode === 'delete' && (
            <div className="col g12">
              <div className="eyebrow">Delete workflow</div>
              <p className="small" style={{ margin: 0 }}>Delete “{current.name}”, its chat and its run results? This can’t be undone.</p>
              <div className="row g8">
                <button type="button" className="btn danger" disabled={working} onClick={() => run(onDelete)}>{working ? 'Deleting…' : 'Delete it'}</button>
                <button type="button" className="btn ghost" onClick={() => setMode('list')}>Keep it</button>
              </div>
            </div>
          )}
          {err && <div role="alert" className="menu-err small">{err}</div>}
        </div>
      )}
    </div>
  );
}

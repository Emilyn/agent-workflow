import { useEffect, useRef, useState } from 'react';

const TEMPLATES = [
  { id: 'blank', label: 'Blank' },
  { id: 'example', label: 'Example' },
  { id: 'copy', label: 'Copy of this one' },
];

// The workflow name in the header opens this menu: switch, create, rename, duplicate, delete.
export default function Switcher({ current, list, disabled, onOpenMenu, onOpen, onCreate, onRename, onDuplicate, onDelete }) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState('list'); // list | new | rename | delete
  const [name, setName] = useState('');
  const [template, setTemplate] = useState('blank');
  const [err, setErr] = useState('');
  const [working, setWorking] = useState(false);
  const box = useRef(null);
  const btn = useRef(null);

  const close = () => {
    setOpen(false);
    setMode('list');
    setErr('');
  };

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => {
      if (e.key === 'Escape') {
        close();
        btn.current?.focus();
      }
    };
    const onDown = (e) => {
      if (box.current && !box.current.contains(e.target)) close();
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onDown);
    };
  }, [open]);

  const toggle = async () => {
    if (open) return close();
    setOpen(true);
    await onOpenMenu();
  };

  const run = async (fn) => {
    setWorking(true);
    setErr('');
    try {
      await fn();
      close();
    } catch (e) {
      setErr(e.message);
    } finally {
      setWorking(false);
    }
  };

  const submitNew = (e) => {
    e.preventDefault();
    run(() => onCreate({ name: name.trim(), template: template === 'copy' ? '' : template, copyOf: template === 'copy' ? current.id : '' }));
  };
  const submitRename = (e) => {
    e.preventDefault();
    if (!name.trim()) return setErr('Give the workflow a name.');
    return run(() => onRename(name.trim()));
  };

  const canDelete = list.length > 1;

  return (
    <div className="switcher" ref={box}>
      <button
        ref={btn}
        type="button"
        className="switch-btn"
        aria-haspopup="true"
        aria-expanded={open}
        disabled={disabled}
        onClick={toggle}
      >
        <span className="serif title">{current.name}</span>
        <span aria-hidden="true" className="caret">▾</span>
      </button>

      {open && (
        <div className="menu" role="group" aria-label="Workflows">
          {mode === 'list' && (
            <>
              <div className="eyebrow menu-head">Your workflows ({list.length})</div>
              <ul className="wf-list">
                {list.map((w) => (
                  <li key={w.id}>
                    <button
                      type="button"
                      className={`wf-item${w.id === current.id ? ' on' : ''}`}
                      aria-current={w.id === current.id ? 'true' : undefined}
                      onClick={() => (w.id === current.id ? close() : run(() => onOpen(w.id)))}
                    >
                      <span className="wf-name">{w.name}</span>
                      <span className="small muted">
                        {w.steps} steps · {w.agents} agents · {w.people} people · {w.progress}/{w.steps} done
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
              <div className="menu-actions">
                <button type="button" className="btn primary" onClick={() => { setMode('new'); setName(''); setTemplate('blank'); setErr(''); }}>New workflow</button>
                <button type="button" className="btn" onClick={() => { setMode('rename'); setName(current.name); setErr(''); }}>Rename</button>
                <button type="button" className="btn" disabled={working} onClick={() => run(onDuplicate)}>Duplicate</button>
                <button type="button" className="btn danger" disabled={!canDelete} title={canDelete ? '' : 'You need at least one workflow'} onClick={() => { setMode('delete'); setErr(''); }}>Delete</button>
              </div>
            </>
          )}

          {mode === 'new' && (
            <form onSubmit={submitNew} className="col g12">
              <div className="eyebrow">New workflow</div>
              <label className="col g6">
                <span className="small">Name</span>
                <input className="field" value={name} maxLength={80} placeholder="Untitled workflow" onChange={(e) => setName(e.target.value)} autoFocus />
              </label>
              <fieldset className="col g6 tpl">
                <legend className="small">Start from</legend>
                {TEMPLATES.map((t) => (
                  <label key={t.id} className="row g8 tpl-opt">
                    <input type="radio" name="template" value={t.id} checked={template === t.id} onChange={() => setTemplate(t.id)} />
                    <span>{t.label}</span>
                  </label>
                ))}
              </fieldset>
              <div className="row g8">
                <button type="submit" className="btn primary" disabled={working}>{working ? 'Creating…' : 'Create'}</button>
                <button type="button" className="btn ghost" onClick={() => setMode('list')}>Back</button>
              </div>
            </form>
          )}

          {mode === 'rename' && (
            <form onSubmit={submitRename} className="col g12">
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
              <p className="small" style={{ margin: 0 }}>
                Delete “{current.name}”, its chat and its run results? This can’t be undone.
              </p>
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

import { useEffect, useRef, useState } from 'react';
import { PERSON_STEPS, allAgents } from '../workflow.js';
import { Person, Spark } from './Icons.jsx';

// The "+" between two steps. It opens a short list of what can go there.
export default function StepAdder({ doc, disabled, onAddAgent, onAddPerson, onCreateAgent }) {
  const [open, setOpen] = useState(false);
  const box = useRef(null);
  const btn = useRef(null);

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => { if (e.key === 'Escape') { setOpen(false); btn.current?.focus(); } };
    const onDown = (e) => { if (box.current && !box.current.contains(e.target)) setOpen(false); };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => { document.removeEventListener('keydown', onKey); document.removeEventListener('mousedown', onDown); };
  }, [open]);

  const pick = (fn) => () => { setOpen(false); fn(); };

  return (
    <div className="adder" ref={box}>
      <div className="link" />
      <button ref={btn} type="button" className="adder-btn" aria-label="Add a step here" aria-haspopup="true" aria-expanded={open} disabled={disabled} onClick={() => setOpen((o) => !o)}>+</button>
      <div className="link" />
      {open && (
        <div className="menu adder-menu" role="group" aria-label="Add a step">
          <div className="eyebrow menu-head">Agents</div>
          {allAgents(doc).map((a) => (
            <button key={a.id} type="button" className="item" onClick={pick(() => onAddAgent(a))}>
              <span className="glyph"><Spark size={16} /></span>
              <span className="col"><span className="name">{a.name}</span><span className="hint">{a.mine ? 'Yours · ' : ''}{a.role || 'Custom agent'}</span></span>
            </button>
          ))}
          <button type="button" className="item dashed" onClick={pick(onCreateAgent)}>+ Create an agent</button>
          <div className="eyebrow menu-head" style={{ marginTop: 12 }}>People</div>
          {PERSON_STEPS.map((p) => (
            <button key={p.key} type="button" className="item" onClick={pick(() => onAddPerson(p))}>
              <span className="glyph person"><Person size={16} /></span>
              <span className="col"><span className="name">{p.name}</span><span className="hint">{p.hint}</span></span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

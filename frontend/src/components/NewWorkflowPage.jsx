import { useState } from 'react';

const TEMPLATES = [
  { id: 'blank', tag: 'Start fresh', name: 'Blank workflow', desc: 'Add your own agents and people step by step.' },
  { id: 'example', tag: 'Example', name: 'Example workflow', desc: 'A research, write and review flow to see how agents and people hand work over.' },
  { id: 'review', tag: 'Engineering', name: 'Code review', desc: 'Reviews every new pull or merge request. A person approves the comments.' },
];

// Pick a starting point and a name.
export default function NewWorkflowPage({ currentName, onCreate, onCancel }) {
  const [template, setTemplate] = useState('blank');
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const options = currentName ? [...TEMPLATES, { id: 'copy', tag: 'Copy', name: `Copy of “${currentName}”`, desc: 'Same steps and agents, without the results.' }] : TEMPLATES;

  const submit = async (e) => {
    e.preventDefault();
    setWorking(true);
    setError('');
    try {
      await onCreate({ name: name.trim(), template });
    } catch (err) {
      setError(err.message);
      setWorking(false);
    }
  };

  return (
    <main className="page" aria-labelledby="new-title">
      <div className="col g6">
        <button type="button" className="crumb" onClick={onCancel}>← Workflows</button>
        <h1 id="new-title" className="serif" style={{ fontSize: 40, margin: 0 }}>New workflow</h1>
      </div>
      <form className="col g20" onSubmit={submit} style={{ maxWidth: 860 }}>
        <div className="col g6" style={{ maxWidth: 420 }}>
          <label className="lbl" htmlFor="nw-wf-name">Name</label>
          <input id="nw-wf-name" className="field" value={name} maxLength={80} placeholder="Untitled workflow" onChange={(e) => setName(e.target.value)} autoFocus />
        </div>
        <fieldset className="tpl-grid" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lbl" style={{ marginBottom: 10 }}>Start from</legend>
          <div className="tiles">
            {options.map((t) => (
              <label key={t.id} className={`tile${template === t.id ? ' on' : ''}`}>
                <input type="radio" name="template" value={t.id} checked={template === t.id} onChange={() => setTemplate(t.id)} className="sr-only" />
                <span className="eyebrow">{t.tag}</span>
                <strong>{t.name}</strong>
                <span className="small" style={{ color: 'var(--ink-2)' }}>{t.desc}</span>
              </label>
            ))}
          </div>
        </fieldset>
        {error && <div className="alert" role="alert">{error}</div>}
        <div className="row g8">
          <button type="submit" className="btn primary" disabled={working}>{working ? 'Creating…' : 'Create workflow'}</button>
          <button type="button" className="btn ghost" onClick={onCancel}>Cancel</button>
        </div>
      </form>
    </main>
  );
}

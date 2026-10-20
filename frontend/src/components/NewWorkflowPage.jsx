import { useState } from 'react';

const TEMPLATES = [
  { id: 'blank', tag: 'Start fresh', name: 'Blank workflow', desc: 'Add your own agents and people step by step.' },
  { id: 'example', tag: 'Example', name: 'Example workflow', desc: 'A research, write and review flow to see how agents and people hand work over.' },
  { id: 'review', tag: 'Engineering', name: 'Code review', desc: 'Reviews every new pull or merge request. A person approves the comments.' },
];

// Pick a starting point and a name.
export default function NewWorkflowPage({ currentName, canDraft, onDraft, onCreate, onCancel }) {
  const [template, setTemplate] = useState('blank');
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const [text, setText] = useState('');
  const [drafting, setDrafting] = useState(false);
  const [draft, setDraft] = useState(null); // Claude's proposal, until accepted or revised
  const [draftError, setDraftError] = useState('');
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

  const makeDraft = async () => {
    setDrafting(true);
    setDraftError('');
    try {
      setDraft(await onDraft(text.trim()));
    } catch (err) {
      setDraft(null);
      setDraftError(err.message);
    }
    setDrafting(false);
  };
  const accept = async () => {
    setWorking(true);
    setDraftError('');
    try {
      await onCreate({ name: draft.name, template: 'blank', draft });
    } catch (err) {
      setDraftError(err.message);
      setWorking(false);
    }
  };

  return (
    <main className="page" aria-labelledby="new-title">
      <div className="col g6">
        <button type="button" className="crumb" onClick={onCancel}>← Workflows</button>
        <h1 id="new-title" className="serif" style={{ fontSize: 40, margin: 0 }}>New workflow</h1>
      </div>
      <section className="checklist" aria-labelledby="describe-title" style={{ maxWidth: 860 }}>
        <h2 id="describe-title" style={{ margin: 0, fontSize: 18 }}>Describe what should happen</h2>
        <label htmlFor="describe" className="sr-only">Describe the workflow</label>
        <textarea id="describe" className="field" rows={3} maxLength={1500} value={text} onChange={(e) => { setText(e.target.value); setDraftError(''); }}
          placeholder="Every Monday, pull last week's numbers from the sheet, explain what changed, write a short report, and have Mo approve it before it goes to the team." />
        <div className="row g12 wrap">
          <button type="button" className="btn primary" onClick={makeDraft} disabled={drafting || !canDraft || text.trim().length < 10}>{drafting ? 'Drafting…' : draft ? 'Draft again' : 'Draft with Claude'}</button>
          <span className="small muted">{canDraft ? 'Claude proposes the steps. Nothing is saved until you accept.' : 'Connect Claude in Settings to draft workflows.'}</span>
        </div>
        {draftError && <div className="alert" role="alert">{draftError}</div>}
        {draft && (
          <div className="col g12" data-testid="draft">
            <div className="row g8" style={{ justifyContent: 'space-between' }}>
              <strong>{draft.name}</strong>
              <span className="pill done">{draft.steps.length} new steps</span>
            </div>
            <ol className="tasks">
              {draft.steps.map((st, i) => (
                <li key={i} className="task draft-step">
                  <span className="task-mark" aria-hidden="true">{i + 1}</span>
                  <span className="col g6" style={{ flex: 1, minWidth: 0 }}>
                    <strong>{st.title}</strong>
                    <span className="small" style={{ color: 'var(--ink-2)' }}>{st.desc}</span>
                    {st.tools.length > 0 && <span className="small muted">Tools: {st.tools.join(', ')}</span>}
                  </span>
                  <span className="small muted">{st.kind === 'agent' ? 'Agent' : 'Person'}</span>
                </li>
              ))}
            </ol>
            <span className="small muted">Person steps are assigned to you. You can change steps, tools and assignees in the editor.</span>
            <div className="row g8">
              <button type="button" className="btn outline" onClick={accept} disabled={working}>{working ? 'Creating…' : 'Accept and open editor'}</button>
              <button type="button" className="btn ghost" onClick={() => setDraft(null)} disabled={working}>Discard</button>
            </div>
          </div>
        )}
      </section>
      <form className="col g20" onSubmit={submit} style={{ maxWidth: 860 }}>
        <div className="col g6" style={{ maxWidth: 420 }}>
          <label className="lbl" htmlFor="nw-wf-name">Name</label>
          <input id="nw-wf-name" className="field" value={name} maxLength={80} placeholder="Untitled workflow" onChange={(e) => setName(e.target.value)} autoFocus />
        </div>
        <fieldset className="tpl-grid" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lbl" style={{ marginBottom: 10 }}>Or start from a template</legend>
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

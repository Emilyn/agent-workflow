import { gitName } from '../workflow.js';

const MODELS = ['Claude Sonnet', 'Claude Opus', 'Claude Haiku'];

export default function BuilderPanel({ doc, form, error, onChange, onSave, onCancel, onGoIntegrations }) {
  const tools = [
    { label: 'Web search' }, { label: 'Docs' }, { label: 'Sheets' }, { label: 'Slack' }, { label: 'Email' },
    { label: gitName(doc), need: 'git' }, { label: 'Jira', need: 'jira' }, { label: 'Figma', need: 'figma' },
  ];

  return (
    <div className="panel">
      <div className="col g6">
        <span className="eyebrow" style={{ fontSize: 11 }}>New agent</span>
        <span className="serif" style={{ fontSize: 28 }}>Create an agent</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Describe what it should do. Once saved, it appears in your Agents list, and you can add it to the workflow or mention it in chat.
        </span>
      </div>

      <div className="col g8">
        <label className="lbl" htmlFor="agent-name">Name</label>
        <input id="agent-name" className="field" placeholder="Release notes" value={form.name} onChange={(e) => onChange({ name: e.target.value })} />
      </div>
      <div className="col g8">
        <label className="lbl" htmlFor="agent-role">What it does, in one line</label>
        <input id="agent-role" className="field" placeholder="Turns merged pull requests into customer-facing notes" value={form.role} onChange={(e) => onChange({ role: e.target.value })} />
      </div>
      <div className="col g8">
        <label className="lbl" htmlFor="agent-instr">Instructions</label>
        <textarea
          id="agent-instr"
          className="field"
          rows={6}
          placeholder="Read the merged pull requests and their linked tickets, then write short release notes in plain language."
          value={form.instructions}
          onChange={(e) => onChange({ instructions: e.target.value })}
        />
      </div>

      <div className="col g8">
        <span className="lbl">Tools and sources</span>
        <div className="row wrap g8">
          {tools.map((t) => {
            if (t.need && !doc.connected[t.need]) {
              return <button key={t.label} type="button" className="tool locked" onClick={onGoIntegrations}>{t.label} · connect first</button>;
            }
            const on = !!form.tools[t.label];
            return (
              <button
                key={t.label}
                type="button"
                className={`tool${on ? ' on' : ''}`}
                aria-pressed={on}
                onClick={() => onChange({ tools: { ...form.tools, [t.label]: !on } })}
              >
                {t.label}
              </button>
            );
          })}
        </div>
        <span className="small muted">Dashed tools need a connection first. Select one to open Integrations.</span>
      </div>

      <div className="col g8">
        <span className="lbl">Model</span>
        <div className="seg" role="group" aria-label="Choose a model">
          {MODELS.map((m) => (
            <button key={m} type="button" className={form.model === m ? 'on' : ''} aria-pressed={form.model === m} onClick={() => onChange({ model: m })}>{m}</button>
          ))}
        </div>
      </div>

      {error && <span className="alert" role="alert">{error}</span>}

      <div className="col g8">
        <button className="btn primary block" type="button" style={{ justifyContent: 'center' }} onClick={() => onSave(true)}>Save and add to workflow</button>
        <button className="btn outline block" type="button" onClick={() => onSave(false)}>Save agent</button>
        <button className="btn ghost" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

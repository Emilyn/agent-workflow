import { useState } from 'react';
import { LIVE_TOOLS } from '../workflow.js';

const MODELS = ['Claude Sonnet', 'Claude Opus', 'Claude Haiku'];

export default function BuilderPanel({ ints, form, error, canDraft, onDraft, onChange, onSave, onCancel, onGoIntegrations }) {
  const [about, setAbout] = useState('');
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState('');
  const hasText = form.instructions.trim().length >= 10;

  // Claude writes (or rewrites) the form from a sentence; the person reviews every field before saving.
  const draft = async () => {
    setBusy(true);
    setNote('');
    try {
      const d = await onDraft(about.trim(), hasText ? form.instructions : '');
      const known = new Set(tools.filter((t) => !t.id || ints?.[t.id]?.connected).map((t) => t.label));
      const picked = {};
      (d.tools || []).forEach((t) => { if (known.has(t)) picked[t] = true; });
      const skipped = (d.tools || []).filter((t) => !known.has(t));
      onChange({ name: form.name.trim() || d.name, role: d.role, instructions: d.instruction, model: d.model, tools: picked });
      setAbout('');
      setNote(skipped.length ? `Drafted. It also wanted ${skipped.join(', ')}, which isn't connected yet.` : 'Drafted. Read it through and change anything before you save.');
    } catch (e) {
      setNote(e.message || 'Could not draft. Try again.');
    } finally {
      setBusy(false);
    }
  };

  const tools = [
    { label: 'Web search' }, { label: 'Docs' }, { label: 'Sheets' }, { label: 'Slack' }, { label: 'Email' },
    ...LIVE_TOOLS.map((t) => ({ label: t.label, id: t.id })),
  ];

  return (
    <div className="panel">
      <div className="col g6">
        <span className="eyebrow" style={{ fontSize: 11 }}>New agent</span>
        <span className="serif" style={{ fontSize: 28 }}>Create an agent</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Describe what it should do. Once saved, you can add it from the + between steps or mention it in chat.
        </span>
      </div>

      <div className="col g8">
        <label className="lbl" htmlFor="agent-about">{hasText ? 'What should change? (optional)' : 'What should this agent do?'}</label>
        <textarea id="agent-about" className="field" rows={3} placeholder="Turn merged pull requests into short release notes a customer can read." value={about} onChange={(e) => setAbout(e.target.value)} />
        <div className="row g8" style={{ alignItems: 'center', flexWrap: 'wrap' }}>
          <button type="button" className="btn primary" onClick={draft} disabled={busy || !canDraft || (about.trim().length < 10 && !hasText)}>
            {busy ? 'Writing…' : hasText ? 'Improve with Claude' : 'Write it with Claude'}
          </button>
          <span className="small muted">{canDraft ? 'Claude writes the instructions, picks tools and a model. Nothing is saved until you save.' : 'Connect Claude in Settings to have it written for you.'}</span>
        </div>
        {note && <span className="small" role="status">{note}</span>}
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

      <details className="col g8" open={Object.values(form.tools).some(Boolean) || undefined}>
      <summary className="lbl" style={{ cursor: 'pointer' }}>Tools and model · {Object.values(form.tools).filter(Boolean).length} tools, {form.model.replace('Claude ', '')}</summary>
      <div className="col g8">
        <span className="lbl">Tools and sources</span>
        <div className="row wrap g8">
          {tools.map((t) => {
            if (t.id && !ints?.[t.id]?.connected) {
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
      </details>

      {error && <span className="alert" role="alert">{error}</span>}

      <div className="col g8">
        <button className="btn primary block" type="button" style={{ justifyContent: 'center' }} onClick={() => onSave(true)}>Save and add to workflow</button>
        <button className="btn outline block" type="button" onClick={() => onSave(false)}>Save agent</button>
        <button className="btn ghost" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

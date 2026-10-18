import { KIND_LABEL, rowOf, statusFor } from '../workflow.js';
import { Pill } from './Canvas.jsx';

export default function SettingsPanel({ doc, node, onEdit, onRemove, onDiscuss }) {
  const row = rowOf(doc.steps, node.id);
  const output = doc.outputs[node.id];

  return (
    <div className="panel">
      <div className="col g8">
        <span className="eyebrow" style={{ fontSize: 11 }}>{KIND_LABEL[node.kind]}</span>
        <span className="serif" style={{ fontSize: 28 }}>{node.title}</span>
        <div><Pill status={statusFor(doc.progress, row, node)} node={node} /></div>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>{node.desc}</span>
      </div>

      {node.kind === 'agent' && (
        <>
          <div className="col g8">
            <label className="lbl" htmlFor="step-instr">Instructions</label>
            <textarea id="step-instr" className="field" value={node.instruction} onChange={(e) => onEdit(node.id, { instruction: e.target.value })} />
          </div>
          <div className="col g8">
            <span className="lbl">Tools and sources</span>
            <div className="row wrap g8">
              {node.tools.length ? node.tools.map((t) => <span key={t} className="tag-chip">{t}</span>) : <span className="small muted">No tools selected.</span>}
            </div>
            <span className="small muted">The GitHub tool is live: when GitHub is connected, this agent can read repositories, issues and pull requests while it runs. Other tools are recorded but not called yet.</span>
          </div>
          <div className="row box" style={{ justifyContent: 'space-between' }}>
            <span>Model</span><strong>{node.who}</strong>
          </div>
          <div className="col g8">
            <div className="row g8">
              <span className="lbl">Latest output</span>
              {output?.simulated && <span className="tag">Simulated</span>}
            </div>
            {output?.used?.length > 0 && <span className="small muted">Used: {output.used.map((u) => u.replace(/^github_/, '').replace(/_/g, ' ')).join(', ')}</span>}
            {output ? <div className="output">{output.text}</div> : <span className="small muted">Nothing yet. Run the workflow to see what this agent produces.</span>}
          </div>
        </>
      )}

      {node.kind === 'human' && (
        <>
          <div className="col g8">
            <label className="lbl" htmlFor="assignee">Assigned to</label>
            <select id="assignee" className="field" value={node.assignee || ''} onChange={(e) => onEdit(node.id, { assignee: e.target.value, who: e.target.value || 'Unassigned' })}>
              <option value="">Unassigned</option>
              {node.assignee && !doc.members.some((m) => m.name === node.assignee) && <option value={node.assignee}>{node.assignee} (not in this workspace)</option>}
              {doc.members.map((m) => <option key={m.userId} value={m.name}>{m.name}</option>)}
            </select>
          </div>
          <div className="col g8">
            <label className="lbl" htmlFor="step-instr">What they will see</label>
            <textarea id="step-instr" className="field" value={node.instruction} onChange={(e) => onEdit(node.id, { instruction: e.target.value })} />
          </div>
        </>
      )}

      {node.kind === 'trigger' && (
        <div className="row box" style={{ justifyContent: 'space-between' }}>
          <span>Starts</span><strong>{node.title}</strong>
        </div>
      )}

      <div className="col g8">
        <span className="lbl">If this step fails</span>
        <div className="box">Pause the run and show the error</div>
      </div>

      {node.kind !== 'trigger' && <button className="btn danger block" type="button" onClick={() => onRemove(node.id)}>Remove this step</button>}
      <button className="btn outline block" type="button" onClick={() => onDiscuss(node)}>Discuss this step in chat</button>
    </div>
  );
}

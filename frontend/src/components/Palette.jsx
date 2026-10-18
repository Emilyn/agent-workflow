import { PERSON_STEPS, allAgents } from '../workflow.js';
import { Person, Spark } from './Icons.jsx';

export default function Palette({ doc, onAddAgent, onAddPerson, onCreateAgent }) {
  return (
    <aside className="palette">
      <div className="col g8">
        <span className="eyebrow">Agents</span>
        <span className="small muted">Click an agent to add it to the workflow after the current step.</span>
        {allAgents(doc).map((a) => (
          <button key={a.id} className="item" type="button" aria-label={`Add ${a.name} to the workflow`} onClick={() => onAddAgent(a)}>
            <span className="glyph"><Spark size={16} /></span>
            <span className="col">
              <span className="name">{a.name}</span>
              <span className="hint">{a.mine ? 'Yours · ' : ''}{a.role || 'Custom agent'}</span>
            </span>
          </button>
        ))}
        <button className="item dashed" type="button" onClick={onCreateAgent}>+ Create an agent</button>
      </div>

      <div className="col g8">
        <span className="eyebrow">People</span>
        {PERSON_STEPS.map((p) => (
          <button key={p.key} className="item" type="button" aria-label={`Add ${p.name} step`} onClick={() => onAddPerson(p)}>
            <span className="glyph person"><Person size={16} /></span>
            <span className="col">
              <span className="name">{p.name}</span>
              <span className="hint">{p.hint}</span>
            </span>
          </button>
        ))}
      </div>

      <div className="col g8">
        <span className="eyebrow">Logic</span>
        <div className="row wrap g8">
          {['Branch', 'Parallel', 'Wait'].map((n) => (
            <button key={n} className="chip" type="button" disabled title="Coming soon">{n}</button>
          ))}
        </div>
      </div>
    </aside>
  );
}

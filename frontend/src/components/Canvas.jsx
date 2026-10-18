import { KIND_LABEL, runSummary, statusFor } from '../workflow.js';
import { Clock, Person, Spark } from './Icons.jsx';

export function Pill({ status, node }) {
  const text = { done: 'Done', running: 'Running', queued: 'Queued', waiting: `Waiting on ${node?.assignee}` }[status];
  return <span className={`pill ${status}`}>{text}</span>;
}

function Badge({ kind }) {
  if (kind === 'agent') return <span className="badge"><Spark /></span>;
  if (kind === 'human') return <span className="badge person"><Person /></span>;
  return <span className="badge trigger"><Clock /></span>;
}

export default function Canvas({ doc, selected, busy, onSelect, onCreateAgent }) {
  const { done, waiting, total } = runSummary(doc);
  const runPill = done ? (
    <span className="pill done">Run complete</span>
  ) : busy ? (
    <span className="pill running">Running</span>
  ) : waiting ? (
    <span className="pill waiting">Paused, waiting on {waiting}</span>
  ) : (
    <span className="pill queued">Ready</span>
  );

  return (
    <main className="canvas-wrap">
      <div className="runbar">
        <div className="row wrap g12">
          <strong style={{ fontSize: 14 }}>Run 12</strong>
          {runPill}
          <span className="small muted">{done ? `All ${total} steps finished` : `Step ${doc.progress + 1} of ${total}`}</span>
        </div>
      </div>
      <div className="canvas">
        {doc.steps.map((group, r) => (
          <div key={group.map((n) => n.id).join('-')} className="col" style={{ alignItems: 'center' }}>
            <div className="rowline">
              {group.map((n) => (
                <button
                  key={n.id}
                  type="button"
                  className={`card${selected === n.id ? ' sel' : ''}`}
                  onClick={() => onSelect(n.id)}
                >
                  <Badge kind={n.kind} />
                  <span className="col g6" style={{ minWidth: 0, flex: 1 }}>
                    <span className="eyebrow" style={{ fontSize: 11 }}>{KIND_LABEL[n.kind]}</span>
                    <span style={{ fontSize: 16, fontWeight: 600 }}>{n.title}</span>
                    <span className="small" style={{ color: 'var(--ink-2)' }}>{n.desc}</span>
                    <span className="row wrap g8" style={{ marginTop: 6 }}>
                      <Pill status={statusFor(doc.progress, r, n)} node={n} />
                      <span className="muted" style={{ fontSize: 12 }}>{n.who}</span>
                      {doc.outputs[n.id] && <span className="tag-chip" style={{ padding: '2px 8px', fontSize: 12 }}>Output ready</span>}
                    </span>
                  </span>
                </button>
              ))}
            </div>
            {r < doc.steps.length - 1 && <div className="link" />}
          </div>
        ))}
        <div className="link" />
        <button className="btn add-step" type="button" onClick={onCreateAgent}>+ Create an agent for a new step</button>
      </div>
    </main>
  );
}

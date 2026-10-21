import { useEffect, useMemo, useState } from 'react';
import { api } from '../api.js';
import ReviewDraft from './ReviewDraft.jsx';
import { REVIEW_HOSTS } from './reviewHosts.js';
import { ago } from './HomePage.jsx';

// What is waiting for this person: review drafts to approve, and steps assigned to them.
export function needsYou(list, runs, userName) {
  const drafts = runs.filter((r) => r.actions.some((a) => a.status === 'pending')).length;
  const steps = list.filter((w) => w.waitingOn && w.waitingOn === userName).length;
  return drafts + steps;
}

const FILTERS = [
  ['needs', 'Needs you'],
  ['running', 'Running'],
  ['failed', 'Failed'],
  ['all', 'All'],
];
const PILL = { done: 'done', running: 'running', queued: 'queued', failed: 'waiting' };

// Every run and every pause in one place, with the decision next to the thing being decided.
export default function RunsPage({ wsId, list, runs, userName, canManage, busyAction, error, onApprove, onDiscard, onRerun, onDecide, onOpenWorkflow }) {
  const [filter, setFilter] = useState('needs');
  const [picked, setPicked] = useState('');
  const nameOf = (id) => list.find((w) => w.id === id)?.name || 'Workflow';

  const items = useMemo(() => {
    const steps = list.filter((w) => w.waitingOn).map((w) => ({
      key: `step:${w.id}`, kind: 'step', wf: w, mine: w.waitingOn === userName, when: w.updated,
    }));
    const rs = runs.map((r) => ({
      key: `run:${r.id}`, kind: 'run', run: r, mine: r.actions.some((a) => a.status === 'pending'), when: r.createdAt,
    }));
    return [...steps, ...rs].sort((a, b) => (b.mine - a.mine) || (b.when || '').localeCompare(a.when || ''));
  }, [list, runs, userName]);

  const match = (it) => {
    if (filter === 'all') return true;
    if (filter === 'needs') return it.mine;
    if (it.kind === 'step') return false;
    return filter === 'failed' ? it.run.status === 'failed' : ['running', 'queued'].includes(it.run.status);
  };
  const count = (f) => items.filter((it) => (f === 'all' ? true : f === 'needs' ? it.mine : it.kind === 'run' && (f === 'failed' ? it.run.status === 'failed' : ['running', 'queued'].includes(it.run.status)))).length;
  const shown = items.filter(match);
  const current = shown.find((it) => it.key === picked) || shown[0];

  return (
    <main className="runs-page" aria-labelledby="runs-title">
      <div className="runs-head">
        <h1 id="runs-title" className="serif" style={{ fontSize: 40, margin: 0 }}>Runs</h1>
        <div className="row g8 wrap" role="group" aria-label="Filter runs">
          {FILTERS.map(([id, label]) => (
            <button key={id} type="button" className={`filter${filter === id ? ' on' : ''}`} aria-pressed={filter === id} onClick={() => { setFilter(id); setPicked(''); }}>{label} {count(id)}</button>
          ))}
        </div>
      </div>
      {error && <div className="alert" role="alert" style={{ padding: '0 40px' }}>{error}</div>}
      <div className="runs-body">
        <ul className="runs-list" aria-label="Runs">
          {shown.length === 0 && (
            <li className="empty" style={{ margin: 16 }}>
              <span>{filter === 'needs' ? 'Nothing is waiting for you.' : 'No runs here.'}</span>
              {filter === 'needs' && items.length > 0 && <button type="button" className="btn sm" onClick={() => setFilter('all')}>Show all runs</button>}
            </li>
          )}
          {shown.map((it) => {
            const on = current?.key === it.key;
            const title = it.kind === 'step' ? it.wf.name : it.run.input.title ? `#${it.run.input.number} ${it.run.input.title}` : nameOf(it.run.workflowId);
            const line = it.kind === 'step' ? `${it.wf.waitingTitle}` : nameOf(it.run.workflowId);
            const status = it.kind === 'step' ? (it.mine ? 'Needs you' : `Waiting on ${it.wf.waitingOn || 'a teammate'}`) : it.mine ? 'Needs you' : it.run.status;
            const pill = it.kind === 'step' || it.mine ? 'waiting' : PILL[it.run.status] || 'queued';
            return (
              <li key={it.key}>
                <button type="button" className={`run-item${on ? ' on' : ''}`} aria-current={on ? 'true' : undefined} onClick={() => setPicked(it.key)}>
                  <span className="row g8" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <strong className="run-item-title">{title}</strong>
                    <span className={`pill ${pill}`} style={{ whiteSpace: 'nowrap' }}>{status}</span>
                  </span>
                  <span className="small" style={{ color: 'var(--ink-2)' }}>{line}</span>
                  <span className="small muted">{ago(it.when)}</span>
                </button>
              </li>
            );
          })}
        </ul>
        <section className="run-detail" aria-label="Run detail">
          {!current ? (
            <div className="empty"><span>Pick a run on the left to see what happened.</span></div>
          ) : current.kind === 'step' ? (
            <StepDetail key={`${current.wf.id}:${current.wf.waitingStep}`} wsId={wsId} wf={current.wf} mine={current.mine} canDecide={current.mine || canManage} onDecide={(body) => onDecide(current.wf, body)} onOpen={() => onOpenWorkflow(current.wf.id)} />
          ) : (
            <RunDetail run={current.run} workflowName={nameOf(current.run.workflowId)} busyAction={busyAction} onApprove={onApprove} onDiscard={onDiscard} onRerun={onRerun} onOpen={() => onOpenWorkflow(current.run.workflowId)} />
          )}
        </section>
      </div>
    </main>
  );
}

function StepDetail({ wsId, wf, mine, canDecide, onDecide, onOpen }) {
  const [doc, setDoc] = useState(null);
  const [note, setNote] = useState('');
  const [sendingBack, setSendingBack] = useState(false);
  const [working, setWorking] = useState('');
  const [error, setError] = useState('');

  // The output being decided on: the latest result from the steps before this one.
  useEffect(() => {
    let off = false;
    api.getWorkflow(wsId, wf.id).then((d) => { if (!off) setDoc(d); }).catch(() => {});
    return () => { off = true; };
  }, [wsId, wf.id, wf.waitingStep]);
  const review = useMemo(() => {
    if (!doc) return null;
    for (let r = Math.min(doc.progress, doc.steps.length) - 1; r >= 0; r -= 1) {
      for (const n of doc.steps[r]) {
        if (doc.outputs[n.id]) return { title: n.title, text: doc.outputs[n.id].text };
      }
    }
    return null;
  }, [doc]);

  const decide = async (decision) => {
    setWorking(decision);
    setError('');
    try {
      await onDecide({ decision, note: note.trim() });
    } catch (e) {
      setError(e.message);
      setWorking('');
    }
  };

  return (
    <div className="col g16">
      <div className="row g12" style={{ justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div className="col g6">
          <span className="eyebrow">{wf.name}</span>
          <h2 style={{ margin: 0, fontSize: 22 }}>{mine ? 'Waiting for your decision' : `Waiting on ${wf.waitingOn || 'a teammate'}`}</h2>
          <span className="small muted">Paused at “{wf.waitingTitle}”, step {wf.progress + 1} of {wf.steps}. Updated {ago(wf.updated)}.</span>
        </div>
        <button type="button" className="btn" onClick={onOpen}>Open workflow</button>
      </div>
      {review && (
        <div className="kv">
          <span className="lbl-s">Output of {review.title}</span>
          <div className="output">{review.text}</div>
        </div>
      )}
      {canDecide ? (
        <div className="col g12">
          {sendingBack && (
            <div className="col g6">
              <label className="lbl" htmlFor="send-back-note">What should change?</label>
              <textarea id="send-back-note" className="field" rows={3} maxLength={1000} value={note} onChange={(e) => { setNote(e.target.value); setError(''); }} placeholder="Tell the agent what to fix. It runs again with your note." autoFocus />
            </div>
          )}
          {!sendingBack && (
            <div className="col g6">
              <label className="lbl" htmlFor="approve-note">Note (optional)</label>
              <input id="approve-note" className="field" maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} />
            </div>
          )}
          {error && <span className="alert" role="alert">{error}</span>}
          <div className="row g8 wrap">
            {!sendingBack ? (
              <>
                <button type="button" className="btn primary" disabled={!!working} onClick={() => decide('approve')}>{working === 'approve' ? 'Approving…' : 'Approve and continue'}</button>
                <button type="button" className="btn" disabled={!!working} onClick={() => { setSendingBack(true); setNote(''); }}>Send back with a note</button>
              </>
            ) : (
              <>
                <button type="button" className="btn primary" disabled={!!working || !note.trim()} onClick={() => decide('send_back')}>{working === 'send_back' ? 'Sending…' : 'Send back'}</button>
                <button type="button" className="btn ghost" disabled={!!working} onClick={() => { setSendingBack(false); setNote(''); }}>Cancel</button>
              </>
            )}
          </div>
          <span className="small muted">Approving lets the run continue from the workflow. Sending back runs the agent step before this one again with your note.</span>
        </div>
      ) : (
        <p className="small muted" style={{ margin: 0 }}>Only {wf.waitingOn || 'the assignee'} or a workspace admin can decide this step.</p>
      )}
    </div>
  );
}

function RunDetail({ run, workflowName, busyAction, onApprove, onDiscard, onRerun, onOpen }) {
  const host = REVIEW_HOSTS.find((h) => h.id === run.input.provider);
  const pending = run.actions.some((a) => a.status === 'pending');
  const heading = pending ? 'Waiting for your approval' : { done: 'Finished', failed: 'Failed', running: 'Running', queued: 'Queued' }[run.status] || run.status;
  return (
    <div className="col g16">
      <div className="row g12" style={{ justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div className="col g6" style={{ minWidth: 0 }}>
          <span className="eyebrow">{workflowName}{run.input.number ? ` · #${run.input.number}` : ''}</span>
          <h2 style={{ margin: 0, fontSize: 22 }}>{heading}</h2>
          <span className="small muted">
            {run.input.title ? `${run.input.title} · ` : ''}{run.input.project} · {run.source === 'webhook' ? 'started by a webhook' : 'started by hand'} {ago(run.createdAt)}
          </span>
        </div>
        <button type="button" className="btn" onClick={onOpen}>Open workflow</button>
      </div>
      {run.error && <span className="alert" role="alert">{run.error}</span>}
      {run.input.url && <a className="small" href={run.input.url} target="_blank" rel="noreferrer noopener">Open the {host?.word || 'request'}</a>}
      <ol className="timeline">
        {run.steps.map((s) => (
          <li key={s.stepId}>
            <span className="tl-dot done" aria-hidden="true">✓</span>
            <div className="col g6" style={{ flex: 1, minWidth: 0 }}>
              <strong>{s.title}</strong>
              <div className="output">{s.text}</div>
              {s.used?.length > 0 && <span className="small muted">Used: {s.used.map((u) => u.replace(/_/g, ' ')).join(', ')}</span>}
            </div>
          </li>
        ))}
        {run.actions.map((a) => (
          <li key={a.id}>
            <span className={`tl-dot ${a.status === 'pending' ? 'now' : 'done'}`} aria-hidden="true">{a.status === 'pending' ? '!' : '✓'}</span>
            <div style={{ flex: 1, minWidth: 0 }}><ReviewDraft a={a} busy={busyAction === a.id} onApprove={onApprove} onDiscard={onDiscard} /></div>
          </li>
        ))}
      </ol>
      {(run.status === 'done' || run.status === 'failed') && (
        <div><button type="button" className="btn sm" onClick={() => onRerun(run)}>Run again</button></div>
      )}
    </div>
  );
}

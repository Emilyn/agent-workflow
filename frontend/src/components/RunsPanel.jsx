import { useCallback, useEffect, useState } from 'react';
import { api } from '../api.js';

import { REVIEW_HOSTS } from './reviewHosts.js';

const when = (iso) => (iso ? new Date(iso).toLocaleString() : '');
const pillFor = (status) => ({ done: 'done', running: 'running', queued: 'queued', failed: 'waiting' }[status] || 'queued');

function ActionCard({ a, onApprove, onDiscard, busy }) {
  return (
    <div className="kv" data-testid="review-draft" style={{ gap: 8, padding: 12, border: '1px solid var(--line-2)', borderRadius: 12, background: 'var(--card)' }}>
      <div className="row g8" style={{ justifyContent: 'space-between' }}>
        <span className="lbl-s">{a.status === 'pending' ? 'Draft review' : 'Review'}</span>
        <span className={`pill ${a.status === 'posted' ? 'done' : a.status === 'pending' ? 'waiting' : 'queued'}`}>{a.status}</span>
      </div>
      <div style={{ whiteSpace: 'pre-wrap', fontSize: 14, lineHeight: 1.5 }}>{a.summary}</div>
      {a.comments.length > 0 && (
        <details className="disclose" open={a.status === 'pending'}>
          <summary>Inline comments ({a.comments.length})</summary>
          <div className="inner">
            {a.comments.map((c, i) => (
              <div key={i} className="small"><code>{c.path}:{c.line}</code><br />{c.body}</div>
            ))}
          </div>
        </details>
      )}
      {a.status === 'posted' && a.result && <a className="small" href={a.result} target="_blank" rel="noreferrer noopener">Open the posted review</a>}
      {a.status === 'failed' && <span className="alert" role="alert">Could not post: {a.result}</span>}
      {a.status === 'pending' && (
        <div className="row g8">
          <button type="button" className="btn sm primary" disabled={busy} onClick={() => onApprove(a)}>{busy ? 'Posting…' : 'Approve and post'}</button>
          <button type="button" className="btn sm" disabled={busy} onClick={() => onDiscard(a)}>Discard</button>
        </div>
      )}
    </div>
  );
}

function RunCard({ run, onRerun, onApprove, onDiscard, busyAction }) {
  const hasDraft = run.actions.some((a) => a.status === 'pending');
  const [open, setOpen] = useState(hasDraft);
  // a draft that arrives while the card is closed opens it, once
  useEffect(() => {
    if (hasDraft) setOpen(true);
  }, [hasDraft]);
  const host = REVIEW_HOSTS.find((h) => h.id === run.input.provider);
  return (
    <div className={`runcard${hasDraft ? ' attn' : ''}`} data-testid="run-card">
      <button type="button" className="run-head" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="run-title">#{run.input.number} {run.input.title}</span>
        <span className="run-meta">
          <span className={`pill ${pillFor(run.status)}`}>{run.status}</span>
          {hasDraft && <span className="tag">Draft waiting</span>}
          <span>{run.input.project}</span>
          <span>{run.source === 'webhook' ? 'Webhook' : 'By hand'}, {when(run.createdAt)}</span>
        </span>
      </button>
      {open && (
        <div className="run-body">
          {run.error && <span className="alert" role="alert">{run.error}</span>}
          {run.input.url && <a className="small" href={run.input.url} target="_blank" rel="noreferrer noopener">Open the {host?.word || 'request'}</a>}
          {run.steps.map((s) => (
            <div key={s.stepId} className="kv">
              <span className="lbl-s">{s.title}</span>
              <div className="output">{s.text}</div>
              {s.used?.length > 0 && <span className="small muted">Used: {s.used.map((u) => u.replace(/_/g, ' ')).join(', ')}</span>}
            </div>
          ))}
          {run.actions.map((a) => (
            <ActionCard key={a.id} a={a} busy={busyAction === a.id} onApprove={onApprove} onDiscard={onDiscard} />
          ))}
          {(run.status === 'done' || run.status === 'failed') && (
            <div><button type="button" className="btn sm" onClick={() => onRerun(run)}>Review again</button></div>
          )}
        </div>
      )}
    </div>
  );
}

export default function RunsPanel({ wsId, wfId, ints, onGoIntegrations, onManageTriggers }) {
  const [triggerCount, setTriggerCount] = useState(0);
  const [runs, setRuns] = useState([]);
  const [error, setError] = useState('');
  const [busyAction, setBusyAction] = useState('');
  const connected = REVIEW_HOSTS.filter((h) => ints?.[h.id]?.connected);
  const [manual, setManual] = useState({ provider: 'github', project: '', number: '' });
  const [working, setWorking] = useState('');

  const load = useCallback(async () => {
    try {
      const [t, r] = await Promise.all([api.triggers(wsId), api.runs(wsId, wfId)]);
      setTriggerCount(t.filter((x) => x.workflowId === wfId && x.enabled).length);
      setRuns(r);
    } catch (e) {
      if (e.status !== 401) setError(e.message);
    }
  }, [wsId, wfId]);

  useEffect(() => {
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [load]);

  // keep the provider choice on something that is connected
  useEffect(() => {
    if (connected.length && !connected.some((h) => h.id === manual.provider)) setManual((m) => ({ ...m, provider: connected[0].id }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ints]);

  const guard = (name, fn) => async (...args) => {
    setWorking(name);
    setError('');
    try {
      await fn(...args);
      await load();
    } catch (e) {
      setError(e.message);
    }
    setWorking('');
  };
  const startManual = guard('manual', async (e) => {
    e.preventDefault();
    await api.startRun(wsId, wfId, { provider: manual.provider, project: manual.project, number: Number(manual.number) });
    setManual((m) => ({ ...m, number: '' }));
  });
  const rerun = guard('rerun', (run) => api.rerun(wsId, run.id));
  const approve = async (a) => {
    setBusyAction(a.id);
    setError('');
    try {
      await api.approveAction(wsId, a.id);
    } catch (e) {
      setError(e.message);
    }
    setBusyAction('');
    load();
  };
  const discard = async (a) => {
    setError('');
    try {
      await api.discardAction(wsId, a.id);
    } catch (e) {
      setError(e.message);
    }
    load();
  };

  const hostOf = (id) => REVIEW_HOSTS.find((h) => h.id === id);
  return (
    <div className="panel tight">
      {error && <span className="alert" role="alert">{error}</span>}
      <div className="row g8" style={{ justifyContent: 'space-between' }}>
        <span className="small muted">
          {triggerCount > 0 ? `${triggerCount} trigger${triggerCount === 1 ? '' : 's'} watching for new requests.` : 'No triggers start this workflow yet.'}
        </span>
        <button type="button" className="btn sm" onClick={onManageTriggers}>Manage triggers</button>
      </div>

      {connected.length === 0 ? (
        <div className="empty">
          <span>Connect GitHub or GitLab, then give the reviewing step that tool.</span>
          <button type="button" className="btn sm" onClick={onGoIntegrations}>Open Integrations</button>
        </div>
      ) : (
        <details className="disclose">
          <summary>Review one request now</summary>
          <form className="inner" onSubmit={startManual} aria-label="Review one now">
            <div className="row g8">
              <select className="field" aria-label="Service" style={{ flex: '0 0 110px', height: 44 }} value={manual.provider} onChange={(e) => setManual({ ...manual, provider: e.target.value })}>
                {connected.map((h) => <option key={h.id} value={h.id}>{h.label}</option>)}
              </select>
              <input className="field" aria-label="Number" required inputMode="numeric" pattern="[0-9]+" style={{ flex: '0 0 84px', height: 44 }} placeholder="#" value={manual.number} onChange={(e) => setManual({ ...manual, number: e.target.value })} />
            </div>
            <input className="field" aria-label="Project" required style={{ height: 44 }} placeholder={hostOf(manual.provider)?.example} value={manual.project} onChange={(e) => setManual({ ...manual, project: e.target.value })} />
            <button type="submit" className="btn sm primary" style={{ alignSelf: 'flex-start' }} disabled={working === 'manual'}>{working === 'manual' ? 'Starting…' : 'Review it now'}</button>
            <span className="small muted">Manual runs always save a draft.</span>
          </form>
        </details>
      )}

      <div className="sect">
        <div className="sect-head"><span className="lbl-s">Recent runs</span></div>
        {runs.length === 0 ? (
          <div className="empty"><span>Nothing has run yet. A person approves each review before it is posted.</span></div>
        ) : (
          runs.map((r) => <RunCard key={r.id} run={r} onRerun={rerun} onApprove={approve} onDiscard={discard} busyAction={busyAction} />)
        )}
      </div>
    </div>
  );
}

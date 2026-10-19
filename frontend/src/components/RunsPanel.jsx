import { useCallback, useEffect, useState } from 'react';
import { api } from '../api.js';

const REVIEW_HOSTS = [
  { id: 'github', label: 'GitHub', example: 'owner/repo', word: 'pull request' },
  { id: 'gitlab', label: 'GitLab', example: 'group/project', word: 'merge request' },
];

const when = (iso) => (iso ? new Date(iso).toLocaleString() : '');
const pillFor = (status) => ({ done: 'done', running: 'running', queued: 'queued', failed: 'waiting' }[status] || 'queued');

function copy(text) {
  try {
    navigator.clipboard?.writeText(text);
  } catch {
    /* clipboard may be blocked; the value is visible anyway */
  }
}

function ReadOnlyValue({ label, value }) {
  return (
    <div className="col g6">
      <span className="lbl">{label}</span>
      <div className="row g8">
        <input className="field" readOnly aria-label={label} value={value} onFocus={(e) => e.target.select()} />
        <button type="button" className="btn" onClick={() => copy(value)}>Copy</button>
      </div>
    </div>
  );
}

function TriggerCard({ t, canManage, onChange, onDelete }) {
  const host = REVIEW_HOSTS.find((h) => h.id === t.provider);
  const registered = t.hookStatus === 'registered';
  return (
    <div className="runcard" data-testid="trigger-card" style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div className="row g8" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div className="col" style={{ minWidth: 0 }}>
          <strong style={{ fontSize: 14, overflowWrap: 'anywhere' }}>{t.project}</strong>
          <span className="small muted">{host.label} · new {host.word}s{t.onPush ? ' and pushes' : ''}</span>
        </div>
        <span className={`pill ${t.enabled ? 'done' : 'queued'}`}>{t.enabled ? 'On' : 'Off'}</span>
      </div>
      <div className="run-meta">
        <span>{t.reviewMode === 'draft' ? 'Reviews wait for approval' : 'Reviews post straight away'}</span>
        <span className={registered ? '' : 'alert'}>{registered ? 'Webhook registered' : 'Webhook needs adding by hand'}</span>
      </div>
      {!registered && t.hookError && <span className="small muted">Reason: {t.hookError}</span>}
      {canManage && (
        <>
          <details className="disclose">
            <summary>Webhook details</summary>
            <div className="inner">
              <ReadOnlyValue label="Payload URL" value={t.webhookUrl || ''} />
              <ReadOnlyValue label="Secret" value={t.secret || ''} />
              <span className="small muted">
                {t.provider === 'github'
                  ? 'GitHub: repository Settings → Webhooks → Add webhook. Content type application/json, event "Pull requests".'
                  : 'GitLab: project Settings → Webhooks. Put the secret in "Secret token" and tick "Merge request events".'}{' '}
                The address must be reachable from {host.label}.
              </span>
            </div>
          </details>
          <div className="row wrap g8">
            <button type="button" className="btn sm" onClick={() => onChange(t.id, { enabled: !t.enabled })}>{t.enabled ? 'Switch off' : 'Switch on'}</button>
            <button type="button" className="btn sm" onClick={() => onChange(t.id, { reviewMode: t.reviewMode === 'draft' ? 'post' : 'draft' })}>
              {t.reviewMode === 'draft' ? 'Post directly' : 'Save as drafts'}
            </button>
            <button type="button" className="btn sm danger" onClick={() => onDelete(t)}>Remove</button>
          </div>
        </>
      )}
    </div>
  );
}

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

export default function RunsPanel({ wsId, wfId, canManage, ints, onGoIntegrations }) {
  const [triggers, setTriggers] = useState([]);
  const [runs, setRuns] = useState([]);
  const [error, setError] = useState('');
  const [busyAction, setBusyAction] = useState('');
  const connected = REVIEW_HOSTS.filter((h) => ints?.[h.id]?.connected);

  const [form, setForm] = useState({ provider: 'github', project: '', reviewMode: 'draft', onPush: false });
  const [manual, setManual] = useState({ provider: 'github', project: '', number: '' });
  const [working, setWorking] = useState('');
  const [view, setView] = useState('activity');

  const load = useCallback(async () => {
    try {
      const [t, r] = await Promise.all([api.triggers(wsId), api.runs(wsId, wfId)]);
      setTriggers(t.filter((x) => x.workflowId === wfId));
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
    if (connected.length && !connected.some((h) => h.id === form.provider)) setForm((f) => ({ ...f, provider: connected[0].id }));
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

  const addTrigger = guard('trigger', async (e) => {
    e.preventDefault();
    await api.createTrigger(wsId, { workflowId: wfId, ...form });
    setForm((f) => ({ ...f, project: '' }));
  });
  const changeTrigger = guard('trigger', (id, body) => api.patchTrigger(wsId, id, body));
  const removeTrigger = guard('trigger', async (t) => {
    if (window.confirm(`Stop reviewing ${t.project}? The webhook is removed at ${t.provider === 'github' ? 'GitHub' : 'GitLab'} when possible.`)) await api.deleteTrigger(wsId, t.id);
  });
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
  const waiting = runs.filter((r) => r.actions.some((x) => x.status === 'pending')).length;

  return (
    <>
      <div className="subnav" role="tablist" aria-label="Runs sections">
        <button type="button" role="tab" aria-selected={view === 'activity'} className={view === 'activity' ? 'on' : ''} onClick={() => setView('activity')}>
          Activity{waiting > 0 && <span className="count" aria-label={`${waiting} drafts waiting`}>{waiting}</span>}
        </button>
        <button type="button" role="tab" aria-selected={view === 'triggers'} className={view === 'triggers' ? 'on' : ''} onClick={() => setView('triggers')}>
          Triggers{triggers.length > 0 && <span className="count" style={{ background: 'var(--muted)' }}>{triggers.length}</span>}
        </button>
      </div>
      <div className="panel tight">
        {error && <span className="alert" role="alert">{error}</span>}

        {connected.length === 0 && (
          <div className="empty">
            <span>Connect GitHub or GitLab, then give the reviewing step that tool.</span>
            <button type="button" className="btn sm" onClick={onGoIntegrations}>Open Integrations</button>
          </div>
        )}

        {view === 'activity' && (
          <>
            <span className="small muted">Every new pull or merge request that a trigger watches shows up here. A person approves each review before it is posted.</span>
            {connected.length > 0 && (
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
                <div className="empty">
                  <span>Nothing has run yet.</span>
                  {connected.length > 0 && triggers.length === 0 && <button type="button" className="btn sm" onClick={() => setView('triggers')}>Add a trigger</button>}
                </div>
              ) : (
                runs.map((r) => <RunCard key={r.id} run={r} onRerun={rerun} onApprove={approve} onDiscard={discard} busyAction={busyAction} />)
              )}
            </div>
          </>
        )}

        {view === 'triggers' && (
          <>
            <span className="small muted">A trigger starts this workflow for every new pull or merge request in one project.</span>
            <div className="sect">
              {triggers.length === 0 && connected.length > 0 && <div className="empty">No triggers yet.</div>}
              {triggers.map((t) => <TriggerCard key={t.id} t={t} canManage={canManage} onChange={changeTrigger} onDelete={removeTrigger} />)}
            </div>
            {connected.length > 0 && (canManage ? (
              <details className="disclose" open={triggers.length === 0}>
                <summary>Add a trigger</summary>
                <form className="inner" onSubmit={addTrigger} aria-label="Add a trigger">
                  <label className="col g6">
                    <span className="small">Service</span>
                    <select className="field" style={{ height: 44 }} value={form.provider} onChange={(e) => setForm({ ...form, provider: e.target.value })}>
                      {connected.map((h) => <option key={h.id} value={h.id}>{h.label}</option>)}
                    </select>
                  </label>
                  <label className="col g6">
                    <span className="small">Project</span>
                    <input className="field" style={{ height: 44 }} required placeholder={hostOf(form.provider)?.example} value={form.project} onChange={(e) => setForm({ ...form, project: e.target.value })} />
                  </label>
                  <label className="col g6">
                    <span className="small">What happens with the review</span>
                    <select className="field" style={{ height: 44 }} value={form.reviewMode} onChange={(e) => setForm({ ...form, reviewMode: e.target.value })}>
                      <option value="draft">Save a draft for approval (recommended)</option>
                      <option value="post">Post it straight away</option>
                    </select>
                  </label>
                  <label className="row g8">
                    <input type="checkbox" checked={form.onPush} onChange={(e) => setForm({ ...form, onPush: e.target.checked })} />
                    <span className="small">Review again when new commits are pushed</span>
                  </label>
                  <button type="submit" className="btn sm primary" style={{ alignSelf: 'flex-start' }} disabled={working === 'trigger'}>{working === 'trigger' ? 'Adding…' : 'Add trigger'}</button>
                </form>
              </details>
            ) : (
              <span className="small muted">Only workspace admins can add triggers.</span>
            ))}
          </>
        )}
      </div>
    </>
  );
}

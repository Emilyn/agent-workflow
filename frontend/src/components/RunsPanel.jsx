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
  return (
    <div className="box col g8" data-testid="trigger-card">
      <div className="row wrap g8" style={{ justifyContent: 'space-between' }}>
        <strong>{host.label} · {t.project}</strong>
        <span className={`pill ${t.enabled ? 'done' : 'queued'}`}>{t.enabled ? 'On' : 'Off'}</span>
      </div>
      <span className="small muted">
        Every new {host.word}{t.onPush ? ' and every new push' : ''} starts this workflow.{' '}
        {t.reviewMode === 'draft' ? 'The review is saved as a draft for a person to approve.' : 'The review is posted straight away.'}
      </span>
      <span className="small">
        {t.hookStatus === 'registered'
          ? `Webhook registered at ${host.label}.`
          : `Webhook not registered automatically${t.hookError ? ` (${t.hookError})` : ''}. Add it by hand with the details below.`}
      </span>
      {canManage && (
        <>
          <details>
            <summary className="small">Webhook details</summary>
            <div className="col g8" style={{ marginTop: 8 }}>
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
            <button type="button" className="btn" onClick={() => onChange(t.id, { enabled: !t.enabled })}>{t.enabled ? 'Switch off' : 'Switch on'}</button>
            <button type="button" className="btn" onClick={() => onChange(t.id, { reviewMode: t.reviewMode === 'draft' ? 'post' : 'draft' })}>
              {t.reviewMode === 'draft' ? 'Post directly' : 'Save as drafts'}
            </button>
            <button type="button" className="btn danger" onClick={() => onDelete(t)}>Remove</button>
          </div>
        </>
      )}
    </div>
  );
}

function ActionCard({ a, onApprove, onDiscard, busy }) {
  return (
    <div className="box col g8" data-testid="review-draft">
      <div className="row wrap g8" style={{ justifyContent: 'space-between' }}>
        <strong>{a.status === 'pending' ? 'Draft review' : 'Review'}</strong>
        <span className={`pill ${a.status === 'posted' ? 'done' : a.status === 'pending' ? 'waiting' : 'queued'}`}>{a.status}</span>
      </div>
      <div className="output">{a.summary}</div>
      {a.comments.length > 0 && (
        <div className="col g6">
          <span className="lbl">Inline comments ({a.comments.length})</span>
          {a.comments.map((c, i) => (
            <div key={i} className="small"><code>{c.path}:{c.line}</code> {c.body}</div>
          ))}
        </div>
      )}
      {a.status === 'posted' && a.result && <a className="small" href={a.result} target="_blank" rel="noreferrer noopener">Open the posted review</a>}
      {a.status === 'failed' && <span className="alert" role="alert">Could not post: {a.result}</span>}
      {a.status === 'pending' && (
        <div className="row g8">
          <button type="button" className="btn primary" disabled={busy} onClick={() => onApprove(a)}>{busy ? 'Posting…' : 'Approve and post'}</button>
          <button type="button" className="btn" disabled={busy} onClick={() => onDiscard(a)}>Discard</button>
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
    <div className="box col g8" data-testid="run-card">
      <button type="button" className="link-btn" style={{ textAlign: 'left' }} aria-expanded={open} onClick={() => setOpen(!open)}>
        <strong>#{run.input.number} {run.input.title}</strong>
        <span className="small muted"> · {run.input.project}</span>
      </button>
      <div className="row wrap g8">
        <span className={`pill ${pillFor(run.status)}`}>{run.status}</span>
        <span className="small muted">{run.source === 'webhook' ? 'Webhook' : 'Started by hand'} · {when(run.createdAt)}</span>
        {hasDraft && <span className="tag">Draft waiting</span>}
      </div>
      {run.error && <span className="alert" role="alert">{run.error}</span>}
      {open && (
        <div className="col g8">
          {run.input.url && <a className="small" href={run.input.url} target="_blank" rel="noreferrer noopener">Open the {host?.word || 'request'}</a>}
          {run.steps.map((s) => (
            <div key={s.stepId} className="col g6">
              <span className="lbl">{s.title}</span>
              <div className="output">{s.text}</div>
              {s.used?.length > 0 && <span className="small muted">Used: {s.used.map((u) => u.replace(/_/g, ' ')).join(', ')}</span>}
            </div>
          ))}
          {run.actions.map((a) => (
            <ActionCard key={a.id} a={a} busy={busyAction === a.id} onApprove={onApprove} onDiscard={onDiscard} />
          ))}
          {(run.status === 'done' || run.status === 'failed') && (
            <div><button type="button" className="btn" onClick={() => onRerun(run)}>Review again</button></div>
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
  return (
    <div className="panel">
      <div className="col g6">
        <span className="serif" style={{ fontSize: 28 }}>Runs and triggers</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Start this workflow automatically for every new pull request or merge request, or try it on one by hand. The agent reads the changes and writes a review; by default a person approves it before it is posted.
        </span>
      </div>
      {error && <span className="alert" role="alert">{error}</span>}

      {connected.length === 0 ? (
        <div className="box col g8">
          <span className="small">Connect GitHub or GitLab first, then add the tool to the reviewing step.</span>
          <button type="button" className="btn" onClick={onGoIntegrations}>Open Integrations</button>
        </div>
      ) : (
        <>
          <div className="col g8">
            <span className="lbl">Triggers</span>
            {triggers.length === 0 && <span className="small muted">No triggers yet.</span>}
            {triggers.map((t) => <TriggerCard key={t.id} t={t} canManage={canManage} onChange={changeTrigger} onDelete={removeTrigger} />)}
          </div>

          {canManage ? (
            <form className="box col g8" onSubmit={addTrigger} aria-label="Add a trigger">
              <span className="lbl">Add a trigger</span>
              <div className="row g8 wrap">
                <label className="col g6" style={{ flex: 1, minWidth: 120 }}>
                  <span className="small">Service</span>
                  <select className="field" value={form.provider} onChange={(e) => setForm({ ...form, provider: e.target.value })}>
                    {connected.map((h) => <option key={h.id} value={h.id}>{h.label}</option>)}
                  </select>
                </label>
                <label className="col g6" style={{ flex: 2, minWidth: 160 }}>
                  <span className="small">Project</span>
                  <input className="field" required placeholder={hostOf(form.provider)?.example} value={form.project} onChange={(e) => setForm({ ...form, project: e.target.value })} />
                </label>
              </div>
              <label className="col g6">
                <span className="small">What happens with the review</span>
                <select className="field" value={form.reviewMode} onChange={(e) => setForm({ ...form, reviewMode: e.target.value })}>
                  <option value="draft">Save as a draft; a person approves it (recommended)</option>
                  <option value="post">Post it straight away</option>
                </select>
              </label>
              <label className="row g8">
                <input type="checkbox" checked={form.onPush} onChange={(e) => setForm({ ...form, onPush: e.target.checked })} />
                <span className="small">Also review again when new commits are pushed</span>
              </label>
              <button type="submit" className="btn primary" disabled={working === 'trigger'}>{working === 'trigger' ? 'Adding…' : 'Add trigger'}</button>
            </form>
          ) : (
            <span className="small muted">Only workspace admins can add triggers.</span>
          )}

          <form className="box col g8" onSubmit={startManual} aria-label="Review one now">
            <span className="lbl">Try it on one request</span>
            <div className="row g8 wrap">
              <select className="field" aria-label="Service" style={{ flex: 1, minWidth: 110 }} value={manual.provider} onChange={(e) => setManual({ ...manual, provider: e.target.value })}>
                {connected.map((h) => <option key={h.id} value={h.id}>{h.label}</option>)}
              </select>
              <input className="field" aria-label="Project" required style={{ flex: 2, minWidth: 140 }} placeholder={hostOf(manual.provider)?.example} value={manual.project} onChange={(e) => setManual({ ...manual, project: e.target.value })} />
              <input className="field" aria-label="Number" required inputMode="numeric" pattern="[0-9]+" style={{ width: 90 }} placeholder="#" value={manual.number} onChange={(e) => setManual({ ...manual, number: e.target.value })} />
            </div>
            <button type="submit" className="btn" disabled={working === 'manual'}>{working === 'manual' ? 'Starting…' : 'Review it now'}</button>
            <span className="small muted">Manual runs always save a draft.</span>
          </form>
        </>
      )}

      <div className="col g8">
        <span className="lbl">Recent runs</span>
        {runs.length === 0 && <span className="small muted">Nothing has run yet.</span>}
        {runs.map((r) => <RunCard key={r.id} run={r} onRerun={rerun} onApprove={approve} onDiscard={discard} busyAction={busyAction} />)}
      </div>
    </div>
  );
}

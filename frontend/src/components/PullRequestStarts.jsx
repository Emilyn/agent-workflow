import { useCallback, useEffect, useState } from 'react';
import { api } from '../api.js';
import { REVIEW_HOSTS, copy } from './reviewHosts.js';

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

function TriggerCard({ t, canManage, onChange, onDelete, confirming, onKeep }) {
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
            <button type="button" className="btn sm danger" onClick={() => onDelete(t)}>{confirming ? `Stop reviewing ${t.project}` : 'Remove'}</button>
            {confirming && <button type="button" className="btn sm ghost" onClick={onKeep}>Keep it</button>}
          </div>
        </>
      )}
    </div>
  );
}

// Starting this workflow on new pull or merge requests, and reviewing one by hand.
export default function PullRequestStarts({ wsId, wfId, canManage, ints, onGoIntegrations }) {
  const [triggers, setTriggers] = useState([]);
  const [error, setError] = useState('');
  const [working, setWorking] = useState('');
  const [confirm, setConfirm] = useState('');
  const connected = REVIEW_HOSTS.filter((h) => ints?.[h.id]?.connected);
  const [form, setForm] = useState({ provider: 'github', project: '', reviewMode: 'draft', onPush: false });
  const [manual, setManual] = useState({ provider: 'github', project: '', number: '' });

  const load = useCallback(async () => {
    try {
      setTriggers((await api.triggers(wsId)).filter((t) => t.workflowId === wfId));
    } catch (e) {
      if (e.status !== 401) setError(e.message);
    }
  }, [wsId, wfId]);
  useEffect(() => { load(); }, [load]);

  // keep the service choices on something that is connected
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
  const add = guard('add', async (e) => {
    e.preventDefault();
    await api.createTrigger(wsId, { ...form, workflowId: wfId });
    setForm((f) => ({ ...f, project: '' }));
  });
  const change = guard('change', (id, body) => api.patchTrigger(wsId, id, body));
  const remove = guard('remove', async (t) => {
    await api.deleteTrigger(wsId, t.id);
    setConfirm('');
  });
  const startManual = guard('manual', async (e) => {
    e.preventDefault();
    await api.startRun(wsId, wfId, { provider: manual.provider, project: manual.project, number: Number(manual.number) });
    setManual((m) => ({ ...m, number: '' }));
  });
  const hostOf = (id) => REVIEW_HOSTS.find((h) => h.id === id);

  if (connected.length === 0 && triggers.length === 0) {
    return (
      <div className="empty">
        <span>Connect GitHub or GitLab to start this workflow on new pull or merge requests, and give its reviewing step that tool.</span>
        <button type="button" className="btn sm" onClick={onGoIntegrations}>Open Integrations</button>
      </div>
    );
  }
  return (
    <div className="col g12">
      {error && <span className="alert" role="alert">{error}</span>}
      {triggers.map((t) => (
        <div key={t.id}>
          <TriggerCard t={t} canManage={canManage} onChange={change} onDelete={(x) => (confirm === x.id ? remove(x) : setConfirm(x.id))} confirming={confirm === t.id} onKeep={() => setConfirm('')} />
        </div>
      ))}
      {connected.length > 0 && (canManage ? (
        <details className="disclose" open={triggers.length === 0}>
          <summary>Add a pull or merge request trigger</summary>
          <form className="inner" onSubmit={add} aria-label="Add a trigger">
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
            <button type="submit" className="btn sm primary" style={{ alignSelf: 'flex-start' }} disabled={working === 'add'}>{working === 'add' ? 'Adding…' : 'Add trigger'}</button>
          </form>
        </details>
      ) : (
        <span className="small muted">Only workspace admins can add triggers.</span>
      ))}
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
            <span className="small muted">Manual runs always save a draft. Find the result on the Runs page.</span>
          </form>
        </details>
      )}
    </div>
  );
}

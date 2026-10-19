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

function TriggerCard({ t, workflowName, canManage, onChange, onDelete }) {
  const host = REVIEW_HOSTS.find((h) => h.id === t.provider);
  const registered = t.hookStatus === 'registered';
  return (
    <div className="runcard" data-testid="trigger-card" style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div className="row g8" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div className="col" style={{ minWidth: 0 }}>
          <strong style={{ fontSize: 14, overflowWrap: 'anywhere' }}>{t.project}</strong>
          <span className="small muted">{host.label} · new {host.word}s{t.onPush ? ' and pushes' : ''}</span>
          <span className="small muted">Starts: {workflowName}</span>
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


// All review triggers of the workspace, and a form to add one.
export default function TriggersSection({ wsId, canManage, ints, workflows, currentWorkflowId, onGoIntegrations }) {
  const [triggers, setTriggers] = useState([]);
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const connected = REVIEW_HOSTS.filter((h) => ints?.[h.id]?.connected);
  const [form, setForm] = useState({ provider: 'github', workflowId: currentWorkflowId || '', project: '', reviewMode: 'draft', onPush: false });

  const load = useCallback(async () => {
    try {
      setTriggers(await api.triggers(wsId));
    } catch (e) {
      if (e.status !== 401) setError(e.message);
    }
  }, [wsId]);
  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    if (connected.length && !connected.some((h) => h.id === form.provider)) setForm((f) => ({ ...f, provider: connected[0].id }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ints]);
  useEffect(() => {
    if (!form.workflowId && workflows[0]) setForm((f) => ({ ...f, workflowId: currentWorkflowId || workflows[0].id }));
  }, [workflows, currentWorkflowId, form.workflowId]);

  const guard = (fn) => async (...args) => {
    setWorking(true);
    setError('');
    try {
      await fn(...args);
      await load();
    } catch (e) {
      setError(e.message);
    }
    setWorking(false);
  };
  const add = guard(async (e) => {
    e.preventDefault();
    await api.createTrigger(wsId, form);
    setForm((f) => ({ ...f, project: '' }));
  });
  const change = guard((id, body) => api.patchTrigger(wsId, id, body));
  const remove = guard(async (t) => {
    if (window.confirm(`Stop reviewing ${t.project}? The webhook is removed at ${t.provider === 'github' ? 'GitHub' : 'GitLab'} when possible.`)) await api.deleteTrigger(wsId, t.id);
  });
  const nameOf = (id) => workflows.find((w) => w.id === id)?.name || 'a workflow';
  const hostOf = (id) => REVIEW_HOSTS.find((h) => h.id === id);

  return (
    <div className="col g16">
      {error && <span className="alert" role="alert">{error}</span>}
      {connected.length === 0 && (
        <div className="empty">
          <span>Connect GitHub or GitLab first. Then give the reviewing step that tool.</span>
          <button type="button" className="btn sm" onClick={onGoIntegrations}>Open Integrations</button>
        </div>
      )}
      <div className="sect">
        {triggers.length === 0 && connected.length > 0 && <div className="empty">No triggers yet.</div>}
        {triggers.map((t) => <TriggerCard key={t.id} t={t} workflowName={nameOf(t.workflowId)} canManage={canManage} onChange={change} onDelete={remove} />)}
      </div>
      {connected.length > 0 && (canManage ? (
        <details className="disclose" open={triggers.length === 0}>
          <summary>Add a trigger</summary>
          <form className="inner" onSubmit={add} aria-label="Add a trigger">
            <label className="col g6">
              <span className="small">Workflow to start</span>
              <select className="field" style={{ height: 44 }} value={form.workflowId} onChange={(e) => setForm({ ...form, workflowId: e.target.value })}>
                {workflows.map((w) => <option key={w.id} value={w.id}>{w.name}</option>)}
              </select>
            </label>
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
            <button type="submit" className="btn sm primary" style={{ alignSelf: 'flex-start' }} disabled={working}>{working ? 'Adding…' : 'Add trigger'}</button>
          </form>
        </details>
      ) : (
        <span className="small muted">Only workspace admins can add triggers.</span>
      ))}
    </div>
  );
}

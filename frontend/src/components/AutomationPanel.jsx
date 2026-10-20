import { useCallback, useEffect, useState } from 'react';
import { api } from '../api.js';
import { ago } from './HomePage.jsx';

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const STATUS = { queued: ['queued', 'Waiting its turn'], running: ['running', 'Running'], waiting: ['waiting', 'Waiting for a person'], done: ['done', 'Done'], failed: ['waiting', 'Failed'], skipped: ['queued', 'Skipped'] };
const SOURCE = { schedule: 'Schedule', webhook: 'Webhook', manual: 'Started by hand' };

const zones = () => {
  try {
    const all = Intl.supportedValuesOf('timeZone');
    return all.includes('UTC') ? all : ['UTC', ...all];
  } catch {
    return ['UTC'];
  }
};
const localZone = () => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
};
const ordinal = (n) => `${n}${[, 'st', 'nd', 'rd'][(n % 100 >> 3) ^ 1 && n % 10] || 'th'}`;

export function describe(sc) {
  const when = `${sc.atTime} (${sc.timezone})`;
  if (sc.freq === 'daily') return `Every day at ${when}`;
  if (sc.freq === 'monthly') return `On the ${ordinal(sc.dayOfMonth)} of every month at ${when}`;
  const days = DAYS.filter((_, i) => sc.weekdays & (1 << i));
  return `Every ${days.length === 7 ? 'day' : days.join(', ')} at ${when}`;
}

const stamp = (iso) => (iso ? new Date(iso).toLocaleString([], { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : '');

// Everything that can start this workflow without someone pressing Run, and what happened to recent starts.
export default function AutomationPanel({ wsId, wfId, canManage, auto, onChanged }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [working, setWorking] = useState('');
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    try {
      setData(await api.automation(wsId, wfId));
    } catch (e) {
      if (e.status !== 401) setError(e.message);
    }
  }, [wsId, wfId]);
  useEffect(() => {
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, 5000);
    return () => clearInterval(t);
  }, [load]);

  const act = (name, fn) => async (...args) => {
    setWorking(name);
    setError('');
    try {
      const next = await fn(...args);
      if (next) setData(next);
      onChanged?.();
    } catch (e) {
      setError(e.message);
    }
    setWorking('');
  };
  const runNow = act('run', () => api.startInBackground(wsId, wfId));
  const toggleSchedule = act('schedule', (sc) => api.setSchedule(wsId, wfId, sc.id, !sc.enabled));
  const removeSchedule = act('schedule', (sc) => api.deleteSchedule(wsId, wfId, sc.id));
  const createHook = act('hook', () => api.createHook(wsId, wfId));
  const toggleHook = act('hook', () => api.setHook(wsId, wfId, !data.hook.enabled));
  const removeHook = act('hook', () => api.deleteHook(wsId, wfId));
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(data.hook.url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setError('Could not copy. Select the address and copy it by hand.');
    }
  };

  if (!data) return <div className="panel tight"><span className="muted small">{error || 'Loading…'}</span></div>;
  const hook = data.hook;
  const absolute = hook.url?.startsWith('http') ? hook.url : hook.url ? `${window.location.origin}${hook.url}` : '';

  return (
    <div className="panel tight" data-testid="automation">
      {error && <span className="alert" role="alert">{error}</span>}

      <div className="sect">
        <div className="sect-head"><span className="lbl-s">Status</span></div>
        <span className="small">
          {auto?.status === 'running' && 'Running on its own right now.'}
          {auto?.status === 'waiting' && 'Paused, waiting for a person. Decide it in Runs.'}
          {!auto?.status && 'Not running. Starts happen when a schedule fires, a webhook arrives, or you start it below.'}
          {auto?.queued > 0 && ` ${auto.queued} more ${auto.queued === 1 ? 'is' : 'are'} waiting for it to be free.`}
        </span>
        <div><button type="button" className="btn sm" disabled={working === 'run'} onClick={runNow}>{working === 'run' ? 'Queuing…' : 'Run in the background now'}</button></div>
        <span className="small muted">Runs the agent steps on the server, pauses at steps for people, and carries on once they decide.</span>
      </div>

      <div className="sect">
        <div className="sect-head"><span className="lbl-s">Schedules</span></div>
        {data.schedules.length === 0 && <div className="empty"><span>No schedule yet.</span></div>}
        {data.schedules.map((sc) => (
          <div key={sc.id} className="runcard" data-testid="schedule">
            <div className="run-head" style={{ cursor: 'default' }}>
              <span className="run-title">{describe(sc)}</span>
              <span className="run-meta">
                <span className={`pill ${sc.enabled ? 'done' : 'queued'}`}>{sc.enabled ? 'On' : 'Off'}</span>
                {sc.enabled && sc.nextRunAt && <span>Next: {stamp(sc.nextRunAt)}</span>}
                {sc.lastRunAt && <span>Last: {ago(sc.lastRunAt)}</span>}
              </span>
              {canManage && (
                <span className="row g8">
                  <button type="button" className="btn sm" disabled={working === 'schedule'} onClick={() => toggleSchedule(sc)}>{sc.enabled ? 'Turn off' : 'Turn on'}</button>
                  <button type="button" className="btn sm danger" disabled={working === 'schedule'} onClick={() => removeSchedule(sc)}>Delete</button>
                </span>
              )}
            </div>
          </div>
        ))}
        {canManage ? <ScheduleForm onCreate={act('schedule', (body) => api.createSchedule(wsId, wfId, body))} busy={working === 'schedule'} /> : <span className="small muted">Only workspace admins can add schedules.</span>}
      </div>

      <div className="sect">
        <div className="sect-head"><span className="lbl-s">Incoming webhook</span></div>
        {!hook.exists ? (
          canManage ? (
            <div className="col g8">
              <span className="small muted">Give this workflow a private address. Anything that can send a web request (a form, Zapier, a CI job) can start it, and the text it sends reaches the first step.</span>
              <div><button type="button" className="btn sm" disabled={working === 'hook'} onClick={createHook}>Create address</button></div>
            </div>
          ) : <div className="empty"><span>No webhook address. An admin can create one.</span></div>
        ) : (
          <div className="col g8" data-testid="hook">
            <div className="row g8"><span className={`pill ${hook.enabled ? 'done' : 'queued'}`}>{hook.enabled ? 'On' : 'Off'}</span><span className="small muted">Send a POST with the text as the body.</span></div>
            {canManage ? (
              <>
                <div className="row g8">
                  <input className="field" readOnly aria-label="Webhook address" value={absolute} onFocus={(e) => e.target.select()} style={{ height: 44, fontSize: 12 }} />
                  <button type="button" className="btn sm" onClick={copy}>{copied ? 'Copied' : 'Copy'}</button>
                </div>
                <details className="disclose">
                  <summary>Try it from a terminal</summary>
                  <div className="inner"><code style={{ wordBreak: 'break-all', display: 'block' }}>curl -X POST --data &quot;New order from Jo&quot; {absolute}</code></div>
                </details>
                <div className="row g8 wrap">
                  <button type="button" className="btn sm" disabled={working === 'hook'} onClick={toggleHook}>{hook.enabled ? 'Switch off' : 'Switch on'}</button>
                  <button type="button" className="btn sm" disabled={working === 'hook'} onClick={createHook}>Replace address</button>
                  <button type="button" className="btn sm danger" disabled={working === 'hook'} onClick={removeHook}>Remove</button>
                </div>
                <span className="small muted">The address is the password: anyone who has it can start this workflow. Replacing it stops the old one. Text it sends is treated as data, never as instructions.</span>
              </>
            ) : <span className="small muted">Only workspace admins can see the address.</span>}
          </div>
        )}
      </div>

      <div className="sect">
        <div className="sect-head"><span className="lbl-s">Recent automatic runs</span></div>
        {data.starts.length === 0 ? <div className="empty"><span>Nothing has started on its own yet.</span></div> : data.starts.map((st) => {
          const [pill, label] = STATUS[st.status] || ['queued', st.status];
          return (
            <div key={st.id} className="kv" data-testid="start" style={{ gap: 4, padding: '8px 0', borderBottom: '1px solid var(--line)' }}>
              <div className="row g8" style={{ justifyContent: 'space-between' }}>
                <span className="small"><strong>{SOURCE[st.source] || st.source}</strong> · {ago(st.createdAt)}</span>
                <span className={`pill ${pill}`}>{label}</span>
              </div>
              {st.error && <span className={st.status === 'failed' ? 'alert' : 'small muted'}>{st.error}</span>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ScheduleForm({ onCreate, busy }) {
  const [freq, setFreq] = useState('weekly');
  const [atTime, setAtTime] = useState('09:00');
  const [weekdays, setWeekdays] = useState(1 << 1);
  const [dom, setDom] = useState(1);
  const [tz, setTz] = useState(localZone());
  const [error, setError] = useState('');
  const toggleDay = (i) => setWeekdays((m) => m ^ (1 << i));

  const submit = async (e) => {
    e.preventDefault();
    if (freq === 'weekly' && weekdays === 0) { setError('Choose at least one day.'); return; }
    setError('');
    await onCreate({ freq, atTime, weekdays: freq === 'weekly' ? weekdays : 0, dayOfMonth: freq === 'monthly' ? Number(dom) : 1, timezone: tz });
  };
  return (
    <details className="disclose">
      <summary>Add a schedule</summary>
      <form className="inner" onSubmit={submit}>
        <div className="row g8 wrap">
          <div className="col g6" style={{ flex: '1 1 120px' }}>
            <label className="lbl" htmlFor="sc-freq">Repeats</label>
            <select id="sc-freq" className="field" style={{ height: 44 }} value={freq} onChange={(e) => setFreq(e.target.value)}>
              <option value="daily">Every day</option>
              <option value="weekly">Every week</option>
              <option value="monthly">Every month</option>
            </select>
          </div>
          <div className="col g6" style={{ flex: '1 1 110px' }}>
            <label className="lbl" htmlFor="sc-time">At</label>
            <input id="sc-time" className="field" style={{ height: 44 }} type="time" required value={atTime} onChange={(e) => setAtTime(e.target.value)} />
          </div>
        </div>
        {freq === 'weekly' && (
          <div role="group" aria-label="Days of the week" className="row g8 wrap">
            {DAYS.map((d, i) => (
              <button key={d} type="button" className={`filter${weekdays & (1 << i) ? ' on' : ''}`} aria-pressed={!!(weekdays & (1 << i))} onClick={() => toggleDay(i)}>{d}</button>
            ))}
          </div>
        )}
        {freq === 'monthly' && (
          <div className="col g6" style={{ maxWidth: 160 }}>
            <label className="lbl" htmlFor="sc-dom">Day of the month</label>
            <input id="sc-dom" className="field" style={{ height: 44 }} type="number" min="1" max="31" value={dom} onChange={(e) => setDom(e.target.value)} />
            {Number(dom) > 28 && <span className="small muted">Shorter months run on their last day.</span>}
          </div>
        )}
        <div className="col g6">
          <label className="lbl" htmlFor="sc-tz">Time zone</label>
          <select id="sc-tz" className="field" style={{ height: 44 }} value={tz} onChange={(e) => setTz(e.target.value)}>
            {[...new Set([tz, ...zones()])].map((z) => <option key={z} value={z}>{z}</option>)}
          </select>
        </div>
        {error && <span className="alert" role="alert">{error}</span>}
        <button type="submit" className="btn sm primary" style={{ alignSelf: 'flex-start' }} disabled={busy}>{busy ? 'Saving…' : 'Add schedule'}</button>
      </form>
    </details>
  );
}

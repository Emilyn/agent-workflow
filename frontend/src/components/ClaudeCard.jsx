import { useState } from 'react';
import { Spark } from './Icons.jsx';

// How agents run: with an Anthropic API key (the default), or, when the person running the server
// turned it on, with the Claude Code installed on that server.
export default function ClaudeCard({ claude, canManage, onConnect, onDisconnect, onMode, onCodeSettings, onRefresh }) {
  const [key, setKey] = useState('');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);

  const connect = async (e) => {
    e.preventDefault();
    if (!key.trim()) {
      setError('Paste your API key first.');
      return;
    }
    setWorking(true);
    setError('');
    try {
      await onConnect(key.trim());
      setKey('');
    } catch (err) {
      setError(err.message);
    } finally {
      setWorking(false);
    }
  };

  const disconnect = async () => {
    setWorking(true);
    setError('');
    try {
      await onDisconnect();
    } catch (err) {
      setError(err.message);
    } finally {
      setWorking(false);
    }
  };

  const switchMode = async (mode) => {
    if (mode === claude?.mode) return;
    setWorking(true);
    setError('');
    try {
      await onMode(mode);
    } catch (err) {
      setError(err.message);
    } finally {
      setWorking(false);
    }
  };
  const recheck = async () => {
    setWorking(true);
    setError('');
    try {
      await onRefresh();
    } catch (err) {
      setError(err.message);
    } finally {
      setWorking(false);
    }
  };

  const code = claude?.claudeCode;
  const codeMode = claude?.mode === 'claude_code';
  const account = code?.account;
  const on = claude?.connected;
  const fromServer = claude?.source === 'server' && !codeMode;

  return (
    <div className="int" data-testid="claude-card">
      <div className="row g12" style={{ alignItems: 'flex-start' }}>
        <span className="logo" style={{ width: 40, height: 40, background: 'var(--accent)' }}><Spark size={20} /></span>
        <div className="col g6" style={{ minWidth: 0, flex: 1 }}>
          <div className="row wrap g8">
            <strong style={{ fontSize: 16 }}>Claude</strong>
            <span className={`pill ${on ? 'done' : 'waiting'}`} style={{ padding: '2px 10px' }}>{on ? 'Connected' : 'Not connected'}</span>
          </div>
          <span className="small" style={{ color: 'var(--ink-2)' }}>
            {codeMode
              ? 'Agents run on the Claude Code installed on the server, with its login.'
              : 'Agents run on Claude. One key is shared by the whole workspace, and usage is billed to the Anthropic account it belongs to.'}
          </span>
        </div>
      </div>

      {code?.enabled && (
        <div className="seg" role="group" aria-label="How agents run">
          <button type="button" className={codeMode ? '' : 'on'} aria-pressed={!codeMode} disabled={working || !canManage} onClick={() => switchMode('api')}>API key</button>
          <button type="button" className={codeMode ? 'on' : ''} aria-pressed={codeMode} disabled={working || !canManage} onClick={() => switchMode('claude_code')}>Claude Code on this machine</button>
        </div>
      )}
      {!code?.enabled && canManage && (
        <span className="small muted">Want agents to run on the Claude Code installed on the server instead of an API key? The person running the server can turn that on with <code>CLAUDE_CODE_ENABLED=true</code>.</span>
      )}

      {codeMode ? (
        <div className="col g8" data-testid="claude-code">
          {account?.ok ? (
            <span className="ok">
              Agents run as <strong>{account.email || 'the account Claude Code is signed in to'}</strong>
              {account.method ? ` (${account.method}${account.plan ? `, ${account.plan}` : ''})` : ''}. Model: {claude.model}.
            </span>
          ) : (
            <span className="alert" role="alert">{account?.error || 'Claude Code is not ready on the server.'}</span>
          )}
          <span className="small muted">
            Each run starts the Claude Code installed on the server and signed in there, with only this app&rsquo;s tools (its own file, shell and web tools are off).
            Limits per run: {code.maxTurns} turns, ${code.maxBudgetUsd} spend, {code.timeoutSeconds} seconds{code.effort ? `, effort ${code.effort}` : ''}.
          </span>
          {canManage && <CodeSettings code={code} onSave={onCodeSettings} />}
          {error && <span className="alert" role="alert">{error}</span>}
          {canManage && <button className="btn outline" type="button" onClick={recheck} disabled={working}>{working ? 'Checking…' : 'Check sign-in again'}</button>}
        </div>
      ) : (
        <>
      {on && !fromServer && (
        <>
          <span className="ok">Connected{claude.keyHint ? ` with an API key ending ${claude.keyHint}` : ''}. Model: {claude.model}.</span>
          {canManage && <button className="btn outline block" type="button" onClick={disconnect} disabled={working}>Disconnect Claude</button>}
        </>
      )}
      {fromServer && (
        <span className="ok">Using the server&rsquo;s shared key. Model: {claude.model}.{canManage ? ' You can connect this workspace\u2019s own key instead.' : ''}</span>
      )}
      {!(on && !fromServer) && !canManage && !on && (
        <span className="small muted">Ask a workspace admin to connect an Anthropic API key. Until then, agent steps and replies are simulated.</span>
      )}
      {!(on && !fromServer) && canManage && (
        <form className="col g8" onSubmit={connect}>
          <label className="lbl" htmlFor="claude-key">Anthropic API key</label>
          <input
            id="claude-key"
            className="field"
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder="sk-ant-…"
            value={key}
            onChange={(e) => { setKey(e.target.value); setError(''); }}
          />
          {error && <span className="alert" role="alert">{error}</span>}
          <button className="btn primary block" type="submit" disabled={working} style={{ justifyContent: 'center' }}>
            {working ? 'Checking the key…' : 'Connect Claude'}
          </button>
          <span className="small muted">
            Create a key in the <a href="https://platform.claude.com/" target="_blank" rel="noreferrer">Claude Console</a>. The key is checked with Anthropic, stored encrypted on this app&rsquo;s server, and never shown again.
          </span>
        </form>
      )}

        </>
      )}

      <div className="box small" style={{ background: 'var(--paper)' }}>
        {codeMode ? (
          <><strong>Whose login is this?</strong> The one Claude Code is signed in with on the server. This app never sees it or asks you for it. Use this mode for your own work on a machine you run; do not use it to give other people access to a personal plan, and check your plan&rsquo;s terms before sharing it with a team.</>
        ) : (
          <><strong>Why not sign in with Claude Pro?</strong> A Claude Pro or Max plan covers Claude.ai, not the API, and this app cannot sign in with a Claude.ai account. Use an API key, or run agents on the Claude Code installed on your own machine.</>
        )}
      </div>
    </div>
  );
}

const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'];

// A workspace's own Claude Code settings. An empty box uses the server's default, shown as the hint.
function CodeSettings({ code, onSave }) {
  const custom = code.custom || {};
  const defaults = code.defaults || {};
  const initial = () => ({
    model: custom.model ?? '',
    effort: custom.effort ?? '',
    maxTurns: custom.maxTurns ?? '',
    maxBudgetUsd: custom.maxBudgetUsd ?? '',
    timeoutSeconds: custom.timeoutSeconds ?? '',
  });
  const [form, setForm] = useState(initial);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [working, setWorking] = useState(false);
  const set = (k) => (e) => { setForm({ ...form, [k]: e.target.value }); setSaved(false); setError(''); };

  const save = async (e) => {
    e.preventDefault();
    const num = (v) => (String(v).trim() === '' ? null : Number(v));
    setWorking(true);
    setError('');
    try {
      await onSave({
        model: form.model.trim() || null,
        effort: form.effort || null,
        maxTurns: num(form.maxTurns),
        maxBudgetUsd: num(form.maxBudgetUsd),
        timeoutSeconds: num(form.timeoutSeconds),
      });
      setSaved(true);
    } catch (err) {
      setError(err.message);
    } finally {
      setWorking(false);
    }
  };
  const reset = () => { setForm({ model: '', effort: '', maxTurns: '', maxBudgetUsd: '', timeoutSeconds: '' }); setSaved(false); };

  return (
    <form className="col g8" onSubmit={save} data-testid="claude-code-settings">
      <strong className="small">Settings for this workspace</strong>
      <label className="lbl" htmlFor="cc-model">Model</label>
      <input id="cc-model" className="field" value={form.model} onChange={set('model')} placeholder={defaults.model || 'server default'} spellCheck={false} autoComplete="off" />
      <label className="lbl" htmlFor="cc-effort">Effort</label>
      <select id="cc-effort" className="field" value={form.effort} onChange={set('effort')}>
        <option value="">{`Default${defaults.effort ? ` (${defaults.effort})` : ''}`}</option>
        {EFFORTS.map((x) => <option key={x} value={x}>{x}</option>)}
      </select>
      <label className="lbl" htmlFor="cc-turns">Turns per run</label>
      <input id="cc-turns" className="field" type="number" min="1" max="100" step="1" value={form.maxTurns} onChange={set('maxTurns')} placeholder={String(defaults.maxTurns ?? '')} />
      <label className="lbl" htmlFor="cc-budget">Spending cap per run (USD)</label>
      <input id="cc-budget" className="field" type="number" min="0.01" max="1000" step="any" value={form.maxBudgetUsd} onChange={set('maxBudgetUsd')} placeholder={String(defaults.maxBudgetUsd ?? '')} />
      <label className="lbl" htmlFor="cc-timeout">Time limit for background runs (seconds)</label>
      <input id="cc-timeout" className="field" type="number" min="30" max="3600" step="1" value={form.timeoutSeconds} onChange={set('timeoutSeconds')} placeholder={String(defaults.timeoutSeconds ?? '')} />
      <span className="small muted">Leave a box empty to use the server&rsquo;s default (shown in grey). Where Claude Code is installed is set by whoever runs the server.</span>
      {error && <span className="alert" role="alert">{error}</span>}
      {saved && <span className="ok">Saved.</span>}
      <div className="row g8">
        <button className="btn primary" type="submit" disabled={working}>{working ? 'Saving…' : 'Save settings'}</button>
        <button className="btn outline" type="button" onClick={reset} disabled={working}>Use defaults</button>
      </div>
    </form>
  );
}

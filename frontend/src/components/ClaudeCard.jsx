import { useState } from 'react';
import { Spark } from './Icons.jsx';

// How agents run: with an Anthropic API key (the default), or, when the person running the server
// turned it on, with the Claude Code installed on that server.
export default function ClaudeCard({ claude, canManage, onConnect, onDisconnect, onMode, onRefresh }) {
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
            Limits per run: {code.maxTurns} turns, ${code.maxBudgetUsd} spend{code.effort ? `, effort ${code.effort}` : ''}. The server&rsquo;s config sets these.
          </span>
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

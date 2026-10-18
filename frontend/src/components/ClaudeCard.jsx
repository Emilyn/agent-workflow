import { useState } from 'react';
import { Spark } from './Icons.jsx';

// Connects the user's own Anthropic API key. Claude.ai sign-in (Free, Pro or Max) is
// deliberately not offered: Anthropic does not allow third-party apps to use it.
export default function ClaudeCard({ claude, canManage, onConnect, onDisconnect }) {
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

  const on = claude?.connected;
  const fromServer = claude?.source === 'server';

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
            Agents run on Claude. One key is shared by the whole workspace, and usage is billed to the Anthropic account it belongs to.
          </span>
        </div>
      </div>

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

      <div className="box small" style={{ background: 'var(--paper)' }}>
        <strong>Why not sign in with Claude Pro?</strong> A Claude Pro or Max plan covers Claude.ai, not the API, and Anthropic does not allow other apps to sign in with Claude.ai accounts or use their credentials. An API key is the supported way to connect.
      </div>
    </div>
  );
}

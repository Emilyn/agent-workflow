import { useState } from 'react';
import { Spark } from './Icons.jsx';

// Connects the user's own Anthropic API key. Claude.ai sign-in (Free, Pro or Max) is
// deliberately not offered: Anthropic does not allow third-party apps to use it.
export default function ClaudeCard({ claude, onConnect, onDisconnect }) {
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
  const fromEnv = claude?.source === 'environment';

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
            Agents run on Claude. Connect your own Anthropic API key and usage is billed to your Anthropic account.
          </span>
        </div>
      </div>

      {on ? (
        <>
          <span className="ok">
            Connected with an API key ending {claude.keyHint || '…'}{fromEnv ? ' (set on the server)' : ''}. Model: {claude.model}.
          </span>
          {fromEnv ? (
            <span className="small muted">This key comes from the server&rsquo;s <code>ANTHROPIC_API_KEY</code>, so it can only be changed there.</span>
          ) : (
            <button className="btn outline block" type="button" onClick={disconnect} disabled={working}>Disconnect Claude</button>
          )}
        </>
      ) : (
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
            Create a key in the <a href="https://platform.claude.com/" target="_blank" rel="noreferrer">Claude Console</a>. The key is checked with Anthropic, stored only on this app&rsquo;s server, and never shown again.
          </span>
        </form>
      )}

      <div className="box small" style={{ background: 'var(--paper)' }}>
        <strong>Why not sign in with Claude Pro?</strong> A Claude Pro or Max plan covers Claude.ai, not the API, and Anthropic does not allow other apps to sign in with Claude.ai accounts or use their credentials. An API key is the supported way to connect.
      </div>
    </div>
  );
}

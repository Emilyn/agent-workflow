import { useState } from 'react';

// Other sources: what agents can read on the web, and remote MCP servers an admin adds.
export default function ConnectorsSection({ connectors, canManage, onAdd, onRefresh, onTools, onDelete, onSignIn }) {
  if (!connectors) return null;
  return (
    <section className="col g16" aria-labelledby="other-sources">
      <div className="col g6">
        <h2 id="other-sources" style={{ margin: 0, fontSize: 22 }}>Other sources</h2>
        <span className="settings-lead">Give agents more to read. A step uses a source when you switch it on in that step&rsquo;s tools. Agents only read: they cannot change anything in these sources.</span>
      </div>
      <div className="settings-grid">
        <WebCard web={connectors.web} />
        {connectors.mcp.map((c) => (
          <McpCard key={c.id} c={c} canManage={canManage} onRefresh={onRefresh} onTools={onTools} onDelete={onDelete} onSignIn={onSignIn} />
        ))}
      </div>
      {canManage ? <AddMcp onAdd={onAdd} /> : <span className="small muted">Only workspace admins can add connectors.</span>}
    </section>
  );
}

function WebCard({ web }) {
  return (
    <div className="int" data-testid="web-card">
      <div className="col g6">
        <div className="row wrap g8">
          <strong style={{ fontSize: 16 }}>Web</strong>
          <span className="pill done" style={{ padding: '2px 10px' }}>Available</span>
        </div>
        <span className="small" style={{ color: 'var(--ink-2)' }}>
          Switch on <strong>Web search</strong> in a step to let an agent read public web pages{web.search ? ' and search the web' : ''}. It cannot reach private or internal addresses.
        </span>
      </div>
      {!web.search && <span className="small muted">Searching is not set up on this server, so agents can read pages they are given but cannot search. The person running the server sets <code>WEB_SEARCH_PROVIDER</code> (see the README).</span>}
    </div>
  );
}

function McpCard({ c, canManage, onRefresh, onTools, onDelete, onSignIn }) {
  const [picked, setPicked] = useState(() => new Set(c.tools.filter((t) => t.enabled).map((t) => t.name)));
  const [working, setWorking] = useState('');
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [confirm, setConfirm] = useState(false);

  const run = (name, fn) => async () => {
    setWorking(name);
    setError('');
    setSaved(false);
    try {
      await fn();
      if (name === 'save') setSaved(true);
    } catch (e) {
      setError(e.message);
    }
    setWorking('');
  };
  const toggle = (name) => {
    setSaved(false);
    setPicked((p) => {
      const n = new Set(p);
      if (n.has(name)) n.delete(name); else n.add(name);
      return n;
    });
  };
  const save = run('save', () => onTools(c.id, [...picked]));
  const signIn = run('signin', () => onSignIn(c.id));
  const needsSignIn = c.auth === 'oauth' && !c.signedIn;
  const refresh = run('refresh', async () => {
    await onRefresh(c.id);
  });
  const changedCount = c.tools.filter((t) => t.changed).length;

  return (
    <div className="int" data-testid={`mcp-${c.name}`}>
      <div className="col g6">
        <div className="row wrap g8">
          <strong style={{ fontSize: 16 }}>{c.name}</strong>
          <span className={`pill ${needsSignIn ? 'waiting' : c.enabledCount > 0 ? 'done' : 'queued'}`} style={{ padding: '2px 10px' }}>{needsSignIn ? 'Needs sign-in' : c.enabledCount > 0 ? `${c.enabledCount} tool${c.enabledCount === 1 ? '' : 's'} on` : 'No tools on'}</span>
        </div>
        <span className="small muted">{c.serverName ? `${c.serverName} · ` : ''}{c.host}{c.hasToken ? ' · token saved' : ''}{c.auth === 'oauth' && c.signedIn ? ' · signed in with OAuth' : ''}</span>
        {c.error && <span className="alert" role="alert">Last refresh failed: {c.error}</span>}
      </div>
      {needsSignIn && <span className="alert" role="alert">{canManage ? 'Sign in to let agents use this server. Its sign-in has not been completed or has expired.' : 'An admin needs to sign in to this server before agents can use it.'}</span>}
      {changedCount > 0 && <span className="alert" role="alert">{changedCount} tool{changedCount === 1 ? ' was' : 's were'} changed by the server and switched off. Read {changedCount === 1 ? 'it' : 'them'} again before switching on.</span>}
      {c.tools.length === 0 ? <span className="small muted">This server offers no tools.</span> : (
        <ul className="people" aria-label={`Tools of ${c.name}`}>
          {c.tools.map((t) => (
            <li key={t.name} className="person-row" style={{ alignItems: 'flex-start' }}>
              <input id={`${c.id}-${t.name}`} type="checkbox" checked={picked.has(t.name)} disabled={!canManage || !t.readOnly} onChange={() => toggle(t.name)} style={{ width: 20, height: 20, marginTop: 2, accentColor: 'var(--accent)' }} />
              <label htmlFor={`${c.id}-${t.name}`} className="col g6" style={{ flex: 1, minWidth: 0 }}>
                <span><code>{t.name}</code>{t.changed && <span className="tag" style={{ marginLeft: 8 }}>Changed</span>}</span>
                <span className="small" style={{ color: 'var(--ink-2)' }}>{t.description || 'No description.'}</span>
                {!t.readOnly && <span className="small muted">Not marked read-only by the server, so agents cannot use it.</span>}
              </label>
            </li>
          ))}
        </ul>
      )}
      {error && <span className="alert" role="alert">{error}</span>}
      {saved && <span className="ok">Saved.</span>}
      {canManage && (
        <div className="row g8 wrap">
          {c.auth === 'oauth' && <button type="button" className={`btn sm${needsSignIn ? ' primary' : ''}`} disabled={!!working} onClick={signIn}>{working === 'signin' ? 'Opening…' : needsSignIn ? 'Sign in' : 'Sign in again'}</button>}
          {!needsSignIn && <button type="button" className="btn primary sm" disabled={!!working} onClick={save}>{working === 'save' ? 'Saving…' : 'Save tools'}</button>}
          {!needsSignIn && <button type="button" className="btn sm" disabled={!!working} onClick={refresh}>{working === 'refresh' ? 'Refreshing…' : 'Refresh tools'}</button>}
          {!confirm ? (
            <button type="button" className="btn sm danger" disabled={!!working} onClick={() => setConfirm(true)}>Remove</button>
          ) : (
            <>
              <button type="button" className="btn sm danger" disabled={!!working} onClick={run('delete', () => onDelete(c.id))}>Remove it</button>
              <button type="button" className="btn sm ghost" onClick={() => setConfirm(false)}>Keep it</button>
            </>
          )}
        </div>
      )}
      <span className="small muted">Text a source returns is treated as data, never as instructions. Tool descriptions come from the server, so only switch on tools you have read.</span>
    </div>
  );
}

function AddMcp({ onAdd }) {
  const [form, setForm] = useState({ name: '', url: '', token: '', headerName: '', clientId: '', clientSecret: '', scopes: '' });
  const [how, setHow] = useState('token'); // token | oauth
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const set = (k) => (e) => { setForm({ ...form, [k]: e.target.value }); setError(''); };

  const submit = async (e) => {
    e.preventDefault();
    setWorking(true);
    setError('');
    try {
      const base = { name: form.name.trim(), url: form.url.trim() };
      await onAdd(how === 'oauth'
        ? { ...base, oauth: true, clientId: form.clientId.trim(), clientSecret: form.clientSecret.trim(), scopes: form.scopes.trim() }
        : { ...base, token: form.token.trim(), headerName: form.headerName.trim() }); // for OAuth this leaves the page to sign in
      setForm({ name: '', url: '', token: '', headerName: '', clientId: '', clientSecret: '', scopes: '' });
    } catch (err) {
      setError(err.message);
    }
    setWorking(false);
  };

  return (
    <details className="disclose" data-testid="add-mcp">
      <summary>Add an MCP server</summary>
      <form className="inner" onSubmit={submit}>
        <span className="small muted">Paste the address of a remote MCP server (streamable HTTP). The app connects, reads its tools, and you choose which ones agents may use.</span>
        <label className="lbl" htmlFor="mcp-name">Name</label>
        <input id="mcp-name" className="field" required value={form.name} onChange={set('name')} placeholder="Company docs" autoComplete="off" />
        <label className="lbl" htmlFor="mcp-url">Server address</label>
        <input id="mcp-url" className="field" required value={form.url} onChange={set('url')} placeholder="https://mcp.example.com/mcp" autoComplete="off" spellCheck={false} />
        <fieldset className="col g6" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="lbl" style={{ marginBottom: 6 }}>How does it sign in?</legend>
          <div className="seg" role="group" aria-label="How does it sign in?">
            <button type="button" className={how === 'token' ? 'on' : ''} aria-pressed={how === 'token'} onClick={() => setHow('token')}>Token or none</button>
            <button type="button" className={how === 'oauth' ? 'on' : ''} aria-pressed={how === 'oauth'} onClick={() => setHow('oauth')}>Sign in with OAuth</button>
          </div>
        </fieldset>
        {how === 'token' ? (
          <>
            <label className="lbl" htmlFor="mcp-token">Token (optional)</label>
            <input id="mcp-token" className="field" type="password" value={form.token} onChange={set('token')} autoComplete="off" spellCheck={false} placeholder="Sent as a bearer token" />
            <details className="disclose">
              <summary>Advanced</summary>
              <div className="inner">
                <label className="lbl" htmlFor="mcp-header">Send the token in this header instead of Authorization</label>
                <input id="mcp-header" className="field" value={form.headerName} onChange={set('headerName')} placeholder="X-API-Key" autoComplete="off" spellCheck={false} />
              </div>
            </details>
          </>
        ) : (
          <>
            <span className="small muted">You will be sent to the service to sign in and approve access. The whole workspace then uses that account, so sign in with one you are happy to share.</span>
            <details className="disclose">
              <summary>Advanced</summary>
              <div className="inner">
                <span className="small muted">Most servers let this app register itself. If yours does not, create an OAuth client there with the redirect address <code>{window.location.origin}/api/connectors/oauth/callback</code> and enter it here.</span>
                <label className="lbl" htmlFor="mcp-cid">Client ID</label>
                <input id="mcp-cid" className="field" value={form.clientId} onChange={set('clientId')} autoComplete="off" spellCheck={false} />
                <label className="lbl" htmlFor="mcp-csecret">Client secret (if it has one)</label>
                <input id="mcp-csecret" className="field" type="password" value={form.clientSecret} onChange={set('clientSecret')} autoComplete="off" spellCheck={false} />
                <label className="lbl" htmlFor="mcp-scopes">Permissions (scopes), if the service needs them</label>
                <input id="mcp-scopes" className="field" value={form.scopes} onChange={set('scopes')} autoComplete="off" spellCheck={false} />
              </div>
            </details>
          </>
        )}
        {error && <span className="alert" role="alert">{error}</span>}
        <button type="submit" className="btn primary sm" style={{ alignSelf: 'flex-start' }} disabled={working}>{working ? 'Connecting…' : how === 'oauth' ? 'Continue to sign in' : 'Connect'}</button>
        <span className="small muted">Tokens are stored encrypted and never shown again. Only https addresses on the public internet are accepted unless the person running the server allows private ones.</span>
      </form>
    </details>
  );
}

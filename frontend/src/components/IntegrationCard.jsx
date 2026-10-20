import { useState } from 'react';
import { INTEGRATION_ICON } from './Icons.jsx';

// A real connection: an admin signs in on the provider's own consent page. The server keeps the
// token encrypted; agents get read-only access through it.
export default function IntegrationCard({ tool, status, canManage, onConnect, onDisconnect, onSaveApp, onRemoveApp }) {
  const [working, setWorking] = useState(false);
  const [error, setError] = useState('');
  const Icon = INTEGRATION_ICON[tool.id];
  if (!status) return null;

  const run = async (fn) => {
    setWorking(true);
    setError('');
    try {
      await fn(tool.id);
    } catch (e) {
      setError(e.message);
    }
    setWorking(false);
  };

  const [editing, setEditing] = useState(false);
  return (
    <div className="int" data-testid={`int-${tool.id}`}>
      <div className="row g12" style={{ alignItems: 'flex-start' }}>
        <span className="logo" style={{ width: 40, height: 40 }}><Icon /></span>
        <div className="col g6" style={{ minWidth: 0, flex: 1 }}>
          <div className="row wrap g8">
            <strong style={{ fontSize: 16 }}>{tool.label}</strong>
            <span className={`pill ${status.connected ? 'done' : 'queued'}`} style={{ padding: '2px 10px' }}>{status.connected ? 'Connected' : 'Not connected'}</span>
          </div>
          <span className="small" style={{ color: 'var(--ink-2)' }}>
            {status.connected ? `Connected as ${status.account}. ${tool.desc}` : tool.desc}
          </span>
        </div>
      </div>
      {canManage && (editing || !status.configured) ? (
        <AppForm
          tool={tool}
          status={status}
          connected={status.connected}
          onSave={async (app) => { await onSaveApp(tool.id, app); setEditing(false); }}
          onCancel={status.configured ? () => setEditing(false) : null}
        />
      ) : status.configured ? (
        <div className="col g6 small" data-testid={`app-${tool.id}`}>
          <span className="muted">
            OAuth app: <code>{status.clientId}</code> ({status.source === 'workspace' ? 'set by this workspace' : 'the server\u2019s shared app'})
          </span>
          {canManage && (
            <div className="row g8">
              <button className="btn outline" type="button" disabled={working} onClick={() => setEditing(true)}>Change app</button>
              {status.source === 'workspace' && (
                <button className="btn outline" type="button" disabled={working} onClick={() => run(() => onRemoveApp(tool.id))}>Remove app</button>
              )}
            </div>
          )}
        </div>
      ) : (
        <span className="small muted">No {tool.label} OAuth app yet. Ask a workspace admin to add one.</span>
      )}
      {status.connected && status.scopes && <span className="small muted">Permissions granted: {status.scopes.split(/[ ,]+/).join(', ')}</span>}
      {error && <span className="alert" role="alert">{error}</span>}
      {canManage ? (
        status.connected ? (
          <button className="btn block outline" type="button" disabled={working} onClick={() => run(onDisconnect)} style={{ justifyContent: 'center' }}>
            {working ? 'Disconnecting…' : `Disconnect ${tool.label}`}
          </button>
        ) : (
          <button className="btn block primary" type="button" disabled={working || !status.configured} onClick={() => run(onConnect)} style={{ justifyContent: 'center' }}>
            {working ? `Opening ${tool.label}…` : `Connect ${tool.label}`}
          </button>
        )
      ) : (
        <span className="small muted">Only workspace admins can change this.</span>
      )}
    </div>
  );
}

const DEV_CONSOLE = {
  github: ['GitHub \u2192 Settings \u2192 Developer settings \u2192 OAuth Apps', 'https://github.com/settings/developers'],
  gitlab: ['GitLab \u2192 Preferences \u2192 Applications', 'https://gitlab.com/-/user_settings/applications'],
  jira: ['the Atlassian developer console (OAuth 2.0 integration)', 'https://developer.atlassian.com/console/myapps/'],
  figma: ['Figma \u2192 Developers \u2192 My apps', 'https://www.figma.com/developers/apps'],
};

// An admin pastes the OAuth app they created at the service. The secret is stored encrypted and never shown again.
function AppForm({ tool, status, connected, onSave, onCancel }) {
  const [clientId, setClientId] = useState(status.source === 'workspace' ? status.clientId || '' : '');
  const [secret, setSecret] = useState('');
  const [scopes, setScopes] = useState(status.source === 'workspace' ? status.appScopes || '' : '');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const own = status.source === 'workspace';
  const [where, link] = DEV_CONSOLE[tool.id];

  const submit = async (e) => {
    e.preventDefault();
    setWorking(true);
    setError('');
    try {
      await onSave({ clientId, clientSecret: secret, scopes });
      setSecret('');
    } catch (err) {
      setError(err.message);
    }
    setWorking(false);
  };

  return (
    <form className="col g8" onSubmit={submit} data-testid={`app-form-${tool.id}`}>
      <span className="small muted">
        Create an OAuth app in <a href={link} target="_blank" rel="noreferrer">{where}</a> and set its callback (redirect) URL to:
      </span>
      <code className="small" style={{ wordBreak: 'break-all', userSelect: 'all' }}>{status.callbackUrl}</code>
      <label className="lbl" htmlFor={`${tool.id}-cid`}>Client ID</label>
      <input id={`${tool.id}-cid`} className="field" value={clientId} onChange={(e) => { setClientId(e.target.value); setError(''); }} autoComplete="off" spellCheck={false} />
      <label className="lbl" htmlFor={`${tool.id}-secret`}>Client secret</label>
      <input id={`${tool.id}-secret`} className="field" type="password" value={secret} onChange={(e) => { setSecret(e.target.value); setError(''); }} autoComplete="off" spellCheck={false} placeholder={own ? 'Leave empty to keep the saved secret' : ''} />
      <label className="lbl" htmlFor={`${tool.id}-scopes`}>Permissions (scopes)</label>
      <input id={`${tool.id}-scopes`} className="field" value={scopes} onChange={(e) => { setScopes(e.target.value); setError(''); }} autoComplete="off" spellCheck={false} placeholder={status.defaultScopes || ''} />
      <span className="small muted">Leave scopes empty to use the default shown in grey.{connected ? ' Changing the client ID disconnects the current sign-in; you will connect again afterwards.' : ''}</span>
      {error && <span className="alert" role="alert">{error}</span>}
      <div className="row g8">
        <button className="btn primary" type="submit" disabled={working}>{working ? 'Saving\u2026' : 'Save OAuth app'}</button>
        {onCancel && <button className="btn outline" type="button" onClick={onCancel} disabled={working}>Cancel</button>}
      </div>
    </form>
  );
}

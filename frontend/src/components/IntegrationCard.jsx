import { useState } from 'react';
import { INTEGRATION_ICON } from './Icons.jsx';

// A real connection: an admin signs in on the provider's own consent page. The server keeps the
// token encrypted; agents get read-only access through it.
export default function IntegrationCard({ tool, status, canManage, onConnect, onDisconnect }) {
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

  const envName = tool.id.toUpperCase();
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
      {!status.configured && !status.connected && (
        <span className="small muted">This server has no {tool.label} OAuth app yet. Whoever runs it sets {envName}_CLIENT_ID and {envName}_CLIENT_SECRET (see the README).</span>
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

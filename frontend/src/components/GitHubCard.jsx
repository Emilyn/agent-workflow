import { useState } from 'react';
import { INTEGRATION_ICON } from './Icons.jsx';

// Real GitHub connection: an admin signs in through GitHub's own consent page.
// The server keeps the token encrypted; agents get read-only access through it.
export default function GitHubCard({ github, canManage, onConnect, onDisconnect }) {
  const [working, setWorking] = useState(false);
  const [error, setError] = useState('');
  const Icon = INTEGRATION_ICON.git;
  if (!github) return null;

  const run = async (fn) => {
    setWorking(true);
    setError('');
    try {
      await fn();
    } catch (e) {
      setError(e.message);
      setWorking(false);
    }
  };

  return (
    <div className="int">
      <div className="row g12" style={{ alignItems: 'flex-start' }}>
        <span className="logo" style={{ width: 40, height: 40 }}><Icon /></span>
        <div className="col g6" style={{ minWidth: 0, flex: 1 }}>
          <div className="row wrap g8">
            <strong style={{ fontSize: 16 }}>GitHub</strong>
            <span className={`pill ${github.connected ? 'done' : 'queued'}`} style={{ padding: '2px 10px' }}>{github.connected ? 'Connected' : 'Not connected'}</span>
          </div>
          <span className="small" style={{ color: 'var(--ink-2)' }}>
            {github.connected
              ? `Connected as ${github.account}. Agents with the GitHub tool can read repositories, issues, pull requests, commits and files this account can see.`
              : 'Let agents read repositories, issues and pull requests. Read only: agents cannot change anything on GitHub.'}
          </span>
        </div>
      </div>
      {!github.configured && !github.connected && (
        <span className="small muted">This server has no GitHub OAuth app yet. Whoever runs it sets GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET (see the README).</span>
      )}
      {github.connected && github.scopes && <span className="small muted">Permissions granted: {github.scopes.split(',').join(', ')}</span>}
      {error && <span className="alert" role="alert">{error}</span>}
      {canManage ? (
        github.connected ? (
          <button className="btn block outline" type="button" disabled={working} onClick={() => run(onDisconnect).then(() => setWorking(false))} style={{ justifyContent: 'center' }}>
            {working ? 'Disconnecting…' : 'Disconnect GitHub'}
          </button>
        ) : (
          <button className="btn block primary" type="button" disabled={working || !github.configured} onClick={() => run(onConnect)} style={{ justifyContent: 'center' }}>
            {working ? 'Opening GitHub…' : 'Connect GitHub'}
          </button>
        )
      ) : (
        <span className="small muted">Only workspace admins can change this.</span>
      )}
    </div>
  );
}

import { INTEGRATIONS, integrationName } from '../workflow.js';
import ClaudeCard from './ClaudeCard.jsx';
import GitHubCard from './GitHubCard.jsx';
import { INTEGRATION_ICON } from './Icons.jsx';

export default function IntegrationsPanel({ doc, claude, github, canManage, onConnectClaude, onDisconnectClaude, onConnectGithub, onDisconnectGithub, onProvider, onToggle }) {
  return (
    <div className="panel">
      <div className="col g6">
        <span className="serif" style={{ fontSize: 28 }}>Connect your tools</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Claude and GitHub are real: connect an API key and agents run on it, connect GitHub and agents can read your repositories. GitLab, Jira and Figma are placeholders for now; no real account is linked. These settings are shared by everyone in the workspace.
        </span>
      </div>
      <ClaudeCard claude={claude} canManage={canManage} onConnect={onConnectClaude} onDisconnect={onDisconnectClaude} />
      <GitHubCard github={github} canManage={canManage} onConnect={onConnectGithub} onDisconnect={onDisconnectGithub} />
      {['git', 'jira', 'figma'].filter((k) => k !== 'git' || doc.gitProvider === 'gitlab' || !github?.configured).map((key) => {
        const it = INTEGRATIONS[key];
        const on = doc.connected[key];
        const name = integrationName(doc, key);
        const Icon = INTEGRATION_ICON[it.icon];
        return (
          <div className="int" key={key}>
            <div className="row g12" style={{ alignItems: 'flex-start' }}>
              <span className="logo" style={{ width: 40, height: 40 }}><Icon /></span>
              <div className="col g6" style={{ minWidth: 0, flex: 1 }}>
                <div className="row wrap g8">
                  <strong style={{ fontSize: 16 }}>{name}</strong>
                  <span className={`pill ${on ? 'done' : 'queued'}`} style={{ padding: '2px 10px' }}>{on ? 'Connected' : 'Not connected'}</span>
                </div>
                <span className="small" style={{ color: 'var(--ink-2)' }}>{it.desc}</span>
              </div>
            </div>
            {canManage && key === 'git' && !on && (
              <div className="seg" role="group" aria-label="Choose a code host">
                <button type="button" className={doc.gitProvider === 'github' ? 'on' : ''} onClick={() => onProvider('github')}>GitHub</button>
                <button type="button" className={doc.gitProvider === 'gitlab' ? 'on' : ''} onClick={() => onProvider('gitlab')}>GitLab</button>
              </div>
            )}
            <div className="col g8">
              <span className="eyebrow">Agents will be able to</span>
              <div className="row wrap g8">{it.can.map((c) => <span key={c} className="tag-chip">{c}</span>)}</div>
            </div>
            {on && <span className="ok">Marked as connected (placeholder).</span>}
            {canManage ? (
              <button className={`btn block ${on ? 'outline' : 'primary'}`} type="button" onClick={() => onToggle(key)} style={{ justifyContent: 'center' }}>
                {on ? 'Disconnect' : 'Connect'} {name}
              </button>
            ) : (
              <span className="small muted">Only workspace admins can change this.</span>
            )}
          </div>
        );
      })}
      <span className="small muted">Connected tools show up as options when you create an agent, and you can disconnect any of them at any time.</span>
    </div>
  );
}

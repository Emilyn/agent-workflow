import { INTEGRATIONS, integrationName } from '../workflow.js';
import { INTEGRATION_ICON } from './Icons.jsx';

export default function IntegrationsPanel({ doc, onProvider, onToggle }) {
  return (
    <div className="panel">
      <div className="col g6">
        <span className="serif" style={{ fontSize: 28 }}>Connect your tools</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Give agents access to your code, tickets and designs. This version simulates the sign-in; no real account is linked.
        </span>
      </div>
      {['git', 'jira', 'figma'].map((key) => {
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
            {key === 'git' && !on && (
              <div className="seg" role="group" aria-label="Choose a code host">
                <button type="button" className={doc.gitProvider === 'github' ? 'on' : ''} onClick={() => onProvider('github')}>GitHub</button>
                <button type="button" className={doc.gitProvider === 'gitlab' ? 'on' : ''} onClick={() => onProvider('gitlab')}>GitLab</button>
              </div>
            )}
            <div className="col g8">
              <span className="eyebrow">Agents will be able to</span>
              <div className="row wrap g8">{it.can.map((c) => <span key={c} className="tag-chip">{c}</span>)}</div>
            </div>
            {on && <span className="ok">Connected. Signed in with your {name} account.</span>}
            <button className={`btn block ${on ? 'outline' : 'primary'}`} type="button" onClick={() => onToggle(key)} style={{ justifyContent: 'center' }}>
              {on ? 'Disconnect' : 'Connect'} {name}
            </button>
          </div>
        );
      })}
      <span className="small muted">Connected tools show up as options when you create an agent, and you can disconnect any of them at any time.</span>
    </div>
  );
}

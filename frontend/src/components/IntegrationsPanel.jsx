import { LIVE_TOOLS } from '../workflow.js';
import ClaudeCard from './ClaudeCard.jsx';
import IntegrationCard from './IntegrationCard.jsx';

export default function IntegrationsPanel({ claude, ints, canManage, onConnectClaude, onDisconnectClaude, onConnect, onDisconnect }) {
  return (
    <div className="panel">
      <div className="col g6">
        <span className="serif" style={{ fontSize: 28 }}>Connect your tools</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Claude runs the agents. GitHub, GitLab, Jira and Figma are real connections: an admin signs in once, and agents whose tools include one can read from it while they run. Access is read only and shared by everyone in the workspace.
        </span>
      </div>
      <ClaudeCard claude={claude} canManage={canManage} onConnect={onConnectClaude} onDisconnect={onDisconnectClaude} />
      {LIVE_TOOLS.map((t) => (
        <IntegrationCard key={t.id} tool={t} status={ints?.[t.id]} canManage={canManage} onConnect={onConnect} onDisconnect={onDisconnect} />
      ))}
      <span className="small muted">Connected tools show up as options when you create an agent or edit a step. Disconnecting removes the stored sign-in.</span>
    </div>
  );
}

import { LIVE_TOOLS } from '../workflow.js';
import ClaudeCard from './ClaudeCard.jsx';
import IntegrationCard from './IntegrationCard.jsx';

export default function IntegrationsPanel({ claude, ints, canManage, onConnectClaude, onDisconnectClaude, onConnect, onDisconnect }) {
  return (
    <div className="panel">
      <div className="col g6">
        <span className="serif" style={{ fontSize: 28 }}>Connect your tools</span>
        <span style={{ fontSize: 14, lineHeight: 1.5, color: 'var(--ink-2)' }}>
          Claude runs the agents. An admin connects each service once; agents with that tool can read from it while they run, and the whole workspace shares the access. The only thing agents write is a code review on GitHub or GitLab, which a person approves first.
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

import { LIVE_TOOLS } from '../workflow.js';
import ClaudeCard from './ClaudeCard.jsx';
import IntegrationCard from './IntegrationCard.jsx';
import TeamPanel from './TeamPanel.jsx';
import TriggersSection from './TriggersSection.jsx';

export const SECTIONS = [
  { id: 'claude', label: 'Claude', hint: 'The API key agents run on' },
  { id: 'integrations', label: 'Integrations', hint: 'GitHub, GitLab, Jira, Figma' },
  { id: 'triggers', label: 'Review triggers', hint: 'Start on new pull requests' },
  { id: 'team', label: 'Team', hint: 'Members, invites, workspace' },
];

// Workspace settings on a page of their own, so the side panel stays for the workflow.
export default function SettingsPage({ section, onSection, onClose, canManage, claude, ints, workflows, currentWorkflowId, wsId, me, team, onConnectClaude, onDisconnectClaude, onConnect, onDisconnect }) {
  const active = SECTIONS.find((s) => s.id === section) || SECTIONS[0];
  return (
    <div className="settings">
      <nav className="settings-nav" aria-label="Settings">
        <button type="button" className="btn ghost sm" style={{ alignSelf: 'flex-start' }} onClick={onClose}>← Back to workflow</button>
        <span className="serif" style={{ fontSize: 26 }}>Settings</span>
        <div className="col g6" role="tablist" aria-label="Settings sections" aria-orientation="vertical">
          {SECTIONS.map((s) => (
            <button key={s.id} type="button" role="tab" aria-selected={active.id === s.id} className={`snav${active.id === s.id ? ' on' : ''}`} onClick={() => onSection(s.id)}>
              <span>{s.label}</span>
              <span className="small muted">{s.hint}</span>
            </button>
          ))}
        </div>
      </nav>
      <main className="settings-main" aria-labelledby="settings-title">
        <h1 id="settings-title" className="serif" style={{ fontSize: 32, margin: 0 }}>{active.label}</h1>
        {active.id === 'claude' && (
          <>
            <span className="settings-lead">Claude runs the agents. One API key is shared by the whole workspace, and usage is billed to the Anthropic account it belongs to.</span>
            <ClaudeCard claude={claude} canManage={canManage} onConnect={onConnectClaude} onDisconnect={onDisconnectClaude} />
          </>
        )}
        {active.id === 'integrations' && (
          <>
            <span className="settings-lead">An admin connects each service once; agents with that tool can read from it while they run, and the whole workspace shares the access. The only thing agents write is a code review on GitHub or GitLab, which a person approves first.</span>
            <div className="settings-grid">
              {LIVE_TOOLS.map((t) => (
                <IntegrationCard key={t.id} tool={t} status={ints?.[t.id]} canManage={canManage} onConnect={onConnect} onDisconnect={onDisconnect} />
              ))}
            </div>
            <span className="small muted">Connected tools show up as options when you create an agent or edit a step. Disconnecting removes the stored sign-in.</span>
          </>
        )}
        {active.id === 'triggers' && (
          <>
            <span className="settings-lead">A trigger starts a workflow for every new pull or merge request in one project. By default the review waits in the workflow's Runs tab until a teammate approves it.</span>
            <TriggersSection wsId={wsId} canManage={canManage} ints={ints} workflows={workflows} currentWorkflowId={currentWorkflowId} onGoIntegrations={() => onSection('integrations')} />
          </>
        )}
        {active.id === 'team' && <TeamPanel me={me} {...team} />}
      </main>
    </div>
  );
}

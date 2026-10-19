import BuilderPanel from './BuilderPanel.jsx';
import ChatPanel from './ChatPanel.jsx';
import IntegrationsPanel from './IntegrationsPanel.jsx';
import RunsPanel from './RunsPanel.jsx';
import SettingsPanel from './SettingsPanel.jsx';
import TeamPanel from './TeamPanel.jsx';

const TABS = [
  ['chat', 'Team chat'],
  ['settings', 'Step settings'],
  ['runs', 'Runs'],
  ['workspace', 'Workspace'],
];
const WORKSPACE = [
  ['integrations', 'Integrations'],
  ['team', 'Team'],
];

export default function Side({ tab, onTab, doc, node, me, chat, settings, runs, integrations, builder, team }) {
  const inWorkspace = tab === 'integrations' || tab === 'team';
  const active = inWorkspace ? 'workspace' : tab;
  return (
    <aside className="side">
      <div className="tabs" role="tablist" aria-label="Side panel">
        {TABS.map(([id, label]) => (
          <button key={id} type="button" role="tab" aria-selected={active === id} className={`tab${active === id ? ' on' : ''}`} onClick={() => onTab(id === 'workspace' ? 'integrations' : id)}>{label}</button>
        ))}
      </div>
      {inWorkspace && (
        <div className="subnav" role="tablist" aria-label="Workspace sections">
          {WORKSPACE.map(([id, label]) => (
            <button key={id} type="button" role="tab" aria-selected={tab === id} className={tab === id ? 'on' : ''} onClick={() => onTab(id)}>{label}</button>
          ))}
        </div>
      )}
      {tab === 'chat' && <ChatPanel doc={doc} me={me} {...chat} />}
      {tab === 'settings' && <SettingsPanel doc={doc} node={node} {...settings} />}
      {tab === 'runs' && <RunsPanel {...runs} />}
      {tab === 'integrations' && <IntegrationsPanel doc={doc} {...integrations} />}
      {tab === 'team' && <TeamPanel me={me} {...team} />}
      {tab === 'agent' && <BuilderPanel {...builder} />}
    </aside>
  );
}

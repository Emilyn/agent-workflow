import BuilderPanel from './BuilderPanel.jsx';
import ChatPanel from './ChatPanel.jsx';
import IntegrationsPanel from './IntegrationsPanel.jsx';
import SettingsPanel from './SettingsPanel.jsx';
import TeamPanel from './TeamPanel.jsx';

const TABS = [
  ['chat', 'Team chat'],
  ['settings', 'Step settings'],
  ['integrations', 'Integrations'],
  ['team', 'Team'],
];

export default function Side({ tab, onTab, doc, node, me, chat, settings, integrations, builder, team }) {
  return (
    <aside className="side">
      <div className="tabs" role="tablist">
        {TABS.map(([id, label]) => (
          <button key={id} type="button" role="tab" aria-selected={tab === id} className={`tab${tab === id ? ' on' : ''}`} onClick={() => onTab(id)}>{label}</button>
        ))}
      </div>
      {tab === 'chat' && <ChatPanel doc={doc} me={me} {...chat} />}
      {tab === 'settings' && <SettingsPanel doc={doc} node={node} {...settings} />}
      {tab === 'integrations' && <IntegrationsPanel doc={doc} {...integrations} />}
      {tab === 'team' && <TeamPanel me={me} {...team} />}
      {tab === 'agent' && <BuilderPanel doc={doc} {...builder} />}
    </aside>
  );
}

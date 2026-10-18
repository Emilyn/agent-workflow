import BuilderPanel from './BuilderPanel.jsx';
import ChatPanel from './ChatPanel.jsx';
import IntegrationsPanel from './IntegrationsPanel.jsx';
import SettingsPanel from './SettingsPanel.jsx';

const TABS = [
  ['chat', 'Team chat'],
  ['settings', 'Step settings'],
  ['integrations', 'Integrations'],
];

export default function Side({ tab, onTab, doc, node, chat, settings, integrations, builder }) {
  return (
    <aside className="side">
      <div className="tabs" role="tablist">
        {TABS.map(([id, label]) => (
          <button key={id} type="button" role="tab" aria-selected={tab === id} className={`tab${tab === id ? ' on' : ''}`} onClick={() => onTab(id)}>{label}</button>
        ))}
      </div>
      {tab === 'chat' && <ChatPanel doc={doc} {...chat} />}
      {tab === 'settings' && <SettingsPanel doc={doc} node={node} {...settings} />}
      {tab === 'integrations' && <IntegrationsPanel doc={doc} {...integrations} />}
      {tab === 'agent' && <BuilderPanel doc={doc} {...builder} />}
    </aside>
  );
}

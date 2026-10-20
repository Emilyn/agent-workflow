import AutomationPanel from './AutomationPanel.jsx';
import BuilderPanel from './BuilderPanel.jsx';
import ChatPanel from './ChatPanel.jsx';
import RunsPanel from './RunsPanel.jsx';
import SettingsPanel from './SettingsPanel.jsx';

const TABS = [
  ['chat', 'Team chat'],
  ['settings', 'Step settings'],
  ['runs', 'Runs'],
  ['automate', 'Automate'],
];

export default function Side({ tab, onTab, doc, node, me, chat, settings, runs, automation, builder }) {
  return (
    <aside className="side">
      <div className="tabs" role="tablist" aria-label="Side panel">
        {TABS.map(([id, label]) => (
          <button key={id} type="button" role="tab" aria-selected={tab === id} className={`tab${tab === id ? ' on' : ''}`} onClick={() => onTab(id)}>{label}</button>
        ))}
      </div>
      {tab === 'chat' && <ChatPanel doc={doc} me={me} {...chat} />}
      {tab === 'settings' && <SettingsPanel doc={doc} node={node} {...settings} />}
      {tab === 'runs' && <RunsPanel {...runs} />}
      {tab === 'automate' && <AutomationPanel {...automation} />}
      {tab === 'agent' && <BuilderPanel {...builder} />}
    </aside>
  );
}

import BuilderPanel from './BuilderPanel.jsx';
import ChatPanel from './ChatPanel.jsx';
import RunsPanel from './RunsPanel.jsx';
import SettingsPanel from './SettingsPanel.jsx';

const TABS = [
  ['chat', 'Team chat'],
  ['settings', 'Step settings'],
  ['runs', 'Runs'],
];

export default function Side({ tab, onTab, doc, node, me, chat, settings, runs, builder }) {
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
      {tab === 'agent' && <BuilderPanel {...builder} />}
    </aside>
  );
}

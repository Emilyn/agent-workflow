import WorkspaceMenu from './WorkspaceMenu.jsx';

const NAV = [
  ['workflows', 'Workflows', 'M3 4h12M3 9h12M3 14h8'],
  ['runs', 'Runs', 'M5 3l9 6-9 6z'],
  ['team', 'Team', 'M9 8a3 3 0 100-6 3 3 0 000 6zM3 16c0-3 2.5-5 6-5s6 2 6 5'],
  ['settings', 'Settings', 'M9 11.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5zM9 2v2M9 14v2M2 9h2M14 9h2'],
];
const RECENT = 5;

// The app's left navigation: which workspace you are in, where you can go, and your latest workflows.
export default function Sidebar({ workspaceMenu, badge = 0, page, section, workflows, currentId, onGo, onOpenWorkflow }) {
  const active = page === 'runs' ? 'runs' : page === 'settings' ? (section === 'team' ? 'team' : 'settings') : 'workflows';
  const recent = [...workflows].sort((a, b) => (b.updated || '').localeCompare(a.updated || '')).slice(0, RECENT);
  return (
    <nav className="sidebar" aria-label="Main">
      <WorkspaceMenu {...workspaceMenu} />
      <div className="side-nav">
        {NAV.map(([id, label, icon]) => (
          <button key={id} type="button" className={`nav-item${active === id ? ' on' : ''}`} aria-current={active === id ? 'page' : undefined} onClick={() => onGo(id)}>
            <svg aria-hidden="true" width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d={icon} /></svg>
            <span>{label}</span>
            {id === 'runs' && badge > 0 && <span className="nav-badge" aria-label={`${badge} waiting for you`}>{badge}</span>}
          </button>
        ))}
      </div>
      {recent.length > 0 && (
        <div className="side-recent">
          <span className="eyebrow">Recent</span>
          {recent.map((w) => (
            <button key={w.id} type="button" className={`nav-item recent${page === 'editor' && w.id === currentId ? ' on' : ''}`} onClick={() => onOpenWorkflow(w.id)} title={w.name}>
              <span className={`dot ${w.steps && w.progress >= w.steps ? 'done' : w.progress > 0 ? 'run' : ''}`} aria-hidden="true" />
              <span className="nav-label">{w.name}</span>
            </button>
          ))}
        </div>
      )}
    </nav>
  );
}

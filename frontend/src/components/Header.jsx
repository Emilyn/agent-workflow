import { Play, Spark } from './Icons.jsx';

const SAVE_LABEL = { saved: 'Saved', saving: 'Saving…', error: 'Not saved' };

export default function Header({ name, saveState, runLabel, busy, onAdvance }) {
  return (
    <header className="topbar">
      <div className="row g12" style={{ minWidth: 0 }}>
        <div className="logo"><Spark size={20} /></div>
        <div className="col" style={{ minWidth: 0 }}>
          <span className="serif title">{name}</span>
          <span className="small muted">Workflows · {SAVE_LABEL[saveState]}</span>
        </div>
      </div>
      <div className="row wrap g12">
        <div className="row" aria-label="Collaborators: Amara, Theo and Claude">
          <span className="avatar" style={{ background: '#EBD9CE', color: '#5A2E18' }} title="Amara">AM</span>
          <span className="avatar" style={{ background: '#D5E3E0', color: '#1C4A45' }} title="Theo">TH</span>
          <span className="avatar agent" title="Claude"><Spark size={16} /></span>
        </div>
        <button className="btn" type="button">Share</button>
        <button className="btn primary" type="button" onClick={onAdvance} disabled={busy}>
          <Play /> <span>{runLabel}</span>
        </button>
      </div>
    </header>
  );
}

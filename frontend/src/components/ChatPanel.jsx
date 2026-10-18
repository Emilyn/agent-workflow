import { useEffect, useRef } from 'react';
import { allAgents, clock, personStyle } from '../workflow.js';
import { Send } from './Icons.jsx';

function Avatar({ author, kind }) {
  if (kind === 'agent') {
    return <span className="avatar agent" style={{ border: 'none' }}>{author === 'Claude' ? 'C' : author.charAt(0)}</span>;
  }
  const p = personStyle(author);
  return <span className="avatar" style={{ background: p.bg, color: p.fg, border: 'none' }}>{p.initials}</span>;
}

export default function ChatPanel({ doc, me, draft, thinking, onDraft, onSend }) {
  const endRef = useRef(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'end' });
  }, [doc.messages.length, thinking]);

  const agentCount = doc.steps.flat().filter((n) => n.kind === 'agent').length;
  const mentions = [...allAgents(doc).map((a) => `@${a.name}`), ...doc.members.filter((m) => m.userId !== me.id).map((m) => `@${m.name}`)];

  return (
    <div className="col" style={{ flex: 1, minHeight: 0 }}>
      <div className="small muted" style={{ padding: '16px 20px 4px' }}>
        {doc.members.length} {doc.members.length === 1 ? 'person' : 'people'} and {agentCount} {agentCount === 1 ? 'agent are' : 'agents are'} in this workflow. Mention an agent by name to add your message to its instructions.
      </div>
      <div className="col g16" style={{ flex: 1, padding: '12px 20px', overflowY: 'auto' }}>
        {doc.messages.map((m) => (
          <div key={m.id} className="row g12" style={{ alignItems: 'flex-start' }}>
            <Avatar author={m.author} kind={m.kind} />
            <div className="col g6" style={{ minWidth: 0, flex: 1 }}>
              <div className="row wrap g8" style={{ alignItems: 'baseline' }}>
                <strong style={{ fontSize: 14 }}>{m.author}</strong>
                {m.kind === 'agent' && <span className="tag">Agent</span>}
                <span className="muted" style={{ fontSize: 12 }}>{clock(m.createdAt)}</span>
              </div>
              <div className={`bubble${m.kind === 'agent' ? ' agent' : ''}`}>{m.text}</div>
            </div>
          </div>
        ))}
        {thinking && <div className="small muted" aria-live="polite">{thinking} is replying…</div>}
        <div ref={endRef} />
      </div>
      <div className="row wrap g8" style={{ padding: '8px 20px 0' }}>
        {mentions.map((l) => (
          <button key={l} type="button" className="tool" style={{ minHeight: 36, padding: '0 12px' }} onClick={() => onDraft(`${draft ? `${draft} ` : ''}${l} `)}>{l}</button>
        ))}
      </div>
      <form
        className="row g8"
        style={{ padding: '12px 20px 20px' }}
        onSubmit={(e) => { e.preventDefault(); onSend(); }}
      >
        <input
          className="field"
          aria-label="Message the team"
          placeholder="Message the team or @mention an agent"
          value={draft}
          autoComplete="off"
          onChange={(e) => onDraft(e.target.value)}
        />
        <button className="btn primary send" type="submit" aria-label="Send message"><Send /></button>
      </form>
    </div>
  );
}

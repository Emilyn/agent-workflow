// A review an agent wrote for a pull or merge request: read it, then post it or throw it away.
export default function ReviewDraft({ a, onApprove, onDiscard, busy }) {
  return (
    <div className="kv" data-testid="review-draft" style={{ gap: 8, padding: 12, border: '1px solid var(--line-2)', borderRadius: 12, background: 'var(--card)' }}>
      <div className="row g8" style={{ justifyContent: 'space-between' }}>
        <span className="lbl-s">{a.status === 'pending' ? 'Draft review' : 'Review'}</span>
        <span className={`pill ${a.status === 'posted' ? 'done' : a.status === 'pending' ? 'waiting' : 'queued'}`}>{a.status}</span>
      </div>
      <div style={{ whiteSpace: 'pre-wrap', fontSize: 14, lineHeight: 1.5 }}>{a.summary}</div>
      {a.comments.length > 0 && (
        <details className="disclose" open={a.status === 'pending'}>
          <summary>Inline comments ({a.comments.length})</summary>
          <div className="inner">
            {a.comments.map((c, i) => (
              <div key={i} className="small"><code>{c.path}:{c.line}</code><br />{c.body}</div>
            ))}
          </div>
        </details>
      )}
      {a.status === 'posted' && a.result && <a className="small" href={a.result} target="_blank" rel="noreferrer noopener">Open the posted review</a>}
      {a.status === 'failed' && <span className="alert" role="alert">Could not post: {a.result}</span>}
      {a.status === 'pending' && (
        <div className="row g8">
          <button type="button" className="btn sm primary" disabled={busy} onClick={() => onApprove(a)}>{busy ? 'Posting…' : 'Approve and post'}</button>
          <button type="button" className="btn sm" disabled={busy} onClick={() => onDiscard(a)}>Discard</button>
        </div>
      )}
    </div>
  );
}

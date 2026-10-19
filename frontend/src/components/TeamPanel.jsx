import { useCallback, useEffect, useState } from 'react';
import { api } from '../api.js';
import { ROLE_LABEL, ROLE_RANK, personStyle } from '../workflow.js';

function Person({ name }) {
  const p = personStyle(name);
  return <span className="avatar" style={{ background: p.bg, color: p.fg, border: 'none' }} aria-hidden="true">{p.initials}</span>;
}

// Members, invite links and workspace settings. Controls only appear for roles that may use them.
export default function TeamPanel({ wsId, wsName, me, role, members, onMembers, onRenamed, onLeft, onDeleted }) {
  const isAdmin = ROLE_RANK[role] >= ROLE_RANK.admin;
  const isOwner = role === 'owner';
  const [invites, setInvites] = useState([]);
  const [inviteRole, setInviteRole] = useState('member');
  const [note, setNote] = useState('');
  const [link, setLink] = useState('');
  const [copied, setCopied] = useState(false);
  const [name, setName] = useState(wsName);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const loadInvites = useCallback(() => {
    if (isAdmin) api.invites(wsId).then(setInvites).catch(() => {});
  }, [wsId, isAdmin]);
  useEffect(loadInvites, [loadInvites]);
  useEffect(() => setName(wsName), [wsName]);

  const act = async (fn) => {
    setBusy(true);
    setError('');
    try { await fn(); } catch (e) { setError(e.message); } finally { setBusy(false); }
  };

  const canEditRole = (m) => isAdmin && m.userId !== me.id && (isOwner || m.role !== 'owner');
  const canRemove = (m) => (m.userId === me.id ? true : isAdmin && (isOwner || ROLE_RANK[m.role] < ROLE_RANK[role]));

  const createInvite = () => act(async () => {
    const r = await api.createInvite(wsId, { role: inviteRole, note });
    setLink(`${window.location.origin}/#/join/${r.token}`);
    setCopied(false);
    setNote('');
    loadInvites();
  });

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
    } catch {
      setError('Copy did not work here. Select the link and copy it by hand.');
    }
  };

  return (
    <div className="panel">
      <span className="settings-lead">
        Everyone here sees every workflow in <strong>{wsName}</strong> and can edit, chat and run them.
      </span>
      {error && <div className="alert" role="alert">{error}</div>}

      <ul className="people" aria-label="Members">
        {members.map((m) => (
          <li key={m.userId} className="person-row">
            <Person name={m.name} />
            <div className="col" style={{ minWidth: 0, flex: 1 }}>
              <strong style={{ fontSize: 14 }}>{m.name}{m.userId === me.id ? ' (you)' : ''}</strong>
              {m.email && <span className="small muted" style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{m.email}</span>}
            </div>
            {canEditRole(m) ? (
              <select className="field role-select" aria-label={`Role of ${m.name}`} value={m.role} disabled={busy}
                onChange={(e) => act(async () => onMembers(await api.setRole(wsId, m.userId, e.target.value)))}>
                <option value="member">Member</option>
                <option value="admin">Admin</option>
                {isOwner && <option value="owner">Owner</option>}
              </select>
            ) : (
              <span className="tag-chip">{ROLE_LABEL[m.role]}</span>
            )}
            {canRemove(m) && (
              <button type="button" className="btn ghost" disabled={busy} aria-label={m.userId === me.id ? 'Leave this workspace' : `Remove ${m.name}`}
                onClick={() => act(async () => {
                  await api.removeMember(wsId, m.userId);
                  if (m.userId === me.id) onLeft();
                  else onMembers(await api.members(wsId));
                })}>
                {m.userId === me.id ? 'Leave' : 'Remove'}
              </button>
            )}
          </li>
        ))}
      </ul>

      {isAdmin && (
        <section className="int" aria-labelledby="invite-h">
          <strong id="invite-h">Invite someone</strong>
          <span className="small muted">
            Create a one-time link and send it yourself. Whoever opens it signs in or creates an account and joins. Links expire after 7 days. This app does not send email.
          </span>
          <div className="row g8 wrap">
            <label className="lbl" htmlFor="invite-role" style={{ minWidth: 40 }}>Role</label>
            <select id="invite-role" className="field" style={{ flex: 1, minWidth: 120 }} value={inviteRole} onChange={(e) => setInviteRole(e.target.value)}>
              <option value="member">Member</option>
              {isOwner && <option value="admin">Admin</option>}
            </select>
          </div>
          <input className="field" aria-label="Note, so you remember who the link is for" placeholder="Who is it for? (optional)" value={note} maxLength={120} onChange={(e) => setNote(e.target.value)} />
          <button type="button" className="btn primary block" style={{ justifyContent: 'center' }} disabled={busy} onClick={createInvite}>Create invite link</button>
          {link && (
            <div className="col g8" role="status">
              <span className="small">Copy this link now. It is not shown again.</span>
              <input className="field" readOnly aria-label="Invite link" value={link} onFocus={(e) => e.target.select()} />
              <button type="button" className="btn" onClick={copy}>{copied ? 'Copied' : 'Copy link'}</button>
            </div>
          )}
          {invites.length > 0 && (
            <div className="col g8">
              <span className="eyebrow">Open links</span>
              {invites.map((i) => (
                <div key={i.id} className="row g8" style={{ justifyContent: 'space-between' }}>
                  <span className="small">{i.note || 'No note'} · {ROLE_LABEL[i.role]} · expires {new Date(i.expiresAt).toLocaleDateString()}</span>
                  <button type="button" className="btn ghost" disabled={busy} onClick={() => act(async () => { await api.deleteInvite(wsId, i.id); loadInvites(); })}>Revoke</button>
                </div>
              ))}
            </div>
          )}
        </section>
      )}

      {isAdmin && (
        <section className="int" aria-labelledby="ws-h">
          <strong id="ws-h">Workspace</strong>
          <form className="col g8" onSubmit={(e) => { e.preventDefault(); act(async () => { const w = await api.renameWorkspace(wsId, name); onRenamed(w.name); }); }}>
            <label className="lbl" htmlFor="ws-name">Name</label>
            <input id="ws-name" className="field" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
            <button type="submit" className="btn" disabled={busy || !name.trim() || name.trim() === wsName}>Save name</button>
          </form>
          {isOwner && (confirmDelete ? (
            <div className="col g8">
              <span className="small">Delete “{wsName}” with all its workflows, chat and stored API key? This can’t be undone.</span>
              <div className="row g8">
                <button type="button" className="btn danger" disabled={busy} onClick={() => act(async () => { await api.deleteWorkspace(wsId); onDeleted(); })}>Delete workspace</button>
                <button type="button" className="btn ghost" onClick={() => setConfirmDelete(false)}>Keep it</button>
              </div>
            </div>
          ) : (
            <button type="button" className="btn danger" onClick={() => setConfirmDelete(true)}>Delete workspace…</button>
          ))}
        </section>
      )}
    </div>
  );
}

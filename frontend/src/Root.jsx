import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from './api.js';
import AuthScreen from './components/AuthScreen.jsx';
import Workspace from './Workspace.jsx';
import { hashFor, parseHash } from './route.js';

// Decides what to show: loading, sign-in, an invite being accepted, or a workspace.
export default function Root() {
  const [me, setMe] = useState(undefined); // undefined: loading, null: signed out
  const [route, setRoute] = useState(parseHash());
  const [invite, setInvite] = useState(null);
  const [notice, setNotice] = useState('');
  const [fatal, setFatal] = useState('');
  const accepted = useRef('');

  useEffect(() => {
    api.me().then(setMe).catch((e) => (e.status === 401 ? setMe(null) : setFatal(e.message)));
    const onHash = () => setRoute(parseHash());
    const onOut = () => setMe(null);
    window.addEventListener('hashchange', onHash);
    window.addEventListener('aw:signed-out', onOut);
    return () => {
      window.removeEventListener('hashchange', onHash);
      window.removeEventListener('aw:signed-out', onOut);
    };
  }, []);

  const refreshMe = useCallback(() => api.me().then(setMe), []);

  // Invite links: show which workspace to signed-out visitors, join it for signed-in ones.
  const token = route.kind === 'join' ? route.token : '';
  useEffect(() => {
    if (!token || me === undefined) return;
    if (me === null) {
      api.invitePreview(token).then(setInvite).catch((e) => setNotice(e.message));
      return;
    }
    if (accepted.current === token) return;
    accepted.current = token;
    api.acceptInvite(token)
      .then(async (ws) => {
        await refreshMe();
        window.location.hash = hashFor(ws.id);
      })
      .catch(async (e) => {
        setNotice(e.message);
        await refreshMe().catch(() => {});
        window.location.hash = '#/';
      });
  }, [token, me, refreshMe]);

  if (fatal) {
    return (
      <div className="splash">
        <span className="serif" style={{ fontSize: 28 }}>Cannot reach the server</span>
        <span>{fatal}</span>
        <span className="muted small">Check that the backend and database are running, then reload.</span>
      </div>
    );
  }
  if (me === undefined) return <div className="splash"><span className="muted">Loading…</span></div>;
  if (me === null) {
    return (
      <>
        {notice && <div className="banner" role="alert"><span>{notice}</span><button type="button" className="link-btn" onClick={() => setNotice('')}>Dismiss</button></div>}
        <AuthScreen invite={token ? invite : null} onAuthed={(m) => { setInvite(null); setMe(m); }} />
      </>
    );
  }
  if (token) return <div className="splash"><span className="muted">Joining the workspace…</span></div>;

  const { user, workspaces } = me;
  const signOut = async () => {
    await api.logout();
    setMe(null);
    window.location.hash = '#/';
  };

  if (workspaces.length === 0) {
    return <NoWorkspace user={user} onCreate={async (name) => { const ws = await api.createWorkspace(name); await refreshMe(); window.location.hash = hashFor(ws.id); }} onSignOut={signOut} />;
  }
  const current = (route.kind === 'ws' && workspaces.find((w) => w.id === route.ws)) || workspaces[0];

  return (
    <>
      {notice && <div className="banner" role="alert"><span>{notice}</span><button type="button" className="link-btn" onClick={() => setNotice('')}>Dismiss</button></div>}
      <Workspace
        key={current.id}
        ws={current}
        workspaces={workspaces}
        user={user}
        wfFromUrl={route.kind === 'ws' && route.ws === current.id ? route.wf : ''}
        onSwitchWorkspace={(id) => { window.location.hash = hashFor(id); }}
        onCreateWorkspace={async (name) => { const ws = await api.createWorkspace(name); await refreshMe(); window.location.hash = hashFor(ws.id); }}
        onRefresh={refreshMe}
        onLeft={async () => { await refreshMe(); window.location.hash = '#/'; }}
        onSignOut={signOut}
      />
    </>
  );
}

function NoWorkspace({ user, onCreate, onSignOut }) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  return (
    <main className="auth">
      <form className="auth-card" onSubmit={async (e) => { e.preventDefault(); try { await onCreate(name.trim()); } catch (err) { setError(err.message); } }}>
        <h1 className="serif" style={{ fontSize: 28, margin: 0 }}>You are not in a workspace</h1>
        <span className="small muted">{user.name}, create a workspace or ask a teammate for an invite link.</span>
        <label className="lbl" htmlFor="nw-name">Workspace name</label>
        <input id="nw-name" className="field" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
        {error && <div className="alert" role="alert">{error}</div>}
        <button className="btn primary block" type="submit" disabled={!name.trim()} style={{ justifyContent: 'center' }}>Create workspace</button>
        <button className="btn ghost" type="button" onClick={onSignOut}>Sign out</button>
      </form>
    </main>
  );
}

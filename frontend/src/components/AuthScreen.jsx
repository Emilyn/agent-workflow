import { useEffect, useState } from 'react';
import { api } from '../api.js';
import { Spark } from './Icons.jsx';

// Sign in or create an account. When the page was opened from an invite link,
// `invite` names the workspace so people know what they are joining.
export default function AuthScreen({ onAuthed, invite }) {
  const [mode, setMode] = useState(invite ? 'signup' : 'login');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [working, setWorking] = useState(false);
  const signup = mode === 'signup';

  // The invite details arrive after the first render; people with a link most likely need a new account.
  useEffect(() => {
    if (invite) setMode('signup');
  }, [invite]);

  const submit = async (e) => {
    e.preventDefault();
    setError('');
    if (signup && !name.trim()) return setError('Enter your name so teammates know who you are.');
    if (!email.trim()) return setError('Enter your email address.');
    if (signup && password.length < 10) return setError('Use a password of at least 10 characters.');
    if (!password) return setError('Enter your password.');
    setWorking(true);
    try {
      const me = signup
        ? await api.signup({ name: name.trim(), email: email.trim(), password })
        : await api.login({ email: email.trim(), password });
      onAuthed(me);
    } catch (err) {
      setError(err.message);
    } finally {
      setWorking(false);
    }
  };

  return (
    <main className="auth">
      <div className="auth-card">
        <div className="row g12">
          <div className="logo"><Spark size={20} /></div>
          <span className="serif" style={{ fontSize: 26 }}>Agent Workflow</span>
        </div>
        {invite && (
          <div className="box" role="status">
            You have been invited to join <strong>{invite.workspace}</strong> as {invite.role === 'admin' ? 'an admin' : 'a member'}. {signup ? 'Create an account' : 'Sign in'} to continue.
          </div>
        )}
        <h1 className="serif" style={{ fontSize: 30, margin: 0 }}>{signup ? 'Create your account' : 'Sign in'}</h1>
        <form className="col g12" onSubmit={submit} noValidate>
          {signup && (
            <div className="col g6">
              <label className="lbl" htmlFor="auth-name">Your name</label>
              <input id="auth-name" className="field" autoComplete="name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
            </div>
          )}
          <div className="col g6">
            <label className="lbl" htmlFor="auth-email">Email</label>
            <input id="auth-email" className="field" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div className="col g6">
            <label className="lbl" htmlFor="auth-password">Password</label>
            <input id="auth-password" className="field" type="password" autoComplete={signup ? 'new-password' : 'current-password'} value={password} onChange={(e) => setPassword(e.target.value)} aria-describedby={signup ? 'pw-hint' : undefined} />
            {signup && <span id="pw-hint" className="small muted">At least 10 characters.</span>}
          </div>
          {error && <div className="alert" role="alert">{error}</div>}
          <button className="btn primary block" type="submit" disabled={working} style={{ justifyContent: 'center' }}>
            {working ? 'One moment…' : signup ? 'Create account' : 'Sign in'}
          </button>
        </form>
        <button type="button" className="link-btn" style={{ alignSelf: 'flex-start', padding: 0 }} onClick={() => { setMode(signup ? 'login' : 'signup'); setError(''); }}>
          {signup ? 'I already have an account' : 'Create an account'}
        </button>
      </div>
    </main>
  );
}

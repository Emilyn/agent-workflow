// Where we are lives in the URL hash, so reload, back/forward and shared links all work.
//   #/s/<workspace>                        the workflows home
//   #/s/<workspace>/runs                   every run, and what is waiting for you
//   #/s/<workspace>/new                    create a workflow
//   #/s/<workspace>/w/<workflow>           a workflow
//   #/s/<workspace>/settings/<section>     workspace settings (claude, integrations, triggers, team)
//   #/join/<token>                         an invite link
export function parseHash(hash = window.location.hash) {
  let m = hash.match(/^#\/join\/([\w-]+)$/);
  if (m) return { kind: 'join', token: m[1] };
  m = hash.match(/^#\/s\/([\w-]+)\/settings(?:\/([\w-]+))?$/);
  if (m) return { kind: 'ws', ws: m[1], wf: '', page: 'settings', section: m[2] || '' };
  m = hash.match(/^#\/s\/([\w-]+)\/runs$/);
  if (m) return { kind: 'ws', ws: m[1], wf: '', page: 'runs' };
  m = hash.match(/^#\/s\/([\w-]+)\/new$/);
  if (m) return { kind: 'ws', ws: m[1], wf: '', page: 'new' };
  m = hash.match(/^#\/s\/([\w-]+)(?:\/w\/([\w-]+))?$/);
  if (m) return { kind: 'ws', ws: m[1], wf: m[2] || '', page: m[2] ? 'editor' : 'home' };
  return { kind: 'none' };
}

export const hashFor = (ws, wf) => `#/s/${ws}${wf ? `/w/${wf}` : ''}`;

export const runsHash = (ws) => `#/s/${ws}/runs`;

export const newHash = (ws) => `#/s/${ws}/new`;

export const settingsHash = (ws, section) => `#/s/${ws}/settings${section ? `/${section}` : ''}`;

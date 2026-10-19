// Where we are lives in the URL hash, so reload, back/forward and shared links all work.
//   #/s/<workspace>/w/<workflow>   a workflow
//   #/s/<workspace>/settings/<section>   workspace settings (claude, integrations, triggers, team)
//   #/join/<token>                 an invite link
export function parseHash(hash = window.location.hash) {
  let m = hash.match(/^#\/join\/([\w-]+)$/);
  if (m) return { kind: 'join', token: m[1] };
  m = hash.match(/^#\/s\/([\w-]+)\/settings(?:\/([\w-]+))?$/);
  if (m) return { kind: 'ws', ws: m[1], wf: '', page: 'settings', section: m[2] || '' };
  m = hash.match(/^#\/s\/([\w-]+)(?:\/w\/([\w-]+))?$/);
  if (m) return { kind: 'ws', ws: m[1], wf: m[2] || '' };
  return { kind: 'none' };
}

export const hashFor = (ws, wf) => `#/s/${ws}${wf ? `/w/${wf}` : ''}`;

export const settingsHash = (ws, section) => `#/s/${ws}/settings${section ? `/${section}` : ''}`;

// Thin wrapper around the Go API. Every call throws an ApiError with a readable message.
export class ApiError extends Error {
  constructor(message, status, data) {
    super(message);
    this.status = status;
    this.data = data;
  }
}

async function request(method, path, body) {
  let res;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError('Cannot reach the server. Is the backend running?', 0);
  }
  let data = null;
  try {
    data = await res.json();
  } catch {
    /* non-JSON body */
  }
  if (!res.ok) {
    // A 401 anywhere except the sign-in calls means the session ended.
    if (res.status === 401 && !path.startsWith('/api/auth/')) window.dispatchEvent(new Event('aw:signed-out'));
    throw new ApiError((data && data.error) || `Request failed (${res.status})`, res.status, data);
  }
  return data;
}

const w = (ws) => `/api/workspaces/${ws}`;

export const api = {
  // account
  signup: (body) => request('POST', '/api/auth/signup', body),
  login: (body) => request('POST', '/api/auth/login', body),
  logout: () => request('POST', '/api/auth/logout'),
  me: () => request('GET', '/api/me'),
  invitePreview: (token) => request('GET', `/api/invites/preview?token=${encodeURIComponent(token)}`),
  acceptInvite: (token) => request('POST', '/api/invites/accept', { token }),

  // workspaces and people
  createWorkspace: (name) => request('POST', '/api/workspaces', { name }),
  renameWorkspace: (ws, name) => request('PATCH', w(ws), { name }),
  deleteWorkspace: (ws) => request('DELETE', w(ws)),
  members: (ws) => request('GET', `${w(ws)}/members`),
  setRole: (ws, uid, role) => request('PATCH', `${w(ws)}/members/${uid}`, { role }),
  removeMember: (ws, uid) => request('DELETE', `${w(ws)}/members/${uid}`),
  invites: (ws) => request('GET', `${w(ws)}/invites`),
  createInvite: (ws, body) => request('POST', `${w(ws)}/invites`, body),
  deleteInvite: (ws, id) => request('DELETE', `${w(ws)}/invites/${id}`),

  // Claude and shared settings
  claudeStatus: (ws) => request('GET', `${w(ws)}/claude`),
  connectClaude: (ws, apiKey) => request('PUT', `${w(ws)}/claude/key`, { apiKey }),
  disconnectClaude: (ws) => request('DELETE', `${w(ws)}/claude/key`),
  draftWorkflow: (ws, description) => request('POST', `${w(ws)}/workflows/draft`, { description }),
  setClaudeCodeSettings: (ws, settings) => request('PUT', `${w(ws)}/claude/code`, settings),
  setClaudeMode: (ws, mode) => request('PUT', `${w(ws)}/claude/mode`, { mode }),
  integrations: (ws) => request('GET', `${w(ws)}/integrations`),
  setIntegrationApp: (ws, id, app) => request('PUT', `${w(ws)}/integrations/${id}/app`, app),
  deleteIntegrationApp: (ws, id) => request('DELETE', `${w(ws)}/integrations/${id}/app`),
  integrationStart: (ws, id) => request('POST', `${w(ws)}/integrations/${id}/start`, {}),
  integrationDisconnect: (ws, id) => request('DELETE', `${w(ws)}/integrations/${id}`),
  triggers: (ws) => request('GET', `${w(ws)}/triggers`),
  createTrigger: (ws, body) => request('POST', `${w(ws)}/triggers`, body),
  patchTrigger: (ws, id, body) => request('PATCH', `${w(ws)}/triggers/${id}`, body),
  deleteTrigger: (ws, id) => request('DELETE', `${w(ws)}/triggers/${id}`),
  decideStep: (ws, wf, step, body) => request('POST', `${w(ws)}/workflows/${wf}/steps/${step}/decide`, body),
  workspaceRuns: (ws) => request('GET', `${w(ws)}/runs`),
  runs: (ws, wf) => request('GET', `${w(ws)}/workflows/${wf}/runs`),
  startRun: (ws, wf, body) => request('POST', `${w(ws)}/workflows/${wf}/runs`, body),
  rerun: (ws, runId) => request('POST', `${w(ws)}/runs/${runId}/rerun`, {}),
  approveAction: (ws, id) => request('POST', `${w(ws)}/review-actions/${id}/approve`, {}),
  discardAction: (ws, id) => request('POST', `${w(ws)}/review-actions/${id}/discard`, {}),
  getSettings: (ws) => request('GET', `${w(ws)}/settings`),
  saveSettings: (ws, s) => request('PUT', `${w(ws)}/settings`, s),

  // workflows
  listWorkflows: (ws) => request('GET', `${w(ws)}/workflows`),
  createWorkflow: (ws, body) => request('POST', `${w(ws)}/workflows`, body),
  getWorkflow: (ws, id) => request('GET', `${w(ws)}/workflows/${id}`),
  saveWorkflow: (ws, id, wf) => request('PUT', `${w(ws)}/workflows/${id}`, wf),
  deleteWorkflow: (ws, id) => request('DELETE', `${w(ws)}/workflows/${id}`),
  sync: (ws, id, after, version) => request('GET', `${w(ws)}/workflows/${id}/sync?after=${after}&version=${version}`),
  postMessage: (ws, id, body) => request('POST', `${w(ws)}/workflows/${id}/messages`, body),
  runStep: (ws, id, stepId) => request('POST', `${w(ws)}/workflows/${id}/run/step`, { stepId }),
  resetRun: (ws, id) => request('POST', `${w(ws)}/workflows/${id}/run/reset`),
  chat: (ws, id, payload) => request('POST', `${w(ws)}/workflows/${id}/chat`, payload),
};

// Thin wrapper around the Go API. Every call throws an Error with a readable message.
async function request(method, path, body) {
  let res;
  try {
    res = await fetch(path, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new Error('Cannot reach the server. Is the Go backend running?');
  }
  let data = null;
  try {
    data = await res.json();
  } catch {
    /* non-JSON body */
  }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status})`);
  return data;
}

export const api = {
  health: () => request('GET', '/api/health'),
  claudeStatus: () => request('GET', '/api/claude'),
  connectClaude: (apiKey) => request('PUT', '/api/claude/key', { apiKey }),
  disconnectClaude: () => request('DELETE', '/api/claude/key'),
  getWorkspace: () => request('GET', '/api/workspace'),
  saveWorkspace: (ws) => request('PUT', '/api/workspace', ws),
  listWorkflows: () => request('GET', '/api/workflows'),
  createWorkflow: (body) => request('POST', '/api/workflows', body),
  getWorkflow: (id) => request('GET', `/api/workflows/${id}`),
  saveWorkflow: (id, wf) => request('PUT', `/api/workflows/${id}`, wf),
  deleteWorkflow: (id) => request('DELETE', `/api/workflows/${id}`),
  runStep: (id, stepId) => request('POST', `/api/workflows/${id}/run/step`, { stepId }),
  resetRun: (id) => request('POST', `/api/workflows/${id}/run/reset`),
  chat: (id, payload) => request('POST', `/api/workflows/${id}/chat`, payload),
};

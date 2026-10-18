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
  getState: () => request('GET', '/api/state'),
  saveState: (doc) => request('PUT', '/api/state', doc),
  runStep: (stepId) => request('POST', '/api/run/step', { stepId }),
  resetRun: () => request('POST', '/api/run/reset'),
  chat: (payload) => request('POST', '/api/chat', payload),
};

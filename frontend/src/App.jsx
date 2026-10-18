import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from './api.js';
import Canvas from './components/Canvas.jsx';
import Header from './components/Header.jsx';
import Palette from './components/Palette.jsx';
import Side from './components/Side.jsx';
import {
  addMessage,
  agentNode,
  allAgents,
  blankForm,
  findNode,
  fullName,
  insertStep,
  integrationName,
  patchNode,
  personNode,
  removeStep,
  runSummary,
} from './workflow.js';

const escapeRegExp = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const hashId = () => {
  const m = window.location.hash.match(/^#\/w\/([\w-]+)$/);
  return m ? m[1] : '';
};

export default function App() {
  const [doc, setDoc] = useState(null); // the open workflow
  const [ws, setWs] = useState(null); // shared by every workflow: custom agents, integrations
  const [list, setList] = useState([]);
  const [selected, setSelected] = useState(null);
  const [tab, setTab] = useState('chat');
  const [form, setForm] = useState(blankForm());
  const [formError, setFormError] = useState('');
  const [draft, setDraft] = useState('');
  const [thinking, setThinking] = useState('');
  const [busy, setBusy] = useState(false);
  const [saveState, setSaveState] = useState('saved');
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [claude, setClaude] = useState(null);
  const dirty = useRef(false);
  const wsDirty = useRef(false);
  const docRef = useRef(null);
  const wsRef = useRef(null);
  docRef.current = doc;
  wsRef.current = ws;

  // Components read one combined object: the workflow plus the shared workspace.
  const view = useMemo(() => (doc && ws ? { ...doc, ...ws } : null), [doc, ws]);

  const show = useCallback((wf) => {
    dirty.current = false;
    setDoc(wf);
    setSelected(wf.steps[0][0].id);
    setDraft('');
    setError('');
    if (hashId() !== wf.id) window.location.hash = `#/w/${wf.id}`;
  }, []);

  // Save anything pending (used before switching, copying and running).
  const flush = useCallback(async () => {
    if (dirty.current && docRef.current) {
      dirty.current = false;
      await api.saveWorkflow(docRef.current.id, docRef.current);
    }
    if (wsDirty.current && wsRef.current) {
      wsDirty.current = false;
      await api.saveWorkspace(wsRef.current);
    }
    setSaveState('saved');
  }, []);

  const openWorkflow = useCallback(async (id) => {
    await flush();
    show(await api.getWorkflow(id));
  }, [flush, show]);

  // Load the workspace and the workflow named in the URL (or the first one).
  useEffect(() => {
    Promise.all([api.listWorkflows(), api.getWorkspace()])
      .then(async ([l, w]) => {
        setList(l);
        setWs(w);
        const id = l.find((x) => x.id === hashId())?.id || l[0].id;
        show(await api.getWorkflow(id));
      })
      .catch((e) => setLoadError(e.message));
    api.claudeStatus().then(setClaude).catch(() => {});
  }, [show]);

  // Back/forward and edited links switch workflows too.
  const openRef = useRef(openWorkflow);
  openRef.current = openWorkflow;
  useEffect(() => {
    const onHash = () => {
      const id = hashId();
      if (id && docRef.current && id !== docRef.current.id) {
        openRef.current(id).catch((e) => setError(`Could not open that workflow: ${e.message}`));
      }
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  // Every change goes through update() / updateWs() so it gets saved after a short pause.
  const update = useCallback((fn) => {
    dirty.current = true;
    setDoc(fn);
  }, []);
  const updateWs = useCallback((fn) => {
    wsDirty.current = true;
    setWs(fn);
  }, []);

  useEffect(() => {
    if (!doc || !dirty.current) return undefined;
    setSaveState('saving');
    const timer = setTimeout(() => {
      dirty.current = false;
      api.saveWorkflow(doc.id, doc)
        .then(() => setSaveState('saved'))
        .catch((e) => {
          setSaveState('error');
          setError(`Could not save: ${e.message}`);
        });
    }, 500);
    return () => clearTimeout(timer);
  }, [doc]);

  useEffect(() => {
    if (!ws || !wsDirty.current) return undefined;
    setSaveState('saving');
    const timer = setTimeout(() => {
      wsDirty.current = false;
      api.saveWorkspace(ws)
        .then(() => setSaveState('saved'))
        .catch((e) => {
          setSaveState('error');
          setError(`Could not save: ${e.message}`);
        });
    }, 500);
    return () => clearTimeout(timer);
  }, [ws]);

  if (loadError) {
    return (
      <div className="splash">
        <span className="serif" style={{ fontSize: 28 }}>Cannot load your workflows</span>
        <span>{loadError}</span>
        <span className="muted small">Start the Go backend with <code>go run .</code> in <code>backend/</code>, then reload.</span>
      </div>
    );
  }
  if (!doc || !ws) return <div className="splash"><span className="muted">Loading…</span></div>;

  const node = findNode(doc.steps, selected) || doc.steps[0][0];
  const { done, waiting } = runSummary(doc);
  const currentRow = done ? [] : doc.steps[doc.progress];
  const runLabel = busy
    ? 'Running…'
    : done
      ? 'Run again'
      : waiting
        ? `Mark ${waiting}’s step done`
        : currentRow.some((n) => n.kind === 'trigger')
          ? 'Start run'
          : `Run step ${doc.progress + 1}`;

  // ---------- actions ----------
  const openBuilder = () => {
    setTab('agent');
    setFormError('');
  };

  const select = (id) => {
    setSelected(id);
    setTab('settings');
  };

  const addAgent = (agent) => {
    const n = agentNode(view, agent);
    const { doc: next, idx } = insertStep(doc, selected, n);
    update(() => addMessage(next, 'Claude', 'agent', `${agent.name} is now step ${idx + 1}. It will receive the output of the step before it.`));
    select(n.id);
  };

  const addPerson = (step) => {
    const n = personNode(view, step);
    const { doc: next, idx } = insertStep(doc, selected, n);
    update(() => addMessage(next, 'Claude', 'agent', `${step.name} is now step ${idx + 1}, assigned to Theo. You can reassign it in Step settings.`));
    select(n.id);
  };

  const removeSelected = (id) => {
    const next = removeStep(doc, id);
    update(() => next);
    setSelected(next.steps[0][0].id);
  };

  const saveAgent = (addToo) => {
    if (!form.name.trim() || !form.instructions.trim()) {
      setFormError('Add a name and instructions so the agent knows what to do.');
      return;
    }
    const agent = {
      id: `c${Date.now()}`,
      name: fullName(form.name),
      role: form.role.trim(),
      instruction: form.instructions.trim(),
      tools: Object.keys(form.tools).filter((k) => form.tools[k]),
      model: form.model,
      mine: true,
    };
    if (allAgents(view).some((a) => a.name.toLowerCase() === agent.name.toLowerCase())) {
      setFormError(`An agent called “${agent.name}” already exists. Pick a different name.`);
      return;
    }
    updateWs((w) => ({ ...w, customAgents: [...w.customAgents, agent] }));
    let next = doc;
    if (addToo) {
      const n = agentNode(view, agent);
      const placed = insertStep(next, selected, n);
      next = addMessage(placed.doc, 'Claude', 'agent', `${agent.name} is now step ${placed.idx + 1}. It will receive the output of the step before it.`);
      setSelected(n.id);
      setTab('settings');
    } else {
      next = addMessage(next, 'Claude', 'agent', `${agent.name} is saved. Add it from the Agents list, or mention it with @${agent.name}.`);
      setTab('chat');
    }
    update(() => next);
    setForm(blankForm());
    setFormError('');
  };

  const toggleIntegration = (key) => {
    const on = !ws.connected[key];
    updateWs((w) => ({ ...w, connected: { ...w.connected, [key]: on } }));
    if (on) {
      const name = integrationName(view, key);
      update((d) => addMessage(d, 'Claude', 'agent', `${name} is marked as connected for all your workflows. This is a placeholder: agents cannot reach ${name} until real sign-in is added.`));
    }
  };

  const connectClaude = async (apiKey) => {
    const status = await api.connectClaude(apiKey); // throws with a readable message if rejected
    setClaude(status);
    update((d) => addMessage(d, 'Claude', 'agent', 'Claude is connected with your API key. Agent steps and replies now run for real.'));
  };

  const disconnectClaude = async () => {
    setClaude(await api.disconnectClaude());
  };

  // ---------- running ----------
  const advance = async () => {
    if (busy) return;
    setError('');
    if (done) {
      try {
        await api.resetRun(doc.id);
        update((d) => ({ ...d, progress: 0, outputs: {} }));
      } catch (e) {
        setError(e.message);
      }
      return;
    }
    const agents = currentRow.filter((n) => n.kind === 'agent');
    setBusy(true);
    try {
      const outputs = {};
      if (agents.length) {
        await flush();
        const results = await Promise.all(agents.map((n) => api.runStep(doc.id, n.id)));
        results.forEach((r) => {
          outputs[r.stepId] = { text: r.text, simulated: r.simulated };
        });
      }
      update((d) => ({ ...d, progress: d.progress + 1, outputs: { ...d.outputs, ...outputs } }));
    } catch (e) {
      setError(`The run paused: ${e.message}`);
      api.getWorkflow(doc.id).then((s) => update((d) => ({ ...d, outputs: s.outputs }))).catch(() => {});
    } finally {
      setBusy(false);
    }
  };

  // ---------- chat ----------
  const sendMessage = async () => {
    const text = draft.trim();
    if (!text || thinking) return;
    setDraft('');
    const lower = text.toLowerCase();
    const hit = allAgents(view).find((a) => lower.includes(`@${a.name.toLowerCase()}`));
    const inFlow = hit && doc.steps.flat().some((n) => n.agentId === hit.id);
    let next = addMessage(doc, 'You', 'human', text);

    if (hit && !inFlow) {
      update(() => addMessage(next, hit.name, 'agent', 'I am not in this workflow yet. Add me from the Agents list and I will pick it up from there.'));
      return;
    }

    let instruction = '';
    if (hit) {
      // The message becomes part of the agent's instructions, so the next run really follows it.
      const note = text.replace(new RegExp(`@${escapeRegExp(hit.name)}`, 'ig'), '').trim();
      next = {
        ...next,
        steps: next.steps.map((g) =>
          g.map((n) => (n.agentId === hit.id ? { ...n, instruction: `${n.instruction}\n\nTeam note: ${note}` } : n))),
      };
      instruction = next.steps.flat().find((n) => n.agentId === hit.id).instruction;
    }
    update(() => next);

    const who = hit ? hit.name : 'Claude';
    setThinking(who);
    try {
      const r = await api.chat(doc.id, { agentName: hit ? hit.name : '', instruction, message: text });
      update((d) => addMessage(d, who, 'agent', r.reply));
    } catch (e) {
      update((d) => addMessage(d, who, 'agent', `I could not reply: ${e.message}`));
    } finally {
      setThinking('');
    }
  };

  return (
    <div className="shell">
      <Header
        current={{ id: doc.id, name: doc.name }}
        switcher={{
          list,
          disabled: busy || !!thinking,
          onOpenMenu: async () => {
            try {
              await flush();
              setList(await api.listWorkflows());
            } catch (e) {
              setError(e.message);
            }
          },
          onOpen: openWorkflow,
          onCreate: async (body) => {
            await flush();
            const wf = await api.createWorkflow(body);
            setList(await api.listWorkflows());
            show(wf);
            setTab('chat');
          },
          onRename: async (name) => {
            const wf = { ...docRef.current, name };
            dirty.current = false;
            await api.saveWorkflow(wf.id, wf);
            setDoc(wf);
            setList(await api.listWorkflows());
          },
          onDuplicate: async () => {
            await flush();
            const wf = await api.createWorkflow({ copyOf: doc.id });
            setList(await api.listWorkflows());
            show(wf);
          },
          onDelete: async () => {
            dirty.current = false;
            const rest = await api.deleteWorkflow(doc.id);
            setList(rest);
            show(await api.getWorkflow(rest[0].id));
          },
        }}
        saveState={saveState}
        runLabel={runLabel}
        busy={busy}
        onAdvance={advance}
      />
      {error && (
        <div className="banner" role="alert">
          <span>{error}</span>
          <button type="button" className="link-btn" onClick={() => setError('')}>Dismiss</button>
        </div>
      )}
      {claude && !claude.connected && (
        <div className="banner info">
          <span>Claude is not connected, so agent steps and replies are simulated.</span>
          <button type="button" className="link-btn" onClick={() => setTab('integrations')}>Connect Claude</button>
        </div>
      )}
      <div className="body">
        <Palette doc={view} onAddAgent={addAgent} onAddPerson={addPerson} onCreateAgent={openBuilder} />
        <Canvas doc={view} selected={node.id} busy={busy} onSelect={select} onCreateAgent={openBuilder} />
        <Side
          tab={tab}
          onTab={setTab}
          doc={view}
          node={node}
          chat={{ draft, thinking, onDraft: setDraft, onSend: sendMessage }}
          settings={{
            onEdit: (id, patch) => update((d) => patchNode(d, id, patch)),
            onRemove: removeSelected,
            onDiscuss: (n) => {
              setTab('chat');
              setDraft(`About “${n.title}”: `);
            },
          }}
          integrations={{
            claude,
            onConnectClaude: connectClaude,
            onDisconnectClaude: disconnectClaude,
            onProvider: (p) => updateWs((w) => ({ ...w, gitProvider: p })),
            onToggle: toggleIntegration,
          }}
          builder={{
            form,
            error: formError,
            onChange: (patch) => {
              setForm((f) => ({ ...f, ...patch }));
              setFormError('');
            },
            onSave: saveAgent,
            onCancel: () => {
              setTab('chat');
              setFormError('');
            },
            onGoIntegrations: () => setTab('integrations'),
          }}
        />
      </div>
    </div>
  );
}


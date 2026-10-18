import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from './api.js';
import Canvas from './components/Canvas.jsx';
import Header from './components/Header.jsx';
import Palette from './components/Palette.jsx';
import Side from './components/Side.jsx';
import { hashFor, parseHash } from './route.js';
import {
  ROLE_RANK,
  agentNode,
  allAgents,
  blankForm,
  findNode,
  fullName,
  insertStep,
  integrationName,
  mergeMessages,
  patchNode,
  personNode,
  removeStep,
  runSummary,
} from './workflow.js';

const escapeRegExp = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const POLL_MS = 4000;

export default function Workspace({ ws, workspaces, user, wfFromUrl, onSwitchWorkspace, onCreateWorkspace, onRefresh, onLeft, onSignOut }) {
  const wsId = ws.id;
  const role = ws.role;
  const canManage = ROLE_RANK[role] >= ROLE_RANK.admin;

  const [doc, setDoc] = useState(null); // the open workflow
  const [settings, setSettings] = useState(null); // custom agents and integrations, shared by the workspace
  const [members, setMembers] = useState([]);
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
  const settingsDirty = useRef(false);
  const saving = useRef(false);
  const docRef = useRef(null);
  const settingsRef = useRef(null);
  const busyRef = useRef(false);
  const version = useRef(0);
  const lastMsg = useRef(0);
  const saveChain = useRef(Promise.resolve());
  docRef.current = doc;
  settingsRef.current = settings;
  busyRef.current = busy || !!thinking;

  // Components read one combined object: the workflow, the shared settings and the people.
  const view = useMemo(() => (doc && settings ? { ...doc, ...settings, members } : null), [doc, settings, members]);

  // ---------- loading ----------
  const show = useCallback((wf, { keepSelection = false, notice = '', replace = false } = {}) => {
    dirty.current = false;
    version.current = wf.version;
    lastMsg.current = wf.messages.length ? wf.messages[wf.messages.length - 1].id : 0;
    setDoc(wf);
    setSelected((cur) => (keepSelection && findNode(wf.steps, cur) ? cur : wf.steps[0][0].id));
    if (!keepSelection) setDraft('');
    setError(notice);
    const want = hashFor(wsId, wf.id);
    if (window.location.hash !== want) window.history[replace ? 'replaceState' : 'pushState'](null, '', want);
  }, [wsId]);

  const persistDoc = useCallback(() => {
    saveChain.current = saveChain.current.then(async () => {
      const cur = docRef.current;
      if (!dirty.current || !cur) return;
      dirty.current = false;
      saving.current = true;
      const { messages, outputs, ...rest } = cur; // chat and results are owned by the server
      try {
        const saved = await api.saveWorkflow(wsId, cur.id, { ...rest, version: version.current });
        version.current = saved.version;
        setSaveState('saved');
      } catch (e) {
        if (e.status === 409 && e.data && e.data.workflow) {
          show(e.data.workflow, { keepSelection: true, notice: 'A teammate saved this workflow at the same moment, so your last change was not applied. Their version is shown.' });
          setSaveState('saved');
        } else {
          dirty.current = true;
          setSaveState('error');
          setError(`Could not save: ${e.message}`);
        }
      } finally {
        saving.current = false;
      }
    });
    return saveChain.current;
  }, [wsId, show]);

  const persistSettings = useCallback(async () => {
    if (!settingsDirty.current || !settingsRef.current) return;
    settingsDirty.current = false;
    try {
      await api.saveSettings(wsId, settingsRef.current);
      setSaveState('saved');
    } catch (e) {
      setSaveState('error');
      setError(`Could not save: ${e.message}`);
      api.getSettings(wsId).then(setSettings).catch(() => {});
    }
  }, [wsId]);

  // Save anything pending (before switching, copying and running).
  const flush = useCallback(async () => {
    await persistDoc();
    await persistSettings();
  }, [persistDoc, persistSettings]);

  const openWorkflow = useCallback(async (id) => {
    await flush();
    show(await api.getWorkflow(wsId, id));
  }, [flush, show, wsId]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [l, st, m, c] = await Promise.all([api.listWorkflows(wsId), api.getSettings(wsId), api.members(wsId), api.claudeStatus(wsId)]);
        if (cancelled) return;
        setList(l);
        setSettings(st);
        setMembers(m);
        setClaude(c);
        const id = l.find((x) => x.id === wfFromUrl)?.id || l[0].id;
        const wf = await api.getWorkflow(wsId, id);
        if (!cancelled) show(wf, { replace: true });
      } catch (e) {
        if (!cancelled && e.status !== 401) setLoadError(e.message);
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsId]);

  // Back/forward and edited links switch workflows.
  const openRef = useRef(openWorkflow);
  openRef.current = openWorkflow;
  useEffect(() => {
    const onHash = () => {
      const r = parseHash();
      if (r.kind === 'ws' && r.ws === wsId && r.wf && docRef.current && r.wf !== docRef.current.id) {
        openRef.current(r.wf).catch((e) => setError(`Could not open that workflow: ${e.message}`));
      }
    };
    window.addEventListener('hashchange', onHash);
    window.addEventListener('popstate', onHash);
    return () => { window.removeEventListener('hashchange', onHash); window.removeEventListener('popstate', onHash); };
  }, [wsId]);

  // ---------- saving ----------
  const update = useCallback((fn) => {
    dirty.current = true;
    setDoc(fn);
  }, []);
  const updateSettings = useCallback((fn) => {
    settingsDirty.current = true;
    setSettings(fn);
  }, []);

  useEffect(() => {
    if (!doc || !dirty.current) return undefined;
    setSaveState('saving');
    const timer = setTimeout(persistDoc, 500);
    return () => clearTimeout(timer);
  }, [doc, persistDoc]);

  useEffect(() => {
    if (!settings || !settingsDirty.current) return undefined;
    setSaveState('saving');
    const timer = setTimeout(persistSettings, 500);
    return () => clearTimeout(timer);
  }, [settings, persistSettings]);

  // Save before the tab closes.
  useEffect(() => {
    const onHide = () => { if (document.visibilityState === 'hidden') flush(); };
    document.addEventListener('visibilitychange', onHide);
    return () => document.removeEventListener('visibilitychange', onHide);
  }, [flush]);

  // ---------- teammates: new messages, results and saved changes ----------
  const docId = doc?.id;
  useEffect(() => {
    if (!docId) return undefined;
    let stopped = false;
    const tick = async () => {
      if (document.hidden) return;
      try {
        const r = await api.sync(wsId, docId, lastMsg.current, version.current);
        if (stopped || docRef.current?.id !== docId) return;
        if (r.workflow && !dirty.current && !saving.current && !busyRef.current) {
          show(r.workflow, { keepSelection: true });
          return;
        }
        if (r.messages.length) {
          lastMsg.current = r.messages[r.messages.length - 1].id;
          setDoc((d) => ({ ...d, messages: mergeMessages(d.messages, r.messages) }));
        }
        setDoc((d) => (JSON.stringify(d.outputs) === JSON.stringify(r.outputs) ? d : { ...d, outputs: r.outputs }));
      } catch (e) {
        if (e.status === 404 && !stopped) {
          // Someone deleted this workflow.
          const l = await api.listWorkflows(wsId).catch(() => []);
          setList(l);
          if (l.length) show(await api.getWorkflow(wsId, l[0].id), { notice: 'That workflow was deleted by a teammate.' });
        }
      }
    };
    const t = setInterval(tick, POLL_MS);
    return () => { stopped = true; clearInterval(t); };
  }, [wsId, docId, show]);

  // Teammates joining or leaving: refresh now and then, and whenever a screen that lists them opens.
  useEffect(() => {
    const t = setInterval(() => { if (!document.hidden) api.members(wsId).then(setMembers).catch(() => {}); }, 15000);
    return () => clearInterval(t);
  }, [wsId]);
  useEffect(() => {
    if (tab === 'team' || tab === 'settings') api.members(wsId).then(setMembers).catch(() => {});
  }, [tab, selected, wsId]);

  if (loadError) {
    return (
      <div className="splash">
        <span className="serif" style={{ fontSize: 28 }}>Cannot load this workspace</span>
        <span>{loadError}</span>
        <button type="button" className="btn" onClick={() => window.location.reload()}>Reload</button>
      </div>
    );
  }
  if (!view) return <div className="splash"><span className="muted">Loading…</span></div>;

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

  // ---------- chat helpers ----------
  const note = (text) =>
    api.postMessage(wsId, doc.id, { text, kind: 'note' })
      .then((m) => {
        lastMsg.current = Math.max(lastMsg.current, m.id);
        setDoc((d) => ({ ...d, messages: mergeMessages(d.messages, [m]) }));
      })
      .catch(() => {});

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
    update(() => next);
    note(`${agent.name} is now step ${idx + 1}. It will receive the output of the step before it.`);
    select(n.id);
  };

  const addPerson = (step) => {
    const n = personNode(view, step, user.name);
    const { doc: next, idx } = insertStep(doc, selected, n);
    update(() => next);
    note(`${step.name} is now step ${idx + 1}, assigned to ${user.name}. You can reassign it in Step settings.`);
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
    updateSettings((st) => ({ ...st, customAgents: [...st.customAgents, agent] }));
    if (addToo) {
      const n = agentNode(view, agent);
      const placed = insertStep(doc, selected, n);
      update(() => placed.doc);
      note(`${agent.name} is now step ${placed.idx + 1}. It will receive the output of the step before it.`);
      setSelected(n.id);
      setTab('settings');
    } else {
      note(`${agent.name} is saved. Add it from the Agents list, or mention it with @${agent.name}.`);
      setTab('chat');
    }
    setForm(blankForm());
    setFormError('');
  };

  const toggleIntegration = (key) => {
    const on = !settings.connected[key];
    updateSettings((st) => ({ ...st, connected: { ...st.connected, [key]: on } }));
    if (on) {
      const name = integrationName(view, key);
      note(`${name} is marked as connected for the whole workspace. This is a placeholder: agents cannot reach ${name} until real sign-in is added.`);
    }
  };

  const connectClaude = async (apiKey) => {
    const status = await api.connectClaude(wsId, apiKey); // throws with a readable message if rejected
    setClaude(status);
    note('Claude is connected with the workspace’s API key. Agent steps and replies now run for real.');
  };

  const disconnectClaude = async () => {
    setClaude(await api.disconnectClaude(wsId));
  };

  // ---------- running ----------
  const advance = async () => {
    if (busy) return;
    setError('');
    if (done) {
      try {
        await flush();
        const fresh = await api.resetRun(wsId, doc.id);
        show(fresh, { keepSelection: true });
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
        const results = await Promise.all(agents.map((n) => api.runStep(wsId, doc.id, n.id)));
        results.forEach((r) => {
          outputs[r.stepId] = { text: r.text, simulated: r.simulated };
        });
      }
      update((d) => ({ ...d, progress: d.progress + 1, outputs: { ...d.outputs, ...outputs } }));
    } catch (e) {
      setError(`The run paused: ${e.message}`);
      api.getWorkflow(wsId, doc.id).then((s) => setDoc((d) => ({ ...d, outputs: s.outputs }))).catch(() => {});
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
    const add = (msgs) => {
      lastMsg.current = Math.max(lastMsg.current, ...msgs.map((m) => m.id));
      setDoc((d) => ({ ...d, messages: mergeMessages(d.messages, msgs) }));
    };

    if (hit && !inFlow) {
      try {
        add([await api.postMessage(wsId, doc.id, { text })]);
        await note(`${hit.name} is not in this workflow yet. Add it from the Agents list and it will pick this up from there.`);
      } catch (e) {
        setError(e.message);
      }
      return;
    }

    let instruction = '';
    if (hit) {
      // The message becomes part of the agent's instructions, so the next run really follows it.
      const noteText = text.replace(new RegExp(`@${escapeRegExp(hit.name)}`, 'ig'), '').trim();
      const steps = doc.steps.map((g) =>
        g.map((n) => (n.agentId === hit.id ? { ...n, instruction: `${n.instruction}\n\nTeam note: ${noteText}` } : n)));
      instruction = steps.flat().find((n) => n.agentId === hit.id).instruction;
      update((d) => ({ ...d, steps }));
    }

    setThinking(hit ? hit.name : 'Claude');
    try {
      const r = await api.chat(wsId, doc.id, { agentName: hit ? hit.name : '', instruction, message: text });
      add(r.messages);
    } catch (e) {
      setError(`Could not send: ${e.message}`);
      setDraft(text);
    } finally {
      setThinking('');
    }
  };

  // ---------- header menus ----------
  const switcher = {
    list,
    disabled: busy || !!thinking,
    onOpenMenu: async () => {
      try {
        await flush();
        setList(await api.listWorkflows(wsId));
      } catch (e) {
        setError(e.message);
      }
    },
    onOpen: openWorkflow,
    onCreate: async (body) => {
      await flush();
      const wf = await api.createWorkflow(wsId, body);
      setList(await api.listWorkflows(wsId));
      show(wf);
      setTab('chat');
    },
    onRename: async (name) => {
      await persistDoc();
      const { messages, outputs, ...rest } = docRef.current;
      const saved = await api.saveWorkflow(wsId, doc.id, { ...rest, name, version: version.current });
      version.current = saved.version;
      setDoc((d) => ({ ...d, name: saved.name, version: saved.version }));
      setList(await api.listWorkflows(wsId));
    },
    onDuplicate: async () => {
      await flush();
      const wf = await api.createWorkflow(wsId, { copyOf: doc.id });
      setList(await api.listWorkflows(wsId));
      show(wf);
    },
    onDelete: async () => {
      dirty.current = false;
      const rest = await api.deleteWorkflow(wsId, doc.id);
      setList(rest);
      show(await api.getWorkflow(wsId, rest[0].id));
    },
  };

  const workspaceMenu = {
    current: ws,
    workspaces,
    user,
    disabled: busy || !!thinking,
    onSwitch: async (id) => { await flush(); onSwitchWorkspace(id); },
    onCreate: async (name) => { await flush(); await onCreateWorkspace(name); },
    onTeam: () => setTab('team'),
    onSignOut: async () => { await flush().catch(() => {}); await onSignOut(); },
  };

  return (
    <div className="shell">
      <Header
        current={{ id: doc.id, name: doc.name }}
        switcher={switcher}
        workspaceMenu={workspaceMenu}
        members={members}
        saveState={saveState}
        runLabel={runLabel}
        busy={busy}
        onAdvance={advance}
        onShare={() => setTab('team')}
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
          <button type="button" className="link-btn" onClick={() => setTab('integrations')}>{canManage ? 'Connect Claude' : 'Learn more'}</button>
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
          me={{ id: user.id, name: user.name }}
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
            canManage,
            onConnectClaude: connectClaude,
            onDisconnectClaude: disconnectClaude,
            onProvider: (p) => updateSettings((st) => ({ ...st, gitProvider: p })),
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
          team={{
            wsId,
            wsName: ws.name,
            role,
            members,
            onMembers: setMembers,
            onRenamed: () => onRefresh(),
            onLeft,
            onDeleted: onLeft,
          }}
        />
      </div>
    </div>
  );
}

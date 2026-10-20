import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from './api.js';
import Canvas from './components/Canvas.jsx';
import Header from './components/Header.jsx';
import HomePage from './components/HomePage.jsx';
import NewWorkflowPage from './components/NewWorkflowPage.jsx';
import RunsPage, { needsYou } from './components/RunsPage.jsx';
import Sidebar from './components/Sidebar.jsx';
import Side from './components/Side.jsx';
import SettingsPage from './components/SettingsPage.jsx';
import { hashFor, newHash, parseHash, runsHash, settingsHash } from './route.js';
import {
  ROLE_RANK,
  LIVE_TOOLS,
  agentNode,
  allAgents,
  blankForm,
  findNode,
  fullName,
  insertAt,
  insertStep,
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
  const [page, setPage] = useState(() => parseHash().page || 'home'); // home | new | editor | settings
  const [wsRuns, setWsRuns] = useState([]); // newest runs of every workflow
  const [busyAction, setBusyAction] = useState('');
  const [copySource, setCopySource] = useState(null); // the workflow offered as "copy of" on the new-workflow page
  const [section, setSection] = useState(() => parseHash().section || 'claude');
  const [form, setForm] = useState(blankForm());
  const [formError, setFormError] = useState('');
  const [draft, setDraft] = useState('');
  const [thinking, setThinking] = useState('');
  const [busy, setBusy] = useState(false);
  const [saveState, setSaveState] = useState('saved');
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [claude, setClaude] = useState(null);
  const [ints, setInts] = useState(null); // connection status of GitHub, GitLab, Jira and Figma

  const dirty = useRef(false);
  const settingsDirty = useRef(false);
  const saving = useRef(false);
  const docRef = useRef(null);
  const settingsRef = useRef(null);
  const busyRef = useRef(false);
  const version = useRef(0);
  const lastMsg = useRef(0);
  const saveChain = useRef(Promise.resolve());
  const pageRef = useRef(page);
  const lastMain = useRef(page === 'editor' ? 'editor' : 'home'); // where leaving settings goes back to
  docRef.current = doc;
  pageRef.current = page;
  settingsRef.current = settings;
  busyRef.current = busy || !!thinking;

  // Components read one combined object: the workflow, the shared settings and the people.
  const view = useMemo(() => (doc && settings ? { ...doc, ...settings, members } : null), [doc, settings, members]);

  const openSettings = useCallback((sec, { replace = false } = {}) => {
    const next = sec || 'claude';
    pageRef.current = 'settings';
    setPage('settings');
    setSection(next);
    const want = settingsHash(wsId, next);
    if (window.location.hash !== want) window.history[replace ? 'replaceState' : 'pushState'](null, '', want);
  }, [wsId]);
  const setMain = useCallback((p, hash) => {
    pageRef.current = p;
    setPage(p);
    if (p === 'home' || p === 'editor') lastMain.current = p;
    if (window.location.hash !== hash) window.history.pushState(null, '', hash);
  }, []);
  const goHome = useCallback(() => setMain('home', hashFor(wsId)), [setMain, wsId]);
  const closeSettings = useCallback(() => {
    if (lastMain.current === 'editor' && docRef.current) setMain('editor', hashFor(wsId, docRef.current.id));
    else goHome();
  }, [goHome, setMain, wsId]);

  // ---------- loading ----------
  const show = useCallback((wf, { keepSelection = false, notice = '', replace = false, open = false } = {}) => {
    dirty.current = false;
    version.current = wf.version;
    lastMsg.current = wf.messages.length ? wf.messages[wf.messages.length - 1].id : 0;
    setDoc(wf);
    setSelected((cur) => (keepSelection && findNode(wf.steps, cur) ? cur : wf.steps[0][0].id));
    if (!keepSelection) setDraft('');
    setError(notice);
    // Opening a workflow shows the editor. Refreshing the open one never moves you to another page.
    if (open && pageRef.current !== 'editor') {
      pageRef.current = 'editor';
      lastMain.current = 'editor';
      setPage('editor');
    }
    if (pageRef.current === 'editor') {
      const want = hashFor(wsId, wf.id);
      if (window.location.hash !== want) window.history[replace ? 'replaceState' : 'pushState'](null, '', want);
    }
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
    show(await api.getWorkflow(wsId, id), { open: true });
  }, [flush, show, wsId]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [l, st, m, c, connections] = await Promise.all([api.listWorkflows(wsId), api.getSettings(wsId), api.members(wsId), api.claudeStatus(wsId), api.integrations(wsId)]);
        if (cancelled) return;
        setList(l);
        setSettings(st);
        setMembers(m);
        setClaude(c);
        setInts(connections);
        const id = l.find((x) => x.id === wfFromUrl)?.id || l[0].id; // a workflow is always loaded in the background
        const wf = await api.getWorkflow(wsId, id);
        if (!cancelled) {
          show(wf, { replace: true });
        }
      } catch (e) {
        if (!cancelled && e.status !== 401) setLoadError(e.message);
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsId]);

  // Coming back from a provider: ?integration=github&result=connected, or result=error&reason=...
  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    const id = q.get('integration');
    if (!id) return;
    const label = LIVE_TOOLS.find((t) => t.id === id)?.label || 'the tool';
    const reasons = {
      denied: `${label} access was not granted.`,
      expired: `The ${label} sign-in took too long. Try again.`,
      state: `The ${label} sign-in could not be verified. Try again from this browser.`,
      session: `You were signed out while connecting ${label}. Sign in and try again.`,
      forbidden: `Only workspace admins can connect ${label}.`,
      exchange: `${label} did not accept the sign-in. Try again.`,
    };
    const result = q.get('result');
    const reason = q.get('reason');
    window.history.replaceState(null, '', window.location.pathname + window.location.hash);
    openSettings('integrations', { replace: true });
    if (result === 'connected') api.integrations(wsId).then(setInts).catch(() => {});
    else setError(reasons[reason] || `Could not connect ${label}.`);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Back/forward and edited links switch workflows.
  const openRef = useRef(openWorkflow);
  openRef.current = openWorkflow;
  useEffect(() => {
    const onHash = () => {
      const r = parseHash();
      if (r.kind !== 'ws' || r.ws !== wsId) return;
      pageRef.current = r.page;
      setPage(r.page);
      if (r.page === 'settings') {
        setSection(r.section || 'claude');
        return;
      }
      if (r.page === 'home' || r.page === 'editor') lastMain.current = r.page;
      if (r.page === 'editor' && r.wf && docRef.current && r.wf !== docRef.current.id) {
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
          if (l.length) {
            show(await api.getWorkflow(wsId, l[0].id), { notice: 'That workflow was deleted by a teammate.' });
            if (pageRef.current === 'editor') setMain('home', hashFor(wsId));
          }
        }
      }
    };
    const t = setInterval(tick, POLL_MS);
    return () => { stopped = true; clearInterval(t); };
  }, [wsId, docId, show, setMain]);

  // Teammates joining or leaving: refresh now and then, and whenever a screen that lists them opens.
  useEffect(() => {
    const t = setInterval(() => { if (!document.hidden) api.members(wsId).then(setMembers).catch(() => {}); }, 15000);
    return () => clearInterval(t);
  }, [wsId]);
  useEffect(() => {
    if (tab === 'team' || tab === 'settings') api.members(wsId).then(setMembers).catch(() => {});
  }, [tab, selected, wsId]);

  // The Runs page and its badge: newest runs and which workflows are paused on a person.
  const refreshInbox = useCallback(async () => {
    try {
      const [r, l] = await Promise.all([api.workspaceRuns(wsId), api.listWorkflows(wsId)]);
      setWsRuns(r);
      setList(l);
    } catch (e) {
      if (e.status === 401) throw e;
    }
  }, [wsId]);
  useEffect(() => {
    refreshInbox().catch(() => {});
    const t = setInterval(() => { if (!document.hidden && !dirty.current) refreshInbox().catch(() => {}); }, 8000);
    return () => clearInterval(t);
  }, [refreshInbox]);

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

  const addAgent = (agent, at) => {
    const n = agentNode(view, agent);
    const { doc: next, idx } = insertAt(doc, at, n);
    update(() => next);
    note(`${agent.name} is now step ${idx + 1}. It will receive the output of the step before it.`);
    select(n.id);
  };

  const addPerson = (step, at) => {
    const n = personNode(view, step, user.name);
    const { doc: next, idx } = insertAt(doc, at, n);
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
      note(`${agent.name} is saved. Add it with the + between steps, or mention it with @${agent.name}.`);
      setTab('chat');
    }
    setForm(blankForm());
    setFormError('');
  };

  const connectClaude = async (apiKey) => {
    const status = await api.connectClaude(wsId, apiKey); // throws with a readable message if rejected
    setClaude(status);
    note('Claude is connected with the workspace’s API key. Agent steps and replies now run for real.');
  };

  const connectTool = async (id) => {
    const { url } = await api.integrationStart(wsId, id); // the provider asks for consent, then sends the browser back here
    window.location.href = url;
  };

  const disconnectTool = async (id) => {
    setInts(await api.integrationDisconnect(wsId, id));
  };

  const changeClaudeMode = async (mode) => {
    setClaude(await api.setClaudeMode(wsId, mode)); // throws with a readable message when it cannot be used
  };
  const saveClaudeCode = async (settings) => {
    setClaude(await api.setClaudeCodeSettings(wsId, settings));
  };
  const refreshClaude = async () => {
    setClaude(await api.claudeStatus(wsId));
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
        await note(`${hit.name} is not in this workflow yet. Add it with the + between steps and it will pick this up from there.`);
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

  // ---------- workflow actions ----------
  const goNew = async () => {
    try {
      await flush();
    } catch (e) {
      setError(e.message);
    }
    setCopySource(page === 'editor' ? { id: doc.id, name: doc.name } : null);
    setMain('new', newHash(wsId));
  };
  const openFromList = (id) => openWorkflow(id).catch((e) => setError(`Could not open that workflow: ${e.message}`));
  const createWorkflow = async ({ name, template, draft }) => {
    await flush();
    const body = template === 'copy' ? { name, template: '', copyOf: copySource.id } : { name, template };
    let wf = await api.createWorkflow(wsId, body);
    if (draft) {
      // Claude's accepted proposal goes in after the trigger row.
      const nodes = draft.steps.map((st, i) => (st.kind === 'agent'
        ? { id: `u${wf.nextId + i}`, kind: 'agent', title: st.title, desc: st.desc, who: 'Claude Sonnet', instruction: st.instruction, tools: st.tools }
        : { id: `u${wf.nextId + i}`, kind: 'human', title: st.title, desc: st.desc, who: user.name, assignee: user.name, instruction: st.instruction, tools: [] }));
      const { messages, outputs, ...rest } = wf;
      wf = { ...wf, ...(await api.saveWorkflow(wsId, wf.id, { ...rest, steps: [wf.steps[0], ...nodes.map((n) => [n])], nextId: wf.nextId + nodes.length, version: wf.version })) };
    }
    setList(await api.listWorkflows(wsId));
    show(wf, { open: true });
    setTab('chat');
  };
  const workflowMenu = {
    canDelete: list.length > 1,
    disabled: busy || !!thinking,
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
      show(wf, { open: true });
    },
    onDelete: async () => {
      dirty.current = false;
      const rest = await api.deleteWorkflow(wsId, doc.id);
      setList(rest);
      show(await api.getWorkflow(wsId, rest[0].id));
      goHome();
    },
  };

  const workspaceMenu = {
    current: ws,
    workspaces,
    user,
    disabled: busy || !!thinking,
    onSwitch: async (id) => { await flush(); onSwitchWorkspace(id); },
    onCreate: async (name) => { await flush(); await onCreateWorkspace(name); },
    onTeam: () => openSettings('team'),
    onSettings: () => openSettings('claude'),
    onSignOut: async () => { await flush().catch(() => {}); await onSignOut(); },
  };

  const decide = (fn) => async (a) => {
    setBusyAction(a.id);
    setError('');
    try {
      await fn(wsId, a.id);
    } catch (e) {
      setError(e.message);
    }
    setBusyAction('');
    refreshInbox().catch(() => {});
  };
  const approveAction = decide(api.approveAction);
  const discardAction = decide(api.discardAction);
  const rerunRun = async (run) => {
    setError('');
    try {
      await api.rerun(wsId, run.id);
    } catch (e) {
      setError(e.message);
    }
    refreshInbox().catch(() => {});
  };

  const decideStep = async (wf, body) => {
    await api.decideStep(wsId, wf.id, wf.waitingStep, body); // throws with a readable message when it is refused
    if (docRef.current?.id === wf.id) show(await api.getWorkflow(wsId, wf.id), { keepSelection: true });
    await refreshInbox();
  };

  const goFromSidebar = async (id) => {
    try {
      await flush();
    } catch (e) {
      setError(e.message);
    }
    if (id === 'workflows') goHome();
    else if (id === 'runs') setMain('runs', runsHash(wsId));
    else openSettings(id === 'settings' ? 'claude' : id);
  };

  return (
    <div className="app">
      <Sidebar
        workspaceMenu={workspaceMenu}
        badge={needsYou(list, wsRuns, user.name)}
        page={page}
        section={section}
        workflows={list}
        currentId={doc.id}
        onGo={goFromSidebar}
        onOpenWorkflow={openFromList}
      />
      <div className="shell">
      {page === 'editor' && (
        <Header
          current={{ id: doc.id, name: doc.name }}
          workflowMenu={workflowMenu}
          members={members}
          saveState={saveState}
          runLabel={runLabel}
          busy={busy}
          onHome={goHome}
          onAdvance={advance}
          onShare={() => openSettings('team')}
        />
      )}
      {error && (
        <div className="banner" role="alert">
          <span>{error}</span>
          <button type="button" className="link-btn" onClick={() => setError('')}>Dismiss</button>
        </div>
      )}
      {claude && !claude.connected && page === 'editor' && (
        <div className="banner info">
          <span>Claude is not connected, so agent steps and replies are simulated.</span>
          <button type="button" className="link-btn" onClick={() => openSettings('claude')}>{canManage ? 'Connect Claude' : 'Learn more'}</button>
        </div>
      )}
      {page === 'home' && (
        <HomePage
          ws={ws}
          list={list}
          claude={claude}
          ints={ints}
          members={members}
          canManage={canManage}
          onNew={goNew}
          onOpen={openFromList}
          onGo={(sec) => openSettings(sec)}
          onInvite={() => openSettings('team')}
        />
      )}
      {page === 'runs' && (
        <RunsPage wsId={wsId} list={list} runs={wsRuns} userName={user.name} canManage={canManage} busyAction={busyAction} onApprove={approveAction} onDiscard={discardAction} onRerun={rerunRun} onDecide={decideStep} onOpenWorkflow={openFromList} />
      )}
      {page === 'new' && (
        <NewWorkflowPage currentName={copySource?.name || ''} canDraft={!!claude?.connected} onDraft={(text) => api.draftWorkflow(wsId, text)} onCreate={createWorkflow} onCancel={goHome} />
      )}
      {page === 'settings' ? (
        <SettingsPage
          section={section}
          onSection={(id) => openSettings(id)}
          onClose={closeSettings}
          canManage={canManage}
          claude={claude}
          ints={ints}
          workflows={list}
          currentWorkflowId={doc.id}
          wsId={wsId}
          me={{ id: user.id, name: user.name }}
          onConnectClaude={connectClaude}
          onDisconnectClaude={disconnectClaude}
          onClaudeMode={changeClaudeMode}
          onClaudeCode={saveClaudeCode}
          onRefreshClaude={refreshClaude}
          onConnect={connectTool}
          onDisconnect={disconnectTool}
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
      ) : page === 'editor' ? (
      <div className="body">
        <Canvas doc={view} selected={node.id} busy={busy} onSelect={select} onCreateAgent={openBuilder} onAddAgent={addAgent} onAddPerson={addPerson} />
        <Side
          tab={tab}
          onTab={setTab}
          doc={view}
          node={node}
          me={{ id: user.id, name: user.name }}
          chat={{ draft, thinking, onDraft: setDraft, onSend: sendMessage }}
          settings={{
            ints,
            onGoIntegrations: () => openSettings('integrations'),
            onEdit: (id, patch) => update((d) => patchNode(d, id, patch)),
            onRemove: removeSelected,
            onDiscuss: (n) => {
              setTab('chat');
              setDraft(`About “${n.title}”: `);
            },
          }}
          runs={{ wsId, wfId: doc.id, ints, onGoIntegrations: () => openSettings('integrations'), onManageTriggers: () => openSettings('triggers') }}
          builder={{
            ints,
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
            onGoIntegrations: () => openSettings('integrations'),
          }}
        />
      </div>
      ) : null}
      </div>
    </div>
  );
}

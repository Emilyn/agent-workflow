/* Agent Workflow: build workflows from agents and people, and talk to the team.
   Plain JavaScript, no build step. Open index.html or run `python3 -m http.server`. */
(function () {
  'use strict';

  var STORE_KEY = 'agent-workflow-v1';
  var root = document.getElementById('app');

  // ---------- icons ----------
  var ICON = {
    spark: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5L18 18M18 6l-2.5 2.5M8.5 15.5L6 18"/></svg>',
    person: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="8" r="4"/><path d="M4 21c0-4 3.5-7 8-7s8 3 8 7"/></svg>',
    clock: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
    play: '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M7 4l13 8-13 8z"/></svg>',
    send: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12h14M13 6l6 6-6 6"/></svg>',
    git: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="6" cy="5" r="2.2"/><circle cx="6" cy="19" r="2.2"/><circle cx="18" cy="9" r="2.2"/><path d="M6 7.2v9.6M18 11.2c0 4-6 3-12 6"/></svg>',
    jira: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4" y="4" width="16" height="16" rx="3"/><path d="M8 12l3 3 5-6"/></svg>',
    figma: '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg>'
  };

  // ---------- data ----------
  var PEOPLE = { Amara: { initials: 'AM', bg: '#EBD9CE', fg: '#5A2E18' }, Theo: { initials: 'TH', bg: '#D5E3E0', fg: '#1C4A45' } };

  var LIBRARY = [
    { id: 'research', name: 'Research agent', role: 'Searches the web and your files', instruction: 'Search the sources you are given, collect the relevant facts with links, and flag anything you could not verify.', tools: ['Web search', 'Docs'], model: 'Claude Sonnet' },
    { id: 'writer', name: 'Writer agent', role: 'Drafts and rewrites', instruction: 'Draft clear, concise copy in the team’s voice from the material provided. Keep to the length and structure requested.', tools: ['Docs'], model: 'Claude Sonnet' },
    { id: 'analyst', name: 'Analyst agent', role: 'Works through data', instruction: 'Look for patterns and changes in the data you receive, compare with earlier periods, and explain what matters.', tools: ['Sheets'], model: 'Claude Sonnet' },
    { id: 'reviewer', name: 'Reviewer agent', role: 'Checks work against criteria', instruction: 'Check the previous step’s output against the criteria you are given and list what needs fixing before it moves on.', tools: ['Docs'], model: 'Claude Sonnet' }
  ];

  var PERSON_STEPS = [
    { key: 'review', name: 'Review and edit', hint: 'A person improves the output', desc: 'Check the work, fix wording, confirm the details.', instruction: 'Read the previous output, edit anything unclear and confirm it is ready to move on.' },
    { key: 'approval', name: 'Approval', hint: 'A yes or no gate', desc: 'A yes or no before anything is shared.', instruction: 'Approve the work, or send it back with a note for the agent before it.' },
    { key: 'input', name: 'Ask for input', hint: 'A question or short form', desc: 'Pause the run and ask for a decision or missing detail.', instruction: 'Answer the question the workflow needs before it can continue.' },
    { key: 'handoff', name: 'Hand off', hint: 'Assign to a teammate', desc: 'Hand the work to a teammate to finish.', instruction: 'Take over from here and finish the remaining work.' }
  ];

  var INTEGRATIONS = {
    git: { desc: 'Let agents read repositories, open pull requests and respond to reviews. Pick GitHub or GitLab.', can: ['Read code and diffs', 'Open pull requests', 'Comment on reviews'], note: 'read code and open pull requests', icon: 'git' },
    jira: { name: 'Jira', desc: 'Let agents read issues and sprints, and move tickets forward when a step finishes.', can: ['Read issues and sprints', 'Create and update tickets', 'Comment on tickets'], note: 'read and update tickets', icon: 'jira' },
    figma: { name: 'Figma', desc: 'Let agents read frames and comments, and attach designs to a step.', can: ['Read files and frames', 'Read comments', 'Export images'], note: 'read frames and comments', icon: 'figma' }
  };

  function baseRows() {
    return [
      [{ id: 'n1', kind: 'trigger', title: 'Every Monday, 9:00', desc: 'Starts the weekly run automatically.', who: 'Schedule', instruction: '', tools: [] }],
      [
        { id: 'n2', agentId: 'research', kind: 'agent', title: 'Research agent', desc: 'Pulls last week’s tickets, reviews and call notes.', who: 'Claude Sonnet', instruction: 'Collect every support ticket, app review and call note from the last 7 days. Remove duplicates and anything marked private.', tools: ['Web search', 'Docs'] },
        { id: 'n3', agentId: 'analyst', kind: 'agent', title: 'Analyst agent', desc: 'Clusters themes and tracks sentiment shifts.', who: 'Claude Sonnet', instruction: 'Group feedback into themes, compare with the previous four weeks and flag anything that grew sharply.', tools: ['Sheets'] }
      ],
      [{ id: 'n4', agentId: 'writer', kind: 'agent', title: 'Writer agent', desc: 'Drafts the digest in the team’s voice.', who: 'Claude Sonnet', instruction: 'Write the weekly digest in under 300 words. Lead with the top three themes and quote customers only from cleared tickets.', tools: ['Docs'] }],
      [{ id: 'n5', kind: 'human', title: 'Review and edit', desc: 'Check the draft, fix wording, confirm every quote.', who: 'Theo', assignee: 'Theo', instruction: 'Read the draft next to the source tickets. Edit anything unclear and confirm each customer quote is cleared.', tools: [] }],
      [{ id: 'n6', kind: 'human', title: 'Approve and send', desc: 'Final yes or no before anything is shared.', who: 'Amara', assignee: 'Amara', instruction: 'Approve the reviewed digest, or send it back with a note for the Writer agent.', tools: [] }],
      [{ id: 'n7', kind: 'agent', title: 'Distribution agent', desc: 'Posts to the insights channel and emails team leads.', who: 'Claude Sonnet', instruction: 'Post the approved digest to the insights channel and email it to team leads with a link to the full data.', tools: ['Slack', 'Email'] }]
    ];
  }

  function blankForm() { return { name: '', role: '', instructions: '', model: 'Claude Sonnet', tools: {} }; }

  var state = {
    steps: baseRows(),
    progress: 4,
    selected: 'n5',
    tab: 'chat',
    nextId: 1,
    gitProvider: 'github',
    connected: { git: false, jira: false, figma: false },
    customAgents: [],
    form: blankForm(),
    formError: '',
    draft: '',
    messages: [
      { author: 'Amara', kind: 'human', time: '9:12', text: 'Can we add a human check before the digest goes out? Last week it quoted a ticket we had not cleared.' },
      { author: 'Claude', kind: 'agent', time: '9:13', text: 'Done. I added a Review & edit step after the Writer and a final approval before sending. Theo, do you want to own the review?' },
      { author: 'Theo', kind: 'human', time: '9:15', text: '@Writer agent keep the digest under 300 words and lead with the top three themes.' },
      { author: 'Writer agent', kind: 'agent', time: '9:16', text: 'Understood. I will cap the draft at 300 words and open with the top three themes. The draft is ready for Theo to review.' }
    ]
  };

  // ---------- persistence (best effort) ----------
  function save() {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify({
        steps: state.steps, progress: state.progress, nextId: state.nextId, connected: state.connected,
        gitProvider: state.gitProvider, customAgents: state.customAgents, messages: state.messages
      }));
    } catch (e) {}
  }
  function load() {
    try {
      var raw = localStorage.getItem(STORE_KEY);
      if (!raw) return;
      var d = JSON.parse(raw);
      ['steps', 'progress', 'nextId', 'connected', 'gitProvider', 'customAgents', 'messages'].forEach(function (k) { if (d[k] !== undefined) state[k] = d[k]; });
      state.selected = state.steps[0][0].id;
    } catch (e) {}
  }

  // ---------- helpers ----------
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function allAgents() { return LIBRARY.concat(state.customAgents); }
  function gitName() { return state.gitProvider === 'gitlab' ? 'GitLab' : 'GitHub'; }
  function intName(key) { return key === 'git' ? gitName() : INTEGRATIONS[key].name; }
  function fullName(n) { n = (n || '').trim(); return /agent$/i.test(n) ? n : n + ' agent'; }
  function findNode(id) {
    for (var i = 0; i < state.steps.length; i++) for (var j = 0; j < state.steps[i].length; j++) if (state.steps[i][j].id === id) return state.steps[i][j];
    return null;
  }
  function rowOf(id) {
    for (var i = 0; i < state.steps.length; i++) for (var j = 0; j < state.steps[i].length; j++) if (state.steps[i][j].id === id) return i;
    return -1;
  }
  function statusFor(r, node) {
    if (r < state.progress) return 'done';
    if (r === state.progress) return node.kind === 'human' ? 'waiting' : 'running';
    return 'queued';
  }
  function pill(status, node) {
    var text = { done: 'Done', running: 'Running', queued: 'Queued', waiting: 'Waiting on ' + (node && node.assignee) }[status];
    return '<span class="pill ' + status + '">' + esc(text) + '</span>';
  }
  function say(author, kind, text) { state.messages.push({ author: author, kind: kind, time: 'Now', text: text }); }
  function avatar(author, kind) {
    if (kind === 'agent') return '<span class="avatar agent" style="border:none;width:32px;height:32px">' + esc(author === 'Claude' ? 'C' : author.charAt(0)) + '</span>';
    var p = PEOPLE[author] || { initials: author.slice(0, 2).toUpperCase(), bg: '#E4E1F2', fg: '#2F2F66' };
    return '<span class="avatar" style="background:' + p.bg + ';color:' + p.fg + ';border:none">' + esc(p.initials) + '</span>';
  }

  // ---------- actions ----------
  function insertStep(node) {
    var sel = rowOf(state.selected);
    var idx = Math.min(state.steps.length, Math.max(sel, state.progress) + 1);
    state.steps.splice(idx, 0, [node]);
    state.selected = node.id;
    state.tab = 'settings';
    state.nextId += 1;
    return idx;
  }
  function addAgentStep(agent) {
    var node = { id: 'u' + state.nextId, agentId: agent.id, kind: 'agent', title: agent.name, desc: agent.role || agent.instruction, who: agent.model, instruction: agent.instruction, tools: agent.tools.slice() };
    var idx = insertStep(node);
    say('Claude', 'agent', agent.name + ' is now step ' + (idx + 1) + '. It will receive the output of the step before it.');
  }
  function addPersonStep(key) {
    var p = PERSON_STEPS.filter(function (x) { return x.key === key; })[0];
    var node = { id: 'u' + state.nextId, kind: 'human', title: p.name, desc: p.desc, who: 'Theo', assignee: 'Theo', instruction: p.instruction, tools: [] };
    var idx = insertStep(node);
    say('Claude', 'agent', p.name + ' is now step ' + (idx + 1) + ', assigned to Theo. You can reassign it in Step settings.');
  }
  function removeStep(id) {
    var out = [], p = state.progress;
    state.steps.forEach(function (g, i) {
      var keep = g.filter(function (n) { return n.id !== id; });
      if (keep.length) out.push(keep); else if (i < p) p -= 1;
    });
    state.steps = out;
    state.progress = Math.min(p, out.length);
    state.selected = out[0][0].id;
  }
  function advance() {
    state.progress = state.progress >= state.steps.length ? 0 : state.progress + 1;
  }
  function toggleIntegration(key) {
    state.connected[key] = !state.connected[key];
    if (state.connected[key]) say('Claude', 'agent', intName(key) + ' is connected. Agents can now ' + INTEGRATIONS[key].note + '.');
  }
  function saveAgent(addToo) {
    var f = state.form;
    if (!f.name.trim() || !f.instructions.trim()) { state.formError = 'Add a name and instructions so the agent knows what to do.'; return; }
    var agent = {
      id: 'c' + Date.now(), name: fullName(f.name), role: f.role.trim(), instruction: f.instructions.trim(),
      tools: Object.keys(f.tools).filter(function (k) { return f.tools[k]; }), model: f.model, mine: true
    };
    if (allAgents().some(function (a) { return a.name.toLowerCase() === agent.name.toLowerCase(); })) {
      state.formError = 'An agent called “' + agent.name + '” already exists. Pick a different name.';
      return;
    }
    state.customAgents.push(agent);
    state.form = blankForm();
    state.formError = '';
    state.tab = 'chat';
    if (addToo) addAgentStep(agent);
    else say('Claude', 'agent', agent.name + ' is saved. Add it from the Agents list, or mention it with @' + agent.name + '.');
  }
  function sendMessage() {
    var text = state.draft.trim();
    if (!text) return;
    say('You', 'human', text);
    var lower = text.toLowerCase();
    var hit = allAgents().filter(function (a) { return lower.indexOf('@' + a.name.toLowerCase()) !== -1; })[0];
    if (hit) {
      var inFlow = state.steps.some(function (g) { return g.some(function (n) { return n.agentId === hit.id; }); });
      say(hit.name, 'agent', inFlow
        ? 'Understood. I will follow that in my step on the next run, alongside my standing instructions.'
        : 'I am not in this workflow yet. Add me from the Agents list and I will pick it up from there.');
    } else {
      say('Claude', 'agent', 'Noted. I will apply that to the next run and tell the agents involved.');
    }
    state.draft = '';
  }

  // ---------- views ----------
  function viewTop() {
    return '<header class="topbar">' +
      '<div class="row g12" style="min-width:0"><div class="logo">' + ICON.spark.replace('width="18" height="18"', 'width="20" height="20"') + '</div>' +
      '<div class="col" style="min-width:0"><span class="serif title">Weekly customer digest</span><span class="small muted">Workflows / Draft · saved on this device</span></div></div>' +
      '<div class="row wrap g12"><div class="row" aria-label="Collaborators: Amara, Theo and Claude">' +
      '<span class="avatar" style="background:#EBD9CE;color:#5A2E18" title="Amara">AM</span>' +
      '<span class="avatar" style="background:#D5E3E0;color:#1C4A45" title="Theo">TH</span>' +
      '<span class="avatar agent" title="Claude">' + ICON.spark.replace('width="18" height="18"', 'width="16" height="16"') + '</span></div>' +
      '<button class="btn" type="button">Share</button>' +
      '<button class="btn primary" type="button" data-action="advance">' + ICON.play + '<span>' + (state.progress >= state.steps.length ? 'Run again' : 'Advance run') + '</span></button></div></header>';
  }

  function viewPalette() {
    var agents = allAgents().map(function (a) {
      return '<button class="item" type="button" data-action="addAgent" data-id="' + esc(a.id) + '" aria-label="Add ' + esc(a.name) + ' to the workflow">' +
        '<span class="glyph">' + ICON.spark.replace('width="18" height="18"', 'width="16" height="16"') + '</span>' +
        '<span class="col"><span class="name">' + esc(a.name) + '</span><span class="hint">' + (a.mine ? 'Yours · ' : '') + esc(a.role || 'Custom agent') + '</span></span></button>';
    }).join('');
    var people = PERSON_STEPS.map(function (p) {
      return '<button class="item" type="button" data-action="addPerson" data-key="' + p.key + '" aria-label="Add ' + esc(p.name) + ' step">' +
        '<span class="glyph person">' + ICON.person.replace('width="18" height="18"', 'width="16" height="16"') + '</span>' +
        '<span class="col"><span class="name">' + esc(p.name) + '</span><span class="hint">' + esc(p.hint) + '</span></span></button>';
    }).join('');
    return '<aside class="palette">' +
      '<div class="col g8"><span class="eyebrow">Agents</span><span class="small muted">Click an agent to add it to the workflow after the current step.</span>' + agents +
      '<button class="item dashed" type="button" data-action="openBuilder">+ Create an agent</button></div>' +
      '<div class="col g8"><span class="eyebrow">People</span>' + people + '</div>' +
      '<div class="col g8"><span class="eyebrow">Logic</span><div class="row wrap g8">' +
      ['Branch', 'Parallel', 'Wait'].map(function (n) { return '<button class="chip" type="button" disabled title="Coming soon">' + n + '</button>'; }).join('') + '</div></div></aside>';
  }

  function viewCanvas() {
    var total = state.steps.length, done = state.progress >= total, waiting = null;
    if (!done) state.steps[state.progress].forEach(function (n) { if (n.kind === 'human') waiting = n.assignee; });
    var runPill = done ? '<span class="pill done">Run complete</span>' : (waiting ? '<span class="pill waiting">Paused, waiting on ' + esc(waiting) + '</span>' : '<span class="pill running">Running</span>');
    var rows = state.steps.map(function (group, r) {
      var cards = group.map(function (n) {
        var badge = n.kind === 'agent' ? '<span class="badge">' + ICON.spark + '</span>' : n.kind === 'human' ? '<span class="badge person">' + ICON.person + '</span>' : '<span class="badge trigger">' + ICON.clock + '</span>';
        var label = { trigger: 'Trigger', agent: 'Agent step', human: 'Person step' }[n.kind];
        return '<button type="button" class="card' + (state.selected === n.id ? ' sel' : '') + '" data-action="select" data-id="' + n.id + '">' + badge +
          '<span class="col g6" style="min-width:0;flex:1"><span class="eyebrow" style="font-size:11px">' + label + '</span>' +
          '<span style="font-size:16px;font-weight:600">' + esc(n.title) + '</span>' +
          '<span class="small" style="color:var(--ink-2)">' + esc(n.desc) + '</span>' +
          '<span class="row wrap g8" style="margin-top:6px">' + pill(statusFor(r, n), n) + '<span class="muted" style="font-size:12px">' + esc(n.who) + '</span></span></span></button>';
      }).join('');
      return '<div class="rowline">' + cards + '</div>' + (r < total - 1 ? '<div class="link"></div>' : '');
    }).join('');
    return '<main class="canvas-wrap"><div class="runbar"><div class="row wrap g12"><strong style="font-size:14px">Run 12</strong>' + runPill +
      '<span class="small muted">' + (done ? 'All ' + total + ' steps finished' : 'Step ' + (state.progress + 1) + ' of ' + total) + '</span></div></div>' +
      '<div class="canvas">' + rows + '<div class="link"></div><button class="btn" type="button" style="border:1.5px dashed #8E8976;background:transparent;border-radius:24px;height:48px;padding:0 22px" data-action="openBuilder">+ Create an agent for a new step</button></div></main>';
  }

  function tabsView() {
    function t(id, label) { return '<button class="tab' + (state.tab === id ? ' on' : '') + '" type="button" data-action="tab" data-id="' + id + '">' + label + '</button>'; }
    return '<div class="tabs">' + t('chat', 'Team chat') + t('settings', 'Step settings') + t('integrations', 'Integrations') + '</div>';
  }

  function viewChat() {
    var agentCount = 0;
    state.steps.forEach(function (g) { g.forEach(function (n) { if (n.kind === 'agent') agentCount++; }); });
    var msgs = state.messages.map(function (m) {
      return '<div class="row g12" style="align-items:flex-start">' + avatar(m.author, m.kind) +
        '<div class="col g6" style="min-width:0;flex:1"><div class="row wrap g8" style="align-items:baseline"><strong style="font-size:14px">' + esc(m.author) + '</strong>' +
        (m.kind === 'agent' ? '<span class="tag">Agent</span>' : '') + '<span class="muted" style="font-size:12px">' + esc(m.time) + '</span></div>' +
        '<div class="bubble' + (m.kind === 'agent' ? ' agent' : '') + '">' + esc(m.text) + '</div></div></div>';
    }).join('');
    var chips = allAgents().map(function (a) { return '@' + a.name; }).concat(['@Theo', '@Amara']).map(function (l) {
      return '<button class="tool" type="button" style="min-height:36px;padding:0 12px" data-action="mention" data-label="' + esc(l) + '">' + esc(l) + '</button>';
    }).join('');
    return '<div class="col" style="flex:1;min-height:0">' +
      '<div class="small muted" style="padding:16px 20px 4px">Amara, Theo and ' + agentCount + (agentCount === 1 ? ' agent are' : ' agents are') + ' in this workflow. Mention an agent by name to give it new instructions.</div>' +
      '<div class="col g16" id="msgs" style="flex:1;padding:12px 20px;overflow-y:auto">' + msgs + '</div>' +
      '<div class="row wrap g8" style="padding:8px 20px 0">' + chips + '</div>' +
      '<form class="row g8" id="chat-form" style="padding:12px 20px 20px"><input class="field" id="chat-input" aria-label="Message the team" placeholder="Message the team or @mention an agent" value="' + esc(state.draft) + '" data-field="draft" autocomplete="off">' +
      '<button class="btn primary" type="submit" aria-label="Send message" style="width:48px;height:48px;padding:0;justify-content:center;flex-shrink:0">' + ICON.send + '</button></form></div>';
  }

  function viewSettings() {
    var n = findNode(state.selected) || state.steps[0][0];
    var r = rowOf(n.id);
    var label = { trigger: 'Trigger', agent: 'Agent step', human: 'Person step' }[n.kind];
    var body = '';
    if (n.kind === 'agent') {
      body = '<div class="col g8"><label class="lbl" for="step-instr">Instructions</label><textarea class="field" id="step-instr" data-field="stepInstruction" data-id="' + n.id + '">' + esc(n.instruction) + '</textarea></div>' +
        '<div class="col g8"><span class="lbl">Tools and sources</span><div class="row wrap g8">' +
        (n.tools.length ? n.tools.map(function (t) { return '<span class="tag-chip">' + esc(t) + '</span>'; }).join('') : '<span class="small muted">No tools selected.</span>') + '</div></div>' +
        '<div class="row box" style="justify-content:space-between"><span>Model</span><strong>' + esc(n.who) + '</strong></div>';
    } else if (n.kind === 'human') {
      body = '<div class="col g8"><label class="lbl" for="assignee">Assigned to</label><select class="field" id="assignee" data-field="assignee" data-id="' + n.id + '">' +
        ['Amara', 'Theo'].map(function (p) { return '<option' + (n.assignee === p ? ' selected' : '') + '>' + p + '</option>'; }).join('') + '</select></div>' +
        '<div class="col g8"><label class="lbl" for="step-instr">What they will see</label><textarea class="field" id="step-instr" data-field="stepInstruction" data-id="' + n.id + '">' + esc(n.instruction) + '</textarea></div>';
    } else {
      body = '<div class="row box" style="justify-content:space-between"><span>Repeats</span><strong>Weekly, Monday 9:00</strong></div>';
    }
    return '<div class="panel"><div class="col g8"><span class="eyebrow" style="font-size:11px">' + label + '</span>' +
      '<span class="serif" style="font-size:28px">' + esc(n.title) + '</span><div>' + pill(statusFor(r, n), n) + '</div>' +
      '<span class="small" style="color:var(--ink-2);font-size:14px">' + esc(n.desc) + '</span></div>' + body +
      '<div class="col g8"><span class="lbl">If this step fails</span><div class="box">Pause the run and ask in team chat</div></div>' +
      (n.kind !== 'trigger' ? '<button class="btn danger block" type="button" data-action="removeStep" data-id="' + n.id + '">Remove this step</button>' : '') +
      '<button class="btn outline block" type="button" data-action="discuss" data-id="' + n.id + '">Discuss this step in chat</button></div>';
  }

  function viewIntegrations() {
    var cards = ['git', 'jira', 'figma'].map(function (key) {
      var it = INTEGRATIONS[key], on = state.connected[key], name = intName(key);
      var providers = key === 'git' && !on ? '<div class="seg" role="group" aria-label="Choose a code host">' +
        '<button type="button" class="' + (state.gitProvider === 'github' ? 'on' : '') + '" data-action="provider" data-id="github">GitHub</button>' +
        '<button type="button" class="' + (state.gitProvider === 'gitlab' ? 'on' : '') + '" data-action="provider" data-id="gitlab">GitLab</button></div>' : '';
      return '<div class="int"><div class="row g12" style="align-items:flex-start"><span class="logo" style="width:40px;height:40px">' + ICON[it.icon] + '</span>' +
        '<div class="col g6" style="min-width:0;flex:1"><div class="row wrap g8"><strong style="font-size:16px">' + esc(name) + '</strong>' +
        '<span class="pill ' + (on ? 'done' : 'queued') + '" style="padding:2px 10px">' + (on ? 'Connected' : 'Not connected') + '</span></div>' +
        '<span class="small" style="color:var(--ink-2)">' + esc(it.desc) + '</span></div></div>' + providers +
        '<div class="col g8"><span class="eyebrow">Agents will be able to</span><div class="row wrap g8">' + it.can.map(function (c) { return '<span class="tag-chip">' + esc(c) + '</span>'; }).join('') + '</div></div>' +
        (on ? '<span class="ok">Connected. Signed in with your ' + esc(name) + ' account.</span>' : '') +
        '<button class="btn block ' + (on ? 'outline' : 'primary') + '" type="button" data-action="toggleInt" data-id="' + key + '" style="justify-content:center">' + (on ? 'Disconnect ' : 'Connect ') + esc(name) + '</button></div>';
    }).join('');
    return '<div class="panel"><div class="col g6"><span class="serif" style="font-size:28px">Connect your tools</span>' +
      '<span style="font-size:14px;line-height:1.5;color:var(--ink-2)">Give agents access to your code, tickets and designs. This prototype simulates the sign-in; no real account is linked.</span></div>' + cards +
      '<span class="small muted">Connected tools show up as options when you create an agent, and you can disconnect any of them at any time.</span></div>';
  }

  function viewBuilder() {
    var f = state.form;
    var toolDefs = [{ l: 'Web search' }, { l: 'Docs' }, { l: 'Sheets' }, { l: 'Slack' }, { l: 'Email' }, { l: gitName(), need: 'git' }, { l: 'Jira', need: 'jira' }, { l: 'Figma', need: 'figma' }];
    var tools = toolDefs.map(function (t) {
      var locked = t.need && !state.connected[t.need];
      if (locked) return '<button type="button" class="tool locked" data-action="goIntegrations">' + esc(t.l) + ' · connect first</button>';
      var on = !!f.tools[t.l];
      return '<button type="button" class="tool' + (on ? ' on' : '') + '" aria-pressed="' + on + '" data-action="toggleTool" data-id="' + esc(t.l) + '">' + esc(t.l) + '</button>';
    }).join('');
    var models = ['Claude Sonnet', 'Claude Opus', 'Claude Haiku'].map(function (m) {
      return '<button type="button" class="' + (f.model === m ? 'on' : '') + '" aria-pressed="' + (f.model === m) + '" data-action="model" data-id="' + m + '">' + m + '</button>';
    }).join('');
    return '<div class="panel"><div class="col g6"><span class="eyebrow" style="font-size:11px">New agent</span><span class="serif" style="font-size:28px">Create an agent</span>' +
      '<span style="font-size:14px;line-height:1.5;color:var(--ink-2)">Describe what it should do. Once saved, it appears in your Agents list, and you can add it to a workflow or mention it in chat.</span></div>' +
      '<div class="col g8"><label class="lbl" for="agent-name">Name</label><input class="field" id="agent-name" placeholder="Release notes" value="' + esc(f.name) + '" data-field="formName"></div>' +
      '<div class="col g8"><label class="lbl" for="agent-role">What it does, in one line</label><input class="field" id="agent-role" placeholder="Turns merged pull requests into customer-facing notes" value="' + esc(f.role) + '" data-field="formRole"></div>' +
      '<div class="col g8"><label class="lbl" for="agent-instr">Instructions</label><textarea class="field" id="agent-instr" rows="6" placeholder="Read the merged pull requests and their linked tickets, then write short release notes in plain language." data-field="formInstructions">' + esc(f.instructions) + '</textarea></div>' +
      '<div class="col g8"><span class="lbl">Tools and sources</span><div class="row wrap g8">' + tools + '</div><span class="small muted">Dashed tools need a connection first. Select one to open Integrations.</span></div>' +
      '<div class="col g8"><span class="lbl">Model</span><div class="seg" role="group" aria-label="Choose a model">' + models + '</div></div>' +
      (state.formError ? '<span class="alert" role="alert">' + esc(state.formError) + '</span>' : '') +
      '<div class="col g8"><button class="btn primary block" type="button" data-action="saveAdd" style="justify-content:center">Save and add to workflow</button>' +
      '<button class="btn outline block" type="button" data-action="saveOnly">Save agent</button>' +
      '<button class="btn ghost" type="button" data-action="cancelBuilder">Cancel</button></div></div>';
  }

  function viewSide() {
    var inner = { chat: viewChat, settings: viewSettings, integrations: viewIntegrations, agent: viewBuilder }[state.tab]();
    return '<aside class="side">' + (state.tab === 'agent' ? '<div class="tabs"><button class="tab" type="button" data-action="tab" data-id="chat">Team chat</button><button class="tab" type="button" data-action="tab" data-id="settings">Step settings</button><button class="tab" type="button" data-action="tab" data-id="integrations">Integrations</button></div>' : tabsView()) + inner + '</aside>';
  }

  // ---------- render + events ----------
  function render() {
    var active = document.activeElement, keep = active && active.id, selStart = active && active.selectionStart;
    root.innerHTML = '<div class="shell">' + viewTop() + '<div class="body">' + viewPalette() + viewCanvas() + viewSide() + '</div></div>';
    var box = document.getElementById('msgs');
    if (box) box.scrollTop = box.scrollHeight;
    if (keep) {
      var el = document.getElementById(keep);
      if (el && el.focus) { el.focus(); try { if (selStart != null) el.setSelectionRange(selStart, selStart); } catch (e) {} }
    }
    save();
  }

  var actions = {
    advance: advance,
    select: function (d) { state.selected = d.id; state.tab = 'settings'; },
    tab: function (d) { state.tab = d.id; },
    addAgent: function (d) { addAgentStep(allAgents().filter(function (a) { return a.id === d.id; })[0]); },
    addPerson: function (d) { addPersonStep(d.key); },
    removeStep: function (d) { removeStep(d.id); },
    discuss: function (d) { state.tab = 'chat'; state.draft = 'About “' + findNode(d.id).title + '”: '; },
    mention: function (d) { state.draft = (state.draft ? state.draft + ' ' : '') + d.label + ' '; },
    provider: function (d) { state.gitProvider = d.id; },
    toggleInt: function (d) { toggleIntegration(d.id); },
    openBuilder: function () { state.tab = 'agent'; state.formError = ''; },
    cancelBuilder: function () { state.tab = 'chat'; state.formError = ''; },
    goIntegrations: function () { state.tab = 'integrations'; },
    toggleTool: function (d) { state.form.tools[d.id] = !state.form.tools[d.id]; },
    model: function (d) { state.form.model = d.id; },
    saveAdd: function () { saveAgent(true); },
    saveOnly: function () { saveAgent(false); }
  };

  root.addEventListener('click', function (e) {
    var el = e.target.closest('[data-action]');
    if (!el || !actions[el.dataset.action]) return;
    actions[el.dataset.action](el.dataset);
    render();
  });

  // Typing updates state without re-rendering, so the cursor stays put.
  root.addEventListener('input', function (e) {
    var f = e.target.dataset && e.target.dataset.field;
    if (!f) return;
    var v = e.target.value;
    if (f === 'draft') state.draft = v;
    else if (f === 'formName') { state.form.name = v; state.formError = ''; }
    else if (f === 'formRole') state.form.role = v;
    else if (f === 'formInstructions') { state.form.instructions = v; state.formError = ''; }
    else if (f === 'stepInstruction') { var n = findNode(e.target.dataset.id); if (n) n.instruction = v; save(); }
  });

  root.addEventListener('change', function (e) {
    if (e.target.dataset && e.target.dataset.field === 'assignee') {
      var n = findNode(e.target.dataset.id);
      if (n) { n.assignee = e.target.value; n.who = e.target.value; render(); }
    }
  });

  root.addEventListener('submit', function (e) {
    if (e.target.id !== 'chat-form') return;
    e.preventDefault();
    sendMessage();
    render();
  });

  load();
  render();
})();

'use strict';

// ── App state ─────────────────────────────────────────────────────────────────
// Exactly one view is ever shown at a time. Nothing besides the bottom bar
// is persistent — every other bit of UI (status, lists, detail, forms) is
// rendered fresh into #content based on this state object.
const state = {
  view: 'home',        // 'home' | 'goals' | 'directives' | 'goal-detail'
  hubTab: 'goals',      // which tab is active when view === 'goals' || 'directives'
  selectedGoalId: null,
  goals: [],
  directives: [],
  connected: null,      // null = unknown yet, true/false once checked
  status: null,         // last GET /api/status payload, or null before first fetch
};

function attentionGoals() {
  return state.goals.filter(g => g.needs_attention);
}

// ── DOM refs ──────────────────────────────────────────────────────────────────
const content      = document.getElementById('content');
const statusBar    = document.getElementById('status-bar');
const navBtn       = document.getElementById('nav-btn');
const navBadge     = document.getElementById('nav-badge');
const micBtn       = document.getElementById('mic-btn');
const sendBtn      = document.getElementById('send-btn');
const barForm      = document.getElementById('bar-form');
const barInput     = document.getElementById('bar-input');
const modalOverlay = document.getElementById('modal-overlay');
const modalTitle   = document.getElementById('modal-title');
const modalBody    = document.getElementById('modal-body');
const modalClose   = document.getElementById('modal-close');

// ── API helpers ───────────────────────────────────────────────────────────────
async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(`${res.status}: ${text}`);
  }
  // DELETE etc may return empty body on some servers; guard against that.
  const text = await res.text();
  return text ? JSON.parse(text) : {};
}

async function checkHealth() {
  try {
    await api('GET', '/api/health');
    state.connected = true;
  } catch {
    state.connected = false;
  }
}

async function refreshGoals() {
  try {
    const data = await api('GET', '/api/goals');
    state.goals = data.goals || [];
  } catch (err) {
    console.error('refreshGoals:', err);
  }
}

async function refreshDirectives() {
  try {
    const data = await api('GET', '/api/directives');
    state.directives = data.directives || [];
  } catch (err) {
    console.error('refreshDirectives:', err);
  }
}

async function refreshStatus() {
  try {
    const data = await api('GET', '/api/status');
    state.status = data.status || null;
  } catch (err) {
    console.error('refreshStatus:', err);
    state.status = null;
  }
}

// ── Navigation ────────────────────────────────────────────────────────────────
function goHome() {
  state.view = 'home';
  state.selectedGoalId = null;
  render();
}

function goHub(tab) {
  state.view = tab || state.hubTab;
  state.hubTab = state.view;
  state.selectedGoalId = null;
  render();
  if (state.view === 'directives') refreshDirectives().then(render);
}

function goGoalDetail(id) {
  state.view = 'goal-detail';
  state.selectedGoalId = id;
  render();
}

navBtn.addEventListener('click', () => {
  if (state.view === 'home') {
    goHub('goals');
  } else {
    goHome();
  }
});

// isEditingInContent reports whether the user is currently focused in a
// text input/textarea inside the dynamic content area (e.g. the directive
// form, the clarification answer box). Used to stop the background poll
// loop from re-rendering out from under them — content.innerHTML = '' would
// otherwise destroy whatever they're typing every few seconds.
function isEditingInContent() {
  const el = document.activeElement;
  if (!el || !content.contains(el)) return false;
  return el.tagName === 'TEXTAREA' || el.tagName === 'INPUT';
}

function updateNavBadge() {
  const attnCount = attentionGoals().length;
  if (attnCount > 0) {
    navBadge.textContent = attnCount > 9 ? '9+' : String(attnCount);
    navBadge.classList.remove('hidden');
  } else {
    navBadge.classList.add('hidden');
  }
}

// ── Rendering ─────────────────────────────────────────────────────────────────
function render() {
  navBtn.classList.toggle('active', state.view !== 'home');
  updateNavBadge();

  const inner = document.createElement('div');
  inner.className = 'content-inner';

  switch (state.view) {
    case 'goals':
      inner.appendChild(renderHubHeader());
      inner.appendChild(renderGoalsList());
      break;
    case 'directives':
      inner.appendChild(renderHubHeader());
      inner.appendChild(renderDirectiveForm());
      inner.appendChild(renderDirectivesList());
      break;
    case 'goal-detail':
      inner.appendChild(renderGoalDetail(state.selectedGoalId));
      break;
    default:
      inner.appendChild(renderHome());
  }

  content.innerHTML = '';
  content.appendChild(inner);
}

// renderStatusBar populates the persistent top bar (#status-bar) from the
// last /api/status fetch. Called directly (not via render()) so it survives
// every view switch and never gets wiped by content.innerHTML = ''. Every
// chip is a <button> with a data-action so a single delegated click
// listener (see below) can open the right modal/view.
function chip(label, value, extraClass, action, title) {
  return `<button type="button" class="status-chip clickable ${extraClass || ''}" data-action="${action || ''}" ${title ? `title="${escHtml(title)}"` : ''}><span class="chip-label">${label}</span><span class="chip-value">${value}</span></button>`;
}

function renderStatusBar() {
  if (state.connected === false || !state.status) {
    statusBar.innerHTML = `<span class="status-chip chip-muted">${state.connected === false ? 'disconnected' : 'connecting…'}</span>`;
    return;
  }

  const s = state.status;
  const goals = s.goals || {};
  const parts = [];

  parts.push(chip('Active', goals.active ?? 0, (goals.active ?? 0) > 0 ? 'chip-ok' : 'chip-muted', 'goals-active', 'View active goals'));
  parts.push(chip('Blocked', goals.blocked ?? 0, (goals.blocked ?? 0) > 0 ? 'chip-err' : 'chip-muted', 'goals-blocked', 'View blocked goals and why'));
  parts.push(chip('Queued', s.queued_goals ?? 0, (s.queued_goals ?? 0) > 0 ? '' : 'chip-muted', 'goals-active', 'Goals waiting to run'));
  parts.push(chip('Directives', s.directives ?? 0, '', 'directives', 'View standing directives'));

  if (s.model) {
    const usingLocal = s.model.local_enabled && s.model.last_used_local;
    const modelName = usingLocal
      ? (s.model.local_model || 'local')
      : (s.model.model || s.model.provider || 'remote');
    const sourceClass = usingLocal ? 'local' : 'remote';
    parts.push(
      `<button type="button" class="status-chip clickable" data-action="model" title="View active model details"><span class="model-pill ${sourceClass}"><span class="model-source"></span><span class="chip-value">${escHtml(modelName)}</span></span></button>`
    );
  }

  if (s.autonomy_text) {
    parts.push(chip('Autonomy', s.autonomy_level ?? '?', 'chip-muted', 'autonomy', s.autonomy_text));
  }

  parts.push('<span class="status-spacer"></span>');
  parts.push(renderInlineConnDot());

  statusBar.innerHTML = parts.join('');
}

// Delegated click handler: attached once, works for chips re-rendered on
// every poll tick since it's bound to the (stable) statusBar container.
statusBar.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  switch (btn.dataset.action) {
    case 'goals-active':
      openGoalsModal('Active Goals', 'active');
      break;
    case 'goals-blocked':
      openGoalsModal('Blocked Goals', 'blocked');
      break;
    case 'directives':
      goHub('directives');
      break;
    case 'model':
      openModelModal();
      break;
    case 'autonomy':
      openAutonomyModal();
      break;
  }
});

function renderInlineConnDot() {
  const dotClass = state.connected === true ? 'dot ok' : state.connected === false ? 'dot err' : 'dot';
  return `<span class="${dotClass}" title="${state.connected === true ? 'connected' : 'disconnected'}"></span>`;
}

// ── Status-bar modal ──────────────────────────────────────────────────────────
// Every chip in the status bar is clickable and opens this shared modal with
// content specific to what was clicked — goal lists (with per-goal detail,
// notably the failure reason for blocked goals), model info, or autonomy
// level info. The modal lives outside #content/.app so it overlays
// everything regardless of which view is underneath.
function openModal(title) {
  modalTitle.textContent = title;
  modalBody.innerHTML = '<div class="modal-empty">Loading…</div>';
  modalOverlay.classList.remove('hidden');
}

function closeModal() {
  modalOverlay.classList.add('hidden');
  modalBody.innerHTML = '';
}

modalClose.addEventListener('click', closeModal);
modalOverlay.addEventListener('click', (e) => {
  if (e.target === modalOverlay) closeModal();
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && !modalOverlay.classList.contains('hidden')) closeModal();
});

// openGoalsModal fetches goals in `stateFilter` and renders each as a card.
// For blocked goals specifically, it also fetches the AgentResult so the
// failure reason is visible without a second click — that's the whole point
// of making the Blocked chip clickable.
async function openGoalsModal(title, stateFilter) {
  openModal(title);
  let goals;
  try {
    const data = await api('GET', `/api/goals?state=${encodeURIComponent(stateFilter)}`);
    goals = data.goals || [];
  } catch (err) {
    modalBody.innerHTML = `<div class="modal-empty">Failed to load: ${escHtml(err.message)}</div>`;
    return;
  }

  if (goals.length === 0) {
    modalBody.innerHTML = `<div class="modal-empty">No ${escHtml(stateFilter)} goals right now.</div>`;
    return;
  }

  goals.sort((a, b) => new Date(b.updated_at || b.created_at) - new Date(a.updated_at || a.created_at));

  // For blocked goals, fetch each result in parallel so the failure reason
  // shows up inline. Best-effort — a missing result just omits the excerpt.
  let results = {};
  if (stateFilter === 'blocked') {
    const pairs = await Promise.all(goals.map(async (g) => {
      try {
        const r = await api('GET', `/api/goals/${g.id}/result`);
        return [g.id, r.result];
      } catch {
        return [g.id, null];
      }
    }));
    results = Object.fromEntries(pairs);
  }

  modalBody.innerHTML = '';
  const label = document.createElement('div');
  label.className = 'modal-section-label';
  label.textContent = `${goals.length} goal${goals.length !== 1 ? 's' : ''}`;
  modalBody.appendChild(label);

  for (const g of goals) {
    const card = document.createElement('div');
    card.className = 'modal-goal-card' + (g.state === 'blocked' ? ' blocked' : '');

    const meta = [
      g.needs_attention ? makeAttentionBadge() : makeBadge(g.state || 'refining'),
      g.created_at ? `<span>${relativeTime(g.created_at)}</span>` : '',
    ].filter(Boolean).join('');

    card.innerHTML = `
      <div class="mgc-desc">${escHtml(g.description)}</div>
      <div class="mgc-meta">${meta}</div>
    `;

    if (g.state === 'blocked') {
      const result = results[g.id];
      const errBox = document.createElement('div');
      errBox.className = 'mgc-error';
      if (result && result.error) {
        errBox.textContent = result.error;
      } else if (result && result.output) {
        errBox.textContent = result.output;
      } else {
        // No AgentResult on file — the process likely restarted since this
        // goal ran (results are cached in-memory, not persisted). Say so
        // explicitly rather than silently showing nothing.
        errBox.textContent = 'No failure details on record (result was not persisted, likely due to a server restart since this ran).';
        errBox.style.color = 'var(--text-dim)';
        errBox.style.fontStyle = 'italic';
      }
      card.appendChild(errBox);
    }

    card.addEventListener('click', () => {
      closeModal();
      goGoalDetail(g.id);
    });
    modalBody.appendChild(card);
  }
}

function openModelModal() {
  openModal('Active Model');
  const m = state.status && state.status.model;
  if (!m) {
    modalBody.innerHTML = '<div class="modal-empty">No model information available.</div>';
    return;
  }

  const usingLocal = m.local_enabled && m.last_used_local;
  const rows = [
    ['Currently serving', usingLocal ? `${m.local_model || 'local'} (local)` : `${m.model || m.provider} (remote)`],
    ['Remote provider', m.provider || '—'],
    ['Remote model', m.model || '(provider default)'],
    ['Local model enabled', m.local_enabled ? 'Yes' : 'No'],
  ];
  if (m.local_enabled) {
    rows.push(['Local model', m.local_model || '—']);
    rows.push(['Last call used local', m.last_used_local ? 'Yes' : 'No — fell back to remote']);
  }

  modalBody.innerHTML = `<div class="modal-kv">${rows.map(([k, v]) => `
    <div class="modal-kv-row"><span class="kv-key">${escHtml(k)}</span><span class="kv-val">${escHtml(String(v))}</span></div>
  `).join('')}</div>`;
}

function openAutonomyModal() {
  openModal('Autonomy Level');
  const s = state.status;
  if (!s) {
    modalBody.innerHTML = '<div class="modal-empty">No status information available.</div>';
    return;
  }

  const levels = [
    [0, 'Observe', 'All steps require explicit user approval; nothing executes automatically.'],
    [1, 'Approved', 'Executes arbiter-approved steps; modified verdicts require approval.'],
    [2, 'Reversible', 'Reversible steps execute without prompt; irreversible steps require approval.'],
    [3, 'Bounded', 'Irreversible steps execute after adversarial review; only block verdicts halt.'],
    [4, 'Trusted', 'Only blocked verdicts halt execution — everything else runs automatically.'],
  ];

  modalBody.innerHTML = `
    <div class="modal-kv-row" style="border-bottom:none;">
      <span class="kv-key">Current level</span>
      <span class="kv-val">${s.autonomy_level} — ${escHtml(levels[s.autonomy_level]?.[1] || '?')}</span>
    </div>
    <div class="modal-kv">
      ${levels.map(([n, name, desc]) => `
        <div class="modal-kv-row" style="${n === s.autonomy_level ? 'color:var(--purple-l)' : ''}">
          <span class="kv-key" style="${n === s.autonomy_level ? 'color:var(--purple-l);font-weight:700' : ''}">${n} · ${escHtml(name)}</span>
          <span class="kv-val" style="text-align:left;font-weight:400;color:var(--text-dim);max-width:60%">${escHtml(desc)}</span>
        </div>
      `).join('')}
    </div>
  `;
}

// renderIdleOrb is the ambient "nothing needs you right now" home state —
// a floating glowing orb rather than a wall of text, since this is the
// screen you'll see most often and it should feel calm, not empty.
function renderIdleOrb() {
  const wrap = document.createElement('div');
  wrap.className = 'idle-stage';
  wrap.innerHTML = `
    <div class="orb-wrap">
      <div class="orb-glow"></div>
      <div class="orb-core"></div>
    </div>
    <div class="idle-caption">All clear — tell hyperi what to do below</div>
  `;
  return wrap;
}

// ── Home view ─────────────────────────────────────────────────────────────────
// Home is deliberately quiet: it shows ONLY goals that need your input.
// Nothing routine (active/done/blocked goals) is shown here by default —
// that's what the Goals tab is for. If nothing needs attention, home is
// just the status line and the input bar below it.
function renderHome() {
  const frag = document.createElement('div');
  frag.style.display = 'flex';
  frag.style.flexDirection = 'column';
  frag.style.gap = '1.25rem';

  const needsAttention = attentionGoals()
    .sort((a, b) => new Date(b.updated_at || b.created_at) - new Date(a.updated_at || a.created_at));

  if (needsAttention.length === 0) {
    frag.appendChild(renderIdleOrb());
    return frag;
  }

  const section = document.createElement('div');
  const banner = document.createElement('div');
  banner.className = 'attention-banner';
  banner.textContent = `${needsAttention.length} goal${needsAttention.length !== 1 ? 's' : ''} need${needsAttention.length === 1 ? 's' : ''} your input`;
  section.appendChild(banner);

  const list = document.createElement('div');
  list.className = 'item-list';
  list.style.marginTop = '0.6rem';
  for (const g of needsAttention) list.appendChild(makeGoalCard(g));
  section.appendChild(list);
  frag.appendChild(section);

  return frag;
}

// ── Goals/Directives hub ─────────────────────────────────────────────────────
function renderHubHeader() {
  const wrap = document.createElement('div');
  wrap.style.display = 'flex';
  wrap.style.flexDirection = 'column';
  wrap.style.gap = '0.9rem';

  const header = document.createElement('div');
  header.className = 'view-header';
  header.innerHTML = `
    <button class="back-btn" id="hub-back-btn">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 6l-6 6 6 6" stroke-linecap="round" stroke-linejoin="round"/></svg>
      Home
    </button>
    <span class="view-title">Goals &amp; Directives</span>
  `;
  wrap.appendChild(header);

  const tabs = document.createElement('div');
  tabs.className = 'tab-row';
  tabs.innerHTML = `
    <button class="tab-btn ${state.view === 'goals' ? 'active' : ''}" data-tab="goals">Goals</button>
    <button class="tab-btn ${state.view === 'directives' ? 'active' : ''}" data-tab="directives">Directives</button>
  `;
  wrap.appendChild(tabs);

  setTimeout(() => {
    document.getElementById('hub-back-btn')?.addEventListener('click', goHome);
    wrap.querySelectorAll('.tab-btn').forEach(btn => {
      btn.addEventListener('click', () => goHub(btn.dataset.tab));
    });
  }, 0);

  return wrap;
}

function renderGoalsList() {
  const wrap = document.createElement('div');
  wrap.style.display = 'flex';
  wrap.style.flexDirection = 'column';
  wrap.style.gap = '0.9rem';

  // Getting to this view already requires an explicit tap on the nav icon,
  // so there's no need for a second layer of hiding here — show everything,
  // with attention-needing goals surfaced first.
  const needsAttention = attentionGoals()
    .sort((a, b) => new Date(b.updated_at || b.created_at) - new Date(a.updated_at || a.created_at));
  const routine = state.goals.filter(g => !g.needs_attention)
    .sort((a, b) => new Date(b.created_at) - new Date(a.created_at));

  if (needsAttention.length === 0 && routine.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    empty.textContent = 'No goals yet.';
    wrap.appendChild(empty);
    return wrap;
  }

  if (needsAttention.length > 0) {
    const section = document.createElement('div');
    const label = document.createElement('div');
    label.className = 'section-label';
    label.textContent = 'Needs your input';
    section.appendChild(label);
    const list = document.createElement('div');
    list.className = 'item-list';
    for (const g of needsAttention) list.appendChild(makeGoalCard(g));
    section.appendChild(list);
    wrap.appendChild(section);
  }

  if (routine.length > 0) {
    const section = document.createElement('div');
    const label = document.createElement('div');
    label.className = 'section-label';
    label.textContent = 'All goals';
    section.appendChild(label);
    const list = document.createElement('div');
    list.className = 'item-list';
    for (const g of routine) list.appendChild(makeGoalCard(g));
    section.appendChild(list);
    wrap.appendChild(section);
  }

  return wrap;
}

function makeGoalCard(g) {
  const card = document.createElement('div');
  const classes = ['card'];
  if (g.state === 'active') classes.push('running');
  if (g.needs_attention) classes.push('needs-attention');
  card.className = classes.join(' ');

  const stateName = g.state || 'refining';
  const dotClass = `card-dot state-${stateName}` + (g.needs_attention ? ' state-attention' : '');
  const badge = g.needs_attention ? makeAttentionBadge() : makeBadge(stateName);
  const createdAt = g.created_at ? relativeTime(g.created_at) : '';

  const descOrQuestion = g.needs_attention && g.clarification_question
    ? `<span class="attention-icon">?</span>${escHtml(g.clarification_question)}`
    : escHtml(g.description);

  card.innerHTML = `
    <div class="${dotClass}"></div>
    <div class="card-body">
      <div class="card-desc">${descOrQuestion}</div>
      ${g.needs_attention ? `<div class="card-subdesc">${escHtml(truncateText(g.description, 140))}</div>` : ''}
      <div class="card-meta">
        ${badge}
        <span>${createdAt}</span>
      </div>
    </div>
  `;
  card.addEventListener('click', () => goGoalDetail(g.id));
  return card;
}

function makeBadge(stateName) {
  const map = {
    active:   ['Active', 'badge-active'],
    done:     ['Done',   'badge-done'],
    blocked:  ['Blocked','badge-blocked'],
    refining: ['Refining','badge-refining'],
    draft:    ['Draft',  'badge-draft'],
  };
  const [label, cls] = map[stateName] || ['Unknown', 'badge-draft'];
  return `<span class="badge ${cls}">${label}</span>`;
}

function makeAttentionBadge() {
  return `<span class="badge badge-attention">Needs input</span>`;
}

// ── Directives ────────────────────────────────────────────────────────────────
function renderDirectiveForm() {
  const form = document.createElement('form');
  form.className = 'directive-form';
  form.innerHTML = `
    <textarea rows="2" placeholder="Add a standing rule for hyperi to always follow…" required></textarea>
    <div class="directive-form-row">
      <span class="directive-priority-label">Priority</span>
      <input type="number" class="directive-priority" value="5" min="1" max="10" />
      <button type="submit" class="action-btn">Add directive</button>
    </div>
  `;
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const textarea = form.querySelector('textarea');
    const priorityInput = form.querySelector('.directive-priority');
    const description = textarea.value.trim();
    if (!description) return;

    const btn = form.querySelector('button');
    btn.disabled = true;
    btn.textContent = 'Adding…';
    try {
      await api('POST', '/api/directives', {
        description,
        priority: parseInt(priorityInput.value, 10) || 5,
      });
      await refreshDirectives();
      render();
    } catch (err) {
      alert(`Failed to add directive: ${err.message}`);
      btn.disabled = false;
      btn.textContent = 'Add directive';
    }
  });
  return form;
}

function renderDirectivesList() {
  const wrap = document.createElement('div');

  const label = document.createElement('div');
  label.className = 'section-label';
  label.textContent = `${state.directives.length} active directive${state.directives.length !== 1 ? 's' : ''}`;
  wrap.appendChild(label);

  if (state.directives.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    empty.textContent = 'No standing directives yet.';
    wrap.appendChild(empty);
    return wrap;
  }

  const sorted = [...state.directives].sort((a, b) => (b.priority || 0) - (a.priority || 0));
  const list = document.createElement('div');
  list.className = 'item-list';

  for (const d of sorted) {
    const card = document.createElement('div');
    card.className = 'card static';
    card.innerHTML = `
      <div class="card-dot" style="background:var(--purple-l);box-shadow:0 0 6px 1px var(--purple)"></div>
      <div class="card-body">
        <div class="card-desc">${escHtml(d.description)}</div>
        <div class="card-meta">
          <span class="badge badge-active">Priority ${d.priority ?? 0}</span>
          ${d.immutable ? '<span class="badge badge-blocked">Immutable</span>' : ''}
        </div>
      </div>
      <div class="card-actions"></div>
    `;
    if (!d.immutable) {
      const actions = card.querySelector('.card-actions');
      const delBtn = document.createElement('button');
      delBtn.className = 'icon-btn';
      delBtn.title = 'Remove directive';
      delBtn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18" stroke-linecap="round"/></svg>';
      delBtn.addEventListener('click', async (e) => {
        e.stopPropagation();
        try {
          await api('DELETE', `/api/directives/${d.id}`);
          await refreshDirectives();
          render();
        } catch (err) {
          alert(`Failed to remove directive: ${err.message}`);
        }
      });
      actions.appendChild(delBtn);
    }
    list.appendChild(card);
  }
  wrap.appendChild(list);
  return wrap;
}

// ── Goal detail view ──────────────────────────────────────────────────────────
function renderGoalDetail(id) {
  const wrap = document.createElement('div');
  wrap.style.display = 'flex';
  wrap.style.flexDirection = 'column';
  wrap.style.gap = '1.1rem';

  const header = document.createElement('div');
  header.className = 'view-header';
  header.innerHTML = `
    <button class="back-btn" id="detail-back-btn">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 6l-6 6 6 6" stroke-linecap="round" stroke-linejoin="round"/></svg>
      Back
    </button>
  `;
  wrap.appendChild(header);
  setTimeout(() => {
    document.getElementById('detail-back-btn')?.addEventListener('click', () => {
      if (state.hubTab && (state.view === 'goal-detail')) {
        // Prefer returning to wherever the user came from — the goals hub
        // if they were browsing, otherwise home.
        goHub('goals');
      } else {
        goHome();
      }
    });
  }, 0);

  const placeholder = document.createElement('div');
  placeholder.className = 'empty-state';
  placeholder.textContent = 'Loading…';
  wrap.appendChild(placeholder);

  loadGoalDetail(id, wrap, placeholder);
  return wrap;
}

async function loadGoalDetail(id, wrap, placeholder) {
  let goal = null;
  let result = null;
  try {
    const goalData = await api('GET', `/api/goals/${id}`);
    goal = goalData.goal;
    result = goalData.result || null;
  } catch (err) {
    placeholder.textContent = `Could not load goal: ${err.message}`;
    return;
  }
  if (!result) {
    try {
      const resultData = await api('GET', `/api/goals/${id}/result`);
      result = resultData.result;
    } catch {
      // no result yet — fine
    }
  }

  if (state.view !== 'goal-detail' || state.selectedGoalId !== id) return; // navigated away
  placeholder.remove();

  const desc = document.createElement('div');
  desc.className = 'detail-desc';
  desc.textContent = goal.description;
  wrap.appendChild(desc);

  const meta = document.createElement('div');
  meta.className = 'detail-meta';
  meta.innerHTML = [
    goal.needs_attention ? makeAttentionBadge() : makeBadge(goal.state),
    goal.created_at ? `<span>${relativeTime(goal.created_at)}</span>` : '',
    `<span style="color:var(--text-faint)">${goal.id}</span>`,
  ].join('');
  wrap.appendChild(meta);

  // Pending question — front and center with an answer box.
  if (goal.needs_attention && goal.clarification_question) {
    const qBox = document.createElement('div');
    qBox.className = 'question-box';
    qBox.innerHTML = `
      <div class="question-label">hyperi is asking</div>
      <div class="question-text">${escHtml(goal.clarification_question)}</div>
    `;
    wrap.appendChild(qBox);

    const form = document.createElement('form');
    form.className = 'answer-form';
    form.innerHTML = `
      <textarea class="answer-input" rows="2" placeholder="Type your answer…" required></textarea>
      <button type="submit" class="action-btn">Send answer</button>
    `;
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const textarea = form.querySelector('textarea');
      const btn = form.querySelector('button');
      const answer = textarea.value.trim();
      if (!answer) return;
      btn.disabled = true;
      btn.textContent = 'Sending…';
      try {
        await api('POST', `/api/goals/${id}/answer`, { answer });
        await refreshGoals();
        goHome();
      } catch (err) {
        btn.disabled = false;
        btn.textContent = 'Send answer';
        alert(`Error: ${err.message}`);
      }
    });
    wrap.appendChild(form);
  }

  // Draft (refining, no pending question): offer to run it.
  if (goal.state === 'refining' && !goal.needs_attention) {
    const runBtn = document.createElement('button');
    runBtn.className = 'action-btn';
    runBtn.textContent = 'Queue & run';
    runBtn.addEventListener('click', async () => {
      runBtn.disabled = true;
      runBtn.textContent = 'Queuing…';
      try {
        await api('POST', `/api/goals/${id}/run`);
        await refreshGoals();
        goHome();
      } catch (err) {
        runBtn.disabled = false;
        runBtn.textContent = 'Queue & run';
        alert(`Error: ${err.message}`);
      }
    });
    wrap.appendChild(runBtn);
  }

  // Result / steps.
  if (result) {
    if (result.output) {
      const out = document.createElement('div');
      out.className = 'result-output';
      out.textContent = result.output;
      wrap.appendChild(out);
    }
    if (result.error) {
      const err = document.createElement('div');
      err.className = 'result-output';
      err.style.color = 'var(--err)';
      err.textContent = result.error;
      wrap.appendChild(err);
    }
    if (result.steps && result.steps.length > 0) {
      const label = document.createElement('div');
      label.className = 'steps-label';
      label.textContent = `${result.steps.length} step${result.steps.length !== 1 ? 's' : ''}`;
      wrap.appendChild(label);

      for (const step of result.steps) {
        const item = document.createElement('div');
        item.className = 'step-item';
        item.innerHTML = `
          <div class="step-tool${step.is_error ? ' err' : ''}">${step.is_error ? `${escHtml(step.tool)} ✗` : escHtml(step.tool)}</div>
          <div class="step-input">$ ${escHtml(step.input)}</div>
          ${step.output ? `<div class="step-output">${escHtml(step.output)}</div>` : ''}
        `;
        wrap.appendChild(item);
      }
    }
  } else if (goal.state === 'active') {
    const pending = document.createElement('div');
    pending.className = 'no-result';
    pending.textContent = 'Running — result will appear when the agent finishes.';
    wrap.appendChild(pending);
  } else if (goal.state !== 'refining') {
    const none = document.createElement('div');
    none.className = 'no-result';
    none.textContent = 'No result recorded yet.';
    wrap.appendChild(none);
  }

  // Delete action, always available.
  const actionRow = document.createElement('div');
  actionRow.className = 'action-row';
  const delBtn = document.createElement('button');
  delBtn.className = 'action-btn danger';
  delBtn.textContent = 'Delete goal';
  delBtn.addEventListener('click', async () => {
    if (!confirm('Delete this goal permanently?')) return;
    try {
      await api('DELETE', `/api/goals/${id}`);
      await refreshGoals();
      goHome();
    } catch (err) {
      alert(`Failed to delete: ${err.message}`);
    }
  });
  actionRow.appendChild(delBtn);
  wrap.appendChild(actionRow);
}

// ── Bottom bar: submit ────────────────────────────────────────────────────────
barForm.addEventListener('submit', (e) => e.preventDefault());
sendBtn.addEventListener('click', submitBarInput);
barInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    submitBarInput();
  }
});
// auto-grow textarea up to CSS max-height
barInput.addEventListener('input', () => {
  barInput.style.height = 'auto';
  barInput.style.height = `${barInput.scrollHeight}px`;
});

async function submitBarInput() {
  const description = barInput.value.trim();
  if (!description) return;

  // Clear and re-focus immediately (optimistic) rather than disabling the
  // textarea for the whole request — goal submission includes a synchronous
  // LLM refine call that can take many seconds, and a disabled textarea
  // can't hold focus or accept new input, which made the bar feel frozen.
  // Only the send button reflects "in flight" state; typing the next thing
  // works right away.
  barInput.value = '';
  barInput.style.height = 'auto';
  barInput.focus();

  sendBtn.disabled = true;
  sendBtn.classList.add('sending');
  try {
    const data = await api('POST', '/api/goals', { description });
    await refreshGoals();
    goGoalDetail(data.goal.id);
  } catch (err) {
    // Restore the text so it isn't lost on failure.
    barInput.value = description;
    barInput.dispatchEvent(new Event('input'));
    alert(`Failed to submit: ${err.message}`);
  } finally {
    sendBtn.disabled = false;
    sendBtn.classList.remove('sending');
  }
}

// ── Voice input (Web Speech API, browser-side; no backend involved) ─────────
const SpeechRecognitionCtor = window.SpeechRecognition || window.webkitSpeechRecognition;
let recognizer = null;
let listening = false;

if (!SpeechRecognitionCtor) {
  micBtn.disabled = true;
  micBtn.title = 'Voice input not supported in this browser';
} else {
  recognizer = new SpeechRecognitionCtor();
  recognizer.continuous = false;
  recognizer.interimResults = true;
  recognizer.lang = navigator.language || 'en-US';

  recognizer.addEventListener('result', (event) => {
    let transcript = '';
    for (let i = 0; i < event.results.length; i++) {
      transcript += event.results[i][0].transcript;
    }
    barInput.value = transcript;
    barInput.dispatchEvent(new Event('input'));
  });

  recognizer.addEventListener('end', () => {
    listening = false;
    micBtn.classList.remove('mic-listening');
  });

  recognizer.addEventListener('error', () => {
    listening = false;
    micBtn.classList.remove('mic-listening');
  });

  micBtn.addEventListener('click', () => {
    if (listening) {
      recognizer.stop();
      return;
    }
    listening = true;
    micBtn.classList.add('mic-listening');
    barInput.focus();
    try {
      recognizer.start();
    } catch {
      listening = false;
      micBtn.classList.remove('mic-listening');
    }
  });
}

// ── Utilities ─────────────────────────────────────────────────────────────────
function escHtml(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function truncateText(str, n) {
  return str.length > n ? str.slice(0, n) + '…' : str;
}

function relativeTime(isoString) {
  const diff = Date.now() - new Date(isoString).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// ── Bootstrap / polling ───────────────────────────────────────────────────────
async function tick() {
  await Promise.all([checkHealth(), refreshGoals(), refreshStatus()]);
  if (state.view === 'directives') await refreshDirectives();

  // The status bar lives outside #content and is safe to refresh
  // unconditionally, even while the user is typing into a form below.
  renderStatusBar();

  // Don't blow away a form the user is actively typing into (directive
  // form, clarification answer box, etc) — content.innerHTML = '' inside
  // render() would otherwise wipe it mid-keystroke every few seconds. Still
  // update the nav badge count, since that's outside the content area.
  if (isEditingInContent()) {
    updateNavBadge();
    return;
  }
  render();
}

tick();
setInterval(tick, 4000);

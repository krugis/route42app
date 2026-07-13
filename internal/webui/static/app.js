/* Route42 Community Console — zero-dependency SPA over the gateway's own API. */
(() => {
  'use strict';

  const $ = (sel, el = document) => el.querySelector(sel);
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const icon = (id, cls = 'icon-sm') => `<svg class="${cls}"><use href="#${id}"/></svg>`;

  // ---------- API plumbing (optional bearer token, kept in localStorage) ----------
  let token = localStorage.getItem('route42_token') || '';

  async function api(path, opts = {}) {
    const headers = { ...(opts.body ? { 'Content-Type': 'application/json' } : {}), ...(opts.headers || {}) };
    if (token) headers['Authorization'] = 'Bearer ' + token;
    const res = await fetch(path, { ...opts, headers });
    if (res.status === 401) {
      $('#authbar').classList.remove('hidden');
      throw new Error('Unauthorized — enter the gateway API token above.');
    }
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try { const j = await res.json(); if (j.error?.message) msg = j.error.message; } catch { /* keep default */ }
      throw new Error(msg);
    }
    return res;
  }
  const getJSON = async (path) => (await api(path)).json();

  $('#token-save').addEventListener('click', () => {
    token = $('#token-input').value.trim();
    localStorage.setItem('route42_token', token);
    $('#authbar').classList.add('hidden');
    TABS[activeTab].load();
    toast('Token saved');
  });

  function showError(msg) {
    const bar = $('#errorbar');
    bar.textContent = msg;
    bar.classList.remove('hidden');
  }
  function clearError() { $('#errorbar').classList.add('hidden'); }

  let toastTimer;
  function toast(msg) {
    const t = $('#toast');
    t.textContent = msg;
    t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.remove('show'), 2200);
  }
  function copyText(text, msg = 'Copied to clipboard') {
    navigator.clipboard?.writeText(text).then(() => toast(msg), () => toast('Copy failed'));
  }

  // ---------- formatting ----------
  const fmtInt = (n) => Number(n || 0).toLocaleString();
  const fmtCents = (c) => {
    const v = Number(c || 0);
    if (v === 0) return '$0.00';
    const usd = v / 100;
    return '$' + (usd < 0.01 ? usd.toFixed(5) : usd.toFixed(2));
  };
  const fmtScore = (v) => (v || v === 0 ? Number(v).toFixed(2) : '—');
  const fmtPrice = (v) => (v ? '$' + Number(v).toFixed(2) : '—');

  // ---------- tabs ----------
  const TABS = {
    dashboard: { title: 'Dashboard', sub: 'Monitor your AI usage and performance', load: loadDashboard, range: true },
    models: { title: 'Models', sub: 'Browse routable models and manage provider keys', load: loadModels },
    preferences: { title: 'Preferences', sub: 'Configure routing behavior', load: loadPreferences },
    playground: { title: 'Playground', sub: 'Test prompt routing', load: loadPlayground },
    history: { title: 'Interaction History', sub: 'Recent routed requests and their decisions', load: loadHistory },
  };
  let activeTab = 'dashboard';

  function switchTab(tab) {
    if (!TABS[tab]) tab = 'dashboard';
    activeTab = tab;
    clearError();
    for (const btn of $('#nav').children) btn.classList.toggle('active', btn.dataset.tab === tab);
    for (const key of Object.keys(TABS)) $('#page-' + key).classList.toggle('hidden', key !== tab);
    $('#page-title').textContent = TABS[tab].title;
    $('#page-sub').textContent = TABS[tab].sub;
    $('#range').classList.toggle('hidden', !TABS[tab].range);
    if (location.hash !== '#' + tab) history.replaceState(null, '', '#' + tab);
    TABS[tab].load();
  }
  $('#nav').addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-tab]');
    if (btn) switchTab(btn.dataset.tab);
  });
  window.addEventListener('hashchange', () => switchTab(location.hash.slice(1)));

  // ---------- Dashboard ----------
  const RANGES = [
    { label: 'Today', days: 1 },
    { label: 'Week', days: 7 },
    { label: 'Month', days: 30 },
    { label: '3 Months', days: 90 },
    { label: 'All', days: 0 },
  ];
  let rangeDays = 30;

  $('#range').innerHTML = RANGES.map((r) =>
    `<button data-days="${r.days}" class="${r.days === rangeDays ? 'active' : ''}">${r.label}</button>`).join('');
  $('#range').addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-days]');
    if (!btn) return;
    rangeDays = Number(btn.dataset.days);
    for (const b of $('#range').children) b.classList.toggle('active', b === btn);
    loadStats();
  });

  const base = location.origin;
  const SNIPPETS = {
    curl: `curl ${base}/api/chat/completions \\
  -H "Content-Type: application/json" \\
  -d '{"messages":[{"role":"user","content":"Explain quantum computing to a 10-year-old"}]}'`,
    python: `from openai import OpenAI

client = OpenAI(base_url="${base}/v1", api_key="unused")

resp = client.chat.completions.create(
    model="auto",  # Route42 picks the model
    messages=[{"role": "user", "content": "Explain quantum computing to a 10-year-old"}],
)
print(resp.choices[0].message.content)
print(resp.model)  # the model Route42 picked`,
    javascript: `import OpenAI from "openai";

const client = new OpenAI({ baseURL: "${base}/v1", apiKey: "unused" });

const resp = await client.chat.completions.create({
  model: "auto",
  messages: [{ role: "user", content: "Explain quantum computing to a 10-year-old" }],
});
console.log(resp.choices[0].message.content);`,
  };

  function dashboardSkeleton() {
    $('#page-dashboard').innerHTML = `
      <div class="banner">
        <div>
          <div class="t">New to Route42?</div>
          <div class="d">Check the README for API examples, integration guides, and configuration.</div>
        </div>
        <a class="btn btn-primary" href="https://github.com/krugis/route42app" target="_blank" rel="noreferrer">View Docs →</a>
      </div>

      <div class="grid grid-2">
        <div class="card">
          <div class="card-head">
            <div class="card-title">
              ${icon('i-server', 'icon c-blue')}
              <div><h3>Backend Health</h3><p>Live health endpoint from the router</p></div>
            </div>
            <button class="btn" id="btn-health">${icon('i-refresh')} Refresh</button>
          </div>
          <div id="health-body" class="statusline"><span class="dot dot-gray"></span><span>Checking…</span></div>
          <div id="health-meta" class="tiny" style="margin-top:8px"></div>
        </div>

        <div class="card">
          <div class="card-head">
            <div class="card-title">
              ${icon('i-cpu', 'icon c-emerald')}
              <div><h3>Local LLM</h3><p>Ollama models discovered by the router</p></div>
            </div>
            <button class="btn" id="btn-local">${icon('i-refresh')} Refresh</button>
          </div>
          <div id="local-body" class="statusline"><span class="dot dot-gray"></span><span>Checking…</span></div>
          <div id="local-meta" class="small muted" style="margin-top:8px"></div>
        </div>
      </div>

      <div class="grid grid-tiles" id="stat-tiles"></div>

      <div class="grid grid-2">
        <div class="card">
          <div class="card-head"><div class="card-title">${icon('i-chart', 'icon c-purple')}<div><h3>Usage by model</h3></div></div></div>
          <div id="by-model" class="bars"><span class="muted small">No usage yet.</span></div>
        </div>
        <div class="card">
          <div class="card-head"><div class="card-title">${icon('i-activity', 'icon c-amber')}<div><h3>Prompts by category</h3></div></div></div>
          <div id="by-category" class="bars"><span class="muted small">No usage yet.</span></div>
        </div>
      </div>

      <div class="card">
        <div class="card-head">
          <div class="card-title">
            ${icon('i-terminal', 'icon c-blue')}
            <div><h3>Client API (one endpoint, auto-selects model)</h3>
            <p>Send OpenAI-compatible requests; Route42 routes to local or cloud and answers in the OpenAI shape.</p></div>
          </div>
          <button class="btn" id="btn-copy-snippet">${icon('i-copy')} Copy</button>
        </div>
        <div class="endpoint-box"><span>${esc(base)}/v1/chat/completions</span>
          <button class="btn" id="btn-copy-endpoint">${icon('i-copy')} Copy</button></div>
        <div style="margin-top:16px">
          <div class="snippet-tabs" id="snippet-tabs">
            <button data-lang="curl" class="active">curl</button>
            <button data-lang="python">Python</button>
            <button data-lang="javascript">TypeScript</button>
          </div>
          <pre class="snippet" id="snippet-body"></pre>
        </div>
      </div>`;

    let lang = 'curl';
    const renderSnippet = () => { $('#snippet-body').textContent = SNIPPETS[lang]; };
    renderSnippet();
    $('#snippet-tabs').addEventListener('click', (e) => {
      const btn = e.target.closest('button[data-lang]');
      if (!btn) return;
      lang = btn.dataset.lang;
      for (const b of $('#snippet-tabs').children) b.classList.toggle('active', b === btn);
      renderSnippet();
    });
    $('#btn-copy-snippet').addEventListener('click', () => copyText(SNIPPETS[lang]));
    $('#btn-copy-endpoint').addEventListener('click', () => copyText(base + '/v1/chat/completions', 'Endpoint copied'));
    $('#btn-health').addEventListener('click', loadHealth);
    $('#btn-local').addEventListener('click', loadLocal);
  }

  async function loadHealth() {
    const body = $('#health-body'), meta = $('#health-meta');
    if (!body) return;
    try {
      const h = await getJSON('/health');
      const ok = h.status === 'ok';
      body.innerHTML = `<span class="dot ${ok ? 'dot-green' : 'dot-amber'}"></span><span>${ok ? 'Healthy' : esc(h.status)}</span>
        <span class="tiny">Checked ${new Date().toLocaleTimeString()}</span>`;
      meta.textContent = `v${h.version} · analyzer: ${h.analyzer} · catalog: ${h.catalog_models} models · up ${h.uptime}`;
    } catch (err) {
      body.innerHTML = `<span class="dot dot-red"></span><span>Down</span>`;
      meta.textContent = err.message;
    }
  }

  async function loadLocal() {
    const body = $('#local-body'), meta = $('#local-meta');
    if (!body) return;
    try {
      const res = await getJSON('/api/models');
      const locals = (res.data || []).filter((m) => m.x_route42?.source === 'local');
      if (locals.length) {
        body.innerHTML = `<span class="dot dot-emerald"></span><span>Local ready</span>`;
        meta.innerHTML = `${locals.length} local model${locals.length === 1 ? '' : 's'} detected — ` +
          locals.slice(0, 4).map((m) => `<span class="chip chip-local">${esc(m.id)}</span>`).join(' ') +
          (locals.length > 4 ? ` <span class="tiny">+${locals.length - 4} more</span>` : '');
      } else {
        body.innerHTML = `<span class="dot dot-amber"></span><span>No local models</span>`;
        meta.textContent = 'Ollama not detected (or has no models). Cloud routing still works with provider keys.';
      }
    } catch (err) {
      body.innerHTML = `<span class="dot dot-red"></span><span>Unavailable</span>`;
      meta.textContent = err.message;
    }
  }

  async function loadStats() {
    const tiles = $('#stat-tiles');
    if (!tiles) return;
    try {
      const s = await getJSON('/api/stats?days=' + rangeDays);
      const saved = (s.by_model || []).filter((m) => m.provider === 'ollama').reduce((acc, m) => acc + m.requests, 0);
      tiles.innerHTML = `
        <div class="tile"><div class="k">${icon('i-activity', 'icon-sm c-blue')} Requests</div><div class="v">${fmtInt(s.requests)}</div>
          <div class="s">${fmtInt(s.errors)} error${s.errors === 1 ? '' : 's'}</div></div>
        <div class="tile"><div class="k">${icon('i-zap', 'icon-sm c-amber')} Tokens</div><div class="v">${fmtInt(s.total_tokens)}</div>
          <div class="s">prompt + completion</div></div>
        <div class="tile"><div class="k">${icon('i-dollar', 'icon-sm c-emerald')} Spend</div><div class="v">${fmtCents(s.cost_cents)}</div>
          <div class="s">estimated, cloud only</div></div>
        <div class="tile"><div class="k">${icon('i-cpu', 'icon-sm c-purple')} Local requests</div><div class="v">${fmtInt(saved)}</div>
          <div class="s">served at $0.00</div></div>`;

      const bars = (rows, labelOf, valueOf, valText) => {
        if (!rows.length) return '<span class="muted small">No usage in this window.</span>';
        const max = Math.max(...rows.map(valueOf)) || 1;
        return rows.map((r) => `
          <div class="bar-row">
            <span class="lbl" title="${esc(labelOf(r))}">${esc(labelOf(r))}</span>
            <div class="bar-track"><div class="bar-fill" style="width:${Math.max(2, (valueOf(r) / max) * 100)}%"></div></div>
            <span class="val">${valText(r)}</span>
          </div>`).join('');
      };
      $('#by-model').innerHTML = bars((s.by_model || []).slice(0, 8), (m) => m.model, (m) => m.requests,
        (m) => `${fmtInt(m.requests)}`);
      const cats = Object.entries(s.by_category || {}).sort((a, b) => b[1] - a[1]);
      $('#by-category').innerHTML = bars(cats, (c) => c[0], (c) => c[1], (c) => fmtInt(c[1]));
    } catch (err) {
      showError('Stats: ' + err.message);
    }
  }

  function loadDashboard() {
    dashboardSkeleton();
    loadHealth();
    loadLocal();
    loadStats();
  }

  // ---------- Models ----------
  let allModels = [];

  async function loadModels() {
    const page = $('#page-models');
    page.innerHTML = `
      <div class="card">
        <div class="card-head">
          <div class="card-title">${icon('i-cpu', 'icon c-blue')}<div><h3>Routable models</h3>
            <p>Catalog + discovered local models. “Available” means the provider has a key or the model is local.</p></div></div>
          <div style="display:flex;gap:8px;align-items:center">
            <input id="model-search" placeholder="Filter models…" style="width:200px">
            <label class="small muted" style="display:flex;align-items:center;gap:6px;white-space:nowrap">
              <input type="checkbox" id="model-avail" style="width:14px;height:14px;accent-color:var(--blue-500)"> available only
            </label>
          </div>
        </div>
        <div class="tbl-wrap"><table class="tbl"><thead><tr>
          <th>Model</th><th>Provider</th><th>Source</th><th class="num">Quality</th>
          <th class="num">$/M in</th><th class="num">$/M out</th><th>Tools</th><th>Available</th>
        </tr></thead><tbody id="model-rows"><tr><td colspan="8" class="muted">Loading…</td></tr></tbody></table></div>
      </div>

      <div class="card">
        <div class="card-head">
          <div class="card-title">${icon('i-key', 'icon c-amber')}<div><h3>Provider keys</h3>
            <p>Stored encrypted in the local database. Values are write-only and shown masked.</p></div></div>
        </div>
        <div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:14px">
          <select id="key-provider">
            ${['openai', 'anthropic', 'google', 'mistral', 'groq', 'deepseek', 'alibaba', 'moonshot', 'nvidia', 'openrouter']
              .map((p) => `<option value="${p}">${p}</option>`).join('')}
          </select>
          <input type="password" id="key-value" placeholder="sk-…" style="flex:1;min-width:220px">
          <button class="btn btn-primary" id="key-add">Add key</button>
        </div>
        <div id="key-list" class="small muted">Loading…</div>
      </div>`;

    $('#model-search').addEventListener('input', renderModelRows);
    $('#model-avail').addEventListener('change', renderModelRows);
    $('#key-add').addEventListener('click', addKey);

    try {
      const res = await getJSON('/api/models');
      allModels = res.data || [];
      renderModelRows();
    } catch (err) {
      $('#model-rows').innerHTML = `<tr><td colspan="8" class="c-red">${esc(err.message)}</td></tr>`;
    }
    loadKeys();
  }

  function renderModelRows() {
    const q = ($('#model-search')?.value || '').toLowerCase();
    const availOnly = $('#model-avail')?.checked;
    const rows = allModels.filter((m) => {
      const x = m.x_route42 || {};
      if (availOnly && !x.available) return false;
      return !q || m.id.toLowerCase().includes(q) || (x.provider || '').toLowerCase().includes(q);
    });
    $('#model-rows').innerHTML = rows.length ? rows.map((m) => {
      const x = m.x_route42 || {};
      return `<tr>
        <td><code>${esc(m.id)}</code></td>
        <td>${esc(x.provider || m.owned_by)}</td>
        <td><span class="chip chip-${x.source === 'local' ? 'local' : 'cloud'}">${esc(x.source || 'cloud')}</span></td>
        <td class="num">${fmtScore(x.quality_score)}</td>
        <td class="num">${fmtPrice(x.input_price_per_mtok)}</td>
        <td class="num">${fmtPrice(x.output_price_per_mtok)}</td>
        <td>${x.supports_tools ? '✓' : '—'}</td>
        <td><span class="dot ${x.available ? 'dot-emerald' : 'dot-gray'}" style="box-shadow:none"></span></td>
      </tr>`;
    }).join('') : '<tr><td colspan="8" class="muted">No models match.</td></tr>';
  }

  async function loadKeys() {
    const list = $('#key-list');
    if (!list) return;
    try {
      const res = await getJSON('/api/keys');
      const providers = res.providers || [];
      list.innerHTML = providers.length ? `<div class="tbl-wrap"><table class="tbl"><tbody>` + providers.map((p) => `
        <tr><td style="width:160px">${esc(p.provider)}</td>
        <td><code class="muted">${esc(p.key_mask || '••••••••')}</code></td>
        <td style="width:60px;text-align:right">
          <button class="btn btn-danger" data-del="${esc(p.provider)}" title="Delete key">${icon('i-trash')}</button>
        </td></tr>`).join('') + `</tbody></table></div>`
        : 'No provider keys configured. Ollama-only routing works without any.';
      list.querySelectorAll('button[data-del]').forEach((btn) => btn.addEventListener('click', async () => {
        try {
          await api('/api/keys?provider=' + encodeURIComponent(btn.dataset.del), { method: 'DELETE' });
          toast(`Removed ${btn.dataset.del} key`);
          loadKeys();
          getJSON('/api/models').then((r) => { allModels = r.data || []; renderModelRows(); }).catch(() => {});
        } catch (err) { showError(err.message); }
      }));
    } catch (err) {
      list.textContent = err.message;
    }
  }

  async function addKey() {
    const provider = $('#key-provider').value;
    const key = $('#key-value').value.trim();
    if (!key) { showError('Enter an API key first.'); return; }
    try {
      clearError();
      await api('/api/keys', { method: 'POST', body: JSON.stringify({ provider, api_key: key }) });
      $('#key-value').value = '';
      toast(`Saved ${provider} key`);
      loadKeys();
      const r = await getJSON('/api/models');
      allModels = r.data || [];
      renderModelRows();
    } catch (err) { showError(err.message); }
  }

  // ---------- Preferences ----------
  const PRIORITIES = [
    { id: 'balanced', name: 'Balanced', desc: 'Optimal balance', icon: 'i-activity', color: 'c-blue', guide: 'Optimizes across quality, speed, and cost.' },
    { id: 'fast', name: 'Fast', desc: 'Lowest latency', icon: 'i-zap', color: 'c-amber', guide: 'Prefers the lowest-latency qualified models.' },
    { id: 'accurate', name: 'Accurate', desc: 'Best quality', icon: 'i-sparkles', color: 'c-purple', guide: 'Prefers the highest quality outputs.' },
    { id: 'cheap', name: 'Cheap', desc: 'Minimum cost', icon: 'i-dollar', color: 'c-emerald', guide: 'Picks the cheapest model that can handle the prompt.' },
  ];
  let prefs = null;

  async function loadPreferences() {
    const page = $('#page-preferences');
    page.innerHTML = '<div class="card muted">Loading preferences…</div>';
    try {
      prefs = await getJSON('/api/prefs');
    } catch (err) {
      page.innerHTML = `<div class="card c-red">${esc(err.message)}</div>`;
      return;
    }

    page.innerHTML = `
    <div class="prefs-layout">
      <div style="display:flex;flex-direction:column;gap:24px;min-width:0">
        <div class="card">
          <div class="card-head"><div class="card-title"><div><h3>Routing Priority</h3></div></div></div>
          <div class="prio-grid" id="prio-grid">
            ${PRIORITIES.map((p) => `
              <button class="prio ${prefs.priority === p.id ? 'active' : ''}" data-prio="${p.id}">
                ${icon(p.icon, 'icon ' + p.color)}
                <div class="n">${p.name}</div><div class="d">${p.desc}</div>
              </button>`).join('')}
          </div>
        </div>

        <div class="card">
          <div class="card-head"><div class="card-title"><div><h3>Model Filters</h3></div></div></div>
          <div style="display:flex;flex-direction:column;gap:12px">
            <label class="check"><input type="checkbox" id="pf-only-free" ${prefs.only_free ? 'checked' : ''}>
              <div><div class="n">Free Models Only</div><div class="d">Route only to models with zero cost</div></div></label>
            <label class="check"><input type="checkbox" id="pf-only-local" ${prefs.only_local ? 'checked' : ''}>
              <div><div class="n">Local Models Only</div><div class="d">Route only to discovered local (Ollama) models</div></div></label>
          </div>
        </div>

        <div class="card">
          <div class="card-head"><div class="card-title"><div><h3>Limits &amp; Fallback</h3>
            <p>Zero means no limit.</p></div></div></div>
          <div class="fields-grid">
            <div class="field"><label>Max cost per request (¢)</label>
              <input type="number" id="pf-max-cost" min="0" step="0.1" value="${prefs.max_cost_cents ?? 0}"></div>
            <div class="field"><label>Latency tolerance (ms)</label>
              <input type="number" id="pf-latency" min="0" value="${prefs.latency_tolerance_ms ?? 0}"></div>
            <div class="field"><label>Max response tokens</label>
              <input type="number" id="pf-max-tokens" min="0" value="${prefs.max_response_tokens ?? 0}"></div>
            <div class="field"><label>Fallback depth</label>
              <input type="number" id="pf-fallback" min="0" value="${prefs.fallback_depth ?? 2}"></div>
            <div class="field"><label>Pinned default model (optional)</label>
              <input id="pf-default" placeholder="e.g. gpt-4o-mini" value="${esc(prefs.default_model || '')}"></div>
            <div class="field"><label>Disallowed models (comma-separated)</label>
              <input id="pf-disallowed" placeholder="model-a, model-b" value="${esc((prefs.disallowed_models || []).join(', '))}"></div>
          </div>
          <div style="margin-top:18px;display:flex;gap:10px;align-items:center">
            <button class="btn btn-primary" id="pf-save">Save preferences</button>
            <span class="tiny" id="pf-status"></span>
          </div>
        </div>
      </div>

      <div style="display:flex;flex-direction:column;gap:24px">
        <div class="profile-card">
          <h3>${icon('i-settings', 'icon-sm c-blue')} Active Profile</h3>
          <div id="profile-body"></div>
        </div>
        <div class="card">
          <div class="card-head"><div class="card-title"><div><h3>Priority Guide</h3></div></div></div>
          ${PRIORITIES.map((p) => `
            <div class="guide-item"><div class="n">${icon(p.icon, 'icon-sm ' + p.color)} ${p.name}</div>
            <div class="d">${p.guide}</div></div>`).join('')}
        </div>
      </div>
    </div>`;

    renderProfile();
    $('#prio-grid').addEventListener('click', (e) => {
      const btn = e.target.closest('button[data-prio]');
      if (!btn) return;
      prefs.priority = btn.dataset.prio;
      for (const b of $('#prio-grid').children) b.classList.toggle('active', b === btn);
      renderProfile();
    });
    for (const id of ['pf-only-free', 'pf-only-local', 'pf-max-cost', 'pf-latency', 'pf-max-tokens', 'pf-fallback', 'pf-default', 'pf-disallowed']) {
      $('#' + id).addEventListener('input', () => { collectPrefs(); renderProfile(); });
    }
    $('#pf-save').addEventListener('click', savePrefs);
  }

  function collectPrefs() {
    prefs.only_free = $('#pf-only-free').checked;
    prefs.only_local = $('#pf-only-local').checked;
    prefs.max_cost_cents = Number($('#pf-max-cost').value) || 0;
    prefs.latency_tolerance_ms = Number($('#pf-latency').value) || 0;
    prefs.max_response_tokens = Number($('#pf-max-tokens').value) || 0;
    prefs.fallback_depth = Number($('#pf-fallback').value) || 0;
    prefs.default_model = $('#pf-default').value.trim();
    prefs.disallowed_models = $('#pf-disallowed').value.split(',').map((s) => s.trim()).filter(Boolean);
  }

  function renderProfile() {
    const inf = '<span style="font-size:15px">∞</span>';
    const filters = [prefs.only_free && 'Free only', prefs.only_local && 'Local only'].filter(Boolean).join(', ') || 'None';
    $('#profile-body').innerHTML = `
      <div class="kv"><span class="k">Priority</span><span class="v c-blue">${esc(cap(prefs.priority))}</span></div>
      <div class="kv"><span class="k">Max Cost</span><span class="v">${prefs.max_cost_cents ? prefs.max_cost_cents + '¢' : inf}</span></div>
      <div class="kv"><span class="k">Latency Limit</span><span class="v">${prefs.latency_tolerance_ms ? prefs.latency_tolerance_ms + ' ms' : inf}</span></div>
      <div class="kv"><span class="k">Max Response</span><span class="v">${prefs.max_response_tokens ? fmtInt(prefs.max_response_tokens) : inf}</span></div>
      <div class="kv"><span class="k">Fallback Depth</span><span class="v">${prefs.fallback_depth ?? 0}</span></div>
      <div class="kv"><span class="k">Filters</span><span class="v">${esc(filters)}</span></div>
      <div class="kv"><span class="k">Default Model</span><span class="v">${esc(prefs.default_model || 'auto')}</span></div>`;
  }
  const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : '');

  async function savePrefs() {
    collectPrefs();
    const status = $('#pf-status');
    try {
      clearError();
      const res = await api('/api/prefs', { method: 'PUT', body: JSON.stringify(prefs) });
      prefs = await res.json();
      status.textContent = 'Saved ✓';
      toast('Preferences saved');
      setTimeout(() => { status.textContent = ''; }, 2500);
    } catch (err) {
      showError('Save failed: ' + err.message);
    }
  }

  // ---------- Playground ----------
  function loadPlayground() {
    const page = $('#page-playground');
    if (page.dataset.ready) return; // keep state between tab switches
    page.dataset.ready = '1';
    page.innerHTML = `
      <div class="card">
        <div class="card-head"><div class="card-title">${icon('i-play', 'icon c-blue')}
          <div><h3>Prompt</h3><p>Recommend explains the routing decision without executing; Send routes and runs it.</p></div></div></div>
        <textarea class="prompt" id="pg-prompt" placeholder="Explain quantum computing to a 10-year-old"></textarea>
        <div style="display:flex;gap:10px;margin-top:12px;flex-wrap:wrap">
          <button class="btn" id="pg-recommend">${icon('i-sparkles')} Recommend</button>
          <button class="btn btn-primary" id="pg-send">${icon('i-send')} Send</button>
        </div>
      </div>
      <div class="play-grid">
        <div class="card">
          <div class="card-head"><div class="card-title">${icon('i-shield', 'icon c-purple')}<div><h3>Routing decision</h3></div></div></div>
          <div id="pg-decision" class="muted small">Run “Recommend” or “Send” to see why Route42 picks a model.</div>
        </div>
        <div class="card">
          <div class="card-head"><div class="card-title">${icon('i-terminal', 'icon c-emerald')}<div><h3>Response</h3></div></div></div>
          <div id="pg-answer" class="answer muted">—</div>
        </div>
      </div>`;

    $('#pg-recommend').addEventListener('click', recommend);
    $('#pg-send').addEventListener('click', sendChat);
  }

  function renderDecision(x, candidates, explanation) {
    const pct = Math.round((x.complexity || 0) * 100);
    let html = `
      <div class="meta-chips" style="margin-bottom:12px">
        <span class="chip chip-cloud">model: ${esc(x.selected_model || '—')}</span>
        <span class="chip">provider: ${esc(x.provider || '—')}</span>
        ${x.category ? `<span class="chip chip-cat">${esc(x.category)}</span>` : ''}
        ${x.analyzer ? `<span class="chip">analyzer: ${esc(x.analyzer)}</span>` : ''}
        ${x.reason ? `<span class="chip">${esc(x.reason)}</span>` : ''}
        ${x.est_cost_cents ? `<span class="chip chip-local">~${fmtCents(x.est_cost_cents)}</span>` : '<span class="chip chip-local">$0.00</span>'}
      </div>
      <div class="small muted">Complexity</div>
      <div class="meter"><div style="width:${Math.max(2, pct)}%"></div></div>
      <div class="tiny" style="margin-top:4px">${(x.complexity || 0).toFixed(3)} · ${x.candidates_considered || 0} candidates considered</div>`;

    if (candidates?.length) {
      html += `<div class="tbl-wrap" style="margin-top:14px"><table class="tbl"><thead><tr>
        <th>Candidate</th><th class="num">Composite</th><th class="num">Quality</th><th class="num">Speed</th>
        <th class="num">Cost</th><th class="num">Est.</th></tr></thead><tbody>` +
        candidates.slice(0, 6).map((c) => `<tr>
          <td><code>${esc(c.model)}</code> <span class="tiny">${esc(c.provider)}</span></td>
          <td class="num">${fmtScore(c.composite)}</td><td class="num">${fmtScore(c.quality)}</td>
          <td class="num">${fmtScore(c.speed)}</td><td class="num">${fmtScore(c.cost)}</td>
          <td class="num">${fmtCents(c.est_cost_cents)}</td></tr>`).join('') + '</tbody></table></div>';
    }
    if (explanation) html += `<pre class="snippet" style="margin-top:14px;border-radius:10px;border-top:1px solid var(--gray-800)">${esc(explanation)}</pre>`;
    $('#pg-decision').innerHTML = html;
  }

  function promptMessages() {
    const text = $('#pg-prompt').value.trim();
    if (!text) { showError('Enter a prompt first.'); return null; }
    clearError();
    return [{ role: 'user', content: text }];
  }

  async function recommend() {
    const messages = promptMessages();
    if (!messages) return;
    $('#pg-decision').innerHTML = '<span class="muted small">Analyzing…</span>';
    try {
      const res = await (await api('/api/recommend', { method: 'POST', body: JSON.stringify({ messages }) })).json();
      renderDecision(res.x_route42 || {}, res.candidates, res.explanation);
    } catch (err) {
      $('#pg-decision').innerHTML = `<span class="c-red small">${esc(err.message)}</span>`;
    }
  }

  async function sendChat() {
    const messages = promptMessages();
    if (!messages) return;
    const answer = $('#pg-answer');
    const btn = $('#pg-send');
    btn.disabled = true;
    answer.classList.remove('muted');
    answer.textContent = '';
    try {
      const res = await api('/api/chat/completions', {
        method: 'POST',
        body: JSON.stringify({ model: 'auto', messages, stream: true }),
      });
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      let meta = null;
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split('\n');
        buf = lines.pop();
        for (const line of lines) {
          const data = line.startsWith('data: ') ? line.slice(6).trim() : '';
          if (!data || data === '[DONE]') continue;
          try {
            const chunk = JSON.parse(data);
            const delta = chunk.choices?.[0]?.delta?.content;
            if (delta) answer.textContent += delta;
            if (chunk.x_route42) meta = chunk.x_route42;
          } catch { /* partial line, ignored */ }
        }
      }
      if (!answer.textContent) answer.textContent = '(empty response)';
      if (meta) renderDecision(meta);
    } catch (err) {
      answer.innerHTML = `<span class="c-red">${esc(err.message)}</span>`;
    } finally {
      btn.disabled = false;
    }
  }

  // ---------- Interaction History ----------
  async function loadHistory() {
    const page = $('#page-history');
    page.innerHTML = `
      <div class="card">
        <div class="card-head">
          <div class="card-title">${icon('i-history', 'icon c-blue')}<div><h3>Recent interactions</h3>
            <p>Newest first. Recorded locally — nothing leaves your machine.</p></div></div>
          <button class="btn" id="hist-refresh">${icon('i-refresh')} Refresh</button>
        </div>
        <div class="tbl-wrap"><table class="tbl"><thead><tr>
          <th>Time</th><th>Model</th><th>Provider</th><th>Category</th><th class="num">Complexity</th>
          <th class="num">Tokens</th><th class="num">Cost</th><th class="num">Latency</th><th>Status</th>
        </tr></thead><tbody id="hist-rows"><tr><td colspan="9" class="muted">Loading…</td></tr></tbody></table></div>
      </div>`;
    $('#hist-refresh').addEventListener('click', loadHistory);
    try {
      const res = await getJSON('/api/interactions?limit=100');
      const rows = res.interactions || [];
      $('#hist-rows').innerHTML = rows.length ? rows.map((r) => `<tr>
        <td class="muted">${esc(new Date(r.ts).toLocaleString())}</td>
        <td><code>${esc(r.model)}</code></td>
        <td>${esc(r.provider)}</td>
        <td>${r.category ? `<span class="chip chip-cat">${esc(r.category)}</span>` : '—'}</td>
        <td class="num">${(r.complexity || 0).toFixed(2)}</td>
        <td class="num">${fmtInt((r.prompt_tokens || 0) + (r.completion_tokens || 0))}</td>
        <td class="num">${fmtCents(r.cost_cents)}</td>
        <td class="num">${fmtInt(r.latency_ms)} ms</td>
        <td>${r.status === 'ok' ? '<span class="c-green">ok</span>' : `<span class="c-red">${esc(r.status)}</span>`}</td>
      </tr>`).join('') : '<tr><td colspan="9" class="muted">No interactions yet — send something through the gateway.</td></tr>';
    } catch (err) {
      $('#hist-rows').innerHTML = `<tr><td colspan="9" class="c-red">${esc(err.message)}</td></tr>`;
    }
  }

  // ---------- boot ----------
  switchTab(location.hash.slice(1) || 'dashboard');
})();

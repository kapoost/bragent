// bragent public demo — read-only catalog + BYOK chat + wire panel.
// Sessions are in-memory server-side; this script keeps the local
// session_id and BYOK config in sessionStorage so a refresh preserves
// the conversation but tab-close clears it (the server cleared it on
// deploy anyway; nothing here is durable).

(function () {
  const SESSION_KEY = 'bragent.demo.session';
  const BYOK_KEY = 'bragent.demo.byok';

  const PROVIDERS = {
    mock: { label: 'mock', endpoint: '', model: '' },
    anthropic: { label: 'anthropic', endpoint: 'https://api.anthropic.com/v1', model: 'claude-haiku-4-5' },
    groq:      { label: 'groq',      endpoint: 'https://api.groq.com/openai/v1', model: 'llama-3.3-70b-versatile' },
    openai:    { label: 'openai',    endpoint: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
    deepseek:  { label: 'deepseek',  endpoint: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  };

  const $ = (id) => document.getElementById(id);
  const productsEl = $('products');
  const chatLog = $('chat-log');
  const chatForm = $('chat-form');
  const chatInput = $('chat-input');
  const sendBtn = $('send-btn');
  const newSessionBtn = $('new-session');
  const sessionIdEl = $('session-id');
  const providerPill = $('provider-pill');
  const byokProvider = $('byok-provider');
  const byokKey = $('byok-key');
  const byokStatus = $('byok-status');
  const wirePane = $('wire-pane');

  function readByok() {
    try { return JSON.parse(sessionStorage.getItem(BYOK_KEY) || '{}'); }
    catch (_) { return {}; }
  }
  function writeByok(b) { sessionStorage.setItem(BYOK_KEY, JSON.stringify(b)); }

  function applyByokUI() {
    const b = readByok();
    byokProvider.value = b.provider || 'mock';
    const spec = PROVIDERS[byokProvider.value] || PROVIDERS.mock;
    if (byokProvider.value === 'mock') {
      byokKey.style.display = 'none';
      byokKey.value = '';
      byokStatus.textContent = 'mock';
      byokStatus.className = 'pill mock';
      providerPill.textContent = 'llm: mock (offline)';
    } else {
      byokKey.style.display = '';
      byokKey.placeholder = `${spec.label} API key (your account; never stored)`;
      byokKey.value = b.api_key || '';
      const hasKey = !!byokKey.value;
      byokStatus.textContent = hasKey ? 'byok' : 'key needed';
      byokStatus.className = hasKey ? 'pill byok' : 'pill mock';
      providerPill.textContent = hasKey ? `llm: ${spec.label}` : 'llm: mock (paste key)';
    }
  }
  byokProvider.addEventListener('change', () => {
    writeByok({ provider: byokProvider.value });
    applyByokUI();
  });
  byokKey.addEventListener('input', () => {
    const cur = readByok();
    writeByok({ provider: cur.provider, api_key: byokKey.value });
    applyByokUI();
  });

  // ───── Catalog ─────
  async function loadProducts() {
    try {
      const res = await fetch('/demo/api/products');
      const data = await res.json();
      const products = data.products || [];
      if (!products.length) {
        productsEl.innerHTML = '<div class="empty">No products in fixture.</div>';
        return;
      }
      productsEl.innerHTML = products.map(p => {
        const price = p.price ? `${p.currency || 'USD'} ${p.price.toFixed(2)}` : 'free';
        const avail = p.available ? 'available' : 'unavailable';
        const klass = p.available ? '' : 'unavail';
        return `
          <div class="product">
            <div class="name">${esc(p.name || p.id)}</div>
            <div class="id">${esc(p.id)}</div>
            <div class="desc">${esc(p.description || '')}</div>
            <div class="meta"><span>${price}</span><span class="${klass}">${avail}</span></div>
          </div>`;
      }).join('');
    } catch (e) {
      productsEl.innerHTML = `<div class="empty">catalog error: ${esc(e.message)}</div>`;
    }
  }

  // ───── Chat ─────
  function appendTurn(role, text, handoffURL) {
    if (chatLog.querySelector('.empty')) chatLog.innerHTML = '';
    const div = document.createElement('div');
    div.className = 'turn ' + role;
    div.innerHTML = `<div class="role">${role}</div><div class="bubble">${esc(text)}</div>` +
      (handoffURL ? `<div class="handoff">↳ ${esc(handoffURL)}</div>` : '');
    chatLog.appendChild(div);
    chatLog.scrollTop = chatLog.scrollHeight;
  }

  function getSession() { return sessionStorage.getItem(SESSION_KEY); }
  function setSession(id) {
    if (id) sessionStorage.setItem(SESSION_KEY, id);
    else sessionStorage.removeItem(SESSION_KEY);
    sessionIdEl.textContent = id || 'no session';
  }
  newSessionBtn.addEventListener('click', () => {
    setSession(null);
    chatLog.innerHTML = '<div class="empty">New session — ask the brand agent something.</div>';
    wirePane.innerHTML = '<div class="empty">Send a turn to see the sponsored_context envelope and the auto-synthesised receipt.</div>';
  });

  chatForm.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const text = chatInput.value.trim();
    if (!text) return;
    appendTurn('host', text);
    chatInput.value = '';
    sendBtn.disabled = true;
    chatInput.disabled = true;
    try {
      const sid = getSession();
      const body = { message: text };
      if (sid) body.session_id = sid;
      else body.intent = text;

      // Attach BYOK if present + valid.
      const b = readByok();
      if (b.provider && b.provider !== 'mock' && b.api_key) {
        const spec = PROVIDERS[b.provider];
        body.llm = { endpoint: spec.endpoint, api_key: b.api_key, model: spec.model };
      }

      const res = await fetch('/demo/api/chat', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || res.statusText);

      if (data.session_id) setSession(data.session_id);
      const msg = data.response && data.response.message;
      const handoff = data.handoff && data.handoff.url;
      if (msg) appendTurn('brand', msg, handoff);
      renderWire(data);
      providerPill.textContent = 'llm: ' + (data.llm_provider || 'mock');
    } catch (e) {
      appendTurn('brand', '(error: ' + e.message + ')');
    } finally {
      sendBtn.disabled = false;
      chatInput.disabled = false;
      chatInput.focus();
    }
  });

  function renderWire(data) {
    const sc = data.sponsored_context;
    const pr = data.prior_receipt;
    let html = '';
    if (sc) {
      html += '<div class="label">Just emitted — sponsored_context</div>';
      html += `<pre>${esc(JSON.stringify(sc, null, 2))}</pre>`;
    }
    if (pr) {
      const matchLabel = matchedContext(sc, pr) ? 'match' : 'mismatch';
      html += `<div class="label ${matchLabel}">Prior turn — sponsored_context_receipt (auto-synthesised, ${matchLabel})</div>`;
      html += `<pre>${esc(JSON.stringify(pr, null, 2))}</pre>`;
    }
    if (!html) html = '<div class="empty">No wire payload on this turn.</div>';
    wirePane.innerHTML = html;
  }

  function matchedContext(sc, pr) {
    try {
      if (!sc || !pr) return true;
      const declared = sc.context_use;
      const accepted = pr.host_receipt && pr.host_receipt.accepted_context_use;
      if (pr.host_receipt && pr.host_receipt.status === 'rejected') return false;
      return declared === accepted;
    } catch (_) { return false; }
  }

  function esc(s) {
    return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  // boot
  applyByokUI();
  setSession(getSession());
  loadProducts();
})();

// bragent admin UI — vanilla JS, no framework, no bundler.
//
// Auth: token is taken from the URL query (?token=...) on first load,
// stashed in sessionStorage, then sent as X-Admin-Token on every fetch.
// Session ID for the chat panel lives in sessionStorage too — refresh
// the page and the conversation stays; close the tab and it clears.

(function () {
  const TOKEN_KEY = 'bragent.admin.token';
  const SESSION_KEY = 'bragent.admin.session';

  // Bootstrap token: prefer query string, fall back to sessionStorage.
  // Rewrite the URL without the token so it doesn't leak via copy-paste.
  const params = new URLSearchParams(location.search);
  if (params.has('token')) {
    sessionStorage.setItem(TOKEN_KEY, params.get('token'));
    params.delete('token');
    const clean = location.pathname + (params.toString() ? '?' + params : '');
    history.replaceState(null, '', clean);
  }
  const token = sessionStorage.getItem(TOKEN_KEY);
  if (!token) {
    document.body.innerHTML = '<div style="padding:40px;font-family:ui-monospace,monospace;color:#e5e7eb;background:#0f1115;height:100vh"><h1 style="font-size:14px">Token required</h1><p style="color:#9ca3af;font-size:13px">Append <code>?token=YOUR_ADMIN_TOKEN</code> to the URL (matches <code>[admin].token</code> in config.toml) to authenticate.</p></div>';
    return;
  }

  async function api(path, opts = {}) {
    const headers = Object.assign({ 'X-Admin-Token': token, 'Content-Type': 'application/json' }, opts.headers || {});
    const res = await fetch('/admin' + path, Object.assign({}, opts, { headers }));
    const txt = await res.text();
    let body = null;
    if (txt) try { body = JSON.parse(txt); } catch (_) { body = txt; }
    if (!res.ok) throw new Error((body && body.error) || res.statusText);
    return body;
  }

  // ──────────────────────────── CATALOG ────────────────────────────

  const productsEl = document.getElementById('products');
  const addForm = document.getElementById('add-form');
  const readonlyBanner = document.getElementById('readonly-banner');
  const editBanner = document.getElementById('edit-banner');
  const editBannerId = document.getElementById('edit-banner-id');
  const cancelEditBtn = document.getElementById('cancel-edit');
  const saveBtn = document.getElementById('save-btn');
  const brandEl = document.getElementById('brand-name');
  const healthEl = document.getElementById('health');

  // Closure-scoped catalog snapshot for click-to-edit. Refreshed on every
  // GET /api/products so editing always operates on server-confirmed data,
  // not stale render state.
  let currentProducts = [];

  async function refreshCatalog() {
    try {
      const view = await api('/api/products');
      brandEl.textContent = view.brand || '—';
      const writable = !!view.writable;
      readonlyBanner.style.display = writable ? 'none' : 'block';
      addForm.querySelectorAll('input, textarea, button').forEach(el => { el.disabled = !writable; });
      currentProducts = view.products || [];
      renderProducts(currentProducts);
      healthEl.textContent = 'ok · ' + currentProducts.length + ' products';
    } catch (e) {
      healthEl.textContent = 'error: ' + e.message;
    }
  }

  function renderProducts(products) {
    if (!products.length) {
      productsEl.innerHTML = '<div class="empty">No products yet. Add the first one above.</div>';
      return;
    }
    productsEl.innerHTML = products.map(p => {
      const tags = (p.tags || []).join(', ');
      const price = p.price ? `${p.currency || 'USD'} ${p.price.toFixed(2)}` : 'free';
      const avail = p.available ? 'available' : 'unavailable';
      const availClass = p.available ? '' : 'unavail';
      return `
        <div class="product" data-id="${escapeHTML(p.id)}">
          <div class="row">
            <div>
              <div class="name">${escapeHTML(p.name || p.id)}</div>
              <div class="id">${escapeHTML(p.id)}</div>
            </div>
            <div class="actions">
              <button class="edit" data-id="${escapeHTML(p.id)}">Edit</button>
              <button class="del" data-id="${escapeHTML(p.id)}">Delete</button>
            </div>
          </div>
          <div class="desc">${escapeHTML(p.description || '')}</div>
          <div class="meta">
            <span>${price}</span>
            <span class="${availClass}">${avail}</span>
            ${tags ? `<span style="color:var(--fg-dim)">${escapeHTML(tags)}</span>` : ''}
          </div>
        </div>`;
    }).join('');
  }

  // "Fill with example" populates every empty input from its data-example
  // (mirrors the placeholder shown in the UI). Lets the operator save a
  // working seed product in one click without re-typing the example.
  //
  // Safari quirk: a programmatic `value =` assignment does not rerun the
  // required/pattern validity check. Dispatch an 'input' event so the
  // browser updates form.checkValidity() — otherwise Save still pops
  // "Fill out this field" on a field that visibly has a value.
  //
  // Guard each addEventListener with `if (btn)` so a missing element on
  // some future HTML edit cannot abort the IIFE before the catalog/chat
  // submit handlers bind. Both forms also carry `onsubmit="return false"`
  // at the HTML level so an unbound handler never triggers a native
  // form GET to /admin/api/... without auth.
  const fillBtn = document.getElementById('fill-example');
  if (fillBtn) {
    fillBtn.addEventListener('click', () => {
      addForm.querySelectorAll('input[data-example], textarea[data-example]').forEach(el => {
        el.value = el.dataset.example;
        el.dispatchEvent(new Event('input', { bubbles: true }));
      });
      addForm.querySelector('input[name="available"]').checked = true;
    });
  }
  const clearBtn = document.getElementById('clear-form');
  if (clearBtn) {
    clearBtn.addEventListener('click', () => {
      exitEditMode();
      addForm.reset();
      addForm.querySelector('input[name="available"]').checked = true;
      addForm.querySelector('input[name="currency"]').value = 'USD';
    });
  }

  // Edit mode: clicking a product's Edit button populates the form with
  // its current values and switches the submit-button label. The ID
  // input is locked (readonly) while editing because renaming an ID
  // would create a sibling product, not rename in place — the server
  // upsert is keyed on ID. Cancel / Clear / successful Save all exit
  // edit mode and restore the new-product affordance.
  function enterEditMode(p) {
    addForm.dataset.editId = p.id;
    addForm.querySelector('input[name="id"]').value = p.id;
    addForm.querySelector('input[name="id"]').readOnly = true;
    addForm.querySelector('input[name="name"]').value = p.name || '';
    addForm.querySelector('textarea[name="description"]').value = p.description || '';
    addForm.querySelector('input[name="price"]').value = p.price || '';
    addForm.querySelector('input[name="currency"]').value = p.currency || 'USD';
    addForm.querySelector('input[name="url"]').value = p.url || '';
    addForm.querySelector('input[name="tags"]').value = (p.tags || []).join(', ');
    addForm.querySelector('input[name="available"]').checked = !!p.available;
    addForm.querySelectorAll('input, textarea').forEach(el => el.dispatchEvent(new Event('input', { bubbles: true })));
    editBannerId.textContent = p.id;
    editBanner.classList.add('active');
    saveBtn.textContent = 'Update product';
    addForm.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
  function exitEditMode() {
    delete addForm.dataset.editId;
    addForm.querySelector('input[name="id"]').readOnly = false;
    editBanner.classList.remove('active');
    editBannerId.textContent = '—';
    saveBtn.textContent = 'Save product';
  }
  if (cancelEditBtn) {
    cancelEditBtn.addEventListener('click', () => {
      exitEditMode();
      addForm.reset();
      addForm.querySelector('input[name="available"]').checked = true;
      addForm.querySelector('input[name="currency"]').value = 'USD';
    });
  }

  addForm.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const fd = new FormData(addForm);
    const product = {
      id: fd.get('id').trim(),
      name: fd.get('name').trim(),
      description: fd.get('description').trim(),
      price: parseFloat(fd.get('price') || '0') || 0,
      currency: fd.get('currency').trim() || 'USD',
      url: fd.get('url').trim(),
      available: fd.get('available') === 'on',
      tags: fd.get('tags').split(',').map(t => t.trim()).filter(Boolean),
    };
    try {
      await api('/api/products', { method: 'POST', body: JSON.stringify(product) });
      exitEditMode();
      addForm.reset();
      // restore default checkbox state
      addForm.querySelector('input[name="available"]').checked = true;
      addForm.querySelector('input[name="currency"]').value = 'USD';
      await refreshCatalog();
    } catch (e) {
      alert('Save failed: ' + e.message);
    }
  });

  productsEl.addEventListener('click', async (ev) => {
    const editBtn = ev.target.closest('button.edit');
    if (editBtn) {
      const id = editBtn.dataset.id;
      const p = currentProducts.find(x => x.id === id);
      if (p) enterEditMode(p);
      return;
    }
    const delBtn = ev.target.closest('button.del');
    if (!delBtn) return;
    const id = delBtn.dataset.id;
    if (!confirm('Delete ' + id + '?')) return;
    try {
      await api('/api/products/' + encodeURIComponent(id), { method: 'DELETE' });
      if (addForm.dataset.editId === id) exitEditMode();
      await refreshCatalog();
    } catch (e) {
      alert('Delete failed: ' + e.message);
    }
  });

  // ──────────────────────────── CHAT ────────────────────────────

  const chatLog = document.getElementById('chat-log');
  const chatForm = document.getElementById('chat-form');
  const chatInput = document.getElementById('chat-input');
  const sendBtn = document.getElementById('send-btn');
  const resetBtn = document.getElementById('reset-chat');
  const sessionStatusEl = document.getElementById('session-status');
  const statusLine = document.getElementById('status-line');

  function renderSessionStatus() {
    const sid = sessionStorage.getItem(SESSION_KEY);
    sessionStatusEl.textContent = sid ? sid : 'no session';
  }

  function appendTurn(role, text, handoffURL) {
    if (chatLog.querySelector('.empty')) chatLog.innerHTML = '';
    const div = document.createElement('div');
    div.className = 'turn ' + role;
    div.innerHTML = `<div class="role">${role}</div><div class="bubble">${escapeHTML(text)}</div>` +
      (handoffURL ? `<div class="handoff">↳ ${escapeHTML(handoffURL)}</div>` : '');
    chatLog.appendChild(div);
    chatLog.scrollTop = chatLog.scrollHeight;
  }

  function setStatusLine(status) {
    if (!status || status === 'active') { statusLine.textContent = ''; statusLine.className = 'status-line'; return; }
    if (status === 'pending_handoff') { statusLine.textContent = '→ pending handoff to brand checkout'; statusLine.className = 'status-line handoff'; }
    if (status === 'terminated') { statusLine.textContent = '× session terminated'; statusLine.className = 'status-line terminated'; }
  }

  resetBtn.addEventListener('click', () => {
    sessionStorage.removeItem(SESSION_KEY);
    chatLog.innerHTML = '<div class="empty">Ask the brand agent about a product, or signal buy intent to see the handoff.</div>';
    setStatusLine('');
    renderSessionStatus();
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
      const sid = sessionStorage.getItem(SESSION_KEY);
      const body = sid
        ? { session_id: sid, message: text }
        : { intent: text, message: text };
      const reply = await api('/api/chat', { method: 'POST', body: JSON.stringify(body) });
      if (reply.session_id && !sid) {
        sessionStorage.setItem(SESSION_KEY, reply.session_id);
        renderSessionStatus();
      }
      const msg = reply.response && reply.response.message;
      const handoff = reply.handoff && reply.handoff.url;
      if (msg) appendTurn('brand', msg, handoff);
      setStatusLine(reply.session_status);
    } catch (e) {
      appendTurn('brand', '(error: ' + e.message + ')');
    } finally {
      sendBtn.disabled = false;
      chatInput.disabled = false;
      chatInput.focus();
    }
  });

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  // boot
  renderSessionStatus();
  refreshCatalog();
})();

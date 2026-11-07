// Progressive enhancement only. The server owns policy, proof and session state.
(() => {
  'use strict';
  const supported = window.isSecureContext && !!window.PublicKeyCredential && !!navigator.credentials;
  const decode = (s) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
  const encode = (b) => btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  const publicKey = (value) => {
    const p = { ...value, challenge: decode(value.challenge) };
    if (p.user) p.user = { ...p.user, id: decode(p.user.id) };
    for (const field of ['allowCredentials', 'excludeCredentials']) {
      if (p[field]) p[field] = p[field].map(c => ({ ...c, id: decode(c.id) }));
    }
    return p;
  };
  const credentialJSON = (c) => {
    if (typeof c.toJSON === 'function') return c.toJSON();
    const response = { clientDataJSON: encode(c.response.clientDataJSON) };
    for (const key of ['attestationObject', 'authenticatorData', 'signature', 'userHandle']) {
      if (c.response[key]) response[key] = encode(c.response[key]);
    }
    if (c.response.getTransports) response.transports = c.response.getTransports();
    return { id: c.id, rawId: encode(c.rawId), type: c.type, authenticatorAttachment: c.authenticatorAttachment,
      response, clientExtensionResults: c.getClientExtensionResults() };
  };
  for (const panel of document.querySelectorAll('[data-passkey-panel]')) {
    const status = panel.querySelector('[data-passkey-status]');
    const actions = () => [...panel.querySelectorAll('[data-passkey-action]')];
    let busy = false, frozen = false;
    const message = (text, error = false) => { status.textContent = text; status.classList.toggle('error', error); };
    const lock = () => actions().forEach(b => { b.disabled = busy || frozen || ((b.hasAttribute('data-passkey-login') || b.closest('form[data-passkey-register]')) && (!supported || panel.dataset.available !== 'true')); });
    const request = async (path, method, body) => {
      let response;
      try {
        response = await fetch(path, { method, credentials: 'same-origin', redirect: 'error', headers: {
          Accept: 'application/json', ...(method === 'GET' ? {} : { 'Content-Type': 'application/json', 'X-CSRF-Token': panel.dataset.csrf })
        }, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(20000) });
      } catch { const e = new Error('请求结果未知，请重新读取账号状态后再操作。'); e.unknown = method !== 'GET'; throw e; }
      let data;
      try { data = await response.json(); } catch { const e = new Error('响应无法确认，请重新读取账号状态。'); e.unknown = method !== 'GET'; throw e; }
      if (!response.ok) {
        const error = new Error(`操作未完成（${data.code || data.error || response.status}）。请重新登录或读取当前状态。`);
        error.unknown = response.status === 409 || data.code === 'outcome_unknown' || response.status >= 500;
        throw error;
      }
      return data;
    };
    const account = panel.dataset.mode === 'account';
    const reload = async () => {
      const data = await request(panel.dataset.base, 'GET');
      if (!Array.isArray(data.items)) throw new Error('Passkey 列表无法确认，请重新读取。');
      const list = panel.querySelector('[data-passkey-list]');
      list.replaceChildren();
      for (const item of data.items) {
        if (typeof item.id !== 'string' || typeof item.name !== 'string' || !Number.isSafeInteger(item.config_version)) throw new Error('Passkey 列表格式无效。');
        const row = document.createElement('li'); row.className = 'passkey-row';
        const label = document.createElement('span'); label.textContent = item.name;
        const detail = document.createElement('small'); detail.className = 'help';
        detail.textContent = `创建于 ${new Date(item.created_at).toLocaleDateString()} · ${item.last_used_at ? '最近使用 ' + new Date(item.last_used_at).toLocaleString() : '尚未用于登录'}`;
        label.append(document.createElement('br'), detail);
        const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'secondary';
        remove.textContent = '删除'; remove.setAttribute('aria-label', `删除 Passkey ${item.name}`); remove.dataset.passkeyAction = '';
        remove.addEventListener('click', async () => {
          if (busy || frozen || !confirm('删除此 Passkey？其他会话将被撤销。请确认还拥有其他可用登录方式。')) return;
          await run(async () => { await request(`${panel.dataset.base}/${encodeURIComponent(item.id)}`, 'DELETE', { config_version: item.config_version }); await reload(); message('Passkey 已删除，其他会话已撤销。'); });
        });
        row.append(label, remove); list.append(row);
      }
      if (!data.items.length) { const empty = document.createElement('li'); empty.className = 'help'; empty.textContent = '尚未添加 Passkey。'; list.append(empty); }
      panel.dataset.available = String(data.available === true && data.enabled === true);
      frozen = false; lock();
    };
    const run = async (operation) => {
      if (busy) return;
      busy = true; lock();
      try { await operation(); } catch (e) {
        frozen ||= e.unknown === true;
        message(e.name === 'NotAllowedError' || e.name === 'AbortError' ? '操作已取消或超时，可以重新发起。' : e.name === 'InvalidStateError' ? '该设备已保存此账号的 Passkey，请使用其他设备或认证器。' : e.message || 'Passkey 操作失败。', true);
      } finally { busy = false; lock(); }
    };
    const form = panel.querySelector('[data-passkey-register]');
    const invoke = async (mode) => {
      if (busy || frozen || !supported || panel.dataset.available !== 'true') return;
      await run(async () => {
        message(mode === 'login' ? '请使用设备上的 Passkey 验证身份…' : '请按照设备提示创建 Passkey…');
        const body = mode === 'register' ? { name: form?.elements.namedItem('name')?.value || 'Passkey',
          ...(account ? {} : { display_name: form?.elements.namedItem('display_name')?.value || '' }) } : {};
        const begin = await request(`${panel.dataset.base}/${mode}/begin`, 'POST', body);
        if (typeof begin.ceremony_id !== 'string' || !begin.options?.publicKey) throw new Error('认证选项无法确认，请重新读取页面。');
        const options = { publicKey: publicKey(begin.options.publicKey) };
        const credential = await navigator.credentials[mode === 'login' ? 'get' : 'create'](options);
        if (!credential) throw new Error('未获得 Passkey 凭据。');
        const result = await request(`${panel.dataset.base}/${mode}/finish`, 'POST', { ceremony_id: begin.ceremony_id, credential: credentialJSON(credential) });
        if (account) { await reload(); message('Passkey 已添加。可在下次登录时使用。'); if (form) form.reset(); }
        else {
          const target = new URL(result.redirect, location.origin);
          const expected = panel.dataset.base.replace(/\/passkey$/, '');
          if (target.origin !== location.origin || target.pathname !== expected || target.search || target.hash) { const e = new Error('登录结果无法确认，请刷新页面。'); e.unknown = true; throw e; }
          location.assign(target.href);
        }
      });
    };
    panel.querySelector('[data-passkey-login]')?.addEventListener('click', () => invoke('login'));
    form?.addEventListener('submit', (e) => { e.preventDefault(); if (form.reportValidity()) invoke('register'); });
    panel.querySelector('[data-passkey-reload]')?.addEventListener('click', () => account ? run(async () => { await reload(); message('已读取当前状态。'); }) : location.reload());
    if (!supported) message('此浏览器或访问环境不支持 Passkey。请使用支持 WebAuthn 的浏览器，通过 HTTPS 或 localhost 访问。');
    lock();
    if (account) run(reload);
  }
})();

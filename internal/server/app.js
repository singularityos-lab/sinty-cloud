(function () {
  'use strict';
  const $ = (id) => document.getElementById(id);
  let here = '';
  let doomed = null;

  const enc = (p) => p.split('/').filter(Boolean).map(encodeURIComponent).join('/');
  const join = (a, b) => (a ? a + '/' + b : b);
  const say = (t) => { $('message').textContent = t; };

  async function call(method, path, body) {
    const r = await fetch(path, { method, body, credentials: 'same-origin', headers: { 'X-Sinty-Request': '1' } });
    if (r.status === 401) { location.assign('/login'); throw new Error('signed out'); }
    return r;
  }

  function size(n) {
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
    return (i ? n.toFixed(1) : n) + ' ' + units[i];
  }

  function crumbs() {
    const nav = $('crumbs');
    nav.replaceChildren();
    const parts = here.split('/').filter(Boolean);
    const add = (label, target) => {
      const b = document.createElement('button');
      b.textContent = label;
      b.addEventListener('click', () => open(target));
      nav.append(b);
    };
    add('Files', '');
    parts.forEach((p, i) => {
      nav.append(document.createTextNode('/'));
      add(p, parts.slice(0, i + 1).join('/'));
    });
  }

  async function usage() {
    const r = await call('GET', '/api/v1/me');
    if (!r.ok) return;
    const me = await r.json();
    $('usage').textContent = size(me.used) + ' of ' + size(me.quota);
  }

  async function open(dir) {
    here = dir;
    say('');
    crumbs();
    const r = await call('GET', '/api/v1/files/' + enc(dir));
    if (!r.ok) { say('This folder cannot be opened.'); return; }
    const data = await r.json();
    const list = $('list');
    list.replaceChildren();
    if (!data.entries.length) {
      const p = document.createElement('p');
      p.className = 'empty';
      p.textContent = 'This folder is empty.';
      list.append(p);
    }
    for (const e of data.entries) {
      const row = document.createElement('sinty-row');
      row.setAttribute('heading', e.name);
      row.setAttribute('subtitle', e.dir ? 'Folder' : size(e.size) + ', ' + new Date(e.modified).toLocaleString());
      const target = join(here, e.name);
      if (e.dir) {
        row.setAttribute('activatable', '');
        row.addEventListener('click', (ev) => { if (ev.target === row) open(target); });
        const go = document.createElement('sinty-icon');
        go.setAttribute('name', 'go-next-symbolic');
        row.append(go);
      } else {
        const get = document.createElement('sinty-button');
        get.setAttribute('variant', 'flat');
        get.textContent = 'Download';
        get.addEventListener('click', () => download(target, e.name));
        row.append(get);
      }
      const del = document.createElement('sinty-button');
      del.setAttribute('variant', 'flat');
      del.setAttribute('aria-label', 'Delete ' + e.name);
      const bin = document.createElement('sinty-icon');
      bin.setAttribute('name', 'user-trash-symbolic');
      del.append(bin);
      del.addEventListener('click', (ev) => {
        ev.stopPropagation();
        doomed = target;
        $('delete-text').textContent = e.name + (e.dir ? ' and everything in it' : '') + ' will be deleted.';
        $('delete-dialog').show();
      });
      row.append(del);
      list.append(row);
    }
    usage();
  }

  async function download(target, name) {
    const r = await call('GET', '/api/v1/files/' + enc(target));
    if (!r.ok) { say('The file cannot be downloaded.'); return; }
    const url = URL.createObjectURL(await r.blob());
    const a = document.createElement('a');
    a.href = url;
    a.download = name;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
  }

  $('upload').addEventListener('click', () => $('picker').click());
  $('picker').addEventListener('change', async () => {
    for (const f of $('picker').files) {
      const r = await call('PUT', '/api/v1/files/' + enc(join(here, f.name)), f);
      if (r.status === 507) { say('There is no room left for ' + f.name + '.'); break; }
      if (!r.ok) { say(f.name + ' could not be uploaded.'); break; }
    }
    $('picker').value = '';
    open(here);
  });

  $('new-folder').addEventListener('click', () => { $('folder-name').value = ''; $('folder-dialog').show(); });
  $('folder-cancel').addEventListener('click', () => $('folder-dialog').close());
  $('folder-create').addEventListener('click', async () => {
    const name = $('folder-name').value.trim();
    if (!name || name.includes('/')) return;
    const r = await call('POST', '/api/v1/folders/' + enc(join(here, name)));
    $('folder-dialog').close();
    if (!r.ok) say('The folder could not be created.');
    open(here);
  });
  $('delete-cancel').addEventListener('click', () => $('delete-dialog').close());
  $('delete-confirm').addEventListener('click', async () => {
    $('delete-dialog').close();
    if (!doomed) return;
    const r = await call('DELETE', '/api/v1/files/' + enc(doomed));
    if (!r.ok) say('It could not be deleted.');
    doomed = null;
    open(here);
  });

  open('');
})();

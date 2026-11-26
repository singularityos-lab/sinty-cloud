(function () {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };
  const icon = (name, color) => {
    const i = document.createElement('sinty-icon');
    i.setAttribute('name', name);
    if (color) i.setAttribute('color', '');
    return i;
  };
  const enc = (p) => p.split('/').filter(Boolean).map(encodeURIComponent).join('/');
  const join = (a, b) => (a ? a + '/' + b : b);
  const parent = (p) => p.split('/').slice(0, -1).join('/');
  const base = (p) => p.split('/').pop();
  const store = {
    get: (k, d) => { try { return localStorage.getItem('sinty-cloud.' + k) || d; } catch { return d; } },
    set: (k, v) => { try { localStorage.setItem('sinty-cloud.' + k, v); } catch { /* private mode */ } },
  };

  const state = {
    view: 'files',
    dir: '',
    query: '',
    items: [],
    selected: new Set(),
    anchor: null,
    mode: store.get('mode', 'list'),
    sort: store.get('sort', 'name'),
    desc: store.get('desc', '') === '1',
    me: null,
    thumbs: new Map(),
  };

  const kinds = [
    [/\.(jpe?g|png|gif|webp|bmp|heic|avif|svg|tiff?)$/i, 'image-x-generic', 'Image'],
    [/\.(mp3|ogg|oga|flac|wav|m4a|opus|aac)$/i, 'audio-x-generic', 'Audio'],
    [/\.(mp4|mkv|webm|mov|avi|m4v)$/i, 'video-x-generic', 'Video'],
    [/\.pdf$/i, 'application-pdf', 'PDF Document'],
    [/\.(odt|docx?|rtf|pages)$/i, 'x-office-document', 'Document'],
    [/\.(ods|xlsx?|csv|numbers)$/i, 'x-office-spreadsheet', 'Spreadsheet'],
    [/\.(odp|pptx?|key)$/i, 'x-office-presentation', 'Presentation'],
    [/\.(zip|tar|gz|tgz|xz|zst|bz2|7z|rar)$/i, 'package-x-generic', 'Archive'],
    [/\.(sh|py|js|ts|go|rs|c|h|cpp|vala|json|ya?ml|toml|xml|html|css)$/i, 'text-x-script', 'Code'],
    [/\.(ttf|otf|woff2?)$/i, 'font-x-generic', 'Font'],
    [/\.(txt|md|log|ini|conf)$/i, 'text-x-generic', 'Text'],
  ];
  const kindOf = (e) => {
    if (e.dir) return ['folder', 'Folder'];
    for (const [re, name, label] of kinds) if (re.test(e.name)) return [name, label];
    return ['text-x-generic', 'File'];
  };
  const isImage = (name) => /\.(jpe?g|png|gif|webp|bmp)$/i.test(name);
  const isText = (name) => /\.(txt|md|log|ini|conf|csv|sh|py|js|ts|go|rs|c|h|cpp|vala|json|ya?ml|toml|xml|html|css|svg)$/i.test(name);

  function size(n) {
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
    return (i ? (n < 10 ? n.toFixed(1) : Math.round(n)) : n) + ' ' + units[i];
  }

  const timeFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
  const dayFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short' });
  const yearFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
  function when(iso) {
    const d = new Date(iso);
    const now = new Date();
    const days = Math.floor((new Date(now.getFullYear(), now.getMonth(), now.getDate()) - new Date(d.getFullYear(), d.getMonth(), d.getDate())) / 86400000);
    if (days === 0) return 'Today ' + timeFmt.format(d);
    if (days === 1) return 'Yesterday ' + timeFmt.format(d);
    return (d.getFullYear() === now.getFullYear() ? dayFmt : yearFmt).format(d);
  }

  async function call(method, path, body, json) {
    const init = { method, credentials: 'same-origin', headers: { 'X-Sinty-Request': '1' } };
    if (json !== undefined) {
      init.body = JSON.stringify(json);
      init.headers['Content-Type'] = 'application/json';
    } else if (body !== undefined) {
      init.body = body;
    }
    const r = await fetch(path, init);
    if (r.status === 401) {
      location.assign('/login?back=' + encodeURIComponent(location.pathname + location.hash));
      throw new Error('signed out');
    }
    return r;
  }

  const reasons = {
    full: 'There is no room left in your cloud.',
    exists: 'Something with that name is already there.',
    invalid_name: 'That name cannot be used.',
    not_found: 'It is no longer there.',
    changed: 'It changed on another device. Reload and try again.',
    not_a_file: 'Only files can be shared with a link.',
  };
  async function why(r) {
    const e = await r.json().catch(() => ({}));
    return reasons[e.error] || 'Something went wrong. Try again.';
  }
  const toast = (text, opts) => SintyUI.toast(text, opts);

  function route() {
    const h = decodeURIComponent(location.hash.slice(1));
    const [view, ...rest] = h.split('/');
    const known = ['files', 'recent', 'starred', 'links', 'trash', 'search'];
    state.view = known.includes(view) ? view : 'files';
    state.dir = state.view === 'files' ? rest.filter(Boolean).join('/') : '';
    state.query = state.view === 'search' ? rest.join('/') : '';
    if (state.view !== 'search') $('search').value = '';
    state.selected.clear();
    load();
  }
  const go = (view, rest) => {
    const h = '#' + view + (rest ? '/' + rest.split('/').map(encodeURIComponent).join('/') : '');
    if (location.hash === h) route(); else location.hash = h;
  };
  const openDir = (p) => go('files', p);

  async function me() {
    const r = await call('GET', '/api/v1/me');
    if (!r.ok) return;
    state.me = await r.json();
    const used = state.me.used / state.me.quota;
    $('space').value = used;
    $('space').setAttribute('level', used > 0.95 ? 'error' : used > 0.8 ? 'warning' : '');
    $('space-text').textContent = size(state.me.used) + ' of ' + size(state.me.quota) + ' used' + (state.me.trash ? ', ' + size(state.me.trash) + ' in the trash' : '');
    $('user').textContent = state.me.user;
    $('avatar').setAttribute('name', state.me.user);
    document.querySelector('[data-view="trash"]').setAttribute('icon', state.me.trash_items ? 'user-trash-full-symbolic' : 'user-trash-symbolic');
  }

  async function load() {
    for (const row of document.querySelectorAll('sinty-sidebar-row[data-view]')) row.toggleAttribute('active', row.dataset.view === state.view);
    const titles = { recent: 'Recent', starred: 'Starred', links: 'Shared Links', trash: 'Trash', search: 'Search' };
    document.title = (state.view === 'files' ? (state.dir ? base(state.dir) : 'Files') : titles[state.view]) + ' - Sinty Cloud';
    crumbs();
    chrome();
    $('app').scrollToTop();
    let items = [];
    let failed = false;
    const fetchList = async (url, key) => {
      const r = await call('GET', url);
      if (!r.ok) { failed = r.status; return []; }
      return (await r.json())[key] || [];
    };
    if (state.view === 'files') {
      const list = await fetchList('/api/v1/files/' + enc(state.dir), 'entries');
      items = list.map((e) => ({ ...e, path: join(state.dir, e.name) }));
    } else if (state.view === 'recent' || state.view === 'starred') {
      items = (await fetchList('/api/v1/' + (state.view === 'recent' ? 'recent' : 'stars'), 'results'));
    } else if (state.view === 'search') {
      items = state.query ? await fetchList('/api/v1/search?q=' + encodeURIComponent(state.query), 'results') : [];
    } else if (state.view === 'links') {
      const links = await fetchList('/api/v1/links', 'links');
      items = links.map((l) => ({ name: base(l.path), path: l.path, token: l.token, expires: l.expires, created: l.created, link: true }));
    } else if (state.view === 'trash') {
      const r = await call('GET', '/api/v1/trash');
      if (r.ok) {
        const t = await r.json();
        items = t.items.map((i) => ({ ...i, path: i.id, trash: true, modified: i.deleted }));
        state.keepDays = t.keep_days;
      } else failed = r.status;
    }
    state.items = items;
    render(failed);
    me();
  }

  function crumbs() {
    const path = $('path');
    if (state.view === 'files') {
      const parts = state.dir.split('/').filter(Boolean);
      path.segments = [{ icon: 'user-home-symbolic', label: 'Files', target: '' }].concat(parts.map((p, i) => ({ label: p, target: parts.slice(0, i + 1).join('/') })));
      path.buttons.forEach((b, i) => dropTarget(b, path.segments[i].target));
    } else {
      const titles = { recent: 'Recent', starred: 'Starred', links: 'Shared Links', trash: 'Trash', search: state.query ? 'Results for “' + state.query + '”' : 'Search' };
      path.segments = [{ label: titles[state.view], target: null }];
    }
  }

  function chrome() {
    const n = state.selected.size;
    const sel = n > 0;
    for (const b of document.querySelectorAll('.std')) b.hidden = sel;
    $('selcount').hidden = !sel;
    $('selcount').textContent = n === 1 ? '1 Selected' : n + ' Selected';
    const v = state.view;
    if (!sel) {
      $('new-folder').hidden = v !== 'files';
      $('upload').hidden = v !== 'files';
      $('empty-trash').hidden = v !== 'trash' || !state.items.length;
      $('sort').hidden = v === 'links' || v === 'trash';
      $('mode').hidden = v === 'links' || v === 'trash';
    }
    const picked = [...state.selected].map((p) => state.items.find((i) => i.path === p)).filter(Boolean);
    const onlyFiles = picked.every((i) => !i.dir);
    const show = {
      download: sel && v !== 'trash' && v !== 'links' && onlyFiles,
      move: sel && v !== 'trash' && v !== 'links',
      copy: sel && v !== 'trash' && v !== 'links',
      star: sel && v !== 'trash' && v !== 'links',
      restore: sel && v === 'trash',
      purge: sel && v === 'trash',
      unshare: sel && v === 'links',
      trash: sel && v !== 'trash' && v !== 'links',
      clear: sel,
    };
    for (const b of document.querySelectorAll('.sel')) b.hidden = !show[b.dataset.act];
    const allStarred = picked.length && picked.every((i) => i.starred);
    const star = document.querySelector('.sel[data-act="star"]');
    star.setAttribute('label', allStarred ? 'Remove Star' : 'Star');
    star.setAttribute('icon', allStarred ? 'non-starred-symbolic' : 'starred-symbolic');
    $('mode').value = state.mode;
  }

  function sorted(items) {
    if (state.view === 'recent' || state.view === 'trash' || state.view === 'links') return items;
    const dir = state.desc ? -1 : 1;
    const byName = (a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
    const by = {
      name: byName,
      size: (a, b) => (a.size || 0) - (b.size || 0),
      modified: (a, b) => new Date(a.modified) - new Date(b.modified),
    }[state.sort];
    return [...items].sort((a, b) => (a.dir !== b.dir ? (a.dir ? -1 : 1) : by(a, b) * dir || byName(a, b)));
  }

  const empties = {
    files: ['folder', 'This Folder Is Empty', 'Drop files here, or use Upload.'],
    recent: ['document-open-recent', 'Nothing Recent', 'Files you add or change show up here.'],
    starred: ['folder', 'No Starred Files', 'Star the files and folders you use most to find them here.'],
    links: ['emblem-shared', 'No Shared Links', 'Share a file to get a link anyone can download it from.'],
    trash: ['user-trash', 'Trash Is Empty', 'Items you move to the trash stay here for 30 days.'],
    search: ['edit-find-symbolic', 'No Results', 'Try another name.'],
  };

  function render(failed) {
    const view = $('view');
    view.replaceChildren();
    const items = sorted(state.items);
    state.shown = items;
    $('app').setAttribute('status', failed || !items.length ? '' : items.length === 1 ? '1 item' : items.length + ' items');
    const note = $('note');
    note.hidden = true;
    if (state.view === 'trash' && items.length) {
      note.hidden = false;
      note.textContent = 'Items in the trash are deleted for good after ' + (state.keepDays || 30) + ' days.';
    }
    const empty = $('empty');
    if (failed) {
      empty.hidden = false;
      empty.setAttribute('icon', 'dialog-warning-symbolic');
      empty.setAttribute('heading', failed === 404 ? 'Not Found' : 'Cannot Open');
      empty.setAttribute('description', failed === 404 ? 'This folder was moved or deleted.' : 'Check the connection and try again.');
      empty.replaceChildren();
      const b = el('sinty-button', '', failed === 404 ? 'Go to Files' : 'Try Again');
      b.setAttribute('pill', '');
      b.addEventListener('click', () => (failed === 404 ? openDir('') : load()));
      empty.append(b);
      return;
    }
    if (!items.length) {
      const [ic, h, d] = state.view === 'search' && !state.query ? ['edit-find-symbolic', 'Search Your Files', 'Type a name in the search field.'] : empties[state.view];
      empty.hidden = false;
      empty.setAttribute('icon', ic);
      empty.setAttribute('heading', h);
      empty.setAttribute('description', d);
      empty.replaceChildren();
      if (state.view === 'files') {
        const b = el('sinty-button', '', 'Upload Files');
        b.setAttribute('variant', 'suggested');
        b.setAttribute('pill', '');
        b.addEventListener('click', () => $('picker').click());
        empty.append(b);
      }
      return;
    }
    empty.hidden = true;
    const grid = state.mode === 'grid' && state.view !== 'links' && state.view !== 'trash';
    view.append(grid ? gridView(items) : listView(items));
    syncSelection();
  }

  function listView(items) {
    const v = state.view;
    const box = el('div', 'sinty-data-view');
    box.setAttribute('role', 'grid');
    box.setAttribute('aria-multiselectable', 'true');
    const cols = v === 'trash' ? ['Name', 'Deleted From', 'Deleted', ''] : v === 'links' ? ['File', 'Location', 'Expires', ''] : ['Name', 'Size', 'Modified', ''];
    const keys = v === 'trash' || v === 'links' ? [null, null, null, null] : ['name', 'size', 'modified', null];
    box.style.setProperty('--sinty-columns', 'minmax(0, 1fr) ' + (v === 'trash' || v === 'links' ? '180px 130px' : '90px 140px') + ' 40px');
    const head = el('div', 'head');
    head.setAttribute('role', 'row');
    cols.forEach((c, i) => {
      const cell = keys[i] && v !== 'recent' ? el('button', i ? 'wide' : '', c) : el('div', i ? 'wide' : '', c);
      cell.setAttribute('role', 'columnheader');
      if (keys[i] && v !== 'recent') {
        if (state.sort === keys[i]) cell.setAttribute('aria-sort', state.desc ? 'descending' : 'ascending');
        cell.addEventListener('click', () => setSort(keys[i], state.sort === keys[i] ? !state.desc : false));
      }
      head.append(cell);
    });
    if (v !== 'links' && v !== 'trash') head.lastChild.classList.remove('wide');
    box.append(head);
    for (const it of items) box.append(listRow(it));
    return box;
  }

  function flags(it) {
    const f = el('span', 'flags');
    if (it.starred) {
      const s = icon('starred-symbolic');
      s.classList.add('star');
      s.setAttribute('label', 'Starred');
      f.append(s);
    }
    if (it.shared) {
      const s = icon('insert-link-symbolic');
      s.classList.add('shared');
      s.setAttribute('label', 'Shared with a link');
      f.append(s);
    }
    return f;
  }

  function listRow(it) {
    const v = state.view;
    const row = el('div', 'item');
    row.setAttribute('role', 'row');
    row.tabIndex = -1;
    row.dataset.path = it.path;
    const [kind, label] = kindOf(it);
    const name = el('div', 'namecell');
    name.setAttribute('role', 'gridcell');
    name.append(icon((kind === 'application-pdf' ? 'x-office-document' : kind) + '-symbolic'));
    const text = el('div');
    const title = el('span', 'name', it.name);
    title.title = it.name;
    const sub = el('span', 'sub');
    if (v === 'trash') sub.textContent = 'From ' + (parent(it.from) || 'Files') + ', ' + when(it.deleted);
    else if (v === 'links') sub.textContent = expiry(it);
    else sub.textContent = (it.dir ? 'Folder' : size(it.size)) + ', ' + when(it.modified);
    text.append(title, sub);
    name.append(text, flags(it));
    const c2 = el('div', 'dim wide');
    const c3 = el('div', 'dim wide');
    if (v === 'trash') {
      c2.textContent = parent(it.from) || 'Files';
      c3.textContent = when(it.deleted);
    } else if (v === 'links') {
      c2.textContent = parent(it.path) || 'Files';
      c3.textContent = expiry(it);
    } else {
      c2.textContent = it.dir ? '' : size(it.size);
      c3.textContent = when(it.modified);
      if (v === 'recent' || v === 'starred' || v === 'search') c2.textContent = it.dir ? label : size(it.size);
    }
    c2.setAttribute('role', 'gridcell');
    c3.setAttribute('role', 'gridcell');
    const end = el('div');
    end.setAttribute('role', 'gridcell');
    const more = el('button', 'rowmenu');
    more.type = 'button';
    more.setAttribute('aria-label', 'More for ' + it.name);
    more.append(icon('view-more-symbolic'));
    more.addEventListener('click', (e) => {
      e.stopPropagation();
      if (!state.selected.has(it.path)) select(it.path, false, false);
      contextMenu(it, more);
    });
    end.append(more);
    row.append(name, c2, c3, end);
    wire(row, it);
    return row;
  }

  function gridView(items) {
    const box = el('div', 'sinty-tile-grid');
    box.setAttribute('role', 'grid');
    box.setAttribute('aria-multiselectable', 'true');
    for (const it of items) {
      const tile = el('div', 'item');
      tile.setAttribute('role', 'gridcell');
      tile.tabIndex = -1;
      tile.dataset.path = it.path;
      const thumb = el('div', 'thumb');
      const [kind] = kindOf(it);
      thumb.append(icon(kind, true));
      if (!it.dir && isImage(it.name)) thumbnail(thumb, it);
      const badges = el('div', 'tile-badges');
      badges.append(flags(it));
      const name = el('span', 'name', it.name);
      name.title = it.name;
      tile.append(badges, thumb, name);
      wire(tile, it);
      box.append(tile);
    }
    return box;
  }

  const thumbObserver = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (!e.isIntersecting) continue;
      thumbObserver.unobserve(e.target);
      e.target.loadThumb();
    }
  }, { rootMargin: '200px' });

  function thumbnail(box, it) {
    const key = it.path + it.etag;
    box.loadThumb = async () => {
      let url = state.thumbs.get(key);
      if (!url) {
        if (it.size > 20e6) return;
        const r = await call('GET', '/api/v1/preview/' + enc(it.path));
        if (!r.ok) return;
        url = URL.createObjectURL(await r.blob());
        state.thumbs.set(key, url);
      }
      const img = el('img');
      img.alt = '';
      img.src = url;
      img.decode().then(() => box.replaceChildren(img), () => {});
    };
    thumbObserver.observe(box);
  }

  function expiry(it) {
    if (!it.expires) return 'Never';
    const days = Math.ceil((new Date(it.expires) - Date.now()) / 86400000);
    return days <= 1 ? 'Within a day' : 'In ' + days + ' days';
  }

  function wire(node, it) {
    node.addEventListener('click', (e) => {
      if (e.target.closest('.rowmenu')) return;
      if (e.shiftKey) range(it.path, e.ctrlKey || e.metaKey);
      else select(it.path, e.ctrlKey || e.metaKey, true);
    });
    node.addEventListener('dblclick', () => activate(it));
    node.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      if (!state.selected.has(it.path)) select(it.path, false, false);
      contextMenu(it, null, { x: e.clientX, y: e.clientY });
    });
    node.addEventListener('keydown', (e) => keyOnItem(e, it));
    if (state.view === 'files' || state.view === 'starred' || state.view === 'recent' || state.view === 'search') {
      node.draggable = true;
      node.addEventListener('dragstart', (e) => {
        if (!state.selected.has(it.path)) select(it.path, false, false);
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('application/x-sinty-cloud', JSON.stringify([...state.selected]));
      });
      if (it.dir) dropTarget(node, it.path);
    }
  }

  let lastTap = 0;
  function select(p, toggle, viaPointer) {
    if (viaPointer && window.matchMedia('(pointer: coarse)').matches && !toggle && !state.selected.size) {
      const it = state.items.find((i) => i.path === p);
      if (it) { activate(it); return; }
    }
    if (toggle) {
      if (state.selected.has(p)) state.selected.delete(p); else state.selected.add(p);
    } else {
      const now = Date.now();
      if (viaPointer && state.selected.size === 1 && state.selected.has(p) && now - lastTap < 400) return;
      lastTap = now;
      state.selected.clear();
      state.selected.add(p);
    }
    state.anchor = p;
    syncSelection();
  }

  function range(p, add) {
    const list = state.shown.map((i) => i.path);
    const a = list.indexOf(state.anchor ?? p);
    const b = list.indexOf(p);
    if (!add) state.selected.clear();
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) state.selected.add(list[i]);
    syncSelection();
  }

  function syncSelection() {
    for (const n of $('view').querySelectorAll('.item')) {
      const on = state.selected.has(n.dataset.path);
      n.setAttribute('aria-selected', String(on));
    }
    chrome();
  }

  function focusItem(p) {
    const n = $('view').querySelector('.item[data-path="' + CSS.escape(p) + '"]');
    if (n) { n.focus(); n.scrollIntoView({ block: 'nearest' }); }
  }

  function keyOnItem(e, it) {
    const list = state.shown;
    const at = list.findIndex((i) => i.path === it.path);
    const grid = state.mode === 'grid' && $('view').querySelector('.sinty-tile-grid');
    const per = grid ? Math.max(1, Math.round(grid.clientWidth / 124)) : 1;
    const move = (to) => {
      const next = list[Math.max(0, Math.min(list.length - 1, to))];
      if (!next) return;
      e.preventDefault();
      if (e.shiftKey) range(next.path, false); else select(next.path, false, false);
      focusItem(next.path);
    };
    if (e.key === 'ArrowDown') move(at + per);
    else if (e.key === 'ArrowUp') move(at - per);
    else if (e.key === 'ArrowRight' && grid) move(at + 1);
    else if (e.key === 'ArrowLeft' && grid) move(at - 1);
    else if (e.key === 'Home') move(0);
    else if (e.key === 'End') move(list.length - 1);
    else if (e.key === 'Enter') { e.preventDefault(); activate(it); }
    else if (e.key === ' ' && !it.dir && state.view !== 'trash') { e.preventDefault(); preview(it); }
    else if (e.key === 'F2' && state.view === 'files') { e.preventDefault(); rename(it); }
    else if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) { e.preventDefault(); contextMenu(it, e.currentTarget); }
  }

  function activate(it) {
    if (it.trash) return;
    if (it.dir) { openDir(it.path); return; }
    preview(it);
  }

  function selectedItems() {
    return [...state.selected].map((p) => state.items.find((i) => i.path === p)).filter(Boolean);
  }

  function contextMenu(it, anchor, point) {
    const many = state.selected.size > 1;
    const items = selectedItems();
    const v = state.view;
    let entries;
    if (v === 'trash') {
      entries = [
        { label: 'Restore', icon: 'edit-undo-symbolic', run: () => restore(items) },
        null,
        { label: 'Delete Forever', icon: 'edit-delete-symbolic', destructive: true, run: () => purge(items) },
      ];
    } else if (v === 'links') {
      entries = [
        { label: 'Copy Link', icon: 'edit-copy-symbolic', disabled: many, run: () => copyText(location.origin + '/s/' + it.token, 'Link copied') },
        { label: 'Show in Folder', icon: 'folder-symbolic', disabled: many, run: () => reveal(it.path) },
        null,
        { label: many ? 'Remove Links' : 'Remove Link', icon: 'list-remove-symbolic', destructive: true, run: () => unshare(items) },
      ];
    } else {
      const allStarred = items.every((i) => i.starred);
      entries = [
        { label: it.dir ? 'Open' : 'Preview', icon: it.dir ? 'folder-symbolic' : 'document-open-symbolic', disabled: many, run: () => activate(it), accel: it.dir ? 'Enter' : 'Space' },
        { label: 'Download', icon: 'folder-download-symbolic', disabled: items.some((i) => i.dir), run: () => downloadAll(items) },
        v !== 'files' ? { label: 'Show in Folder', icon: 'folder-symbolic', disabled: many, run: () => reveal(it.path) } : undefined,
        null,
        { label: 'Share Link…', icon: 'insert-link-symbolic', disabled: many || it.dir, run: () => share(it) },
        { label: allStarred ? 'Remove Star' : 'Star', icon: allStarred ? 'non-starred-symbolic' : 'starred-symbolic', run: () => star(items, !allStarred) },
        null,
        { label: 'Rename…', icon: 'document-edit-symbolic', disabled: many, run: () => rename(it), accel: 'F2' },
        { label: 'Move To…', icon: 'go-up-symbolic', run: () => pick(items, 'move') },
        { label: 'Copy To…', icon: 'edit-copy-symbolic', run: () => pick(items, 'copy') },
        { label: 'Make a Copy', icon: 'edit-copy-symbolic', disabled: many, run: () => duplicate(it) },
        null,
        { label: 'Move to Trash', icon: 'user-trash-symbolic', destructive: true, run: () => trash(items), accel: 'Delete' },
      ].filter((x) => x !== undefined);
    }
    SintyUI.menu(entries).open(anchor, point);
  }

  async function copyText(text, done) {
    try {
      await navigator.clipboard.writeText(text);
      toast(done);
    } catch {
      toast('Copy the link from the box.');
    }
  }

  function reveal(p) {
    const dir = parent(p);
    go('files', dir);
    const wait = setInterval(() => {
      if (state.view === 'files' && state.items.some((i) => i.path === p)) {
        clearInterval(wait);
        select(p, false, false);
        focusItem(p);
      }
    }, 100);
    setTimeout(() => clearInterval(wait), 3000);
  }

  function download(p) {
    const a = el('a');
    a.href = '/api/v1/files/' + enc(p);
    a.download = base(p);
    fetch(a.href, { credentials: 'same-origin', headers: { 'X-Sinty-Request': '1' } }).then(async (r) => {
      if (!r.ok) { toast(await why(r)); return; }
      const url = URL.createObjectURL(await r.blob());
      a.href = url;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 30000);
    });
  }
  function downloadAll(items) {
    items.filter((i) => !i.dir).forEach((i, n) => setTimeout(() => download(i.path), n * 300));
  }

  async function trash(items) {
    if (!items.length) return;
    const done = [];
    for (const it of items) {
      const r = await call('POST', '/api/v1/trash/' + enc(it.path));
      if (r.ok) done.push(await r.json());
      else { toast(it.name + ': ' + (await why(r))); break; }
    }
    state.selected.clear();
    if (done.length) {
      toast(done.length === 1 ? '“' + done[0].name + '” moved to the trash' : done.length + ' items moved to the trash', {
        action: 'Undo',
        onaction: async () => {
          for (const d of done) await call('POST', '/api/v1/restore/' + d.id);
          load();
        },
        timeout: 7000,
      });
    }
    load();
  }

  async function restore(items) {
    let n = 0;
    for (const it of items) {
      const r = await call('POST', '/api/v1/restore/' + it.id);
      if (r.ok) n++; else toast(it.name + ': ' + (await why(r)));
    }
    state.selected.clear();
    if (n) toast(n === 1 ? 'Restored to its folder' : n + ' items restored');
    load();
  }

  function confirm(heading, text, ok, cb) {
    const d = $('confirm-dialog');
    d.setAttribute('heading', heading);
    $('confirm-text').textContent = text;
    $('confirm-ok').textContent = ok;
    $('confirm-ok').onclick = () => { d.close(); cb(); };
    d.show();
  }

  function purge(items) {
    const n = items.length;
    confirm(n === 1 ? 'Delete “' + items[0].name + '” Forever?' : 'Delete ' + n + ' Items Forever?', 'This cannot be undone.', 'Delete Forever', async () => {
      for (const it of items) await call('DELETE', '/api/v1/trash/' + it.id);
      state.selected.clear();
      toast(n === 1 ? 'Deleted for good' : n + ' items deleted for good');
      load();
    });
  }

  async function unshare(items) {
    for (const it of items) await call('DELETE', '/api/v1/links/' + it.token);
    state.selected.clear();
    toast(items.length === 1 ? 'Link removed. It no longer works.' : 'Links removed. They no longer work.');
    load();
  }

  async function star(items, on) {
    for (const it of items) await call(on ? 'PUT' : 'DELETE', '/api/v1/stars/' + enc(it.path));
    toast(on ? (items.length === 1 ? 'Starred' : items.length + ' items starred') : 'Star removed');
    load();
  }

  async function duplicate(it) {
    const r = await call('POST', '/api/v1/copy', undefined, { from: it.path, to: it.path });
    if (!r.ok) { toast(await why(r)); return; }
    const made = await r.json();
    toast('Copied as “' + base(made.path) + '”');
    await load();
    select(made.path, false, false);
  }

  function nameDialog(heading, ok, value, check, run) {
    const d = $('name-dialog');
    d.setAttribute('heading', heading);
    $('name-ok').textContent = ok;
    const entry = $('name-entry');
    entry.value = value;
    $('name-error').textContent = '';
    const submit = async () => {
      const name = entry.value.trim();
      const problem = !name ? 'Type a name.' : name.includes('/') ? 'A name cannot contain “/”.' : name.startsWith('.sinty-') ? 'That name is reserved.' : check(name);
      if (problem) { $('name-error').textContent = problem; return; }
      const err = await run(name);
      if (err) $('name-error').textContent = err; else d.close();
    };
    $('name-ok').onclick = submit;
    entry.onkeydown = (e) => { if (e.key === 'Enter') submit(); };
    d.show();
    requestAnimationFrame(() => {
      entry.focus();
      const dot = value.lastIndexOf('.');
      entry.input.setSelectionRange(0, dot > 0 ? dot : value.length);
    });
  }

  const taken = (name, except) => state.items.some((i) => i.name.toLowerCase() === name.toLowerCase() && i.name !== except);

  function newFolder() {
    nameDialog('New Folder', 'Create', '', (n) => (taken(n) ? 'Something with that name is already here.' : ''), async (name) => {
      const r = await call('POST', '/api/v1/folders/' + enc(join(state.dir, name)));
      if (!r.ok) return why(r);
      await load();
      select(join(state.dir, name), false, false);
      focusItem(join(state.dir, name));
      return '';
    });
  }

  function rename(it) {
    nameDialog('Rename', 'Rename', it.name, (n) => (n !== it.name && taken(n, it.name) ? 'Something with that name is already here.' : ''), async (name) => {
      if (name === it.name) return '';
      const to = join(parent(it.path), name);
      const r = await call('POST', '/api/v1/move', undefined, { from: it.path, to });
      if (!r.ok) return why(r);
      await load();
      select(to, false, false);
      return '';
    });
  }

  async function moveInto(paths, dir) {
    let n = 0;
    for (const p of paths) {
      if (parent(p) === dir || dir === p || dir.startsWith(p + '/')) continue;
      const r = await call('POST', '/api/v1/move', undefined, { from: p, to: join(dir, base(p)) });
      if (r.ok) n++; else { toast(base(p) + ': ' + (await why(r))); break; }
    }
    state.selected.clear();
    if (n) toast((n === 1 ? 'Moved' : n + ' items moved') + ' to “' + (dir ? base(dir) : 'Files') + '”', { action: 'Open', onaction: () => openDir(dir) });
    load();
  }

  async function copyInto(paths, dir) {
    let n = 0;
    for (const p of paths) {
      const r = await call('POST', '/api/v1/copy', undefined, { from: p, to: join(dir, base(p)) });
      if (r.ok) n++; else { toast(base(p) + ': ' + (await why(r))); break; }
    }
    state.selected.clear();
    if (n) toast((n === 1 ? 'Copied' : n + ' items copied') + ' to “' + (dir ? base(dir) : 'Files') + '”', { action: 'Open', onaction: () => openDir(dir) });
    load();
  }

  function dropTarget(node, dir) {
    let depth = 0;
    node.addEventListener('dragenter', (e) => {
      if (!e.dataTransfer.types.includes('application/x-sinty-cloud') && !e.dataTransfer.types.includes('Files')) return;
      depth++;
      node.classList.add('drop-target');
      e.preventDefault();
    });
    node.addEventListener('dragover', (e) => {
      if (!node.classList.contains('drop-target')) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = e.dataTransfer.types.includes('Files') ? 'copy' : 'move';
    });
    node.addEventListener('dragleave', () => {
      if (--depth <= 0) { depth = 0; node.classList.remove('drop-target'); }
    });
    node.addEventListener('drop', (e) => {
      depth = 0;
      node.classList.remove('drop-target');
      hideDrop();
      e.preventDefault();
      e.stopPropagation();
      const raw = e.dataTransfer.getData('application/x-sinty-cloud');
      if (raw) moveInto(JSON.parse(raw), dir);
      else if (e.dataTransfer.items.length) dropFiles(e.dataTransfer, dir);
    });
  }

  let pickState = null;
  async function pick(items, kind) {
    pickState = { items, kind, dir: state.view === 'files' ? state.dir : '' };
    const d = $('pick-dialog');
    d.setAttribute('heading', (kind === 'move' ? 'Move ' : 'Copy ') + (items.length === 1 ? '“' + items[0].name + '”' : items.length + ' Items'));
    $('pick-ok').textContent = kind === 'move' ? 'Move Here' : 'Copy Here';
    d.show();
    pickOpen(pickState.dir);
  }

  async function pickOpen(dir) {
    pickState.dir = dir;
    $('pick-where').textContent = 'To: ' + (dir ? 'Files / ' + dir.split('/').join(' / ') : 'Files');
    const list = $('pick-list');
    list.replaceChildren(el('sinty-spinner'));
    const r = await call('GET', '/api/v1/files/' + enc(dir));
    const entries = r.ok ? (await r.json()).entries.filter((e) => e.dir) : [];
    list.replaceChildren();
    if (dir) {
      const up = el('button', 'up');
      up.type = 'button';
      up.append(icon('go-up-symbolic'), el('span', '', parent(dir) ? base(parent(dir)) : 'Files'));
      up.addEventListener('click', () => pickOpen(parent(dir)));
      list.append(up);
    }
    const moving = pickState.items.map((i) => i.path);
    for (const e of entries) {
      const p = join(dir, e.name);
      const b = el('button');
      b.type = 'button';
      b.append(icon('folder-symbolic'), el('span', '', e.name));
      if (pickState.kind === 'move' && moving.includes(p)) b.disabled = true;
      b.addEventListener('click', () => pickOpen(p));
      list.append(b);
    }
    if (!entries.length) list.append(el('span', 'loc', ' No folders here'));
    const same = pickState.kind === 'move' && pickState.items.every((i) => parent(i.path) === dir);
    $('pick-ok').toggleAttribute('disabled', same || moving.some((p) => dir === p || dir.startsWith(p + '/')));
  }

  function share(it) {
    const d = $('share-dialog');
    d.setAttribute('heading', 'Share “' + it.name + '”');
    const existing = state.links?.find((l) => l.path === it.path);
    const show = (l) => {
      $('share-box').hidden = !l;
      $('share-days').hidden = !!l;
      $('share-make').hidden = !!l;
      $('share-copy').hidden = !l;
      $('share-remove').hidden = !l;
      $('share-when').hidden = !l;
      if (!l) return;
      $('share-url').value = location.origin + '/s/' + l.token;
      $('share-when').textContent = l.expires ? 'The link stops working on ' + yearFmt.format(new Date(l.expires)) + '.' : 'The link works until you remove it.';
      $('share-copy').onclick = () => copyText($('share-url').value, 'Link copied');
      $('share-remove').onclick = async () => {
        await call('DELETE', '/api/v1/links/' + l.token);
        d.close();
        toast('Link removed. It no longer works.');
        load();
      };
    };
    show(existing);
    $('share-make').onclick = async () => {
      const r = await call('POST', '/api/v1/links', undefined, { path: it.path, days: parseInt($('share-for').value, 10) });
      if (!r.ok) { toast(await why(r)); return; }
      const l = await r.json();
      show(l);
      copyText($('share-url').value, 'Link created and copied');
      load();
    };
    d.show();
  }

  let viewing = null;
  async function preview(it) {
    viewing = it;
    const v = $('viewer');
    v.hidden = false;
    $('viewer-title').textContent = it.name;
    $('viewer-star').setAttribute('icon', it.starred ? 'starred-symbolic' : 'non-starred-symbolic');
    $('viewer-star').setAttribute('label', it.starred ? 'Remove Star' : 'Star');
    const files = state.shown.filter((i) => !i.dir);
    const at = files.findIndex((i) => i.path === it.path);
    $('viewer-prev').hidden = at <= 0;
    $('viewer-next').hidden = at < 0 || at >= files.length - 1;
    const stage = $('viewer-stage');
    stage.replaceChildren(el('sinty-spinner'));
    $('viewer-close').focus();
    if (isImage(it.name) || isText(it.name)) {
      const r = await call('GET', '/api/v1/preview/' + enc(it.path));
      if (viewing !== it) return;
      if (r.ok) {
        const type = r.headers.get('Content-Type') || '';
        if (type.startsWith('image/')) {
          const img = el('img');
          img.alt = it.name;
          img.src = URL.createObjectURL(await r.blob());
          stage.replaceChildren(img);
        } else {
          stage.replaceChildren(el('pre', '', await r.text()));
        }
        return;
      }
    }
    const [kind, label] = kindOf(it);
    const page = el('sinty-status-page');
    page.setAttribute('icon', kind);
    page.setAttribute('heading', it.name);
    page.setAttribute('description', label + ', ' + size(it.size) + '. There is no preview for this file.');
    const b = el('sinty-button', '', 'Download');
    b.setAttribute('variant', 'suggested');
    b.setAttribute('pill', '');
    b.addEventListener('click', () => download(it.path));
    page.append(b);
    stage.replaceChildren(page);
  }
  function closeViewer() {
    const img = $('viewer-stage').querySelector('img');
    if (img) URL.revokeObjectURL(img.src);
    $('viewer').hidden = true;
    const it = viewing;
    viewing = null;
    if (it) focusItem(it.path);
  }
  function stepViewer(d) {
    const files = state.shown.filter((i) => !i.dir);
    const at = files.findIndex((i) => i.path === viewing.path);
    const next = files[at + d];
    if (next) {
      select(next.path, false, false);
      preview(next);
    }
  }

  function freeName(name, names) {
    if (!names.has(name.toLowerCase())) return name;
    const dot = name.lastIndexOf('.');
    const stem = dot > 0 ? name.slice(0, dot) : name;
    const ext = dot > 0 ? name.slice(dot) : '';
    for (let i = 2; ; i++) {
      const c = stem + ' (' + i + ')' + ext;
      if (!names.has(c.toLowerCase())) return c;
    }
  }

  function askClash(clashes) {
    return new Promise((resolve) => {
      const d = $('clash-dialog');
      $('clash-text').textContent = clashes.length === 1
        ? '“' + clashes[0] + '” is already in this folder. Replace it, or keep both?'
        : clashes.length + ' of these files are already in this folder. Replace them, or keep both?';
      const done = (v) => { d.close(); resolve(v); };
      $('clash-skip').onclick = () => done('skip');
      $('clash-both').onclick = () => done('both');
      $('clash-replace').onclick = () => done('replace');
      d.addEventListener('close', () => resolve('skip'), { once: true });
      d.show();
    });
  }

  function put(path, file, onProgress) {
    return new Promise((resolve) => {
      const x = new XMLHttpRequest();
      x.open('PUT', '/api/v1/files/' + enc(path));
      x.setRequestHeader('X-Sinty-Request', '1');
      x.withCredentials = true;
      x.upload.onprogress = (e) => onProgress(e.loaded);
      x.onload = () => {
        if (x.status === 401) location.assign('/login?back=' + encodeURIComponent(location.pathname + location.hash));
        let err = {};
        try { err = JSON.parse(x.responseText); } catch { /* not json */ }
        resolve({ ok: x.status >= 200 && x.status < 300, status: x.status, error: err.error });
      };
      x.onerror = () => resolve({ ok: false, status: 0 });
      x.send(file);
    });
  }

  async function upload(list, dir) {
    if (!list.length) return;
    const names = new Set(state.view === 'files' && dir === state.dir ? state.items.map((i) => i.name.toLowerCase()) : []);
    const top = (f) => f.rel.split('/')[0];
    const clashes = [...new Set(list.map(top))].filter((n) => names.has(n.toLowerCase()));
    let policy = 'replace';
    if (clashes.length) policy = await askClash(clashes);
    const rename = new Map();
    const work = list.filter((f) => {
      const t = top(f);
      if (!names.has(t.toLowerCase())) return true;
      if (policy === 'skip') return false;
      if (policy === 'both' && !rename.has(t)) {
        const n = freeName(t, names);
        names.add(n.toLowerCase());
        rename.set(t, n);
      }
      return true;
    });
    if (!work.length) return;
    const total = work.reduce((s, f) => s + f.file.size, 0) || 1;
    let sent = 0;
    const t = toast('Uploading ' + (work.length === 1 ? '“' + work[0].file.name + '”' : work.length + ' files'), { progress: 0, timeout: 0 });
    let failed = null;
    let done = 0;
    for (const f of work) {
      const parts = f.rel.split('/');
      if (rename.has(parts[0])) parts[0] = rename.get(parts[0]);
      const r = await put(join(dir, parts.join('/')), f.file, (n) => { t.progress = (sent + n) / total; });
      sent += f.file.size;
      t.progress = sent / total;
      if (!r.ok) {
        failed = r.status === 507 ? 'There is no room left for “' + f.file.name + '”.' : '“' + f.file.name + '” could not be uploaded.';
        break;
      }
      done++;
    }
    t.dismiss();
    if (failed) toast(failed, { timeout: 8000 });
    else toast(done === 1 ? 'Uploaded “' + work[0].file.name + '”' : done + ' files uploaded', dir !== state.dir || state.view !== 'files' ? { action: 'Open', onaction: () => openDir(dir) } : undefined);
    load();
  }

  async function dropFiles(dt, dir) {
    const list = [];
    const walk = (entry, prefix) => new Promise((resolve) => {
      if (entry.isFile) {
        entry.file((file) => { list.push({ file, rel: prefix + file.name }); resolve(); }, resolve);
      } else if (entry.isDirectory) {
        const reader = entry.createReader();
        const all = [];
        const next = () => reader.readEntries(async (batch) => {
          if (!batch.length) {
            for (const e of all) await walk(e, prefix + entry.name + '/');
            resolve();
          } else {
            all.push(...batch);
            next();
          }
        }, resolve);
        next();
      } else resolve();
    });
    const entries = [...dt.items].map((i) => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
    if (entries.length) for (const e of entries) await walk(e, '');
    else for (const file of dt.files) list.push({ file, rel: file.name });
    upload(list, dir);
  }

  function setSort(key, desc) {
    state.sort = key;
    state.desc = desc;
    store.set('sort', key);
    store.set('desc', desc ? '1' : '');
    render();
  }

  $('sort').addEventListener('click', () => {
    const opt = (label, key) => ({ label, checkable: true, checked: state.sort === key, run: () => setSort(key, state.desc) });
    SintyUI.menu([
      opt('Name', 'name'), opt('Size', 'size'), opt('Modified', 'modified'),
      null,
      { label: 'Ascending', checkable: true, checked: !state.desc, run: () => setSort(state.sort, false) },
      { label: 'Descending', checkable: true, checked: state.desc, run: () => setSort(state.sort, true) },
    ]).open($('sort'));
  });

  $('mode').addEventListener('change', () => {
    state.mode = $('mode').value;
    store.set('mode', state.mode);
    render();
  });

  $('upload').addEventListener('click', () => {
    SintyUI.menu([
      { label: 'Upload Files…', icon: 'text-x-generic-symbolic', run: () => $('picker').click() },
      { label: 'Upload a Folder…', icon: 'folder-symbolic', run: () => $('folder-picker').click() },
    ]).open($('upload'));
  });
  $('picker').addEventListener('change', () => {
    upload([...$('picker').files].map((file) => ({ file, rel: file.name })), state.dir);
    $('picker').value = '';
  });
  $('folder-picker').addEventListener('change', () => {
    upload([...$('folder-picker').files].map((file) => ({ file, rel: file.webkitRelativePath || file.name })), state.dir);
    $('folder-picker').value = '';
  });
  $('new-folder').addEventListener('click', newFolder);
  $('name-cancel').addEventListener('click', () => $('name-dialog').close());
  $('confirm-cancel').addEventListener('click', () => $('confirm-dialog').close());
  $('pick-cancel').addEventListener('click', () => $('pick-dialog').close());
  $('pick-ok').addEventListener('click', () => {
    $('pick-dialog').close();
    const paths = pickState.items.map((i) => i.path);
    if (pickState.kind === 'move') moveInto(paths, pickState.dir); else copyInto(paths, pickState.dir);
  });
  $('pick-new').addEventListener('click', () => {
    const name = 'New Folder';
    call('POST', '/api/v1/folders/' + enc(join(pickState.dir, name))).then((r) => (r.ok || r.status === 409) && pickOpen(join(pickState.dir, name)));
  });
  $('share-close').addEventListener('click', () => $('share-dialog').close());
  $('empty-trash').addEventListener('click', () => {
    confirm('Empty the Trash?', 'All ' + state.items.length + ' items in the trash are deleted for good. This cannot be undone.', 'Empty Trash', async () => {
      const r = await call('DELETE', '/api/v1/trash');
      toast(r.ok ? 'Trash emptied' : await why(r));
      load();
    });
  });

  for (const b of document.querySelectorAll('.sel')) {
    b.addEventListener('click', (e) => {
      const items = selectedItems();
      const act = b.dataset.act;
      if (act === 'clear') { state.selected.clear(); syncSelection(); return; }
      if (act === 'download') downloadAll(items);
      else if (act === 'move' || act === 'copy') pick(items, act);
      else if (act === 'star') star(items, !items.every((i) => i.starred));
      else if (act === 'trash') trash(items);
      else if (act === 'restore') restore(items);
      else if (act === 'purge') purge(items);
      else if (act === 'unshare') unshare(items);
      e.stopPropagation();
    });
  }

  for (const row of document.querySelectorAll('sinty-sidebar-row[data-view]')) {
    row.addEventListener('click', () => go(row.dataset.view));
    if (row.dataset.view === 'files') dropTarget(row, '');
  }

  let searchTimer = 0;
  $('search').addEventListener('input', () => {
    clearTimeout(searchTimer);
    const q = $('search').value.trim();
    searchTimer = setTimeout(() => {
      if (q) {
        history.replaceState(null, '', '#search/' + encodeURIComponent(q));
        state.view = 'search';
        state.query = q;
        state.selected.clear();
        load();
      } else if (state.view === 'search') go('files');
    }, 250);
  });

  $('path').addEventListener('navigate', (e) => { if (e.detail.target !== null) openDir(e.detail.target); });
  $('account').addEventListener('click', () => {
    SintyUI.menu([
      { label: 'Signed in as ' + (state.me ? state.me.user : ''), disabled: true },
      null,
      { label: 'Sign Out', icon: 'system-log-out-symbolic', run: async () => {
        await call('POST', '/logout');
        location.assign('/login');
      } },
    ]).open($('account'));
  });
  $('account').addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); $('account').click(); } });

  $('viewer-close').addEventListener('click', closeViewer);
  $('viewer-prev').addEventListener('click', () => stepViewer(-1));
  $('viewer-next').addEventListener('click', () => stepViewer(1));
  $('viewer-download').addEventListener('click', () => viewing && download(viewing.path));
  $('viewer-share').addEventListener('click', () => viewing && share(viewing));
  $('viewer-star').addEventListener('click', async () => {
    if (!viewing) return;
    const on = !viewing.starred;
    await call(on ? 'PUT' : 'DELETE', '/api/v1/stars/' + enc(viewing.path));
    viewing.starred = on;
    $('viewer-star').setAttribute('icon', on ? 'starred-symbolic' : 'non-starred-symbolic');
    $('viewer-star').setAttribute('label', on ? 'Remove Star' : 'Star');
    load();
  });
  $('viewer').addEventListener('keydown', (e) => {
    if (e.key === 'Escape' || e.key === ' ') { e.preventDefault(); closeViewer(); }
    else if (e.key === 'ArrowLeft') stepViewer(-1);
    else if (e.key === 'ArrowRight') stepViewer(1);
  });

  let dragDepth = 0;
  const hideDrop = () => { dragDepth = 0; $('drop').hidden = true; };
  document.addEventListener('dragenter', (e) => {
    if (state.view !== 'files' || !e.dataTransfer.types.includes('Files')) return;
    dragDepth++;
    $('drop').hidden = false;
  });
  document.addEventListener('dragleave', () => { if (--dragDepth <= 0) hideDrop(); });
  document.addEventListener('dragover', (e) => {
    if (state.view === 'files' && e.dataTransfer.types.includes('Files')) e.preventDefault();
  });
  document.addEventListener('drop', (e) => {
    if (!e.dataTransfer.types.includes('Files')) return;
    e.preventDefault();
    hideDrop();
    if (state.view === 'files') dropFiles(e.dataTransfer, state.dir);
  });

  $('view').addEventListener('click', (e) => {
    if (e.target === $('view') || e.target.classList.contains('sinty-tile-grid')) { state.selected.clear(); syncSelection(); }
  });

  document.addEventListener('keydown', (e) => {
    if (!$('viewer').hidden || e.target.closest('sinty-dialog, sinty-entry, sinty-search, input')) return;
    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.key === 'f') { e.preventDefault(); $('search').focus(); }
    else if (mod && e.key === 'a' && state.shown?.length) {
      e.preventDefault();
      for (const i of state.shown) state.selected.add(i.path);
      syncSelection();
    } else if (e.key === 'Escape' && state.selected.size) { state.selected.clear(); syncSelection(); }
    else if (e.key === 'Delete' && state.selected.size) {
      e.preventDefault();
      if (state.view === 'trash') purge(selectedItems());
      else if (state.view === 'links') unshare(selectedItems());
      else trash(selectedItems());
    } else if ((e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) && state.view === 'files' && state.dir) {
      e.preventDefault();
      openDir(parent(state.dir));
    } else if (mod && e.shiftKey && e.key === 'N' && state.view === 'files') {
      e.preventDefault();
      newFolder();
    } else if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && state.shown?.length && !e.target.closest('.item')) {
      e.preventDefault();
      const first = state.shown[e.key === 'ArrowDown' ? 0 : state.shown.length - 1];
      select(first.path, false, false);
      focusItem(first.path);
    }
  });

  const loadLinks = async () => {
    const r = await call('GET', '/api/v1/links');
    state.links = r.ok ? (await r.json()).links : [];
  };
  $('share-dialog').addEventListener('close', loadLinks);

  window.addEventListener('hashchange', route);
  loadLinks();
  if (!location.hash) history.replaceState(null, '', '#files');
  route();
})();

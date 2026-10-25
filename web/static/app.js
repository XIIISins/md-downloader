  const list = document.getElementById('list');
  const goBtn = document.getElementById('go');
  const logEl = document.getElementById('log');
  const msg = document.getElementById('msg');
  const selected = new Set();
  const rows = {};
  const activeTags = new Set();
  const excludedTags = new Set();
  const jobs = {}; // manga id -> job from the downloader's queue
  let allItems = [];

  // A series that is queued or running can't be queued again.
  function locked(id) {
    const j = jobs[id];
    return !!j && (j.state === 'queued' || j.state === 'running');
  }

  const FLAGS = { ko: '\u{1F1F0}\u{1F1F7}', ja: '\u{1F1EF}\u{1F1F5}', zh: '\u{1F1E8}\u{1F1F3}',
                  'zh-hk': '\u{1F1ED}\u{1F1F0}', en: '\u{1F1EC}\u{1F1E7}' };

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  function refresh() {
    document.getElementById('count').textContent = selected.size + ' selected';
    goBtn.disabled = selected.size === 0;
    goBtn.textContent = 'Queue ' + selected.size;
    for (const [id, r] of Object.entries(rows)) {
      const yes = selected.has(id);
      r.y.className = yes ? 'on-y' : '';
      r.n.className = yes ? '' : 'on-n';
      r.el.classList.toggle('locked', locked(id));
      r.cancel.hidden = !locked(id);
    }
  }

  function set(id, yes) {
    if (yes && !locked(id)) selected.add(id); else selected.delete(id);
    refresh();
  }

  function row(item) {
    const r = el('div', 'row');

    const img = el('img');
    img.loading = 'lazy';
    img.alt = '';
    img.src = '/cover/' + item.id;
    r.appendChild(img);

    const main = el('div', 'main');
    main.appendChild(el('div', 'name', (FLAGS[item.lang] || item.lang || '') + ' ' + item.title));
    if (item.tags && item.tags.length) {
      const tags = el('div', 'tags');
      for (const t of item.tags) {
        const chip = el('span', 'tag', t);
        chip.title = 'Click: include ' + t + ' / Shift-click: exclude';
        chip.onclick = (e) => e.shiftKey ? toggleExcluded(t) : toggleTag(t);
        tags.appendChild(chip);
      }
      main.appendChild(tags);
    }
    const desc = el('div', 'desc', item.description || 'No summary.');
    if ((item.description || '').length > 450) desc.classList.add('long');
    desc.onclick = () => desc.classList.toggle('open');
    main.appendChild(desc);
    r.appendChild(main);

    const side = el('div', 'side');
    const stats = el('div', 'stats');
    if (item.rating) stats.appendChild(el('span', '', '★ ' + item.rating.toFixed(2)));
    if (item.follows) stats.appendChild(el('span', '', '\u{1F516} ' + item.follows.toLocaleString()));
    side.appendChild(stats);
    if (item.status) side.appendChild(el('div', 'status ' + item.status, item.status));
    const meta = [item.year ? 'Since ' + item.year : '', item.demographic].filter(Boolean).join(' · ');
    if (meta) side.appendChild(el('div', 'meta', meta));

    const badge = el('span', 'badge');
    badge.hidden = true;
    const detail = el('div', 'detail');
    const cancel = el('button', 'cancel', 'Cancel');
    cancel.hidden = true;
    cancel.onclick = () => post('/api/cancel/' + item.id);
    side.append(badge, detail, cancel);

    const yn = el('div', 'yn');
    const y = el('button', '', 'Y');
    const n = el('button', '', 'N');
    y.onclick = () => set(item.id, true);
    n.onclick = () => set(item.id, false);
    yn.append(y, n);
    side.appendChild(yn);
    r.appendChild(side);

    rows[item.id] = { el: r, item, y, n, badge, detail, cancel };
    return r;
  }

  // A tag is either included, excluded, or neither, never both.
  function toggleTag(t) {
    excludedTags.delete(t);
    if (activeTags.has(t)) activeTags.delete(t); else activeTags.add(t);
    applyFilter();
  }

  function toggleExcluded(t) {
    activeTags.delete(t);
    if (excludedTags.has(t)) excludedTags.delete(t); else excludedTags.add(t);
    applyFilter();
  }

  function visibleIDs() {
    return Object.entries(rows).filter(([, r]) => !r.el.hidden).map(([id]) => id);
  }

  // Rows must match the search text and carry every active tag.
  function applyFilter() {
    const q = document.getElementById('q').value.trim().toLowerCase();
    let shown = 0;
    for (const r of Object.values(rows)) {
      const okText = !q || r.item.title.toLowerCase().includes(q);
      const tags = r.item.tags || [];
      const okTags = [...activeTags].every(t => tags.includes(t));
      const okEx = ![...excludedTags].some(t => tags.includes(t));
      const demo = document.getElementById('demoSel').value;
      const okDemo = !demo || (r.item.demographic || 'none') === demo;
      // Titles with no known year are hidden while a year bound is set.
      const yFrom = parseInt(document.getElementById('yFrom').value, 10);
      const yTo = parseInt(document.getElementById('yTo').value, 10);
      const y = r.item.year || 0;
      const okYear = (isNaN(yFrom) && isNaN(yTo)) ||
        (y > 0 && (isNaN(yFrom) || y >= yFrom) && (isNaN(yTo) || y <= yTo));
      r.el.hidden = !(okText && okTags && okEx && okDemo && okYear);
      if (!r.el.hidden) shown++;
      for (const c of r.el.querySelectorAll('.tag')) {
        c.classList.toggle('on', activeTags.has(c.textContent));
        c.classList.toggle('ex', excludedTags.has(c.textContent));
      }
    }
    document.getElementById('shown').textContent = shown + ' of ' + allItems.length + ' shown';

    const box = document.getElementById('activeTags');
    box.replaceChildren();
    for (const t of activeTags) {
      const b = el('button', 'active-tag', t + ' ×');
      b.onclick = () => toggleTag(t);
      box.appendChild(b);
    }
    for (const t of excludedTags) {
      const b = el('button', 'active-tag ex', '− ' + t + ' ×');
      b.onclick = () => toggleExcluded(t);
      box.appendChild(b);
    }
    document.getElementById('clearTags').hidden = activeTags.size + excludedTags.size === 0;
  }

  // Missing values (0) always sort last, whichever direction is chosen.
  function byNumber(field, dir) {
    return (a, b) => {
      const va = a.item[field] || 0, vb = b.item[field] || 0;
      if (!va && !vb) return a.item.title.localeCompare(b.item.title);
      if (!va) return 1;
      if (!vb) return -1;
      return dir * (va - vb) || a.item.title.localeCompare(b.item.title);
    };
  }

  const SORTS = {
    title: (a, b) => a.item.title.localeCompare(b.item.title),
    titleDesc: (a, b) => b.item.title.localeCompare(a.item.title),
    rating: byNumber('rating', -1),
    follows: byNumber('follows', -1),
    yearNew: byNumber('year', -1),
    yearOld: byNumber('year', 1),
  };

  function applySort() {
    const sorted = Object.values(rows).sort(SORTS[document.getElementById('sortSel').value]);
    for (const r of sorted) list.appendChild(r.el);
  }

  function buildTagSelect(items) {
    const counts = {};
    for (const it of items) for (const t of it.tags || []) counts[t] = (counts[t] || 0) + 1;
    const sel = document.getElementById('tagSel');
    for (const t of Object.keys(counts).sort()) {
      const o = el('option', '', t + ' (' + counts[t] + ')');
      o.value = t;
      sel.appendChild(o);
    }
    const ex = document.getElementById('exSel');
    for (const t of Object.keys(counts).sort()) {
      const o = el('option', '', t + ' (' + counts[t] + ')');
      o.value = t;
      ex.appendChild(o);
    }
    sel.onchange = () => {
      if (sel.value) { excludedTags.delete(sel.value); activeTags.add(sel.value); }
      sel.value = '';
      applyFilter();
    };
    ex.onchange = () => {
      if (ex.value) { activeTags.delete(ex.value); excludedTags.add(ex.value); }
      ex.value = '';
      applyFilter();
    };

    const demoCounts = {};
    for (const it of items) { const d = it.demographic || 'none'; demoCounts[d] = (demoCounts[d] || 0) + 1; }
    const demo = document.getElementById('demoSel');
    for (const d of Object.keys(demoCounts).sort()) {
      const o = el('option', '', d + ' (' + demoCounts[d] + ')');
      o.value = d;
      demo.appendChild(o);
    }
    demo.onchange = applyFilter;
  }

  async function load() {
    const items = await (await fetch('/api/library')).json();
    msg.hidden = items.length > 0;
    if (!items.length) msg.textContent = 'Nothing in this list.';
    allItems = items;
    for (const it of items) list.appendChild(row(it));
    buildTagSelect(items);
    applyFilter();
    refresh();
    poll();
  }

  const banner = document.getElementById('banner');

  function showBanner(text) {
    banner.textContent = text || '';
    banner.hidden = !text;
  }

  async function post(path, body) {
    const res = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      let text = await res.text();
      try { text = JSON.parse(text).error || text; } catch (e) {}
      showBanner(text);
    }
    await pollOnce();
    return res;
  }

  // One round trip to the downloader's queue (through this server).
  async function pollOnce() {
    const res = await fetch('/api/queue');
    const p = await res.json();
    if (!res.ok) { showBanner(p.error || 'Downloader not reachable.'); return; }
    showBanner('');

    for (const id of Object.keys(jobs)) delete jobs[id];
    for (const j of p.jobs || []) jobs[j.id] = j;
    for (const [id, r] of Object.entries(rows)) {
      const j = jobs[id];
      r.badge.hidden = !j;
      if (j) {
        r.badge.textContent = j.state;
        r.badge.className = 'badge ' + j.state;
      }
      r.detail.textContent = j ? j.detail : '';
      if (locked(id)) selected.delete(id);
    }
    if (p.log && p.log.length) {
      logEl.style.display = 'block';
      logEl.textContent = p.log.slice(-40).join('\n');
      logEl.scrollTop = logEl.scrollHeight;
    }
    refresh();
  }

  async function poll() {
    try { await pollOnce(); } catch (e) { return; /* web app stopped; stop quietly */ }
    setTimeout(poll, 1500);
  }

  goBtn.onclick = async () => {
    const ids = [...selected].filter(id => !locked(id));
    if (!ids.length) return;
    const res = await post('/api/download', { ids });
    if (res.ok) {
      const r = await res.clone().json().catch(() => ({}));
      selected.clear();
      if (r.skipped && r.skipped.length) {
        showBanner(r.skipped.length + ' already queued, skipped.');
        setTimeout(() => { if (banner.textContent.endsWith('skipped.')) showBanner(''); }, 4000);
      }
      refresh();
    }
  };
  document.getElementById('clearDone').onclick = () => post('/api/clear');
  // Bulk Y/N only touches the rows currently shown by the filters.
  document.getElementById('allY').onclick = () => { visibleIDs().filter(id => !locked(id)).forEach(id => selected.add(id)); refresh(); };
  document.getElementById('allN').onclick = () => { visibleIDs().forEach(id => selected.delete(id)); refresh(); };
  document.getElementById('q').oninput = applyFilter;
  document.getElementById('yFrom').oninput = applyFilter;
  document.getElementById('yTo').oninput = applyFilter;
  document.getElementById('sortSel').onchange = applySort;
  document.getElementById('clearTags').onclick = () => { activeTags.clear(); excludedTags.clear(); applyFilter(); };

  load();

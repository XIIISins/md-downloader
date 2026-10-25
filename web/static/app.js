  const $ = (id) => document.getElementById(id);
  const list = $('list');
  const goBtn = $('go');
  const logEl = $('log');
  const msg = $('msg');
  const banner = $('banner');
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
    $('count').textContent = selected.size + ' selected';
    goBtn.disabled = selected.size === 0;
    goBtn.textContent = 'Queue ' + selected.size;
    for (const [id, r] of Object.entries(rows)) {
      const yes = selected.has(id);
      const lock = locked(id);
      r.y.setAttribute('aria-pressed', yes);
      r.n.setAttribute('aria-pressed', !yes);
      r.y.disabled = r.n.disabled = lock;
      r.el.classList.toggle('picked', yes);
      r.el.classList.toggle('locked', lock);
      r.cancel.hidden = !lock;
    }
  }

  function set(id, yes) {
    if (yes && !locked(id)) selected.add(id); else selected.delete(id);
    refresh();
  }

  function row(item) {
    const r = el('article', 'row');

    const img = el('img', 'cover');
    img.loading = 'lazy';
    img.alt = '';
    img.width = 148;
    img.height = 222;
    img.src = '/api/cover/' + item.id;
    r.appendChild(img);

    const main = el('div', 'main');
    const name = el('h2', 'name');
    const flag = FLAGS[item.lang] || item.lang;
    if (flag) {
      const f = el('span', 'lang', flag);
      f.title = item.lang;
      name.appendChild(f);
    }
    name.append(item.title);
    main.appendChild(name);

    if (item.tags && item.tags.length) {
      const tags = el('div', 'tags');
      for (const t of item.tags) {
        const chip = el('button', 'tag', t);
        chip.type = 'button';
        chip.title = 'Click to cycle include, exclude, off';
        chip.onclick = (e) => cycleTag(t, e.shiftKey);
        tags.appendChild(chip);
      }
      main.appendChild(tags);
    }

    const text = item.description || 'No summary.';
    const desc = el('p', 'desc', text);
    main.appendChild(desc);
    if (text.length > 300) {
      desc.classList.add('clamp');
      desc.id = 'desc-' + item.id;
      const more = el('button', 'link more', 'Show more');
      more.type = 'button';
      more.setAttribute('aria-expanded', 'false');
      more.setAttribute('aria-controls', desc.id);
      more.onclick = () => {
        const open = desc.classList.toggle('clamp') === false;
        more.textContent = open ? 'Show less' : 'Show more';
        more.setAttribute('aria-expanded', open);
      };
      main.appendChild(more);
    }
    r.appendChild(main);

    const side = el('div', 'side');
    const stats = el('div', 'stats');
    if (item.rating) {
      const s = el('span', '', '★ ' + item.rating.toFixed(2));
      s.title = 'Rating';
      stats.appendChild(s);
    }
    if (item.follows) {
      const s = el('span', '', '\u{1F516} ' + item.follows.toLocaleString());
      s.title = 'Follows';
      stats.appendChild(s);
    }
    if (stats.children.length) side.appendChild(stats);
    if (item.status) side.appendChild(el('div', 'status ' + item.status, item.status));
    const meta = [item.year ? 'Since ' + item.year : '', item.demographic].filter(Boolean).join(' · ');
    if (meta) side.appendChild(el('div', 'meta', meta));

    const job = el('div', 'job');
    const badge = el('span', 'badge');
    badge.hidden = true;
    const detail = el('div', 'detail');
    const cancel = el('button', 'cancel', 'Cancel');
    cancel.type = 'button';
    cancel.setAttribute('aria-label', 'Cancel download of ' + item.title);
    cancel.hidden = true;
    cancel.onclick = () => post('/api/cancel/' + item.id);
    job.append(badge, detail, cancel);
    side.appendChild(job);

    const yn = el('div', 'yn');
    yn.setAttribute('role', 'group');
    yn.setAttribute('aria-label', 'Queue ' + item.title);
    const y = el('button', 'y', 'Y');
    const n = el('button', 'n', 'N');
    y.type = n.type = 'button';
    y.title = 'Queue this series';
    n.title = 'Do not queue';
    y.onclick = () => set(item.id, true);
    n.onclick = () => set(item.id, false);
    yn.append(y, n);
    side.appendChild(yn);
    r.appendChild(side);

    rows[item.id] = { el: r, item, y, n, badge, detail, cancel };
    return r;
  }

  // A tag is included, excluded or neither, never both. A plain click cycles
  // through the three states (so it works on touch); shift-click jumps to exclude.
  function cycleTag(t, shift) {
    if (shift) return toggleExcluded(t);
    if (activeTags.has(t)) toggleExcluded(t);
    else if (excludedTags.has(t)) { excludedTags.delete(t); applyFilter(); }
    else toggleTag(t);
  }

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

  // Rows must match the search text, demographic and year range, carry every
  // included tag and none of the excluded ones.
  function applyFilter() {
    const q = $('q').value.trim().toLowerCase();
    const demo = $('demoSel').value;
    const yFrom = parseInt($('yFrom').value, 10);
    const yTo = parseInt($('yTo').value, 10);
    let shown = 0;
    for (const r of Object.values(rows)) {
      const okText = !q || r.item.title.toLowerCase().includes(q);
      const tags = r.item.tags || [];
      const okTags = [...activeTags].every(t => tags.includes(t));
      const okEx = ![...excludedTags].some(t => tags.includes(t));
      const okDemo = !demo || (r.item.demographic || 'none') === demo;
      // Titles with no known year are hidden while a year bound is set.
      const y = r.item.year || 0;
      const okYear = (isNaN(yFrom) && isNaN(yTo)) ||
        (y > 0 && (isNaN(yFrom) || y >= yFrom) && (isNaN(yTo) || y <= yTo));
      r.el.hidden = !(okText && okTags && okEx && okDemo && okYear);
      if (!r.el.hidden) shown++;
      for (const c of r.el.querySelectorAll('.tag')) {
        const on = activeTags.has(c.textContent), ex = excludedTags.has(c.textContent);
        c.classList.toggle('on', on);
        c.classList.toggle('ex', ex);
        c.setAttribute('aria-pressed', on || ex ? (on ? 'true' : 'mixed') : 'false');
        c.title = on ? 'Included, click to exclude' : ex ? 'Excluded, click to clear' : 'Click to include (shift-click to exclude)';
      }
    }
    $('shown').textContent = shown + ' of ' + allItems.length + ' shown';
    if (allItems.length) {
      msg.hidden = shown > 0;
      msg.textContent = 'No series match these filters.';
    }

    const box = $('activeTags');
    box.replaceChildren();
    for (const t of activeTags) {
      const b = el('button', 'active-tag', t + ' ×');
      b.type = 'button';
      b.setAttribute('aria-label', 'Remove included tag ' + t);
      b.onclick = () => toggleTag(t);
      box.appendChild(b);
    }
    for (const t of excludedTags) {
      const b = el('button', 'active-tag ex', '− ' + t + ' ×');
      b.type = 'button';
      b.setAttribute('aria-label', 'Remove excluded tag ' + t);
      b.onclick = () => toggleExcluded(t);
      box.appendChild(b);
    }

    const filtersOn = activeTags.size + excludedTags.size + (demo ? 1 : 0) +
      (isNaN(yFrom) && isNaN(yTo) ? 0 : 1);
    $('clearFilters').hidden = filtersOn === 0;
    const pill = $('filterCount');
    pill.hidden = filtersOn === 0;
    pill.textContent = filtersOn;
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
    const sorted = Object.values(rows).sort(SORTS[$('sortSel').value]);
    for (const r of sorted) list.appendChild(r.el);
  }

  function fillSelect(sel, counts) {
    for (const k of Object.keys(counts).sort()) {
      const o = el('option', '', k + ' (' + counts[k] + ')');
      o.value = k;
      sel.appendChild(o);
    }
  }

  function buildSelects(items) {
    const tagCounts = {}, demoCounts = {};
    for (const it of items) {
      for (const t of it.tags || []) tagCounts[t] = (tagCounts[t] || 0) + 1;
      const d = it.demographic || 'none';
      demoCounts[d] = (demoCounts[d] || 0) + 1;
    }
    const inc = $('tagSel'), ex = $('exSel');
    fillSelect(inc, tagCounts);
    fillSelect(ex, tagCounts);
    fillSelect($('demoSel'), demoCounts);
    inc.onchange = () => {
      if (inc.value) { excludedTags.delete(inc.value); activeTags.add(inc.value); }
      inc.value = '';
      applyFilter();
    };
    ex.onchange = () => {
      if (ex.value) { activeTags.delete(ex.value); excludedTags.add(ex.value); }
      ex.value = '';
      applyFilter();
    };
    $('demoSel').onchange = applyFilter;
  }

  async function load() {
    let items;
    try {
      const res = await fetch('/api/library');
      if (!res.ok) throw new Error(await errorText(res));
      items = await res.json();
    } catch (e) {
      msg.textContent = 'Could not load the library: ' + e.message;
      return;
    }
    msg.hidden = items.length > 0;
    if (!items.length) msg.textContent = 'Nothing in this list.';
    allItems = items;
    for (const it of items) list.appendChild(row(it));
    buildSelects(items);
    applySort();
    applyFilter();
    refresh();
    poll();
  }

  async function errorText(res) {
    let text = await res.text();
    try { text = JSON.parse(text).error || text; } catch (e) {}
    return text || res.statusText;
  }

  let noticeTimer;
  function showBanner(text, info) {
    clearTimeout(noticeTimer);
    banner.textContent = text || '';
    banner.classList.toggle('info', !!info);
    banner.hidden = !text;
  }

  async function post(path, body) {
    const res = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) showBanner(await errorText(res));
    await pollOnce(!res.ok);
    return res;
  }

  function summarize(js) {
    const n = (s) => js.filter(j => j.state === s).length;
    return [[n('running'), 'running'], [n('queued'), 'queued'], [n('failed'), 'failed']]
      .filter(([c]) => c).map(([c, s]) => c + ' ' + s).join(' · ');
  }

  // One round trip to the downloader's queue (through this server).
  // keepBanner leaves an error already shown by the caller in place.
  async function pollOnce(keepBanner) {
    const res = await fetch('/api/queue');
    const p = await res.json();
    if (!res.ok) { showBanner(p.error || 'Downloader not reachable.'); return; }
    if (!keepBanner && !banner.classList.contains('info')) showBanner('');

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
    $('activity').textContent = summarize(p.jobs || []);
    if (p.log && p.log.length) {
      $('logBox').hidden = false;
      const stick = logEl.scrollTop + logEl.clientHeight >= logEl.scrollHeight - 8;
      logEl.textContent = p.log.slice(-40).join('\n');
      if (stick) logEl.scrollTop = logEl.scrollHeight;
    }
    refresh();
  }

  async function poll() {
    try { await pollOnce(); } catch (e) {
      showBanner('Lost connection to the web server. Retrying…');
      setTimeout(poll, 5000);
      return;
    }
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
        showBanner(r.skipped.length + ' already queued, skipped.', true);
        noticeTimer = setTimeout(() => showBanner(''), 4000);
      }
      refresh();
    }
  };
  $('clearDone').onclick = () => post('/api/clear');
  // Bulk Y/N only touches the rows currently shown by the filters.
  $('allY').onclick = () => { visibleIDs().filter(id => !locked(id)).forEach(id => selected.add(id)); refresh(); };
  $('allN').onclick = () => { visibleIDs().forEach(id => selected.delete(id)); refresh(); };
  $('q').oninput = applyFilter;
  $('yFrom').oninput = applyFilter;
  $('yTo').oninput = applyFilter;
  $('sortSel').onchange = applySort;
  $('clearFilters').onclick = () => {
    activeTags.clear(); excludedTags.clear();
    $('demoSel').value = $('yFrom').value = $('yTo').value = '';
    applyFilter();
  };
  $('filtersBtn').onclick = () => {
    const open = $('adv').classList.toggle('open');
    $('filtersBtn').setAttribute('aria-expanded', open);
  };

  load();

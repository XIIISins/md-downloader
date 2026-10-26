'use strict';
const $ = (s) => document.querySelector(s);
const el = (tag, props = {}, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.startsWith('aria-') || k.startsWith('data-') || k === 'role') e.setAttribute(k, v);
    else if (k === 'style') e.style.cssText = v;
    else e[k] = v;
  }
  e.append(...kids.filter((k) => k !== '' && k != null && k !== false));
  return e;
};
const cover = (id) => '/api/cover/' + id;
const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : '');
const LANG = { ja: 'Japanese', ko: 'Korean', zh: 'Chinese', 'zh-hk': 'Chinese (HK)', en: 'English' };
const PLUS = '<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>';
const CHECK = '<svg viewBox="0 0 24 24"><path d="m5 12.5 4.5 4.5L19 7.5"/></svg>';

// The app name lives in index.html's <title>; the header shows the same text.
$('h1').textContent = document.title;

/* preferences, kept in localStorage under "prefs" */
const DEF = { theme: 'system', dark: 'default', light: 'default', accent: 'auto', poster: 150, view: 'posters', sort: 'title', meta: true, openQueue: false };
const P = (() => { try { return { ...DEF, ...JSON.parse(localStorage.getItem('prefs') || '{}') }; } catch { return { ...DEF }; } })();
const savePrefs = () => { try { localStorage.setItem('prefs', JSON.stringify(P)); } catch { /* storage blocked: prefs last for this visit */ } applyPrefs(); };

/* colour schemes: [name, bg, surface, raise, text, text-2, text-3, accent, accent-ink, running, waiting, error] */
const SCHEMES = {
  dark: {
    default: ['Default', '#16171d', '#1e1f27', '#292a35', '#eeeef3', '#b3b4c0', '#81828f', null],
    mocha: ['Catppuccin Mocha', '#1e1e2e', '#262637', '#313244', '#cdd6f4', '#a6adc8', '#7f849c', '#cba6f7', '#1e1e2e', '#a6e3a1', '#f9e2af', '#f38ba8'],
    macchiato: ['Catppuccin Macchiato', '#24273a', '#2c3047', '#363a4f', '#cad3f5', '#a5adcb', '#8087a2', '#c6a0f6', '#24273a', '#a6da95', '#eed49f', '#ed8796'],
    tokyo: ['Tokyo Night', '#1a1b26', '#20222f', '#292e42', '#c0caf5', '#a9b1d6', '#7982ad', '#7aa2f7', '#1a1b26', '#9ece6a', '#e0af68', '#f7768e'],
    storm: ['Tokyo Night Storm', '#24283b', '#2a2f45', '#343a55', '#c0caf5', '#a9b1d6', '#7982ad', '#7aa2f7', '#1f2335', '#9ece6a', '#e0af68', '#f7768e'],
    onedark: ['One Dark', '#282c34', '#2f343e', '#3a404c', '#abb2bf', '#9da5b4', '#7f8799', '#61afef', '#21252b', '#98c379', '#e5c07b', '#e06c75'],
    gruvbox: ['Gruvbox', '#282828', '#32302f', '#3c3836', '#ebdbb2', '#bdae93', '#a89984', '#fabd2f', '#282828', '#b8bb26', '#fe8019', '#fb4934'],
    nord: ['Nord', '#2e3440', '#3b4252', '#434c5e', '#eceff4', '#d8dee9', '#9aa5bb', '#88c0d0', '#2e3440', '#a3be8c', '#ebcb8b', '#bf616a'],
    rose: ['Rosé Pine', '#191724', '#1f1d2e', '#26233a', '#e0def4', '#a8a4c4', '#8a86a6', '#c4a7e7', '#191724', '#9ccfd8', '#f6c177', '#eb6f92'],
    dracula: ['Dracula', '#282a36', '#313442', '#44475a', '#f8f8f2', '#c4c8dc', '#8e98c4', '#bd93f9', '#282a36', '#50fa7b', '#f1fa8c', '#ff5555'],
  },
  light: {
    default: ['Default', '#f4f4f6', '#ffffff', '#ebebef', '#14141a', '#55565f', '#8b8c96', null],
    latte: ['Catppuccin Latte', '#eff1f5', '#e6e9ef', '#ccd0da', '#4c4f69', '#5c5f77', '#6c6f85', '#8839ef', '#ffffff', '#40a02b', '#b8730f', '#d20f39'],
    onelight: ['One Light', '#fafafa', '#f0f0f1', '#e2e2e4', '#383a42', '#50525a', '#696c77', '#4078f2', '#ffffff', '#50a14f', '#a06f00', '#e45649'],
    tokyoday: ['Tokyo Night Day', '#e1e2e7', '#d5d6db', '#c4c8da', '#343b58', '#4c5372', '#68709a', '#2e7de9', '#ffffff', '#587539', '#8c6c3e', '#f52a65'],
    dawn: ['Rosé Pine Dawn', '#faf4ed', '#fffaf3', '#f2e9e1', '#575279', '#6e6a86', '#797593', '#907aa9', '#ffffff', '#286983', '#c07a1a', '#b4637a'],
  },
};
const VARS = ['--bg', '--surface', '--raise', '--text', '--text-2', '--text-3', '--accent', '--accent-ink', '--run', '--wait', '--bad', '--glass', '--shadow'];
const dm = matchMedia('(prefers-color-scheme: dark)');
const mode = () => (P.theme === 'system' ? (dm.matches ? 'dark' : 'light') : P.theme);
function applyPrefs() {
  const r = document.documentElement, m = mode(), s = SCHEMES[m][P[m]] || SCHEMES[m].default;
  r.setAttribute('data-theme', m);
  VARS.forEach((v) => r.style.removeProperty(v));
  if (s[7]) { // a named scheme overrides the base palette
    VARS.slice(0, 11).forEach((v, i) => r.style.setProperty(v, s[i + 1]));
    r.style.setProperty('--glass', `color-mix(in srgb, ${s[2]} 78%, transparent)`);
    r.style.setProperty('--shadow', `0 12px 34px rgba(0,0,0,${m === 'dark' ? 0.5 : 0.16})`);
  }
  if (P.accent !== 'auto') {
    r.style.setProperty('--hue', P.accent);
    if (s[7]) { // named schemes carry a fixed accent, so rebuild it from the hue
      r.style.setProperty('--accent', `oklch(${m === 'dark' ? 0.74 : 0.58} ${m === 'dark' ? 0.15 : 0.2} ${P.accent})`);
      r.style.setProperty('--accent-ink', m === 'dark' ? '#0d0d11' : '#ffffff');
    }
  } else r.style.setProperty('--hue', 285);
  r.style.setProperty('--poster', P.poster + 'px');
  document.body.classList.toggle('nometa', !P.meta);
}
dm.addEventListener('change', applyPrefs);
applyPrefs();

/* themed dropdown: replaces a native <select> but keeps it as the source of truth (value, input and change events) */
const nativeValue = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');
let ddOpen = null;
function closeDD() { if (ddOpen) { ddOpen.list.remove(); ddOpen.btn.setAttribute('aria-expanded', 'false'); ddOpen = null; } }
function dropdown(sel) {
  const btn = el('button', { className: 'select dd-btn', type: 'button', 'aria-haspopup': 'listbox', 'aria-expanded': 'false' });
  const label = sel.getAttribute('aria-label');
  if (label) btn.setAttribute('aria-label', label);
  const wrap = el('div', { className: 'dd' }, btn, sel);
  sel.hidden = true; sel.tabIndex = -1;
  const sync = () => { const o = sel.options[sel.selectedIndex]; btn.textContent = o ? o.textContent : ''; btn.classList.toggle('on', !!sel.value && sel.options[0].value === ''); };
  Object.defineProperty(sel, 'value', { get() { return nativeValue.get.call(sel); }, set(v) { nativeValue.set.call(sel, v); sync(); } });
  const choose = (i) => {
    nativeValue.set.call(sel, sel.options[i].value); sync(); closeDD();
    sel.dispatchEvent(new Event('input', { bubbles: true })); sel.dispatchEvent(new Event('change', { bubbles: true }));
    btn.focus();
  };
  function open() {
    closeAll('dd');
    const list = el('div', { className: 'dd-list', role: 'listbox', tabIndex: -1 });
    let cur = Math.max(0, sel.selectedIndex);
    const opts = [...sel.options].map((o, i) => {
      const b = el('div', { className: 'dd-opt', role: 'option', textContent: o.textContent, 'aria-selected': i === sel.selectedIndex });
      b.onclick = () => choose(i); b.onmousemove = () => mark(i);
      return b;
    });
    const mark = (i) => { cur = i; opts.forEach((o, j) => o.classList.toggle('cur', j === i)); opts[i].scrollIntoView({ block: 'nearest' }); };
    list.append(...opts); document.body.append(list);
    // Open below the button, or above it when there is no room.
    const r = btn.getBoundingClientRect(), h = Math.min(list.scrollHeight, 300), below = innerHeight - r.bottom - 12;
    list.style.minWidth = r.width + 'px';
    list.style.maxWidth = innerWidth - 24 + 'px';
    list.style.left = Math.max(12, Math.min(r.left, innerWidth - Math.max(r.width, 200) - 12)) + 'px';
    if (below >= h || below >= r.top) { list.style.top = r.bottom + 6 + 'px'; list.style.maxHeight = Math.min(300, below) + 'px'; }
    else { list.style.bottom = innerHeight - r.top + 6 + 'px'; list.style.maxHeight = Math.min(300, r.top - 12) + 'px'; }
    btn.setAttribute('aria-expanded', 'true'); ddOpen = { list, btn }; mark(cur); list.focus();
    list.onkeydown = (e) => {
      const k = e.key;
      if (k === 'ArrowDown') mark(Math.min(cur + 1, opts.length - 1));
      else if (k === 'ArrowUp') mark(Math.max(cur - 1, 0));
      else if (k === 'Home') mark(0);
      else if (k === 'End') mark(opts.length - 1);
      else if (k === 'Enter' || k === ' ') choose(cur);
      else if (k === 'Escape') { closeDD(); btn.focus(); }
      else if (k === 'Tab') closeDD();
      else if (k.length === 1) { // type-ahead
        const i = [...sel.options].findIndex((o, j) => j > cur && o.textContent.toLowerCase().startsWith(k.toLowerCase()));
        if (i >= 0) mark(i);
        return;
      } else return;
      e.preventDefault(); e.stopPropagation();
    };
  }
  btn.onclick = (e) => { e.stopPropagation(); ddOpen && ddOpen.btn === btn ? closeDD() : open(); };
  sync();
  return wrap;
}
document.addEventListener('click', (e) => { if (ddOpen && !e.target.closest('.dd-list')) closeDD(); });
addEventListener('resize', () => { closeDD(); if (!$('#tagPicker').hidden) placePicker(); });
document.addEventListener('scroll', (e) => { if (ddOpen && !e.target.closest?.('.dd-list')) closeDD(); }, true);

/* Only one popover is open at a time: the tag picker, a dropdown list, or the settings/queue panel. */
function closeAll(keep) {
  if (keep !== 'dd') closeDD();
  if (keep !== 'picker') picker(false);
  if (keep !== 'pop' && openPop) pop(openPop);
}

/* library state */
const SORTS = {};
const byNum = (f, dir) => (a, b) => {
  const x = a[f] || 0, y = b[f] || 0; // missing values (0) sort last in either direction
  if (!x !== !y) return x ? -1 : 1;
  return dir * (x - y) || a.title.localeCompare(b.title);
};
const S = { items: [], byId: new Map(), groups: {}, hasGroups: false, sel: new Set(), tags: new Map(), mode: 'any', q: '', rating: '', demo: '', status: '', dl: '', lang: '', from: 0, to: 0, sort: P.sort, view: P.view };
const Q = new Map(); // series id -> job from the downloader's queue
const locked = (id) => { const j = Q.get(id); return !!j && (j.state === 'queued' || j.state === 'running'); };

/* download status of a title: the live queue wins over what is on disk (dl_status from the API, absent on older servers) */
const DL = { downloading: 'Downloading', queued: 'In queue', partial: 'Partial', up_to_date: 'Up to date', complete: 'Complete', none: 'Not downloaded' };
const DOT = { downloading: 'busy', queued: 'busy', partial: 'partial', up_to_date: 'ok', complete: 'ok' };
function dlState(it) {
  const j = Q.get(it.id);
  if (j && j.state === 'running') return 'downloading';
  if (j && j.state === 'queued') return 'queued';
  return DL[it.dl_status] ? it.dl_status : '';
}
// A status dot; "none" has no dot. The text label (or the title/aria-label of the caller) carries the meaning too.
const dot = (st) => el('i', { className: 'dot', 'data-st': DOT[st], 'data-run': st === 'downloading' ? '1' : '0', 'aria-hidden': 'true' });

/* banner for errors from the API and short notices */
let bannerKind = '', bannerTimer = 0;
function showBanner(text, kind = 'error') {
  clearTimeout(bannerTimer);
  const b = $('#banner');
  b.textContent = text; b.dataset.kind = kind; b.hidden = false; bannerKind = kind;
  // Errors from polling clear when polling recovers; notices and failed actions time out.
  if (kind !== 'error') bannerTimer = setTimeout(() => clearBanner(kind), kind === 'info' ? 4000 : 8000);
}
function clearBanner(kind) { if (bannerKind === kind) { $('#banner').hidden = true; bannerKind = ''; } }
async function errorText(res) {
  let t = await res.text();
  try { t = JSON.parse(t).error || t; } catch { /* plain text body */ }
  return t || res.statusText;
}
async function post(path, body) {
  const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  if (!res.ok) showBanner(await errorText(res), 'action');
  return res;
}

async function fetchLibrary() {
  const res = await fetch('/api/library');
  if (!res.ok) throw new Error(await errorText(res));
  return res.json();
}
function ingest(items) {
  for (const it of items) {
    it.tags = it.tags || [];
    Object.assign(S.groups, it.tag_groups); // global tag -> format|genre|theme|content map
  }
  S.items = items; S.byId = new Map(items.map((i) => [i.id, i]));
  S.hasGroups = Object.keys(S.groups).length > 0;
  S.hasChapters = items.some((i) => i.chapters > 0);
  S.hasDl = items.some((i) => i.dl_status);
}
async function load() {
  let items;
  try { items = await fetchLibrary(); } catch (e) {
    $('#count').textContent = 'Could not load the library.';
    showBanner('Could not load the library: ' + e.message);
    return;
  }
  ingest(items);
  SORTS.title = ['Title A to Z', (a, b) => a.title.localeCompare(b.title)];
  SORTS.titleZ = ['Title Z to A', (a, b) => b.title.localeCompare(a.title)];
  SORTS.rating = ['Highest rated', byNum('rating', -1)];
  SORTS.follows = ['Most followed', byNum('follows', -1)];
  if (S.hasChapters) { SORTS.chapters = ['Most chapters', byNum('chapters', -1)]; SORTS.fewest = ['Fewest chapters', byNum('chapters', 1)]; }
  SORTS.newest = ['Newest first', byNum('year', -1)];
  SORTS.oldest = ['Oldest first', byNum('year', 1)];
  if (!SORTS[S.sort]) S.sort = 'title';
  $('#grid').setAttribute('aria-busy', 'false');
  initFilters(); render(); poll();
}
load();
// A finished job changes what is on disk, so fetch the library again to refresh the statuses.
async function refreshLibrary() {
  try { ingest(await fetchLibrary()); render(); } catch { /* keep the old statuses until the next change */ }
}

/* filters */
function initFilters() {
  const fill = (sel, f, label = cap) => {
    const m = new Map();
    S.items.forEach((i) => { const v = f(i); if (v) m.set(v, (m.get(v) || 0) + 1); });
    [...m].sort((a, b) => b[1] - a[1]).forEach(([v, n]) => sel.append(el('option', { value: v, textContent: `${label(v)} (${n})` })));
    sel.closest('label').hidden = m.size === 0; // nothing to filter by, e.g. the API doesn't send content ratings yet
  };
  $('#fDl').closest('label').hidden = !S.hasDl; // older servers don't report what is on disk
  Object.keys(DL).forEach((k) => $('#fDl').append(el('option', { value: k, textContent: DL[k] })));
  fill($('#fRating'), (i) => i.content_rating); fill($('#fDemo'), (i) => i.demographic);
  fill($('#fStatus'), (i) => i.status); fill($('#fLang'), (i) => i.lang, (v) => LANG[v] || v.toUpperCase());
  Object.entries(SORTS).forEach(([k, [l]]) => $('#fSort').append(el('option', { value: k, textContent: l })));
  let timer = 0;
  const bind = (id, key, num) => $(id).addEventListener('input', (e) => {
    S[key] = num ? +e.target.value || 0 : e.target.value;
    clearTimeout(timer); timer = setTimeout(render, key === 'q' ? 120 : 0);
  });
  bind('#fSort', 'sort'); bind('#fRating', 'rating'); bind('#fDemo', 'demo'); bind('#fStatus', 'status'); bind('#fDl', 'dl'); bind('#fLang', 'lang');
  bind('#yFrom', 'from', 1); bind('#yTo', 'to', 1); bind('#q', 'q');
  $('#fSort').value = S.sort;
  document.querySelectorAll('#filters select').forEach((s) => { const ph = document.createComment(''); s.replaceWith(ph); ph.replaceWith(dropdown(s)); });
  $('#toggleFilters').onclick = () => { const f = $('#filters'); closeAll(); f.hidden = !f.hidden; $('#toggleFilters').setAttribute('aria-expanded', !f.hidden); };
  $('#reset').onclick = () => {
    Object.assign(S, { q: '', rating: '', demo: '', status: '', dl: '', lang: '', from: 0, to: 0, mode: 'any' }); S.tags.clear();
    ['#q', '#fRating', '#fDemo', '#fStatus', '#fDl', '#fLang', '#yFrom', '#yTo'].forEach((i) => ($(i).value = ''));
    render();
  };
  $('#lucky').onclick = () => { const l = list(); if (l.length) openDetail(l[Math.floor(Math.random() * l.length)], $('#lucky')); };
  $('#tagBtn').onclick = (e) => { e.stopPropagation(); $('#tagPicker').hidden ? picker(true) : picker(false); };
  document.addEventListener('click', (e) => { if (!e.target.closest('#tagPicker, #tagBtn, .dd-list')) picker(false); });
  [['#vPosters', 'posters'], ['#vRows', 'rows'], ['#vList', 'list']].forEach(([id, v]) => ($(id).onclick = () => { S.view = P.view = v; savePrefs(); syncView(); render(); }));
  syncView();
}
function syncView() { [['#vPosters', 'posters'], ['#vRows', 'rows'], ['#vList', 'list']].forEach(([id, v]) => $(id).setAttribute('aria-pressed', S.view === v)); }

function list() {
  const q = S.q.trim().toLowerCase();
  const inc = [...S.tags].filter(([, v]) => v === 'in').map(([t]) => t), exc = [...S.tags].filter(([, v]) => v === 'out').map(([t]) => t);
  const years = S.from || S.to; // series with no known year are hidden while a bound is set
  return S.items.filter((i) => (!q || i.title.toLowerCase().includes(q)) && (!S.rating || i.content_rating === S.rating) && (!S.demo || i.demographic === S.demo)
    && (!S.status || i.status === S.status) && (!S.dl || dlState(i) === S.dl) && (!S.lang || i.lang === S.lang)
    && (!years || (i.year > 0 && (!S.from || i.year >= S.from) && (!S.to || i.year <= S.to)))
    && (S.mode === 'all' ? inc.every((t) => i.tags.includes(t)) : !inc.length || inc.some((t) => i.tags.includes(t))) && !exc.some((t) => i.tags.includes(t))).sort(SORTS[S.sort][1]);
}

function render() {
  const rows = list(), g = $('#grid');
  g.className = 'grid ' + S.view;
  g.replaceChildren(...rows.map(card));
  $('#count').textContent = rows.length ? `${rows.length} of ${S.items.length} series` : S.items.length ? 'No series match these filters.' : 'Nothing in this library yet.';
  const n = [S.rating, S.demo, S.status, S.dl, S.lang, S.from || S.to, S.tags.size].filter(Boolean).length;
  const b = $('#fcount'); b.hidden = !n; b.textContent = n;
  $('#toggleFilters').setAttribute('aria-label', n ? `Filters, ${n} active` : 'Filters');
  const tl = S.tags.size ? [...S.tags].map(([t, v]) => (v === 'out' ? '−' : '') + t).slice(0, 2).join(', ') + (S.tags.size > 2 ? ` +${S.tags.size - 2}` : '') : 'Any';
  $('#tagLabel').textContent = tl; $('#tagBtn').classList.toggle('on', S.tags.size > 0);
  dlCounts();
  dock();
}

// Counts in the Download status filter follow the queue, so they are refreshed on every render.
function dlCounts() {
  const sel = $('#fDl');
  if (!S.hasDl || !sel) return;
  const n = {};
  S.items.forEach((i) => { const k = dlState(i); n[k] = (n[k] || 0) + 1; });
  [...sel.options].forEach((o) => { if (o.value) o.textContent = `${DL[o.value]} (${n[o.value] || 0})`; });
  sel.value = S.dl; // re-sync the themed button's label
}

const metaLine = (it, withYear) => [it.rating ? `★ ${it.rating.toFixed(1)}` : '', it.chapters ? (it.downloaded > 0 ? `${it.downloaded}/${it.chapters} ch` : `${it.chapters} ch`) : '', withYear ? it.year || '' : '', cap(it.status)].filter(Boolean).join('  ·  ');

function card(it) {
  const lock = locked(it.id), st = dlState(it), shown = DOT[st] ? st : '';
  const li = el('div', { className: 'p' });
  const art = el('button', { className: 'art', type: 'button', 'aria-label': `Open ${it.title}` + (st ? `, ${DL[st]}` : ''), onclick: () => openDetail(it, art) }, el('img', { loading: 'lazy', alt: '', src: cover(it.id) }));
  // Posters show a dot on the cover (status in its title); rows and list show the dot with the label.
  if (shown && S.view === 'posters') { const d = dot(st); d.title = DL[st]; art.append(d); }
  const dl = shown && S.view !== 'posters' ? el('span', { className: 'dl' }, dot(st), DL[st]) : '';
  const plus = el('button', { className: 'plus', type: 'button', 'aria-label': `Add ${it.title} to downloads`, disabled: lock });
  const paint = () => {
    const on = S.sel.has(it.id);
    plus.innerHTML = (on ? CHECK : PLUS) + (S.view === 'rows' ? `<span>${on ? 'Added' : 'Add'}</span>` : '');
    li.dataset.sel = on ? '1' : ''; plus.setAttribute('aria-pressed', on);
  };
  plus.onclick = (e) => { e.stopPropagation(); S.sel.has(it.id) ? S.sel.delete(it.id) : S.sel.add(it.id); paint(); dock(); };
  paint();
  const meta = metaLine(it, S.view !== 'posters');
  const body = [el('div', { className: 't', textContent: it.title }), el('div', { className: 'm', textContent: meta })];
  if (S.view === 'rows') body.splice(2, 0, dl);
  if (S.view === 'rows') {
    body.push(el('p', { className: 'sum', textContent: it.description }));
    body.push(el('div', { className: 'rtags' }, ...it.tags.slice(0, 7).map((t) => el('span', { textContent: t }))));
  }
  li.append(...(S.view === 'posters' ? [el('div', { style: 'position:relative' }, art, plus), ...body]
    : S.view === 'rows' ? [art, el('div', {}, ...body), plus]
    : [art, el('div', {}, body[0], dl), el('span', { className: 'stat', textContent: meta }), plus]));
  if (S.view === 'rows') plus.style.alignSelf = 'start';
  return li;
}

/* tag picker (grouped like MangaDex) */
let hintDismissed = false;
function placePicker() {
  const p = $('#tagPicker'), r = $('#tagBtn').getBoundingClientRect();
  p.style.top = r.bottom + 8 + 'px';
  p.style.left = Math.max(12, Math.min(r.left, innerWidth - p.offsetWidth - 12)) + 'px';
}
function picker(show) {
  const p = $('#tagPicker');
  $('#tagBtn').setAttribute('aria-expanded', !!show);
  if (!show) { p.hidden = true; return; }
  closeAll('picker');
  const counts = new Map();
  S.items.forEach((i) => i.tags.forEach((t) => counts.set(t, (counts.get(t) || 0) + 1)));
  const build = (filter = '') => {
    p.replaceChildren();
    const search = el('input', { type: 'search', placeholder: 'Search tags', value: filter, 'aria-label': 'Search tags' });
    const reset = el('button', { type: 'button', textContent: 'Reset', onclick: () => { S.tags.clear(); render(); build(search.value); } });
    p.append(el('div', { className: 'top' }, search, reset));
    const match = el('select', { 'aria-label': 'Tag matching' }, el('option', { value: 'any', textContent: 'Include any' }), el('option', { value: 'all', textContent: 'Include all' }));
    match.value = S.mode; match.onchange = () => { S.mode = match.value; render(); };
    p.append(el('div', { className: 'mode' }, el('span', { textContent: 'Match' }), dropdown(match)));
    if (!hintDismissed) p.append(el('div', { className: 'hint' }, el('span', { textContent: 'Click a tag once to include it, again to exclude it, once more to clear it.' }),
      el('button', { type: 'button', textContent: 'Dismiss', onclick: () => { hintDismissed = true; build(search.value); } })));
    // Without group data from the API every tag goes into one ungrouped list.
    const order = S.hasGroups ? ['format', 'genre', 'theme', 'content'] : [''];
    for (const g of order) {
      const tags = [...counts].filter(([t]) => (S.hasGroups ? S.groups[t] || 'theme' : '') === g && t.toLowerCase().includes(filter.toLowerCase())).sort((a, b) => a[0].localeCompare(b[0]));
      if (!tags.length) continue;
      const box = el('div', { className: 'tags' });
      tags.forEach(([t, n]) => {
        const b = el('button', { className: 'tag', type: 'button' }, t, el('small', { textContent: n }));
        const paint = () => { const s = S.tags.get(t) || ''; b.dataset.s = s; b.setAttribute('aria-pressed', s === 'in' ? 'true' : s === 'out' ? 'mixed' : 'false'); b.title = s === 'in' ? 'Included' : s === 'out' ? 'Excluded' : ''; };
        b.onclick = () => { const c = S.tags.get(t); c ? (c === 'in' ? S.tags.set(t, 'out') : S.tags.delete(t)) : S.tags.set(t, 'in'); paint(); render(); };
        paint(); box.append(b);
      });
      if (g) p.append(el('h3', { textContent: cap(g) }));
      p.append(box);
    }
    if (!p.querySelector('.tags')) p.append(el('p', { className: 'hint', textContent: 'No tags match.' }));
    search.oninput = () => { build(search.value); const i = p.querySelector('input'); i.focus(); i.setSelectionRange(i.value.length, i.value.length); };
  };
  build(); p.hidden = false; placePicker();
}

/* detail dialog: centred pop-up, a bottom sheet on phones */
let detailFrom = null;
function openDetail(it, from) {
  closeAll();
  const j = Q.get(it.id), d = $('#detail'), on = S.sel.has(it.id);
  detailFrom = from || null;
  const back = el('div', { className: 'back' }); back.style.backgroundImage = `url(${cover(it.id)})`;
  const x = el('button', { className: 'x', type: 'button', onclick: closeDetail, 'aria-label': 'Close' });
  x.innerHTML = '<svg viewBox="0 0 24 24"><path d="m6 6 12 12M18 6 6 18"/></svg>';
  const fact = (b, t) => el('span', {}, b ? el('b', { textContent: b }) : '', t ? (b ? ' ' : '') + t : '');
  const facts = el('div', { className: 'facts' },
    it.rating ? fact('★ ' + it.rating.toFixed(2), 'rating') : '', it.chapters ? fact(String(it.chapters), 'chapters') : '',
    it.follows ? fact(it.follows.toLocaleString(), 'follows') : '', it.year ? fact(String(it.year)) : '', it.status ? fact(cap(it.status)) : '',
    it.demographic ? fact(cap(it.demographic)) : '', fact('', LANG[it.lang] || it.lang), it.content_rating ? fact('', cap(it.content_rating)) : '');
  const st = dlState(it), busy = st === 'queued' || st === 'downloading';
  const again = { partial: 'Download missing', up_to_date: 'Check for new chapters', complete: 'Check for new chapters' }[st] || (j ? 'Download again' : 'Add to downloads');
  const main = el('button', { className: 'main', type: 'button', disabled: busy, textContent: busy ? DL[st] : on ? 'Added to downloads' : again });
  const have = it.downloaded > 0 && it.chapters > 0 ? ` · ${it.downloaded} of ${it.chapters} chapters downloaded` : '';
  const status = st ? el('p', { className: 'dl' }, DOT[st] ? dot(st) : '', DL[st] + have) : '';
  main.onclick = () => { S.sel.add(it.id); closeDetail(); render(); };
  d.setAttribute('aria-label', it.title);
  d.replaceChildren(back, x, el('div', { className: 'wrap' }, el('img', { alt: '', src: cover(it.id) }),
    el('div', {}, el('h2', { textContent: it.title }), facts, status, el('p', { className: 'desc', textContent: it.description || 'No summary.' }),
      el('div', { className: 'rtags' }, ...it.tags.map((t) => el('span', { textContent: t }))),
      el('div', { className: 'cta' }, main, el('button', { type: 'button', textContent: 'Close', onclick: closeDetail })))));
  d.hidden = false; $('#scrim').hidden = false; d.scrollTop = 0; x.focus();
}
function closeDetail() {
  if ($('#detail').hidden) return;
  $('#detail').hidden = true; $('#scrim').hidden = true;
  if (detailFrom && detailFrom.isConnected) detailFrom.focus();
  detailFrom = null;
}
$('#scrim').onclick = closeDetail;
$('#detail').addEventListener('keydown', (e) => { // keep Tab inside the modal dialog
  if (e.key !== 'Tab') return;
  const f = [...$('#detail').querySelectorAll('button:not(:disabled)')];
  if (!f.length) return;
  const first = f[0], last = f[f.length - 1];
  if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
  else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
});

/* header popovers: settings and queue share one slot */
let openPop = null;
function pop(name) {
  if (openPop !== name) closeAll('pop');
  openPop = openPop === name ? null : name;
  $('#settingsPanel').hidden = openPop !== 'settings'; $('#queuePanel').hidden = openPop !== 'queue';
  $('#settingsBtn').setAttribute('aria-expanded', openPop === 'settings'); $('#queueBtn').setAttribute('aria-expanded', openPop === 'queue');
  $('#qToggle').setAttribute('aria-expanded', openPop === 'queue');
  if (openPop === 'settings') renderSettings();
  if (openPop === 'queue') renderQueue();
  if (openPop) hidePill();
}
$('#settingsBtn').onclick = (e) => { e.stopPropagation(); pop('settings'); };
$('#queueBtn').onclick = (e) => { e.stopPropagation(); pop('queue'); };
document.addEventListener('click', (e) => { if (openPop && !e.target.closest('#settingsPanel, #queuePanel, .dd-list, #qToggle')) pop(openPop); });
document.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape' || ddOpen) return; // an open dropdown handles its own Escape
  if (!$('#detail').hidden) closeDetail();
  else if (!$('#tagPicker').hidden) { picker(false); $('#tagBtn').focus(); }
  else if (openPop) { const b = openPop === 'settings' ? '#settingsBtn' : '#queueBtn'; pop(openPop); $(b).focus(); }
});

function renderSettings() {
  const v = $('#settingsPanel'), m = mode();
  const seg = (label, opts, cur, set) => el('div', { className: 'seg', role: 'group', 'aria-label': label }, ...opts.map(([val, text]) => {
    const b = el('button', { type: 'button', textContent: text, 'aria-pressed': cur === val });
    b.onclick = () => { set(val); savePrefs(); renderSettings(); };
    return b;
  }));
  const row = (title, sub, ctl) => el('div', { className: 'row' }, el('div', { className: 'l' }, el('b', { textContent: title }), el('span', { textContent: sub })), ctl);
  const sw = (key, label) => { const i = el('input', { type: 'checkbox', className: 'switch', checked: P[key], role: 'switch', 'aria-label': label }); i.onchange = () => { P[key] = i.checked; savePrefs(); }; return i; };
  const schemes = (md) => el('div', { className: 'schemes' }, ...Object.entries(SCHEMES[md]).map(([k, s]) => {
    const b = el('button', { className: 'scheme', type: 'button', 'aria-pressed': P[md] === k });
    const ac = s[7] || 'oklch(' + (md === 'dark' ? '.74 .15' : '.58 .2') + ' 285)';
    b.append(el('span', { className: 'dots' }, ...[s[1], s[3], ac].map((c) => el('i', { style: 'background:' + c }))), el('span', { textContent: s[0] }));
    b.onclick = () => { P[md] = k; savePrefs(); renderSettings(); };
    return b;
  }));
  const hues = [[285, 'Violet'], [250, 'Blue'], [190, 'Teal'], [150, 'Green'], [5, 'Rose'], [60, 'Amber']];
  const swatches = el('div', { className: 'swatches', role: 'group', 'aria-label': 'Accent colour' }, ...[['auto', 'From colour scheme'], ...hues].map(([h, n]) => {
    const b = el('button', { className: 'sw' + (h === 'auto' ? ' auto' : ''), type: 'button', title: n, 'aria-label': n, 'aria-pressed': P.accent === h });
    if (h !== 'auto') b.style.setProperty('--c', `oklch(.68 .17 ${h})`);
    b.onclick = () => { P.accent = h; savePrefs(); renderSettings(); };
    return b;
  }));
  const range = el('input', { type: 'range', min: 120, max: 240, step: 10, value: P.poster, 'aria-label': 'Poster size' });
  range.oninput = () => { P.poster = +range.value; savePrefs(); };
  const sort = el('select', { 'aria-label': 'Default sort' }, ...Object.entries(SORTS).map(([k, [l]]) => el('option', { value: k, textContent: l, selected: S.sort === k })));
  sort.onchange = () => { P.sort = S.sort = sort.value; $('#fSort').value = S.sort; savePrefs(); render(); };
  const modes = P.theme === 'system' ? ['dark', 'light'] : [m];
  v.replaceChildren(
    el('h3', { textContent: 'Appearance' }),
    seg('Mode', [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']], P.theme, (x) => (P.theme = x)),
    ...modes.flatMap((md) => [el('h3', { textContent: P.theme === 'system' ? (md === 'dark' ? 'Dark colours' : 'Light colours') : 'Colour scheme' }), schemes(md)]),
    row('Accent', 'Overrides the scheme accent', swatches),
    el('h3', { textContent: 'Library' }),
    row('Poster size', 'Posters layout', range),
    row('Details under posters', S.hasChapters ? 'Rating, chapters, status' : 'Rating, status', sw('meta', 'Details under posters')),
    row('Default layout', 'When the page opens', seg('Default layout', [['posters', 'Posters'], ['rows', 'Rows'], ['list', 'List']], P.view, (x) => { P.view = S.view = x; syncView(); render(); })),
    row('Default sort', 'When the page opens', dropdown(sort)),
    el('h3', { textContent: 'Downloads' }),
    row('Open the queue after adding', 'Instead of the brief banner', sw('openQueue', 'Open the queue after adding')),
    el('button', { className: 'reset', type: 'button', textContent: 'Reset to defaults', onclick: () => {
      Object.assign(P, DEF); S.view = P.view; S.sort = SORTS[P.sort] ? P.sort : 'title'; $('#fSort').value = S.sort;
      savePrefs(); renderSettings(); syncView(); render();
    } }));
}

/* queue: header button with a live count, a dropdown with status, and a brief banner */
const FINISHED = ['done', 'failed', 'canceled'];
const jobsNow = () => [...Q.values()];
const LABEL = { running: 'Downloading', queued: 'Waiting', done: 'Done', failed: 'Failed', canceled: 'Canceled' };
// While a series downloads the daemon reports "[3/24] Ch. 5 (20 pages)"; anything else is shown as is.
function progress(j) {
  const m = /\[(\d+)\/(\d+)\]/.exec(j.detail || '');
  if (j.state !== 'running' || !m) return { text: j.state === 'queued' ? 'Waiting' : j.detail || '', pct: -1 };
  return { text: `Chapter ${m[1]} of ${m[2]}`, pct: Math.round((+m[1] / +m[2]) * 100) };
}
function renderQueue() {
  const jobs = jobsNow(), run = jobs.filter((j) => j.state === 'running'), wait = jobs.filter((j) => j.state === 'queued');
  const fin = jobs.filter((j) => FINISHED.includes(j.state)).sort((a, b) => (b.updated || '').localeCompare(a.updated || ''));
  const p = $('#queuePanel');
  const head = el('div', { className: 'qhead' }, el('div', {},
    el('b', { textContent: run.length || wait.length ? `${run.length} downloading, ${wait.length} waiting` : 'Nothing in the queue' }),
    el('span', { textContent: fin.length ? `${fin.length} finished` : 'Add titles from the library' })));
  if (fin.length) head.append(el('button', { className: 'reset', type: 'button', textContent: 'Clear finished', onclick: clearFinished }));
  const rows = [...run, ...wait, ...fin].map((j) => {
    const it = S.byId.get(j.id), pr = progress(j);
    const r = el('div', { className: 'j' }, el('img', { alt: '', src: cover(j.id) }),
      el('div', {}, el('div', { className: 'n', textContent: it ? it.title : j.title }), el('div', { className: 's', textContent: pr.text, title: j.detail || '' }),
        pr.pct >= 0 ? el('div', { className: 'bar2', role: 'progressbar', 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-valuenow': pr.pct, 'aria-label': 'Progress' }, el('i', { style: `width:${pr.pct}%` })) : ''),
      el('span', { className: 'tag', textContent: LABEL[j.state] || j.state, 'data-k': j.state }));
    if (!FINISHED.includes(j.state)) r.append(el('button', { className: 'cancel', type: 'button', textContent: 'Cancel', 'aria-label': `Cancel ${it ? it.title : j.title}`, onclick: () => cancelJob(j.id) }));
    return r;
  });
  p.replaceChildren(head, ...(rows.length ? [el('div', { className: 'jobs' }, ...rows)] : []));
}
async function cancelJob(id) { await post('/api/cancel/' + id); await pollOnce().catch(() => {}); }
async function clearFinished() { await post('/api/clear'); await pollOnce().catch(() => {}); }

let pillTimer = 0;
const pillEl = () => $('.pill');
function hidePill() { clearTimeout(pillTimer); if (!S.sel.size) pillEl().dataset.hide = '1'; }
function showPill(msg) {
  const p = pillEl(), jobs = jobsNow(), run = jobs.filter((j) => j.state === 'running').length, wait = jobs.filter((j) => j.state === 'queued').length;
  $('#qToggle').replaceChildren(el('i', { className: run ? 'run' : '' }), el('span', { textContent: msg || (run || wait ? `${run} downloading, ${wait} waiting` : 'Nothing queued') }));
  delete p.dataset.hide; clearTimeout(pillTimer);
  pillTimer = setTimeout(() => { if (!S.sel.size && !p.matches(':hover, :focus-within')) p.dataset.hide = '1'; else showPill(msg); }, 5000);
}
let lastSel = 0;
function dock() {
  const jobs = jobsNow(), run = jobs.filter((j) => j.state === 'running').length, active = run + jobs.filter((j) => j.state === 'queued').length;
  const qb = $('#queueBtn'); qb.classList.toggle('busy', run > 0); qb.setAttribute('aria-label', active ? `Queue, ${active} active` : 'Queue');
  const bd = $('#queueCount'); bd.hidden = !active; bd.textContent = active;
  const a = $('#qAdd'), n = S.sel.size;
  a.hidden = !n; a.replaceChildren(`Download ${n}`, el('span', { className: 'long', textContent: ' series' }));
  if (n && n !== lastSel) showPill();
  lastSel = n;
  if (openPop === 'queue') renderQueue();
}
$('#qAdd').onclick = async () => {
  const ids = [...S.sel].filter((id) => !locked(id));
  if (!ids.length) return;
  const a = $('#qAdd'); a.disabled = true;
  try {
    const res = await post('/api/download', { ids });
    if (!res.ok) return;
    const r = await res.json().catch(() => ({}));
    S.sel.clear();
    const skipped = (r.skipped || []).length;
    await pollOnce().catch(() => {});
    render();
    showPill(`Added ${r.added ?? ids.length - skipped} to the queue` + (skipped ? `, ${skipped} already queued` : ''));
    if (P.openQueue && openPop !== 'queue') pop('queue');
  } catch (e) {
    showBanner('Could not reach the server: ' + e.message, 'action');
  } finally { a.disabled = false; }
};
$('#qToggle').onclick = () => pop('queue');

/* one round trip to the downloader's queue (through this server); re-render the cards only when the set of locked series changes */
let sig = '', active = new Set();
async function pollOnce() {
  const res = await fetch('/api/queue');
  const p = await res.json().catch(() => ({}));
  if (!res.ok) { showBanner(p.error || 'Downloader not reachable.'); return; }
  clearBanner('error');
  Q.clear();
  for (const j of p.jobs || []) Q.set(j.id, j);
  S.sel.forEach((id) => { if (locked(id)) S.sel.delete(id); });
  const next = jobsNow().map((j) => j.id + j.state).sort().join();
  const act = new Set(jobsNow().filter((j) => locked(j.id)).map((j) => j.id));
  const finished = S.hasDl && [...active].some((id) => !act.has(id));
  active = act;
  if (next !== sig) { sig = next; render(); } else dock();
  if (finished) refreshLibrary();
}
async function poll() {
  let wait = 1500;
  if (!document.hidden) {
    try { await pollOnce(); } catch { showBanner('Lost connection to the web server. Retrying…'); wait = 5000; }
  }
  setTimeout(poll, wait);
}

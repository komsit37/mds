// ── State ──
let state = { project: '', files: [], isGit: false, filterChanged: false, showAll: false, recentLimit: 10 };

// ── Theme ──
// Modes: 'auto' (follow system), 'light', 'dark'
function getThemePref() {
  return localStorage.getItem('mds-theme') || 'auto';
}

function isDark() {
  const pref = getThemePref();
  if (pref === 'dark') return true;
  if (pref === 'light') return false;
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function applyTheme() {
  const pref = getThemePref();
  const root = document.documentElement;
  if (pref === 'auto') {
    root.removeAttribute('data-theme');
  } else {
    root.setAttribute('data-theme', pref);
  }
  // Highlight.js theme
  const hljsLink = document.getElementById('hljs-theme');
  if (hljsLink) {
    hljsLink.href = isDark() ? '/vendor/highlight-dark.min.css' : '/vendor/highlight-light.min.css';
  }
}

function cycleTheme() {
  const order = ['auto', 'light', 'dark'];
  const cur = getThemePref();
  const next = order[(order.indexOf(cur) + 1) % order.length];
  localStorage.setItem('mds-theme', next);
  applyTheme();
  // Re-init mermaid with correct theme
  mermaid.initialize({ startOnLoad: false, theme: isDark() ? 'dark' : 'default', securityLevel: 'loose' });
  // Update toggle button icon
  const btn = document.getElementById('theme-btn');
  if (btn) btn.textContent = themeIcon();
}

function themeIcon() {
  const pref = getThemePref();
  if (pref === 'light') return '☀️';
  if (pref === 'dark') return '🌙';
  return '◐';
}

// ── Init ──
document.addEventListener('DOMContentLoaded', () => {
  applyTheme();
  mermaid.initialize({
    startOnLoad: false,
    theme: isDark() ? 'dark' : 'default',
    securityLevel: 'loose',
  });
  // Re-apply theme when system preference changes (for auto mode)
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (getThemePref() === 'auto') applyTheme();
  });
  route();
  window.addEventListener('hashchange', route);
});

// ── Router ──
function route() {
  const hash = location.hash || '#/';
  if (hash.startsWith('#/view/')) {
    const path = decodeURIComponent(hash.slice(7));
    showView(path);
  } else {
    showFileList();
  }
}

// ── File List Page ──
async function showFileList() {
  const app = document.getElementById('app');
  app.innerHTML = '<div class="loading">Loading…</div>';

  try {
    const url = state.showAll ? '/api/files?all=true' : '/api/files';
    const res = await fetch(url);
    const data = await res.json();
    state.project = data.project;
    state.files = data.files;
    state.isGit = data.isGit;
    document.title = `${data.project} — mds`;
  } catch (e) {
    app.innerHTML = '<div class="loading">Error loading files</div>';
    return;
  }

  renderFileList();
}

// ── Tree Expand/Collapse ──
function expandAll() {
  const folders = document.querySelectorAll('.tree-folder-label[data-dir-id]');
  folders.forEach(folder => {
    const id = folder.getAttribute('data-dir-id');
    const el = document.getElementById(id);
    const arrow = document.getElementById('arrow-' + id);
    if (el && arrow) {
      el.style.display = '';
      arrow.classList.add('open');
    }
  });
}

function collapseAll() {
  const folders = document.querySelectorAll('.tree-folder-label[data-dir-id]');
  folders.forEach(folder => {
    const id = folder.getAttribute('data-dir-id');
    const el = document.getElementById(id);
    const arrow = document.getElementById('arrow-' + id);
    if (el && arrow) {
      el.style.display = 'none';
      arrow.classList.remove('open');
    }
  });
}

function renderFileList() {
  const app = document.getElementById('app');
  const files = state.filterChanged ? state.files.filter(f => f.changed) : state.files;
  const recentFiles = files.slice(0, state.recentLimit);

  let html = `
    <div class="header">
      <h1><a href="#/"><span class="icon">📄</span>${esc(state.project)}</a></h1>
      <div class="toolbar">
        <button class="btn ${state.showAll ? 'active' : ''}" onclick="toggleAll()">
          ${state.showAll ? '✓ All files' : '.md'}
        </button>
        ${state.isGit ? `<button class="btn ${state.filterChanged ? 'active' : ''}" onclick="toggleFilter()">
          ${state.filterChanged ? '✓ ' : ''}Changed
        </button>` : ''}
        <button class="theme-toggle" id="theme-btn" onclick="cycleTheme()" title="Toggle theme">${themeIcon()}</button>
      </div>
    </div>
  `;

  // File list section (recent files removed)
  html += '<div class="file-list-section">';
  html += '<div class="section-title">Recently Changed</div>';
  if (recentFiles.length === 0) {
    html += '<div class="loading">No files found</div>';
  } else {
    html += '<ul class="file-list">';
    for (const f of recentFiles) {
      html += fileItemHTML(f);
    }
    html += '</ul>';
  }
  html += '</div>';

  // File tree
  if (!state.filterChanged) {
    html += '<div class="tree-section">';
    html += '<div class="section-title">All Files</div>';
    html += '<div class="tree-controls">';
    html += `<button class="btn" onclick="expandAll()" title="Expand all">⬌ Expand all</button>`;
    html += `<button class="btn" onclick="collapseAll()" title="Collapse all">⬌ Collapse all</button>`;
    html += '</div>';
    html += buildTreeHTML(state.files);
    html += '</div>';
  } else if (files.length > 20) {
    html += '<div class="tree-section">';
    html += '<div class="section-title">All Changed Files</div>';
    html += '<div class="tree-controls">';
    html += `<button class="btn" onclick="expandAll()" title="Expand all">⬌ Expand all</button>`;
    html += `<button class="btn" onclick="collapseAll()" title="Collapse all">⬌ Collapse all</button>`;
    html += '</div>';
    html += buildTreeHTML(files);
    html += '</div>';
  } else {
    // No tree to show
    html += '<div class="tree-section"></div>';
  }

  app.innerHTML = html;
}

function fileItemHTML(f) {
  const dir = f.dir === '.' ? '' : f.dir + '/';
  return `
    <li class="file-item" onclick="location.hash='#/view/${encodeURIComponent(f.path)}'">
      <span class="file-name">${esc(f.name)}</span>
      <span class="file-dir">${esc(dir)}</span>
      ${f.changed ? '<span class="badge badge-changed">M</span>' : ''}
      <span class="file-time">${timeAgo(f.modTime)}</span>
    </li>
  `;
}

function toggleFilter() {
  state.filterChanged = !state.filterChanged;
  state.recentLimit = 10;
  renderFileList();
}

function toggleAll() {
  state.showAll = !state.showAll;
  state.recentLimit = 10;
  showFileList();
}

function showMoreRecent() {
  state.recentLimit += 20;
  renderFileList();
}

// ── File Tree Builder ──
function buildTreeHTML(files) {
  const tree = {};
  for (const f of files) {
    const parts = f.path.split('/');
    let node = tree;
    for (let i = 0; i < parts.length - 1; i++) {
      if (!node[parts[i]]) node[parts[i]] = {};
      node = node[parts[i]];
    }
    node[parts[parts.length - 1]] = f;
  }
  return renderTree(tree, '');
}

function renderTree(node, prefix) {
  let html = '<ul class="tree-dir">';
  const keys = Object.keys(node).sort((a, b) => {
    const aIsDir = typeof node[a] === 'object' && !node[a].path;
    const bIsDir = typeof node[b] === 'object' && !node[b].path;
    if (aIsDir !== bIsDir) return aIsDir ? -1 : 1;
    return a.localeCompare(b);
  });

  for (const key of keys) {
    const val = node[key];
    if (val && val.path) {
      // File
      html += `
        <li class="tree-file" onclick="location.hash='#/view/${encodeURIComponent(val.path)}'">
          <span class="file-name">${esc(key)}</span>
          ${val.changed ? '<span class="badge badge-changed">M</span>' : ''}
        </li>
      `;
    } else {
      // Directory
      const id = 'dir-' + (prefix + key).replace(/[^a-zA-Z0-9]/g, '-');
      html += `
        <li>
          <div class="tree-folder-label" onclick="toggleDir('${id}')" data-dir-id="${id}">
            <span class="arrow open" id="arrow-${id}">▶</span>
            📁 ${esc(key)}
          </div>
          <div id="${id}">
            ${renderTree(val, prefix + key + '/')}
          </div>
        </li>
      `;
    }
  }
  html += '</ul>';
  return html;
}

function toggleDir(id) {
  const el = document.getElementById(id);
  const arrow = document.getElementById('arrow-' + id);
  if (el.style.display === 'none') {
    el.style.display = '';
    arrow.classList.add('open');
  } else {
    el.style.display = 'none';
    arrow.classList.remove('open');
  }
}

// ── View Page ──
async function showView(path) {
  const app = document.getElementById('app');
  app.innerHTML = '<div class="loading">Loading…</div>';

  // Breadcrumb
  const parts = path.split('/');
  let breadcrumb = `<a href="#/">📄 ${esc(state.project || 'Home')}</a>`;
  for (let i = 0; i < parts.length; i++) {
    breadcrumb += `<span class="sep">/</span>`;
    if (i === parts.length - 1) {
      breadcrumb += `<strong>${esc(parts[i])}</strong>`;
    } else {
      breadcrumb += `<span>${esc(parts[i])}</span>`;
    }
  }

  let html = `
    <div class="header">
      <h1><a href="#/">📄 ${esc(state.project || 'Home')}</a></h1>
      <button class="theme-toggle" id="theme-btn" onclick="cycleTheme()" title="Toggle theme">${themeIcon()}</button>
    </div>
    <div class="breadcrumb">${breadcrumb}</div>
    <div class="view-toolbar">
      <button class="tab active" id="tab-read" onclick="switchTab('read', '${encodeURIComponent(path)}')">📖 Read</button>
      <button class="tab" id="tab-diff" onclick="switchTab('diff', '${encodeURIComponent(path)}')">± Diff</button>
      <button class="tab" id="tab-history" onclick="switchTab('history', '${encodeURIComponent(path)}')">🕘 History</button>
    </div>
    <div id="view-content"><div class="loading">Loading…</div></div>
  `;
  app.innerHTML = html;

  loadReadView(path);
}

async function switchTab(tab, encodedPath) {
  const path = decodeURIComponent(encodedPath);
  document.querySelectorAll('.view-toolbar .tab').forEach(t => t.classList.remove('active'));
  document.getElementById('tab-' + tab).classList.add('active');

  if (tab === 'read') {
    loadReadView(path);
  } else if (tab === 'history') {
    loadHistoryView(path);
  } else {
    loadDiffView(path);
  }
}

async function loadReadView(path) {
  const content = document.getElementById('view-content');
  content.innerHTML = '<div class="loading">Loading…</div>';

  try {
    const res = await fetch('/api/content?path=' + encodeURIComponent(path));
    const text = await res.text();
    if (path.toLowerCase().endsWith('.md')) {
      content.innerHTML = '<div class="md-content">' + renderMarkdown(text) + '</div>';
      highlightCode(content);
      await renderMermaidBlocks(content);
    } else {
      // Raw file — show as syntax-highlighted code block
      const ext = path.split('.').pop().toLowerCase();
      const langMap = { js: 'javascript', ts: 'typescript', py: 'python', sh: 'bash',
        go: 'go', yaml: 'yaml', yml: 'yaml', json: 'json', sql: 'sql',
        dockerfile: 'dockerfile', proto: 'protobuf', rb: 'ruby', rs: 'rust',
        java: 'java', kt: 'kotlin', css: 'css', html: 'xml', xml: 'xml',
        toml: 'toml', ini: 'ini', cfg: 'ini', mod: 'go', sum: 'plaintext' };
      const lang = langMap[ext] || '';
      const highlighted = lang && hljs.getLanguage(lang)
        ? hljs.highlight(text, { language: lang }).value
        : esc(text);
      content.innerHTML = `<pre><code class="hljs language-${esc(lang)}">${highlighted}</code></pre>`;
    }
  } catch (e) {
    content.innerHTML = '<div class="loading">Error loading file</div>';
  }
}

async function loadDiffView(path) {
  const content = document.getElementById('view-content');
  content.innerHTML = '<div class="loading">Loading diff…</div>';

  try {
    const res = await fetch('/api/diff?path=' + encodeURIComponent(path));
    const data = await res.json();
    content.innerHTML = renderDiff(data);
  } catch (e) {
    content.innerHTML = '<div class="loading">Error loading diff</div>';
  }
}

// ── History View ──
async function loadHistoryView(path) {
  const content = document.getElementById('view-content');
  content.innerHTML = '<div class="loading">Loading history…</div>';

  try {
    const res = await fetch('/api/history?path=' + encodeURIComponent(path));
    const data = await res.json();
    content.innerHTML = renderHistory(data, path);
  } catch (e) {
    content.innerHTML = '<div class="loading">Error loading history</div>';
  }
}

function renderHistory(data, path) {
  if (!data.commits || data.commits.length === 0) {
    return '<div class="diff-empty">No git history for this file</div>';
  }

  let html = '<ul class="commit-list">';
  for (const c of data.commits) {
    html += `
      <li class="commit-item" onclick="loadCommitDiff('${encodeURIComponent(path)}', '${c.hash}')">
        <div class="commit-top">
          <span class="commit-hash">${esc(c.shortHash)}</span>
          <span class="commit-age">${esc(c.age)}</span>
        </div>
        <div class="commit-message">${esc(c.message)}</div>
        <div class="commit-author">${esc(c.author)}</div>
      </li>
    `;
  }
  html += '</ul>';
  return html;
}

async function loadCommitDiff(encodedPath, commitHash) {
  const path = decodeURIComponent(encodedPath);
  const content = document.getElementById('view-content');
  content.innerHTML = '<div class="loading">Loading diff…</div>';

  try {
    const res = await fetch('/api/diff?path=' + encodeURIComponent(path) + '&commit=' + commitHash);
    const data = await res.json();
    let html = `<button class="btn commit-back" onclick="loadHistoryView('${esc(path)}')">← Back to history</button>`;
    html += renderDiff(data);
    content.innerHTML = html;
  } catch (e) {
    content.innerHTML = '<div class="loading">Error loading diff</div>';
  }
}

// ── Markdown Rendering ──
function renderMarkdown(md) {
  const renderer = new marked.Renderer();
  const originalCode = renderer.code;

  renderer.code = function ({ text, lang }) {
    if (lang === 'mermaid') {
      return `<div class="mermaid-container"><pre class="mermaid">${esc(text)}</pre></div>`;
    }
    const highlighted = lang && hljs.getLanguage(lang)
      ? hljs.highlight(text, { language: lang }).value
      : esc(text);
    return `<pre><code class="hljs language-${esc(lang || '')}">${highlighted}</code></pre>`;
  };

  return marked.parse(md, { renderer, breaks: false, gfm: true });
}

function highlightCode(container) {
  container.querySelectorAll('pre code:not(.hljs)').forEach(block => {
    hljs.highlightElement(block);
  });
}

async function renderMermaidBlocks(container) {
  const blocks = container.querySelectorAll('.mermaid');
  for (let i = 0; i < blocks.length; i++) {
    const block = blocks[i];
    const code = block.textContent;
    try {
      const id = 'mermaid-' + Date.now() + '-' + i;
      const { svg } = await mermaid.render(id, code);
      block.parentElement.innerHTML = svg;
    } catch (e) {
      console.warn('Mermaid render error:', e);
      block.parentElement.innerHTML = `<pre style="color:var(--red)">Mermaid error: ${esc(e.message || String(e))}\n\n${esc(code)}</pre>`;
    }
  }
}

// ── Diff Rendering ──
function renderDiff(data) {
  let html = '<div class="diff-container">';
  html += `<div class="diff-label">${esc(data.label || 'Diff')}</div>`;

  if (!data.hasChanges || !data.diff) {
    html += '<div class="diff-empty">No changes</div>';
    html += '</div>';
    return html;
  }

  const lines = data.diff.split('\n');
  for (const line of lines) {
    let cls = '';
    if (line.startsWith('+') && !line.startsWith('+++')) cls = 'add';
    else if (line.startsWith('-') && !line.startsWith('---')) cls = 'del';
    else if (line.startsWith('@@')) cls = 'hunk';
    else if (line.startsWith('diff ') || line.startsWith('index ') || line.startsWith('---') || line.startsWith('+++')) cls = 'meta';
    html += `<div class="diff-line ${cls}">${esc(line)}</div>`;
  }

  html += '</div>';
  return html;
}

// ── Utilities ──
function esc(str) {
  if (!str) return '';
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

function timeAgo(ms) {
  const secs = Math.floor((Date.now() - ms) / 1000);
  if (secs < 60) return 'just now';
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(months / 12)}y ago`;
}

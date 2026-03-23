# MDS — Markdown Spec Server

## Problem Statement

Developers working with AI on remote servers need to read markdown spec/design files from mobile devices. Terminal-based markdown reading on a phone is painful — files are scattered, hard to find, and impossible to read nicely. There's no simple way to see what changed recently or view diffs of spec files.

**Target user:** Developer on mobile (phone/tablet) accessing a remote dev server over Tailscale.

## Architecture

```mermaid
graph TD
    A[Mobile Browser] -->|HTTP over Tailscale| B[mds Go Server]
    B -->|embed.FS| C[Static Assets<br>HTML/CSS/JS]
    B -->|os.ReadFile| D[Project .md Files]
    B -->|exec.Command| E[git CLI]
    E -->|ls-files| F[File Discovery]
    E -->|diff/log| G[Change Detection]
    C -->|Client-side| H[marked.js + highlight.js + mermaid.js]
```

**Single Go binary** with all assets embedded via `//go:embed`. Zero external dependencies at runtime. Client-side rendering — server sends raw markdown, browser renders it.

### Project Structure

```
mds/
├── main.go                    # Go server (all backend logic)
├── go.mod                     # Go module
├── static/
│   ├── index.html             # SPA shell
│   ├── app.js                 # Frontend logic (routing, rendering, UI)
│   ├── style.css              # Mobile-first responsive styles
│   └── vendor/                # Vendored JS/CSS (embedded in binary)
│       ├── marked.min.js      # Markdown parser
│       ├── highlight.min.js   # Syntax highlighting
│       ├── highlight-light.min.css
│       ├── highlight-dark.min.css
│       ├── hljs-{lang}.min.js # Language grammars (go, yaml, json, typescript,
│       │                      #   javascript, python, bash, sql, dockerfile, protobuf)
│       └── mermaid.min.js     # Diagram rendering
└── specs/
    └── spec.md                # This file
```

## Usage

```bash
# Serve current directory
mds

# Serve a specific project
mds /path/to/project
```

- Project name is derived from the directory name (e.g., `/home/user/myapp` → "myapp")
- Listens on `0.0.0.0` (accessible over Tailscale)
- Prints bound address to stdout on startup

## API Specification

### `GET /api/recent`

Returns recent file changes grouped by git commit. Used by the file list page to show commit-grouped activity.

**Response:**
```json
{
  "project": "myapp",
  "isGit": true,
  "groups": [
    {
      "type": "uncommitted",
      "message": "",
      "shortHash": "",
      "age": "",
      "date": 0,
      "files": [
        { "path": "specs/auth.md", "name": "auth.md", "dir": "specs", "modTime": 1710000000000 }
      ]
    },
    {
      "type": "commit",
      "message": "add auth flow",
      "shortHash": "abc1234",
      "age": "2 hours ago",
      "date": 1710000000000,
      "files": [
        { "path": "specs/auth.md", "name": "auth.md", "dir": "specs", "modTime": 1710000000000 },
        { "path": "specs/auth-api.md", "name": "auth-api.md", "dir": "specs", "modTime": 1710000000000 }
      ]
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `groups[].type` | string | `"uncommitted"` or `"commit"` |
| `groups[].message` | string | Commit subject line (empty for uncommitted) |
| `groups[].shortHash` | string | Abbreviated commit SHA |
| `groups[].age` | string | Human-readable relative time |
| `groups[].date` | int64 | Unix milliseconds |
| `groups[].files[]` | array | Files in this group (max 4 per group) |

- Uncommitted group appears first (if any uncommitted .md changes exist)
- Up to 5 commit groups from `git log --name-only`
- Files may appear in both uncommitted and commit groups (intentional — shows work-in-progress on top of last commit)
- Non-git repos return `isGit: false` with empty groups

### `GET /api/related?path=<relative-path>`

Returns files related to the given file, scored by 3 weighted signals.

**Response:**
```json
{
  "related": [
    {
      "path": "specs/auth-api.md",
      "name": "auth-api.md",
      "dir": "specs",
      "score": 0.85,
      "signals": ["linked", "similar"]
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `related[].path` | string | Relative path from project root |
| `related[].name` | string | File basename |
| `related[].dir` | string | Parent directory |
| `related[].score` | float | Composite score (0–1) |
| `related[].signals` | array | Which signals contributed: `"linked"`, `"similar"`, `"nearby"` |

**Scoring algorithm (3 signals):**

| Signal | Weight | Method |
|--------|--------|--------|
| Cross-references | 0.45 | Parse `[text](path)` markdown links. Bidirectional: file links to target (1.0) or target links to file (0.8) |
| Heading similarity | 0.30 | Extract H1–H3 headings + filename tokens. Jaccard similarity on lowercase tokens with stopword filtering |
| Directory proximity | 0.25 | Same dir=1.0, parent/child=0.6, sibling dirs=0.4, shared prefix=0.2, unrelated=0.0 |

Returns top 8 results with score >0.05, sorted by score descending.

### `GET /api/files`

Returns all `.md` files in the project directory, sorted by modification time (newest first).

**Response:**
```json
{
  "project": "myapp",
  "isGit": true,
  "files": [
    {
      "path": "docs/architecture.md",
      "name": "architecture.md",
      "dir": "docs",
      "modTime": 1710000000000,
      "changed": true
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `project` | string | Directory base name |
| `isGit` | bool | Whether project is in a git repo |
| `files[].path` | string | Relative path from project root |
| `files[].name` | string | File basename |
| `files[].dir` | string | Parent directory (`.` for root) |
| `files[].modTime` | int64 | Unix milliseconds of last modification |
| `files[].changed` | bool | Has uncommitted git changes |

### `GET /api/content?path=<relative-path>`

Returns raw markdown content of a file.

- **Response:** `text/plain; charset=utf-8`
- **400** if path is missing, absolute, or contains `..`
- **404** if file doesn't exist

### `GET /api/diff?path=<relative-path>[&commit=<hash>]`

Returns git diff for a file. If `commit` is provided, shows diff for that specific commit.

**Response:**
```json
{
  "diff": "unified diff text...",
  "hasChanges": true,
  "label": "Uncommitted changes"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `diff` | string | Unified diff text (empty if no changes) |
| `hasChanges` | bool | Whether diff content exists |
| `label` | string | Human-readable description of what's shown |

**Without `commit` param (default diff resolution):**
1. `git diff HEAD -- <file>` → uncommitted changes (staged + unstaged vs HEAD)
2. If empty: `git log -1` to find last commit that touched the file, then `git diff <commit>~1 <commit> -- <file>`
3. If first commit: synthesize a diff showing entire file as additions
4. If not a git repo: `label: "Not a git repository"`

**With `commit` param:**
1. Validate commit hash (hex characters only)
2. `git diff <commit>~1 <commit> -- <file>` → diff for that specific commit
3. If first commit: synthesize full-file addition diff
4. Label includes commit message and relative age (e.g., `"add license section (2 hours ago)"`)

### `GET /api/history?path=<relative-path>`

Returns git commit history for a file (up to 50 commits, follows renames).

**Response:**
```json
{
  "commits": [
    {
      "hash": "729a54c31e66...",
      "shortHash": "729a54c",
      "message": "add copyright year",
      "author": "pkomsit",
      "date": 1710000000000,
      "age": "2 hours ago"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `commits[].hash` | string | Full commit SHA |
| `commits[].shortHash` | string | Abbreviated commit SHA |
| `commits[].message` | string | Commit subject line |
| `commits[].author` | string | Author name |
| `commits[].date` | int64 | Unix milliseconds of commit date |
| `commits[].age` | string | Human-readable relative time |

Uses `git log --follow` to track file renames. NUL-delimited format for safe parsing.

## File Discovery

### Git repo (preferred)
1. `git ls-files --full-name '*.md' '**/*.md'` — tracked `.md` files
2. `git ls-files --others --exclude-standard '*.md' '**/*.md'` — untracked but not ignored
3. Merge both lists, deduplicate

This automatically respects `.gitignore` rules.

### Non-git fallback
- `filepath.WalkDir` recursive scan
- Skip directories: `node_modules`, `vendor`, `.git`, `__pycache__`
- Match `*.md` (case-insensitive)

### Change detection (git only)
- `git diff --name-only HEAD` — staged + unstaged changes vs HEAD
- `git diff --name-only --cached` — staged changes
- `git ls-files --others --exclude-standard` — untracked `.md` files
- All paths converted from git-root-relative to project-dir-relative

**No file watching or caching.** Full scan on every `/api/files` request. Fast enough for typical projects (milliseconds).

## Frontend (SPA)

### Routing

Hash-based routing, no server-side routing needed:

| Hash | Page |
|------|------|
| `#/` | File list |
| `#/view/<encoded-path>` | View a file |

### File List Page (`#/`)

Two sections:

1. **Recent Activity** — files grouped by git commit
   - Fetches from `/api/recent`
   - **Uncommitted** group at top (if any): shows files with **M** badge, sorted by mod time
   - **Commit groups** below: each shows commit message + age as header, with files listed underneath
   - Up to 5 commit groups, each showing up to 4 files
   - Wide screens (≥1024px) show 20 items; mobile shows 10
   - Files may appear in both uncommitted and a commit group (intentional)
   - On mobile (<600px): directory path hidden to save space
   - Falls back to flat mod-time sorted list for non-git repos

2. **All Files** — collapsible directory tree
   - Directories sorted before files, both alphabetical
   - Folders collapsible with ▶ arrow toggle
   - **M** badge on changed files

**Filter toggle** (only shown when `isGit` is true):
- "Changed only" button in header toolbar
- When active: both sections filter to only `changed: true` files
- Button shows `✓ Changed only` with active styling when enabled

### View Page (`#/view/<path>`)

**Header:** Project name linking back to `#/`

**Breadcrumb:** `☰ toggle button` + `📄 project / dir / **filename.md**` — project links back to file list

**Content toolbar:**
- `📖 Read` — rendered markdown (default)
- `± Diff` — colorized unified diff

**Left sidebar** (collapsible, with two tabs):

#### Sidebar: Related Tab
- Fetches from `/api/related?path=...` (non-blocking, never delays content rendering)
- Results grouped by signal type:
  - **Linked** — files connected by markdown links (bidirectional)
  - **Similar** — files with overlapping heading terms
  - **Nearby** — files in the same or sibling directories
- Each item shows filename (accent color, clickable) + directory (muted)
- Clicking navigates to that file; sidebar stays open, caches cleared
- Cached in memory — no re-fetch on tab switch back

#### Sidebar: History Tab
- Fetches from `/api/history?path=...` (only when tab is clicked)
- Shows list of commits that touched this file (up to 50, follows renames)
- Each entry: short hash (styled as code badge), commit message, author, relative time
- Clicking a commit switches content area to Diff mode showing that commit's diff
- Cached in memory — no re-fetch on tab switch back

#### Sidebar UX
- **Toggle:** `☰` button in breadcrumb row (shows `✕` when open)
- **Default state:** open on wide screens (≥1024px), closed on mobile
- **Persistence:** open/close state saved in `localStorage` (`mds-sidebar`)
- **Desktop (≥768px):** inline flex layout, sidebar pushes content right (240px wide, 24px content padding)
- **Mobile (<768px):** overlay drawer from left (280px wide) with semi-transparent backdrop; clicking backdrop closes
- **No animations:** instant show/hide

#### Read Mode
- Markdown rendered client-side with `marked.js` (GFM enabled)
- Code blocks: syntax highlighted with `highlight.js`
  - Language detection via ` ```lang ` fence
  - Fallback: auto-detect for unfenced blocks
- Mermaid blocks (` ```mermaid `): rendered as SVG diagrams
  - Wrapped in scrollable/zoomable container (`.mermaid-container`)
  - `max-height: 80vh` with overflow scroll
  - Theme follows system dark/light preference
- Tables, blockquotes, images, links all styled

#### Diff Mode
- Fetches from `/api/diff?path=...`
- Shows label at top (e.g., "Uncommitted changes", "Last commit (2 hours ago)")
- Colorized unified diff:
  - `+` lines: green background
  - `-` lines: red background
  - `@@` hunk headers: yellow background
  - `diff`, `index`, `---`, `+++` metadata: muted bold
- If no changes: centered "No changes" message
- Monospace font, `pre-wrap` with `break-all` for mobile

## Styling

### Design System (CSS Custom Properties)

```
Light mode:
  --bg:              #ffffff     (page background)
  --bg-secondary:    #f6f8fa     (code blocks, cards)
  --bg-hover:        #f0f2f5     (hover state)
  --text:            #1f2328     (primary text)
  --text-secondary:  #656d76     (muted text)
  --border:          #d0d7de     (borders, dividers)
  --accent:          #0969da     (links, active states)
  --accent-bg:       #ddf4ff     (active button background)
  --green/green-bg:  #1a7f37 / #dafbe1  (diff additions)
  --red/red-bg:      #cf222e / #ffebe9  (diff deletions)
  --yellow-bg:       #fff8c5     (diff hunk headers)
  --badge-changed:   #da5a0b / #fff1e5  (M badge)

Dark mode (prefers-color-scheme: dark):
  --bg:              #0d1117
  --bg-secondary:    #161b22
  --text:            #e6edf3
  --accent:          #58a6ff
  ... (GitHub-dark-inspired palette)
```

### Typography
- System font stack: `-apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif`
- Code font: `"SF Mono", "Fira Code", "Fira Mono", Menlo, Consolas, monospace`
- Base font size: `15px` (desktop), `14px` (mobile <600px)
- Line height: `1.6` (body), `1.7` (markdown content), `1.5` (code)

### Responsive Breakpoints
- `<600px` (mobile): smaller fonts, tighter padding, hide directory paths in file list

### Dark/Light Mode
- Three modes: auto (system preference), light, dark — cycled via `◐` button in header
- Auto mode follows `prefers-color-scheme` media query
- Preference stored in `localStorage` (`mds-theme`)
- Highlight.js theme swapped dynamically (light/dark CSS)
- Mermaid theme re-initialized on mode change

## Port Auto-Shifting

1. Try binding to `0.0.0.0:8090`
2. If port taken (`net.Listen` returns error), try `8091`, `8092`, ..., up to `8110`
3. If all taken, exit with error
4. Print actual bound port to stdout

This allows running multiple instances (one per project) simultaneously.

## Security

- **Path traversal protection:** All file paths are cleaned with `filepath.Clean`, rejected if they start with `..` or are absolute
- **No authentication:** Designed to run on Tailscale (private network)
- **Binds to `0.0.0.0`:** Accessible from any network interface (required for Tailscale)

## Vendored Dependencies

All JS/CSS libraries are downloaded and embedded in the binary. No CDN requests at runtime.

| Library | Version | Size | Purpose |
|---------|---------|------|---------|
| marked.js | 15.0.7 | 39KB | Markdown → HTML parser |
| highlight.js | 11.11.1 | 125KB | Syntax highlighting engine |
| highlight.js languages | 11.11.1 | ~32KB | go, yaml, json, typescript, javascript, python, bash, sql, dockerfile, protobuf |
| highlight.js themes | 11.11.1 | 2.6KB | github (light) + github-dark |
| mermaid.js | 11.5.0 | 2.5MB | Diagram rendering |

**Total embedded assets:** ~2.8MB (binary size ~11MB with Go runtime)

## Key Design Decisions

1. **Client-side rendering** — Server sends raw markdown, browser renders. Keeps server simple, offloads CPU to client, enables rich interactive rendering (Mermaid).

2. **No file watching / no caching** — Scan on every request. Simple, stateless, fast enough (typical project scans complete in <10ms).

3. **`git ls-files` for discovery** — Leverages git's own `.gitignore` machinery instead of reimplementing gitignore parsing.

4. **Hash-based routing** — Single `index.html` served for all routes. No server-side routing complexity. URLs are bookmarkable (`#/view/docs/arch.md`).

5. **Embedded assets** — Single binary deployment. Copy one file to any server, run it. No `npm install`, no asset directories.

6. **Diff fallback chain** — Always shows something useful: uncommitted changes → last commit diff → initial commit → "no changes".

## Out of Scope (Future)

- Multi-project support in one instance
- Text search across documents
- Auto-refresh / live reload (WebSocket/SSE)
- Authentication / authorization
- Control plane actions (builds, logs, agent management)

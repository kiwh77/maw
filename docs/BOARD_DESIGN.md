# Board design system: Workbench

> Languages: **English** | [简体中文](zh/BOARD_DESIGN.md)

The board is a read-only multi-project agent scheduling workbench. The first screen must answer: where are the tasks, who is running them, what needs attention, and what to do next.

## Status quo

The old page expanded all ten task statuses; empty columns shared width with live work, so the main flow and done work often sat outside the horizontal viewport. Next steps, risks, filters, agents, and stats shared similar visual weight without a stable reading order. Titles sat next to project tags and IDs, wrapping poorly; copy actions competed with body text.

## Design direction

Visual thesis: calm and precise like a developer tool—graphite surfaces, fine dividers, signal green for sustained work.

Content structure: project & Agent sidebar → workspace location → filtered stats → filters & search → task flow → scheduling hints; details open in a right drawer. Sidebar Agents show only working (not idle/offline). On small screens the sidebar hides; projects remain selectable via filters.

Interaction thesis: light entrance for hierarchy; hover shows cards are openable; drawer expands a short distance. Respect reduced-motion by disabling transitions.

## Visual tokens

All tokens live in `internal/board/page.html` `:root`—no font/icon CDN. Light and dark share semantic tokens and structure.

| Token | Dark | Light | Use |
| --- | --- | --- | --- |
| `--bg` | `#141618` | `#f7f8f9` | Main workspace |
| `--panel` | `#191c1f` | `#eef1f3` | Sidebar, filters, drawer |
| `--card` | `#202427` | `#ffffff` | Openable task objects |
| `--line` | `#2d3338` | `#dce1e5` | Separators, card edges |
| `--text` | `#e6eaed` | `#20282f` | Titles, body |
| `--muted` | `#9ba5ad` | `#5b6871` | Labels, secondary |
| `--accent` | `#9cdbb0` | `#267044` | Join actions, selection, progress |
| `--warn` | `#e5bc78` | `#8a601f` | Attention states |
| `--danger` | `#ed9797` | `#b53e45` | Blocked, conflict, sync failure |
| `--radius` | `6px` | `6px` | Forms, tasks |

Theme defaults to system (`prefers-color-scheme`). Top-right supports System / Light / Dark; choice persists in localStorage. Body copy uses system sans; IDs, paths, and commands use monospace. UI language (en/zh) is switchable and stored as `maw-board-lang`.

## Status lanes

| Stage | Exact statuses |
| --- | --- |
| Queued | backlog, ready |
| Running | claimed, in_progress, self_check |
| Review & merge | in_review, merging |
| Needs attention | conflict, blocked |
| Finished | done, cancelled |

Cards keep exact status labels; filters support every status. Hide the attention lane when empty. Unknown statuses get their own column. Columns scroll independently; only the flow area scrolls horizontally when narrow.

## Views

The workspace toggles **Board** vs **Dependency topology** (`maw-board-view`); the board keeps rendering status lanes. **Topology is available only when a single project is selected**; choosing **All projects** forces the board view and disables the topology control (stacked multi-project graphs are unreadable).

Dependency topology is drawn with **X6** on a free canvas, ordered by **wave**: a wave is the dependency depth, so prerequisites sit on the left and dependents on the right. Wave columns, orthogonal rounded edges, and nodes show ID, title, status, owner, priority, and in/out degree; dashed edges and stub nodes mark dependencies outside the current filter. The canvas pans by dragging empty space, zooms with Ctrl/⌘ + wheel, and the toolbar offers fit and relayout; hovering highlights neighbouring dependencies and dims the rest, and theme switches redraw immediately. Filters and search apply to the board and (single-project) topology; click, Enter, or Space opens the drawer.

### Build and assets

- Front-end lives in `internal/board/web` (**Vite** + TypeScript + `@antv/x6`): entry `src/main.ts`, topology rendering in `src/topology.ts`.
- `npm run build --prefix internal/board/web` emits the IIFE bundle `internal/board/assets/dist/board-web.js` (global `MawBoard`, no sourcemap). The root `.gitignore` rule for `dist/` also matches this directory, so the output must be added explicitly with `git add -f`.
- Go embeds it via `//go:embed assets/dist` and serves it under `/assets/*` with `no-store`, no directory listing, and 404 for directory requests; `page.html` loads it through `<script id="board-web" src="assets/board-web.js" defer>`.
- When the bundle is unavailable the page falls back to the legacy SVG topology. Colours come only from `page.html` `:root` semantic tokens; the bundle never hard-codes them.

## Interaction

- Filters apply immediately; reset also clears search; `/` focuses search.
- Project nav syncs with the project select; selection writes URL `#projectId`.
- Cards open with click / Enter / Space; copy controls have accessible names.
- Drawer traps focus; Escape / close / mask dismiss it.
- SSE plus 3s polling; identical task content does not rebuild lists.
- Sync failure keeps last good data and shows failure status.

## Live notifications

`GET /api/events` is `text/event-stream`: `ready` on connect, `board-changed` on data change, comment heartbeats every 15s. Suggested reconnect 3s. Clients refetch snapshots; they do not replay history.

References: [MDN SSE](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events), [prefers-color-scheme](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/At-rules/@media/prefers-color-scheme), [Go ResponseController](https://pkg.go.dev/net/http#ResponseController).

## Verification

Front-end: `node --test internal/board/page.test.cjs`. Project: `go test ./...` and `go build ./cmd/maw`. Visual checks cover real data, detail drawer, search, filters, copy, narrow layout, empty and error states.

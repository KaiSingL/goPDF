# Modern PDF Reader — Development Plan

Execution plan for `Modern-Pdf-Spec.md`, verified against the real MyGo (v0.6.4) and
go-fitz toolchains on this machine (Windows 11, x64).

## 0. Spec corrections (verified against docs)

The spec is directionally right; these details change:

| Spec says | Reality (verified) |
| --- | --- |
| Go 1.22+ | MyGo v0.6.4 requires **Go 1.27+** |
| `mygo.yml` | Config file is **`mygo.json`** |
| `App.SetFileAssociations()` API | File associations are declared in **`mygo.json` → `fileAssociations`** |
| `mygo.ShowOpenDialog()` | Dialogs live in the `mygo.Dialog` API (verify exact name in `/docs/native`) |
| Build custom tab bar / sidebar / find UI | MyGo ships **`ui.Tabs`, `ui.Sidebar`, `ui.ScrollView`, `ui.FindBar`, `ui.SearchField`, `ui.Tree`, `ui.Outline`, `ui.Image`** — use them |
| Command palette via "Launcher API" | Launchers doc is for Raycast-style apps. Our Ctrl+K palette is an **in-app overlay** (`/docs/ui/overlays`) |
| Render pool shares one `*fitz.Doc` | go-fitz: **each goroutine needs its own `*fitz.Document`** (separate MuPDF contexts). Never touch a Document concurrently |
| zig for cross-compile | Not needed on Windows: go-fitz **bundles prebuilt static MuPDF** (`libs/libmupdf_windows_amd64.a`). Need mingw-w64 gcc as `CC` |
| `mygo.NewDocuments` | That API is for editable document apps (one window per file). We are a **read-only tabbed reader in one window** — use `App.OnOpenFile` + `RequestSingleInstanceLock` + `OnSecondInstance` directly |

Also noted: bundled MuPDF libs are built **without CJK fonts** (v1 limitation, document it),
and MuPDF/go-fitz is **AGPL-3.0** — the reader must ship AGPL (fine per spec §10, open source).

## 1. Environment setup (done by lead agent before spawning builders)

1. Install Go 1.27+ (winget `GoLang.Go`) — in progress.
2. Install mingw-w64 gcc (choco `mingw` or portable WinLibs) → set `CC`.
3. `git init` the repo (safety/rollback for multi-agent work).
4. Scaffold app: `go run github.com/egoist/mygo/cmd/mygo@latest init -template native -module modern-pdf modern-pdf`
   inside `C:\dev\goPDF` → produces `modern-pdf/{main.go, main_test.go, go.mod (CLI as tool), mygo.json, resources/icon.png, .agents/skills/mygo-maintenance/}`.
5. `go get github.com/gen2brain/go-fitz` + `go get github.com/egoist/mygo@latest`.
6. **Smoke test**: tiny program that opens a test PDF via fitz and renders page 0 to PNG,
   built with `CGO_ENABLED=1 CC=<mingw-gcc>`. Gate: everything else depends on cgo working.
7. Write contract files (see §3), commit baseline.

## 2. Architecture

Module `gopdf`, app root `C:\dev\goPDF` (no extra nesting):

```
C:\dev\goPDF\
├─ main.go                 [D] lifecycle, single instance, menus, file assoc events, window
├─ main_test.go            [D] headless smoke of the shell
├─ mygo.json               [D] name/identifier/fileAssociations(pdf)
├─ cmd/smoke/              [lead] toolchain + perf smoke test (fitz end-to-end)
├─ internal/pdf/           [A] NO mygo imports — pure Go + go-fitz + stdlib (testable headless)
│  ├─ pdf.go               Doc: Open, worker goroutine threading model, RenderAsync, IndexAsync,
│  │                       outline tree, page geometry, rotation; RenderReq/CacheKey/OutlineNode/TextSpan/SearchHit
│  ├─ cache.go             PageCache: thread-safe LRU of rendered pages by pixel bytes
│  ├─ search.go            Index: span extraction from MuPDF stext HTML + substring search with hit rects
│  └─ *_test.go            tests against generated PDFs (gen_test.go)
├─ internal/app/           [B,C,D] MyGo ui code
│  ├─ state.go             [lead] AppState, Tab, actions (frozen contract; UI-thread rules in header)
│  ├─ components.go        [lead] cross-module seam: stub functions per module + window/update/Update,
│  │                       SetWindow, SearchFocus; each owner deletes its stubs when implementing
│  ├─ view.go              [B] root layout: shortcuts / tabBar / toolbar / sidebar+viewer / statusBar / paletteOverlay
│  ├─ viewer.go            [B] scroll view, zoom/rotate/fit, page tracking, render wiring, highlights
│  ├─ gen_test.go          [B] test-only PDF generator for package app tests
│  ├─ tabs.go              [C] tab bar + close + "+" + reopen-closed
│  ├─ toolbar.go           [C] nav back/forward, reload, search field (FocusBind←SearchFocus), zoom controls
│  ├─ sidebar.go           [C] virtualized thumbnails @72dpi + outline tree
│  ├─ statusbar.go         [C] page X/Y input, fit mode, zoom %, dark toggle
│  ├─ palette.go           [D] Ctrl+K overlay: recent docs, outline jump, page jump, commands
│  ├─ shortcuts.go         [D] all spec §5 shortcuts + openFilePicker
│  ├─ theme.go             [D] theme mode cycle (auto/dark/light) via mygo theme API
│  └─ settings.go          [D] persisted settings (JSON in user data dir)
```

Rendering pipeline (spec §3 honored): UI thread never blocks on fitz. `pdf.Renderer`
runs a worker pool; results are posted back with `Window.Update(func(){...})`.
Zoom-drag only scales the cached bitmap (Painter transform / element size);
a 150ms-debounced re-render at the settled DPI follows.

## 3. Contracts (written by lead before parallel build)

- `internal/app/state.go`: `AppState`, `Tab`, `ZoomMode`, `SearchState` per spec §6
  plus method **signatures** (bodies stubbed): `OpenTab(path)`, `CloseTab(id)`,
  `Navigate(page)`, `SetZoom(modeOrFactor)`, `Rotate(deg)`, `ToggleSidebar()`,
  `StartSearch(q)`, `NextHit()/PrevHit()`, `ToggleTheme()`. Builders may not change
  signatures — only D fills in bodies; B/C only call them.
- `internal/pdf/doc.go`: `Document` API + types (`OutlineNode`, `TextBlock`, `SearchHit`,
  `RenderRequest/RenderResult`). A implements; B/C/D consume. Package imports no mygo.
- Both files compile at baseline; each agent must keep `go build ./...` green.

## 4. Parallel build (4 subagents, model magpie/zai/glm-5.3-flash#high)

All agents: read `.agents/skills/mygo-maintenance/SKILL.md` + cited MyGo docs **before**
coding; verify every ui.* API against docs (never invent); never run the GUI;
verify with `CGO_ENABLED=1 go build ./... && go vet ./...` (+ `go test` where they own tests);
disjoint file ownership; no edits outside owned files except adding new files they own.

- **A — pdf-core**: `internal/pdf/**`. Render + cache + search + outline + pool + tests.
  Must respect fitz one-Document-per-goroutine. Gate: `go test ./internal/pdf/`.
- **B — viewer**: `internal/app/{view,viewer}.go` + `view_test.go`. Continuous scroll,
  zoom/rotate/fit modes, ctrl+wheel, page tracking, render-on-demand wiring to pool,
  page shadow/centering, max-width 900, thumbnails request hooks. Read: /docs/ui,
  /docs/ui/{views,layout,input,scroll,image,drawing,custom-widgets,testing,windows}.
- **C — chrome**: `internal/app/{tabs,toolbar,sidebar,statusbar}.go` + tests. Read widget
  docs: /docs/ui/{tabs,sidebar,toolbar,search-field,find-bar,tree,outline,list,icon,segmented}.
- **D — shell**: `main.go`, `mygo.json`, `internal/app/{actions,shortcuts,palette,theme,settings}.go`.
  Read: /docs/{app,windows,menus,native,documents(opening-files part),configuration}, /docs/ui/overlays.

Integration by lead after all four: build, vet, test, run app once, fix seams, commit.

## 5. Verification gates

1. cgo smoke test renders a PDF (§1.6) — before spawning.
2. Each agent: package builds + tests green.
3. Integration: `go tool mygo build` produces exe; manual open/scroll/zoom/search.
4. Perf sanity vs spec §9 (100MB PDF <300ms first page, RAM targets) — measure, record.

## 6. Milestones (map to spec §12)

- M1 = setup + contracts + A(render first page) + B(min viewer) 
- M2 = C(tabs/sidebar/outline) + D(navigation/history)
- M3 = search/zoom/rotate/file-assoc/recent (A+B+C+D contributions)
- M4 = polish: palette, dark mode, shortcuts, installer

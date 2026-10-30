# Modern PDF Reader - Product & Technical Spec
## Stack: MyGo native UI + go-fitz (MuPDF cgo)

### 1. Vision
Replace SumatraPDF's legacy Win32 UI with a modern, GPU-drawn, tabbed reader that keeps Sumatra's core promise: instant open, low RAM, keyboard-driven, but with modern UX like Arc/Linear - tabs, command palette, dark mode, thumbnail sidebar.

**Goals:**
- Open 100MB PDF in <300ms to first page visible
- <70MB RAM idle, <150MB with 3 pages cached at 200 DPI
- Single binary, Windows 10/11 x64/arm64 primary, macOS/Linux secondary
- Feels like a native tool, not a browser

**Non-goals v1:**
- PDF editing, form filling, annotation creation (read-only + search + select)
- OCR, PDF creation

### 2. Why MyGo + go-fitz (MuPDF)

- **MyGo native UI:** Draws itself on GPU with Direct3D11 on Windows, Metal on macOS, no HTML/JS/webview. Window opens at once, low memory. View is a function of state: `func (s *State) view(c *ui.Context)`
- **go-fitz:** Go binding to libmupdf (libfitz). C, highly optimized, SIMD, small. API: `fitz.New()`, `doc.Image(n)`, `doc.Text()`, `doc.NumPages()`. Fastest open-source renderer.
- Tradeoff: Using cgo means we lose MyGo's "any machine can compile every platform" - need zig/clang for cross-compile. Acceptable for best perf.

### 3. Architecture

```
App (Go main)
├─ MyGo App lifecycle, single instance, file associations (.pdf)
├─ Window Manager (tabs)
│  └─ Each Tab = Document
│     ├─ DocHandle (go-fitz *fitz.Doc)
│     ├─ PageCache LRU (prev, current, next as image.RGBA)
│     ├─ OutlineCache (TOC)
│     └─ SearchIndex (text per page)
└─ Native UI State
   ├─ Sidebar (Thumbnails / Outline)
   ├─ Toolbar (tabs, search, zoom)
   └─ Viewer (ui.Scroll + ui.Image)
```

Rendering pipeline:
1. User opens file -> Go opens with fitz.New(path)
2. Background goroutine renders page N at target DPI via doc.ImageDPI(N, 200)
3. Convert *image.RGBA to ui.ImageSource via ui.NewImageSourceFromRGBA()
4. Invalidate UI -> view() draws ui.Image
5. Pre-render N+1, N-1 in background pool (worker count = NumCPU)

No re-render on zoom drag - scale cached image with FitWidth(), re-render debounced 150ms after zoom stops.

### 4. UI Spec (matches mockup)

**Window chrome:**
- Custom titlebar, rounded 12px, MyGo Glass blur optional
- Top: tab bar like browser, + button, drag to reorder
- Second bar: back/forward per tab, reload, search input (⌘+F / Ctrl+F), zoom - / 100% dropdown / +, share, comment (disabled v1), more menu, profile

**Layout:**
- Left sidebar 280px collapsible: Toggle between Thumbnails / Outline
  - Thumbnails: virtualized list, lazy render at 72 DPI, highlight current
  - Outline: tree from MuPDF outline (doc.Outline()), click to jump
- Center: scrollable page view, dark mode paper with shadow, max width 900px, centered
- Bottom status: Page X of Y, Fit Width / Fit Page, Zoom, Dark Mode toggle, Autosaved indicator

**Dark mode:**
- UI chrome dark, page inverted via custom? v1: keep page white, UI dark. v2: smart invert via MuPDF sepia.

**Interactions:**
- Scroll wheel: scroll page, Ctrl+scroll = zoom
- Drag & drop PDF to open new tab
- Double-click tab bar to open file dialog

### 5. Feature Spec

**MVP v1.0:**
- File open: dialog, drag-drop, file association double-click, recent docs jump list, single instance reuses window
- Tabs, close, reopen closed tab
- Rendering: fit width, fit page, custom zoom 25%-400%, rotate 0/90/180/270
- Navigation: thumbnails, outline, page number input, back/forward history
- Search: in-document search with highlights (use doc.Text() + search), highlight all, next/prev
- Copy text selection (MuPDF text selection rectangles)
- Print via MyGo printing API
- Keyboard shortcuts: Ctrl+O, Ctrl+W, Ctrl+T, Ctrl+F, Ctrl+-, Ctrl+=, Ctrl+0, PageDown/Up, Home/End
- Dark mode for chrome, respects system
- Settings: default zoom, default layout, sidebar default

**v1.5:**
- Command palette (MyGo Launcher API): Spotlight-style ⌘+K to open recent, search outline, go to page
- Text selection + copy with proper line breaks
- Search highlights rendered as overlay on ui.Image using ui.Canvas

**v2.0:**
- Annotations display (highlight, underline from file)
- Basic highlight creation (requires write, needs MuPDF save - AGPL implications)
- Password-protected PDFs
- Presentation mode

### 6. Data Model

```go
type Tab struct {
  ID       string
  Path     string
  Doc      *fitz.Doc
  NumPages int
  CurPage  int
  Zoom     float64 // 1.0 = 100%
  Rotate   int
  History  []int
  Outline  []OutlineNode
  Search   *SearchState
}

type PageCacheEntry struct {
  Page int
  DPI  float64
  Img  *image.RGBA
  UISource ui.ImageSource
  LastUsed time.Time
}

type AppState struct {
  Tabs      []*Tab
  ActiveTab int
  SidebarTab string // "thumbnails" | "outline"
  ShowSidebar bool
  IsDark bool
}
```

### 7. MyGo Integration Points

- `mygo.App.WhenReady()` + `mygo.NewWindow(WindowOptions{Title, Content: ui.View(app.view), Width: 1200, Height: 800})`
- File associations: `mygo.App.SetFileAssociations([]FileAssociation{{Ext: "pdf"}})`
- Recent docs: `mygo.App.AddRecentDocument(path)`
- Single instance: `mygo.App.RequestSingleInstanceLock()` + OnSecondInstance open tab
- Dialogs: `mygo.ShowOpenDialog()`
- Menus: App menu with File, Edit, View, Go, Window
- Global shortcut: Ctrl+K for launcher

### 8. Build & Distribution

- Requires: Go 1.22+, zig for cross-compile (for cgo), libmupdf source bundled or via pkg-config
- Build: `CGO_ENABLED=1 CC="zig cc -target x86_64-windows-gnu" go tool mygo build -o dist/reader.exe`
- For Windows: include `libmupdf` DLL or static link via `go-fitz` static build
- `mygo doctor` will warn about cgo toolchain - expected
- Distribution: `mygo build --installer msi` for Windows, dmg for macOS
- Auto-updater: MyGo Updater module

### 9. Performance Targets

- Cold open empty window: <150ms (MyGo native benefit)
- Open 10MB PDF to first page: <250ms
- Search 1000-page PDF: <500ms to index (using doc.Text() in goroutine pool)
- Page render cache hit: <16ms frame
- Memory: <70MB idle, <150MB 3 pages @200DPI

### 10. Licensing & Risk

- MuPDF is AGPL-3.0. If you distribute binary without open-sourcing your reader, you must buy commercial license from Artifex. For open-source project, AGPL is fine if your reader is also AGPL.
- Alternative if you want MIT/BSD commercial: swap go-fitz for `klippa-app/go-pdfium` WASM mode (BSD). Spec remains same, only DocHandle changes.
- Risk: cgo cross-compile complexity - mitigate with zig and CI (GitHub Actions windows-latest with mingw)

### 11. Project Structure

```
modern-pdf/
├─ main.go (App setup, single instance)
├─ app/
│  ├─ state.go (AppState, Tab)
│  ├─ view.go (ui.Column/Row, Sidebar, Viewer)
│  ├─ tabs.go (tab bar)
│  └─ shortcuts.go
├─ pdf/
│  ├─ doc.go (wrapper around fitz.Doc, outline, text)
│  ├─ cache.go (LRU page cache)
│  └─ search.go
├─ assets/
└─ mygo.yml
```

### 12. Milestones

M1 (2 days): MyGo native init + go-fitz open + render first page as ui.Image
M2 (3 days): Tabs, sidebar thumbnails (low DPI), outline, page navigation
M3 (3 days): Search, zoom, rotate, file assoc, recent docs
M4 (2 days): Polish - dark mode, shortcuts, command palette, installer

### 13. Open Questions

- AGPL vs commercial license - will this be open source?
- Text selection: use fitz text selection or overlay with ui.Canvas hit test?
- Thumbnail DPI tradeoff: 72 vs 96 for speed?

---
End of spec

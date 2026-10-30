<div align="center">
  <img src="resources/icon.png" width="96" alt="GoPDF icon" />
  <h1>GoPDF</h1>
  <p><strong>A modern, GPU-drawn, tabbed PDF reader — instant open, low RAM, keyboard-driven.</strong><br/>
  Tabs, Ctrl+K palette, dark chrome, thumbnail sidebar, in-document search with highlights.</p>
</div>

---

Built with [MyGo](https://mygo.egoist.dev) native UI (Go only, no webview — drawn with
Direct3D 11 on Windows) and [MuPDF](https://mupdf.com) through
[go-fitz](https://github.com/gen2brain/go-fitz).

## Features

- Continuous page viewer: fit width/page, custom 25–400% zoom, rotate
- Virtualized thumbnail sidebar + outline tree, back/forward history
- In-document search with highlight overlays; text selection + copy
- Tabs, Ctrl+K command palette, dark/light/follow-system theme
- File associations, drag-drop, recents, single instance, printing
- Memory bounded by design: pixel caches are byte-budgeted, with a
  ~300 MB worst-case ceiling and RSS that drops when tabs close

## Status

**v0.1.0 MVP complete.** See [PLAN.md](PLAN.md) for the build plan and
[Modern-Pdf-Spec.md](Modern-Pdf-Spec.md) for the product spec.
Measured: 100-page doc opens in 23–33 ms, first 150-DPI render 37–58 ms.

## Development

Requires Go 1.27+ and a mingw-w64 gcc (cgo, for MuPDF). On this machine:

```powershell
. .\dev.ps1        # sets PATH, CGO_ENABLED=1, CC
go build ./...     # compile everything
go test ./...      # headless tests (pdf core + UI view tests)
go run ./cmd/smoke # fitz end-to-end smoke test + perf probe
go tool mygo dev   # run the app with live reload
go tool mygo build # package installers
```

## License

**AGPL-3.0** — see [LICENSE](LICENSE). MuPDF/go-fitz are AGPL, so this
reader ships under the same license; closed-source distribution needs a
commercial MuPDF license from Artifex.

## Notes & limitations

- The MuPDF builds bundled with go-fitz have **no CJK fonts**; CJK PDFs
  relying on embedded fonts render fine, others may miss glyphs.
- Password-protected PDFs surface an error in v1 (prompt planned for v2).

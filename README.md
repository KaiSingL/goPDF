# GoPDF

A modern, GPU-drawn, tabbed PDF reader for Windows/macOS/Linux — SumatraPDF's
core promise (instant open, low RAM, keyboard-driven) with a modern UX:
tabs, a ⌘/Ctrl+K command palette, dark chrome, thumbnail sidebar and
in-document search with highlights.

Built with [MyGo](https://mygo.egoist.dev) native UI (Go only, no webview)
and [MuPDF](https://mupdf.com) through [go-fitz](https://github.com/gen2brain/go-fitz).

## Status

Under active development — see [PLAN.md](PLAN.md) for the build plan and
[Modern-Pdf-Spec.md](Modern-Pdf-Spec.md) for the product spec.

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

## Notes & limitations

- **AGPL-3.0.** MuPDF/go-fitz is AGPL; this reader is open source and ships
  under the same license (spec §10). A commercial MuPDF license from Artifex
  is required for closed-source distribution.
- The MuPDF builds bundled with go-fitz have **no CJK fonts**; CJK PDFs that
  rely on embedded fonts render fine, others may miss glyphs (v1 limitation).
- Password-protected PDFs are surfaced as an error in v1 (prompt planned for v2).

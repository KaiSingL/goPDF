# goPDF — Roadmap

The v1.0 MVP is complete (see PLAN.md). This file tracks the remaining
features: the spec's v2.0 list plus the editing UX the vendored go-fitz
fork's PDF editing layer makes possible. `internal/pdf` (headless core)
and `internal/app` (MyGo UI, Windows-only builds) live behind the same
module; core work is testable on any platform, UI work is Windows-only.

## Working rules

- One branch per issue (`feat/<slug>`, cut from master). Merge into
  master only after: (1) all related headless tests pass
  (`CGO_ENABLED=1 go test ./internal/pdf/...` and the branch's own UI
  view tests), (2) `internal/app` + `main` cross-compile clean for
  `GOOS=windows` with mingw-w64 gcc, and (3) an independent OpenCode
  review of the branch diff has been acted on.
- `internal/pdf` stays free of mygo imports, pure Go + go-fitz, and
  honors the one-`*fitz.Document`-per-goroutine rule.
- The vendored fork (third_party/go-fitz) documents every new C helper
  as a numbered patch in its GOPDF_PATCHES.md.

## Issues

1. **Edit facade (core)** — `feat/pdf-edit-facade`. Wrap the fork's
   pdf_edit bindings in internal/pdf APIs (annotations, pages, save)
   with the render-thread worker model and reopen-oracle tests. This is
   the foundation of every editing feature below. Headless-testable.
2. **Password-protected PDFs (open + prompt)** — `feat/password-prompt`.
   internal/pdf: authenticate/permission surface. internal/app: modal
   password prompt on open (replaces the v1 hard error), retry loop,
   permission-aware UI (no annotate tools when `PermAnnotate` denied).
3. **Annotation creation UI** — `feat/annotate-tools`. Toolbar
   annotation modes (highlight, underline, strikeout, square, text
   note, ink), selection-to-highlight through the existing selection
   geometry, right-click delete on existing annots, save prompts (full
   or incremental). Depends on issue 1 (+2 for password docs).
4. **Presentation mode** — `feat/presentation-mode`. Fullscreen
   page-turner: one page at a time, dark chrome, click/arrows/keys to
   navigate, Esc exits, black background current page.
5. **CJK glyph fallback** — `feat/cjk-fallback`. The bundled MuPDF
   static libs ship without CJK fonts; non-embedded-font CJK PDFs miss
   glyphs. Investigate runtime fallback (system font loading via MuPDF)
   vs rebuilding bundled libs with the CJK font pack; document the
   chosen route in GOPDF_PATCHES.md.

Deferred (documented, not scheduled): smart dark-mode page inversion,
auto-updater, installer polish across more locales.

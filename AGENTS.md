# AGENTS.md — agent guidance for the goPDF repo

Windows PDF reader: MyGo native UI (Direct3D, no webview) + go-fitz (MuPDF 1.28)
through a vendored patched fork (third_party/go-fitz via a `replace` in go.mod;
every fork change is documented as a numbered patch in its GOPDF_PATCHES.md).
Module name is `gopdf`. Before coding, read `.agents/skills/mygo-maintenance/SKILL.md`
(MyGo lifetime/threading/testing rules), `ROADMAP.md` (the issue tracker), and
`internal/pdf/edit.go` (the editing facade's freshness contract and save-routing
semantics; reviewers and agents quote its comment block verbatim).

## Platform split — what runs where

- This agent dev machine is a Mac; runtime GUI checks happen ONLY on the user's
  Windows machine (dev.ps1 sets PATH/CGO/CC there). Never fake a run report.
- Headless here (darwin_arm64 works: `CGO_ENABLED=1 go build ./internal/...`):
  internal/pdf (the core), the vendored fork (`cd third_party/go-fitz && go build
  ./... && go vet ./...` and its test suite), and `cmd/smoke`.
- internal/app + internal/print + main are Windows-only: they do NOT compile
  natively on macOS (expected: internal/print is COM-only). Verify app-layer
  changes by cross-compile:
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build ./...`
  (GOARCH=amd64 is REQUIRED: without it the arm64 host fails inside runtime/cgo;
  mingw-w64 is installed via brew). Same-env `go vet ./...` in the same env.
  Cross-compiling proves only that it COMPILES: internal/app's test suite runs
  nowhere but the Windows machine, so run `. .\dev.ps1 ; go test ./internal/app/`
  there before merging any app-layer change (three app tests that could never
  run on the Mac slipped through this gap and shipped red to master).
- CGO_ENABLED=0 does NOT build here: the fork's document surface (search/selection
  quads, the PDF-layer patch 4 hooks) is cgo-only. Do not follow generic mygo
  advice telling you to run `CGO_ENABLED=0 go test ./...` in this repo.
- Fork edits are gated: new internal/pdf code may only WRAP the fork's Go
  surface. A change that seems to need a new C helper is a REAL decision:
  implement it in third_party/go-fitz, document it as the next numbered patch in
  its GOPDF_PATCHES.md, and prove the feature's oracle test RED first (the CJK
  fallback, patch 4, is the worked example).

## Commands

```
CGO_ENABLED=1 go test -race ./internal/pdf/...        # the core gate
cd third_party/go-fitz && go build ./... && go vet ./... && go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build ./...
CGO_ENABLED=1 go run ./cmd/smoke <pdf>                # fitz smoke + perf probe
go tool mygo dev                                      # run the app (Windows)
go tool mygo build                                    # package installers (Windows)
```

CJK fallback tests honor `GOPDF_TEST_NO_CJK=1` (force the skip) and
`GOPDF_TEST_CJK_FONT=<path>` (candidate override), so fontless machines can
still run the rest of the suite.

## Architecture map

- `internal/pdf` — headless core, NO mygo imports. One `*fitz.Document` per
  Doc, touched ONLY by its worker goroutine (hi/lo task lanes; the `go`
  statement in Open is the happens-before edge). Derived state (page count,
  sizes, origins, outline, permission snapshot) lives under `stMu`.
  - `edit.go` — the editing facade: annotations, page organization, save
    routing (POSIX: sibling temp+rename for same-path full saves, durable;
    Windows: demoted to incremental — MuPDF 1.28 removes+recreates same-path
    targets, a sharing violation while the doc's read stream is open).
    Incremental saves reconcile against open-time bytes. Read its contract
    before every change.
  - `password.go` — OpenWithPassword + the permission snapshot (CanAnnotate
    gates the annotation UI).
  - CJK fallback lives in the fork (patch 4): MuPDF 1.28's
    fz_install_load_system_font_funcs hooks; per-context record in the fz
    user slot, magic-checked; lazy load; silent failure keeps Latin rendering
    byte-identical.
- `internal/app` — the MyGo UI: viewer (render pipeline, selection, zoom),
  annotate.go (annotation tools + overlay), save.go, password.go (the modal
  prompt), palette.go (Ctrl+K), present.go (presentation mode: the View root
  branches BEFORE the reader's shortcuts so the slide view cannot leak chrome
  keys), state.go (AppState + tab lifecycle; activation always goes through
  the guarded SelectTabBy, never direct ActiveTab writes).
- A fitz Document's Close is NOT idempotent: one owner per Document (in
  tests, never `defer g.Close()` a doc from openGen — its t.Cleanup already
  closes it).

## Working conventions

- One branch per ROADMAP issue (`feat/<slug>`, cut from master); merge to
  master only after the headless gate, the Windows cross-compile, and an
  independent OpenCode review round (review-only briefs, verdict merge /
  fix-first, findings verified against the code then applied).
- Conventional commits, detailed bodies; no em dashes in user-facing strings
  or commit messages.
- Pushing is the user's task (gh unauthed on the dev machine).
- Deferred (documented in ROADMAP.md): ink annotations, smart dark-mode page
  inversion, auto-updater.

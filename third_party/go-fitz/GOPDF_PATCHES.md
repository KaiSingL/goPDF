# go-fitz (GoPDF fork)

Patched copy of github.com/gen2brain/go-fitz v1.28.2, used via a `replace`
directive in the root go.mod. License: AGPL-3.0 (see COPYING), as upstream.

## Patches

1. **Annotations render** — `fitz_cgo.go`: new C helper `run_page_full`
   wrapping `fz_run_page` (contents *and* annotations, per MuPDF's page
   running model). The image paths `ImageDPI` and `ImagePNG` use it instead
   of `run_page_contents`, so PDF annotations (highlights, squares, notes'
   appearance streams) appear in rendered pages. The stext/SVG paths
   (Text/HTML/SVG) still run contents only.

2. **Exact search quads** - `fitz_cgo.go`: new C helper `search_page` wrapping `fz_search_page`, and `Document.SearchPage(pageNumber, needle) ([]SearchQuad, error)` returning per-hit bounding boxes in page points. goPDF uses these to highlight matched text precisely (the HTML text extraction has no span widths).

3. **Exact selection geometry** - `fitz_cgo.go`: C helpers `stext_from_page` (a page's structured text, run exactly as `fz_search_page` runs the page — whole page, identity ctm — so selection and search share one coordinate space), `selection_quads` (`fz_highlight_selection` + `fz_copy_selection`) and `drop_string`. `Document.SelectionQuads(pageNumber, x0, y0, x1, y1) ([]SearchQuad, string, error)` returns the exact highlight quads and the plain text of a point-to-point selection; `Document` caches the structured text of the last four pages touched (`stextPages`/`stextOrder`, dropped in `Close`), so drag updates reuse one extraction. goPDF's text selection paints these quads — the index's span rectangles are estimates that misaligned selection overlays on real documents.

4. **OS CJK fallback font** — `fitz_cgo.go`: C helpers `gopdf_install_cjk_fallback`, `gopdf_drop_cjk_fallback` plus two static hooks around `fz_install_load_system_font_funcs`, and `Document.InstallCJKFallback(path) bool` / `fitz.CJKFallbackFontPath() (string, bool)` on top. The bundled MuPDF is built with `TOFU` + `TOFU_CJK` (no baked CJK font), so a PDF whose CJK font is NOT embedded fails its font load and MuPDF substitutes its hail-mary Helvetica: the page paints mojibake and the text layer extracts garbage. With the install armed, MuPDF's request for a system font is answered with one OS font file (candidates per platform, first that stats, no font probing), and MuPDF itself re-encodes CIDs through the collection's UCS2 CMap into the font's unicode cmap, so plain TrueType/TTC system fonts work with no CID-keyed font required. Design points, verified against the shipped 1.28 objects and pdf-font.c:
   - Which hook fires matters. The PDF route is the **CJK hook** (`fz_load_system_cjk_font_fn`, called from `pdf_load_substitute_cjk_font` for Adobe-GB1/CNS1/Japan1/Korea1 collections); the script/language **fallback hook** (`fz_load_system_fallback_font_fn`) is only reached through `fz_encode_character_with_fallback`, which the PDF content interpreter never calls (it serves fz_show_string/story/HTML layout). Both are installed: the CJK hook answers any of the four Adobe orderings; the fallback hook answers UCDN scripts HAN/HANGUL/HIRAGANA/KATAKANA/BOPOMOFO, plus COMMON/INHERITED characters (CJK punctuation) when the language is zh/ja/ko — Latin runs can never pick the CJK font up.
   - The font loads **lazily on first request**, so documents that never hit the hooks pay nothing, and each context owns its private `fz_font` (the record rides in the context's user slot, `fz_set_user_context`), so no state is shared across contexts and all refcount traffic stays on one goroutine per MuPDF's threading rule. Hooks hand out kept references; the record's own reference is dropped in `Document.Close` (`gopdf_drop_cjk_fallback`) before `fz_drop_context`, alongside the stext cache pattern.
   - `fz_new_font_from_file(ctx, "gopdf-cjk-fallback", path, 0, 0)`: index 0 is the first face of a .ttc (msyh, PingFang, Noto CJK collections) and the only face of a .ttf; glyph-box caching off, as MuPDF does for CJK sizes.
   - Failure is silent by design: no candidate path, a corrupt font file, a second install on the same context (per-context once, a no-op) — every path leaves rendering exactly as before. goPDF arms it in `internal/pdf` at open time for PDFs (see `armCJKFallback`); the oracle tests pinning armed-vs-unarmed renders, Latin pixel-identity and text extraction live in `internal/pdf/cjk_fallback_test.go`.

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

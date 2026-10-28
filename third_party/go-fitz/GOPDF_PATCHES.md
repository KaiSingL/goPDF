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

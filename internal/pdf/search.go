package pdf

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Index is the searchable text of a document, built once per Doc by
// IndexAsync. All fields are read-only after the build callback fires.
type Index struct {
	pages int
	// Spans[p] holds the positioned text runs of page p (may be empty when
	// span extraction failed or the page has no text).
	Spans [][]TextSpan
	// Text[p] holds the plain text of page p.
	Text []string
}

// NumPages returns the number of pages in the index.
func (ix *Index) NumPages() int { return ix.pages }

// PageText returns the plain text of a page ("" out of range).
func (ix *Index) PageText(p int) string {
	if p < 0 || p >= ix.pages {
		return ""
	}
	return ix.Text[p]
}

// PageSpans returns the positioned spans of a page (nil out of range).
func (ix *Index) PageSpans(p int) []TextSpan {
	if p < 0 || p >= ix.pages {
		return nil
	}
	return ix.Spans[p]
}

// Search returns all case-insensitive substring matches of query, ordered by
// page then reading order. Hits whose page has spans carry the covered
// spans (for highlight rectangles); pages without spans yield jump-to-page
// hits only. An empty query returns nil.
func (ix *Index) Search(query string) []SearchHit {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	var hits []SearchHit
	for p := 0; p < ix.pages; p++ {
		if len(ix.Spans[p]) > 0 {
			hits = append(hits, searchSpans(p, ix.Spans[p], query)...)
			continue
		}
		// No positioned spans: plain-text match jumps to the page.
		if strings.Contains(strings.ToLower(ix.Text[p]), query) {
			hits = append(hits, SearchHit{Page: p})
		}
	}
	return hits
}

// searchSpans finds query in the concatenation of a page's spans (a single
// space between spans) and returns hits covering the overlapped spans.
func searchSpans(page int, spans []TextSpan, query string) []SearchHit {
	var sb strings.Builder
	type spanRange struct{ start, end, idx int }
	ranges := make([]spanRange, 0, len(spans))
	for i, sp := range spans {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		ranges = append(ranges, spanRange{sb.Len(), sb.Len() + len(sp.Text), i})
		sb.WriteString(sp.Text)
	}
	hay := strings.ToLower(sb.String())
	var hits []SearchHit
	off := 0
	for {
		i := strings.Index(hay[off:], query)
		if i < 0 {
			break
		}
		start, end := off+i, off+i+len(query)
		off = start + 1
		var covered []TextSpan
		for _, r := range ranges {
			if r.start < end && r.end > start {
				covered = append(covered, spans[r.idx])
			}
		}
		if len(covered) > 0 {
			hits = append(hits, SearchHit{Page: page, Spans: covered})
		}
	}
	return hits
}

// MuPDF v1.28 (go-fitz v1.28.2) emits one <p> per positioned text line,
// e.g. for a page with "Hello modern-pdf" at PDF (72,720):
//
//	<div id="page0" style="width:612.0pt;height:792.0pt">
//	<p style="top:52.8pt;left:72.0pt;line-height:24.0pt"><span style="font-family:Arial,sans-serif;font-size:24.0pt;color:#000000">Hello modern-pdf</span></p>
//	</div>
//
// top/left are the line's top-left corner in points from the page's
// top-left origin (y grows down); line-height is the line box height.
// Notably the format carries NO width, so extractSpans estimates one from
// the font size and rune count, clamped to the page box — good enough for
// highlight rectangles.
var spanP = regexp.MustCompile(`(?s)<p\s+style="([^"]*)"[^>]*>(.*?)</p>`)

// pageDiv matches the per-page wrapper carrying the page box size.
var pageDiv = regexp.MustCompile(`<div id="page\d+" style="width:([\d.]+)pt;height:([\d.]+)pt"`)

var tagRE = regexp.MustCompile(`<[^>]*>`)

// stylePt picks "name:<number>pt" pairs out of style attributes. Requiring
// the pt unit keeps font-family lists and colors out of the matches.
var stylePt = regexp.MustCompile(`([a-z-]+):(-?[\d.]+)pt`)

// avgCharWidth is the assumed fraction of font-size per rune used to
// estimate span width (MuPDF 1.28's stext HTML has no width). Roughly the
// mean advance of Helvetica mixed-case text.
const avgCharWidth = 0.55

// extractSpans parses positioned text spans out of one page of MuPDF stext
// HTML (doc.HTML(page, false)). Inner tags are stripped and HTML entities
// unescaped. Returns nil when nothing parses.
func extractSpans(docHTML string, page int) []TextSpan {
	pageW := 0.0
	if m := pageDiv.FindStringSubmatch(docHTML); m != nil {
		pageW, _ = strconv.ParseFloat(m[1], 64)
	}
	matches := spanP.FindAllStringSubmatch(docHTML, -1)
	if len(matches) == 0 {
		return nil
	}
	spans := make([]TextSpan, 0, len(matches))
	for _, m := range matches {
		// Style values live on the <p> (top/left/line-height) and the inner
		// <span> (font-size); search both, first occurrence wins.
		style := stylePt.FindAllStringSubmatch(m[1]+" "+m[2], -1)
		val := func(name string) (float64, bool) {
			for _, s := range style {
				if s[1] == name {
					v, err := strconv.ParseFloat(s[2], 64)
					return v, err == nil
				}
			}
			return 0, false
		}
		top, ok1 := val("top")
		left, ok2 := val("left")
		if !ok1 || !ok2 {
			continue
		}
		lineH, _ := val("line-height")
		fontSize, _ := val("font-size")
		if fontSize <= 0 {
			fontSize = lineH
		}
		if fontSize <= 0 {
			fontSize = 12
		}
		h := lineH
		if h <= 0 {
			h = fontSize
		}
		text := strings.TrimSpace(html.UnescapeString(tagRE.ReplaceAllString(m[2], "")))
		if text == "" {
			continue
		}
		w := fontSize * avgCharWidth * float64(len([]rune(text)))
		if pageW > 0 && left+w > pageW {
			w = pageW - left // clamp into the page box
		}
		if w < 0 {
			w = 0
		}
		spans = append(spans, TextSpan{Page: page, X: left, Y: top, W: w, H: h, Text: text})
	}
	if len(spans) == 0 {
		return nil
	}
	return spans
}

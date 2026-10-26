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

// spanDiv matches one absolutely-positioned block of MuPDF's stext HTML
// output, e.g.
//
//	<div style="position:absolute;top:720;left:72;width:230.4;height:24">
//	  <span class="txt1">Hello</span></div>
//
// Coordinates are in points from the page's top-left. Values may be decimal
// and negative for cropped pages. Tolerant of the tag soup inside the div.
var spanDiv = regexp.MustCompile(`(?s)position:absolute;\s*top:(-?[\d.]+);\s*left:(-?[\d.]+);\s*width:(-?[\d.]+);\s*height:(-?[\d.]+)"[^>]*>(.*?)</div>`)

var tagRE = regexp.MustCompile(`<[^>]*>`)

// extractSpans parses positioned text spans out of one page of MuPDF stext
// HTML (doc.HTML(page, false)). Inner tags are stripped and HTML entities
// unescaped. Returns nil when nothing parses.
func extractSpans(docHTML string, page int) []TextSpan {
	matches := spanDiv.FindAllStringSubmatch(docHTML, -1)
	if len(matches) == 0 {
		return nil
	}
	spans := make([]TextSpan, 0, len(matches))
	for _, m := range matches {
		top, err1 := strconv.ParseFloat(m[1], 64)
		left, err2 := strconv.ParseFloat(m[2], 64)
		w, err3 := strconv.ParseFloat(m[3], 64)
		h, err4 := strconv.ParseFloat(m[4], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		text := strings.TrimSpace(html.UnescapeString(tagRE.ReplaceAllString(m[5], "")))
		if text == "" {
			continue
		}
		spans = append(spans, TextSpan{Page: page, X: left, Y: top, W: w, H: h, Text: text})
	}
	if len(spans) == 0 {
		return nil
	}
	return spans
}

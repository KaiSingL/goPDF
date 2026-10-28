// docprops.go is the Document Properties dialog: a ui.Modal (File ▸
// Document Properties) over the UI, showing the active document's metadata
// and file facts. Read-only: it closes with its Close button, Escape or a
// click outside (Modal's own dismissal, verified against the pinned MyGo
// v0.6.4 source). view.go builds the overlay after the palette so it draws
// above everything; the flag is s.ShowProperties.
package app

import (
	"fmt"
	"os"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// propertiesOverlay renders the Document Properties dialog while
// s.ShowProperties. The dialog needs an open document: with none (no tabs,
// a launcher tab, a failed open) it drops the flag instead of building.
func propertiesOverlay(c *ui.Context, s *AppState) {
	if !s.ShowProperties {
		return
	}
	if t := s.Active(); t == nil || t.Doc == nil {
		s.ShowProperties = false // tab closed under the flag, or a launcher
		return
	}
	ui.Modal(c, &s.ShowProperties, func() { docPropsPanel(c, s) })
}

// docPropsPanel builds the dialog: the header, the field list and the
// Close button. It re-checks the active tab — the view can rebuild after
// the tab closed — and reads only plain fields of pdf.Doc: Meta, NumPages
// and PageSize are collected at Open and safe even for a closed doc.
func docPropsPanel(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil || t.Doc == nil {
		s.ShowProperties = false
		return
	}
	th := c.Theme()
	ui.Column(c).Width(480).Gap(th.Space(2)).Children(func() {
		ui.Text(c, "Document Properties").Bold().FontSize(th.FontSize * 1.15)
		ui.Text(c, t.Title).TextColor(th.TextMuted).
			FontSize(th.FontSize * 0.9).SingleLine()
		ui.Divider(c)

		ui.Column(c).FillWidth().Gap(th.Space(1)).Children(func() {
			for _, f := range docPropsFields(t) {
				docPropsRow(c, th, f)
			}
		})

		// Close, right-aligned; Escape and a backdrop click also clear
		// the flag (Modal's dismissal writes straight through the
		// pointer).
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "Close").Clicked() {
				s.ShowProperties = false
			}
		})
	})
}

// propField is one dialog row: a muted label and a value. single cuts an
// overlong value with an ellipsis instead of wrapping (the file path).
type propField struct {
	label  string
	value  string
	single bool
}

// docPropsFields collects the dialog's rows from the tab's document:
// metadata first, then the file facts. Empty metadata values are skipped —
// a PDF's Info dictionary rarely fills every key.
func docPropsFields(t *Tab) []propField {
	meta := t.Doc.Meta()
	fields := []propField{
		{"Title", meta["title"], false},
		{"Author", meta["author"], false},
		{"Subject", meta["subject"], false},
		{"Keywords", meta["keywords"], false},
		{"Creator", meta["creator"], false},
		{"Producer", meta["producer"], false},
		{"Format", meta["format"], false},
		{"Created", meta["creationDate"], false},
		{"Modified", meta["modDate"], false},
		{"Encryption", meta["encryption"], false}, // shown only when set
		{"Pages", strconv.Itoa(t.NumPages), false},
		{"File", t.Path, true}, // paths wrap badly: cut with an ellipsis
	}
	if st, err := os.Stat(t.Path); err == nil {
		fields = append(fields, propField{"File size", humanSize(st.Size()), false})
	}
	if w, h := t.Doc.PageSize(0); w > 0 && h > 0 {
		fields = append(fields, propField{"Page size",
			fmt.Sprintf("%.0f × %.0f pt", w, h), false})
	}
	out := make([]propField, 0, len(fields))
	for _, f := range fields {
		if f.value != "" {
			out = append(out, f)
		}
	}
	return out
}

// docPropsRow is one field row: the label in a fixed muted column so the
// values align, the value growing into the rest — long text wraps within
// the dialog's width, a single-lined value is cut with an ellipsis.
func docPropsRow(c *ui.Context, th *ui.Theme, f propField) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(th.Space(2)).Children(func() {
		ui.Text(c, f.label).TextColor(th.TextMuted).Width(th.Space(23))
		v := ui.Text(c, f.value).Grow(1)
		if f.single {
			v.SingleLine()
		}
	})
}

// humanSize renders a byte count as B, KB or MB, one decimal from KB up.
func humanSize(n int64) string {
	const kb, mb = 1 << 10, 1 << 20
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

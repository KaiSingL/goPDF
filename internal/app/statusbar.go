// statusbar.go renders the bottom bar (spec §4: "Page X of Y, Fit Width /
// Fit Page, Zoom, Dark Mode toggle, Autosaved indicator"). One of the four
// chrome modules (Agent C).
//
// API notes (verified against mygo v0.6.4 source):
//   - The page-number input is a ui.TextInput bound to a per-tab *string
//     (bound values need storage that outlives the pass). The current page
//     shows as the placeholder, so typing replaces it and submitting (or
//     invalid input) resets the field to the committed page. Enter is the
//     input's Submitted.
//   - The dark-mode button shows a moon in the light theme and a sun in
//     the dark one (c.Theme().Dark), and calls toggleTheme (Agent D).
package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// pageEdits holds one page-input string per tab (pruned lazily).
var pageEdits = map[string]*string{}

// pageEditFor returns the tab's page-input string, starting empty so the
// placeholder shows the committed page. statusBar prunes closed tabs'
// entries before building the input.
func pageEditFor(t *Tab) *string {
	if e := pageEdits[t.ID]; e != nil {
		return e
	}
	e := new(string)
	pageEdits[t.ID] = e
	return e
}

// statusBar renders the bottom bar; the empty state shows the app name.
func statusBar(c *ui.Context, s *AppState) {
	th := c.Theme()
	if len(pageEdits) > 64 {
		pruneByTabID(pageEdits, s)
	}
	t := s.Active()
	ui.Row(c).Height(28).PaddingX(th.Space(2)).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
		if t == nil {
			ui.Text(c, "GoPDF").TextColor(th.TextMuted).FontSize(12)
			return
		}

		ui.Textf(c, "Page %d of %d", t.CurPage+1, t.NumPages).FontSize(12)
		pageInput(c, t)

		ui.Spacer(c)
		ui.Textf(c, "%s · %s", fitModeLabel(t.ZoomMode), t.ZoomPercent()).
			FontSize(12).TextColor(th.TextMuted)

		ui.Spacer(c)
		theme := ui.Button(c, "").Label("Toggle dark mode").Tooltip("Toggle dark mode")
		theme.Children(func() {
			icon := chromeIcons.moon
			if th.Dark {
				icon = chromeIcons.sun
			}
			ui.Icon(c, icon).FontSize(14)
		})
		theme.OnClick(toggleTheme)
		ui.Text(c, "Saved").FontSize(12).TextColor(th.TextMuted) // settings autosave (spec §4)
	})
}

// pageInput is the inline page-number field: submit navigates to n-1
// (Navigate clamps); invalid input just resets the display.
func pageInput(c *ui.Context, t *Tab) {
	edit := pageEditFor(t)
	in := ui.TextInput(c, edit).
		Label("Go to page").Tooltip("Go to page").
		Width(56).Placeholder(fmt.Sprintf("%d", t.CurPage+1)).
		FontSize(12)
	if in.Submitted() {
		n, err := strconv.Atoi(strings.TrimSpace(*edit))
		*edit = "" // reset in every case; the placeholder shows the page
		if err == nil {
			t.Navigate(n-1, 0)
		}
	}
}

// fitModeLabel names the zoom mode for the status bar.
func fitModeLabel(m ZoomMode) string {
	switch m {
	case ZoomFitPage:
		return "Fit Page"
	case ZoomCustom:
		return "Custom"
	default:
		return "Fit Width"
	}
}

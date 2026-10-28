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
//   - The theme button shows the current MODE's icon — monitor for auto
//     (follow system), moon for dark, sun for light — under a stable
//     accessible label, with a tooltip naming the mode and the next one;
//     a click cycles via toggleTheme (Agent D).
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
	// One compact line: a fixed height with symmetric vertical padding
	// (PaddingY is included in Height, border-box style) so the controls
	// center with visible space above and below. The page input and the
	// theme button are slimmed in kind below: mygo pads both by default to
	// ~28+ DIPs tall, which would fill this bar edge to edge.
	ui.Row(c).Height(36).PaddingY(th.Space(1)).
		PaddingX(th.Space(2)).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
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
		// Slim vertical padding: the button's default (Space(1.5)) plus its
		// border is taller than the bar's content box, which would sit it
		// flush against the bar's edges. The icon follows the MODE, not
		// the resolved theme, so the button reads as "which mode am I in";
		// the label stays stable for accessibility and the tooltip names
		// the mode and what a click switches to.
		theme := ui.Button(c, "").Label("Toggle dark mode").Tooltip(themeButtonTooltip()).
			PaddingY(th.Space(0.5))
		theme.Children(func() {
			ui.Icon(c, themeModeIcon()).FontSize(14)
		})
		theme.OnClick(toggleTheme)
		ui.Text(c, "Saved").FontSize(12).TextColor(th.TextMuted) // settings autosave (spec §4)
	})
}

// pageInput is the inline page-number field: submit navigates to n-1
// (Navigate clamps); invalid input just resets the display.
func pageInput(c *ui.Context, t *Tab) {
	th := c.Theme()
	edit := pageEditFor(t)
	// Slim vertical padding so the input (mygo's default is Space(1.5),
	// ~28 DIPs tall with the border) centers in the bar with room above
	// and below instead of filling it edge to edge.
	in := ui.TextInput(c, edit).
		Label("Go to page").Tooltip("Go to page").
		Width(56).PaddingY(th.Space(0.5)).Placeholder(fmt.Sprintf("%d", t.CurPage+1)).
		FontSize(12)
	if in.Submitted() {
		n, err := strconv.Atoi(strings.TrimSpace(*edit))
		*edit = "" // reset in every case; the placeholder shows the page
		if err == nil {
			t.Navigate(n-1, 0)
		}
	}
}

// themeModeIcon returns the current MODE's icon (not the resolved
// theme's, as the old moon-in-light/sun-in-dark pair did): monitor for
// auto — follow system —, moon for dark, sun for light. theme_ui_test.go
// asserts the mapping, since the Tester can neither read icons nor
// tooltips.
func themeModeIcon() *ui.SVG {
	switch ThemeMode {
	case "dark":
		return chromeIcons.moon
	case "light":
		return chromeIcons.sun
	default:
		return chromeIcons.monitor
	}
}

// themeButtonTooltip names the current mode and what a click switches to
// (the toggleTheme cycle auto → dark → light → auto).
func themeButtonTooltip() string {
	switch ThemeMode {
	case "dark":
		return "Theme: dark — click for light"
	case "light":
		return "Theme: light — click for system"
	default:
		return "Theme: follow system — click for dark"
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

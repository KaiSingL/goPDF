// statusbar.go renders the bottom bar: the fit-mode and zoom readout on
// the left, the theme switch and the autosaved indicator on the right
// (spec §4's "Page X of Y" moved to the toolbar — its indicator opens
// the go-to row under the toolbar, toolbar.go).
//
// The theme switch is a three-segment icon slider (system / light /
// dark): a pill track whose knob slides to the chosen mode with a short
// animation — the same idiom mygo's own Toggle paints its knob with (an
// Animate value driving a rounded rect in Draw, under the icon children).
package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// statusBar renders the bottom bar; the empty state shows the app name.
func statusBar(c *ui.Context, s *AppState) {
	th := c.Theme()
	t := s.Active()
	// One compact line: a fixed height with symmetric vertical padding
	// (PaddingY is included in Height, border-box style) so the controls
	// center with visible space above and below.
	ui.Row(c).Height(36).PaddingY(th.Space(1)).
		PaddingX(th.Space(2)).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
		if t == nil {
			ui.Text(c, "GoPDF").TextColor(th.TextMuted).FontSize(12)
			return
		}

		if debugOn { // debugviewer.go: the frame counter, for capture correlation
			ui.Textf(c, "f%d", debugFrame.Load()).FontSize(11).TextColor(th.TextMuted)
		}

		ui.Spacer(c)
		ui.Textf(c, "%s · %s", fitModeLabel(t.ZoomMode), t.ZoomPercent()).
			FontSize(12).TextColor(th.TextMuted)

		ui.Spacer(c)
		themeSwitch(c)
		ui.Text(c, "Saved").FontSize(12).TextColor(th.TextMuted) // settings autosave (spec §4)
	})
}

// themeSwitchSeg is one segment of the theme switch.
type themeSwitchSeg struct {
	label string
	mode  string
	icon  *ui.SVG
}

// themeSegments lists the modes in slider order: follow system, light,
// dark.
var themeSegments = []themeSwitchSeg{
	{"Theme: follow system", "auto", chromeIcons.monitor},
	{"Theme: light", "light", chromeIcons.sun},
	{"Theme: dark", "dark", chromeIcons.moon},
}

// themeSwitch is the three-mode theme slider: a pill of three icon
// segments; a click picks that mode (SetThemeMode — the cycle is gone,
// every mode is one visible click away), and the knob glides to it. The
// knob paints in Draw — before the icon children, so it slides UNDER
// them — at the Animate value's segment offset.
func themeSwitch(c *ui.Context) {
	th := c.Theme()
	const seg = 32.0 // one segment's width in DIPs (the knob's too)

	cur := 0
	mode := NormalizeThemeMode(ThemeMode)
	for i, m := range themeSegments {
		if m.mode == mode {
			cur = i
		}
	}

	e := ui.Box(c).Width(seg * float32(len(themeSegments))).Height(24).
		Radius(12).Background(th.SurfaceHover).Clip()
	at := e.Animate("knob", float32(cur), 160*time.Millisecond)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		// The knob: a full segment slice of the pill, surface-colored
		// with a soft shadow — mygo's own switch knob idiom (widgets.go).
		kr := ui.Rect{X: r.X + at*seg, Y: r.Y, W: seg, H: r.H}
		p.Shadow(kr, 12, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.25))
		p.Fill(kr, th.Surface, 12)
	})
	e.Children(func() {
		ui.Row(c).Fill().Children(func() {
			for _, m := range themeSegments {
				b := ui.ButtonBase(c).Label(m.label).Tooltip(m.label).
					Grow(1).Cursor(ui.CursorPointer)
				chosen := m.mode == mode
				b.Children(func() {
					icon := ui.Icon(c, m.icon).FontSize(14)
					if !chosen {
						icon.TextColor(th.TextMuted)
					}
				})
				// The click captures mode for the closure.
				mode := m.mode
				b.OnClick(func() { SetThemeMode(mode) })
			}
		})
	})
}

// themeModeIcon returns the current MODE's icon (not the resolved
// theme's): monitor for auto — follow system —, sun for light, moon for
// dark. theme_ui_test.go asserts the mapping, since the Tester can
// neither read icons nor tooltips.
func themeModeIcon() *ui.SVG {
	switch NormalizeThemeMode(ThemeMode) {
	case "dark":
		return chromeIcons.moon
	case "light":
		return chromeIcons.sun
	default:
		return chromeIcons.monitor
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

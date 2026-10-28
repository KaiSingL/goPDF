// theme_ui_test.go drives the theme surfaces: the status bar's theme
// slider (three segments, a click picks the mode, the knob animates),
// the View menu's Theme submenu and the palette's three Theme commands.
// Safe headless throughout: with no running app, ApplyTheme/SetThemeMode
// only do the bookkeeping (the mygo hop is gated on mygo.App.IsReady).
package app

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// withThemeMode sets ThemeMode for the test and restores it after.
func withThemeMode(t *testing.T, mode string) {
	t.Helper()
	old := ThemeMode
	ThemeMode = mode
	t.Cleanup(func() { ThemeMode = old })
}

// TestStatusBarThemeSwitch drives the theme slider: the three segments
// render, clicking one applies that mode (through SetThemeMode — no
// cycling), and frames while the knob animates are calm.
func TestStatusBarThemeSwitch(t *testing.T) {
	withThemeMode(t, "auto")
	s := chromeState(1)
	tt := ui.NewTester(func(c *ui.Context) { statusBar(c, s) }, 700, 80)

	for _, tc := range []struct{ label, want string }{
		{"Theme: dark", "dark"},
		{"Theme: light", "light"},
		{"Theme: follow system", "auto"},
	} {
		if err := tt.Click(tc.label); err != nil {
			t.Fatal(err)
		}
		if ThemeMode != tc.want {
			t.Fatalf("after clicking %q: ThemeMode = %q, want %q", tc.label, ThemeMode, tc.want)
		}
		tt.Advance(32 * time.Millisecond) // a few frames of knob glide
		tt.Frame()
	}
}

// TestThemeModeIcon checks the mode→icon mapping through its helper:
// the Tester can neither read which SVG an Icon builds, so the slider's
// mode-dependent surface is asserted here directly. Icon = the current
// MODE (monitor = follow system, moon = dark, sun = light), NOT the
// resolved theme.
func TestThemeModeIcon(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		wantIcon *ui.SVG
	}{
		{"auto", chromeIcons.monitor},
		{"dark", chromeIcons.moon},
		{"light", chromeIcons.sun},
	} {
		withThemeMode(t, tc.mode)
		if got := themeModeIcon(); got != tc.wantIcon {
			t.Errorf("mode %q: icon %v, want the %q icon", tc.mode, got, tc.mode)
		}
	}
}

// TestViewMenuThemeSubmenu checks the View menu's Theme submenu: it
// replaced the Dark Mode item, lists the three modes, and choosing one
// applies it. (The Checked state of the items is not observable through
// the Tester's menu labels; the check marks follow ThemeMode in the
// build, covered by review.)
func TestViewMenuThemeSubmenu(t *testing.T) {
	withThemeMode(t, "auto")
	s := chromeState(1)
	tt := ui.NewTester(titleView(s), 900, 48)
	tt.SetTitleBar(winButtons)

	if err := tt.Click("View"); err != nil {
		t.Fatal(err)
	}
	got := tt.Menu()
	if !slices.Contains(got, "Theme") {
		t.Fatalf("View menu missing the Theme submenu; items %q", got)
	}
	if slices.Contains(got, "Dark Mode") {
		t.Errorf("View menu still carries the old Dark Mode item; items %q", got)
	}

	// Each pick applies at once; ChooseMenuItem closes the popup, so the
	// menu reopens per choice (as the File menu's Open Recent test does).
	for _, tc := range []struct{ item, want string }{
		{"Dark", "dark"},
		{"Light", "light"},
		{"Follow System", "auto"},
	} {
		if err := tt.Click("View"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Theme", tc.item); err != nil {
			t.Fatalf("Theme > %s: %v", tc.item, err)
		}
		if ThemeMode != tc.want {
			t.Fatalf("Theme > %s: ThemeMode = %q, want %q", tc.item, ThemeMode, tc.want)
		}
	}
}

// TestPaletteThemeCommands checks the palette's three explicit Theme
// commands: a "theme" query narrows to them and clicking one applies it.
func TestPaletteThemeCommands(t *testing.T) {
	withThemeMode(t, "auto")
	s := chromeState(1)
	tt := ui.NewTester(s.View, 900, 700)
	tt.Frame()

	tt.Key(ui.Ctrl, ui.KeyK)
	tt.Frame()
	if !s.ShowPalette {
		t.Fatal("Ctrl+K did not open the palette")
	}
	// The field is AutoFocus: typing lands in it without a click (the
	// established TestPaletteFlow pattern — the placeholder is not a
	// clickable label).
	tt.Type("theme")
	tt.Frame()

	for _, want := range []string{"Theme: Follow System", "Theme: Dark", "Theme: Light"} {
		if !tt.HasText(want) {
			t.Errorf("palette missing %q for query \"theme\"; texts %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Theme: Dark"); err != nil {
		t.Fatal(err)
	}
	if ThemeMode != "dark" {
		t.Errorf("after Theme: Dark, ThemeMode = %q, want dark", ThemeMode)
	}
}

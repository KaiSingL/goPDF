// theme_ui_test.go drives the theme surfaces: the status bar's theme
// button (stable label, mode icon, mode-naming tooltip, click = cycle),
// the View menu's Theme submenu (three checked items whose picks apply
// through SetThemeMode, mirroring chrome_test.go's menu-driving patterns)
// and the palette's three Theme commands. Safe headless throughout: with
// no running app, ApplyTheme/SetThemeMode only do the bookkeeping (the
// mygo hop is gated on mygo.App.IsReady).
package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// withThemeMode sets ThemeMode for the test and restores it after.
func withThemeMode(t *testing.T, mode string) {
	t.Helper()
	old := ThemeMode
	ThemeMode = mode
	t.Cleanup(func() { ThemeMode = old })
}

// TestStatusBarThemeButton checks that the button renders under its
// stable accessible label and that clicking it still cycles the mode
// auto → dark → light → auto.
func TestStatusBarThemeButton(t *testing.T) {
	withThemeMode(t, "auto")
	s := chromeState(1)
	tt := ui.NewTester(func(c *ui.Context) { statusBar(c, s) }, 700, 80)

	if _, ok := tt.Find("Toggle dark mode"); !ok {
		t.Fatalf("theme button missing; texts %q", tt.Texts())
	}

	// Each click advances the cycle (OnClick runs after the build pass,
	// so the click itself is enough — no extra frames needed to flip).
	for _, want := range []string{"dark", "light", "auto"} {
		if err := tt.Click("Toggle dark mode"); err != nil {
			t.Fatal(err)
		}
		if ThemeMode != want {
			t.Fatalf("after click: ThemeMode = %q, want %q", ThemeMode, want)
		}
	}

	// Extra frames while the icon swaps between modes: no panic, the
	// button stays.
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Toggle dark mode"); !ok {
		t.Errorf("theme button lost after cycling; texts %q", tt.Texts())
	}
}

// TestThemeButtonIconAndTooltip checks the mode→icon and mode→tooltip
// mappings through their helpers: the Tester can neither read which SVG
// an Icon builds nor a button's tooltip, so the button's mode-dependent
// surface is asserted here directly. Icon = the current MODE (monitor =
// follow system, moon = dark, sun = light), NOT the resolved theme.
func TestThemeButtonIconAndTooltip(t *testing.T) {
	for _, tc := range []struct {
		mode, wantTooltip string
		wantIcon          *ui.SVG
	}{
		{"auto", "Theme: follow system — click for dark", chromeIcons.monitor},
		{"dark", "Theme: dark — click for light", chromeIcons.moon},
		{"light", "Theme: light — click for system", chromeIcons.sun},
	} {
		withThemeMode(t, tc.mode)
		if got := themeModeIcon(); got != tc.wantIcon {
			t.Errorf("mode %q: icon %v, want the %q icon", tc.mode, got, tc.mode)
		}
		if got := themeButtonTooltip(); got != tc.wantTooltip {
			t.Errorf("mode %q: tooltip %q, want %q", tc.mode, got, tc.wantTooltip)
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
// commands: a "theme" query narrows to them and clicking one applies it
// (the row's OnClick runs palRun, which runs the command).
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

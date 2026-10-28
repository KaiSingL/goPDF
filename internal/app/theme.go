// theme.go owns the app-visible appearance mode: auto (follow the system,
// the default), dark or light. The mode is package state because main and
// the settings load write it at startup and everything else applies it
// through the same helpers.
//
// The mode maps 1:1 to mygo.Theme.SetSource (ThemeSystem, ThemeDark,
// ThemeLight). MyGo documents SetSource as callable before App.Run from
// the main goroutine (Run applies the stored source) and as safe from any
// goroutine once the app runs — but a call from a non-main goroutine
// before Run would wait forever for the event loop, which is exactly the
// headless-test situation. ApplyTheme therefore hops only once the app is
// ready; startup application goes through ApplyThemeAtStartup, which main
// calls from the main goroutine before Run.
package app

import "github.com/egoist/mygo"

// ThemeMode is the manual appearance override: "auto" (default), "dark"
// or "light". main sets it from the saved settings at startup; everything
// else goes through ApplyTheme/ToggleTheme.
var ThemeMode = "auto"

// setThemeSource applies a mygo theme source. A variable so tests can
// record calls instead of reaching mygo.
var setThemeSource = func(s mygo.ThemeSource) { mygo.Theme.SetSource(s) }

// themeSource maps ThemeMode to mygo's sources.
func themeSource() mygo.ThemeSource {
	switch ThemeMode {
	case "dark":
		return mygo.ThemeDark
	case "light":
		return mygo.ThemeLight
	default:
		return mygo.ThemeSystem
	}
}

// ApplyTheme keeps the persisted Settings in step with ThemeMode
// (FollowSystemTheme ⇔ auto) and, once the app runs, applies the mode at
// once. In a headless test (no running app) it only does the bookkeeping:
// there is no event loop to serve the mygo call.
func ApplyTheme() {
	ThemeMode = NormalizeThemeMode(ThemeMode)
	if current != nil {
		current.Settings.FollowSystemTheme = ThemeMode == "auto"
	}
	if mygo.App.IsReady() {
		setThemeSource(themeSource())
	}
}

// ApplyThemeAtStartup applies the mode before App.Run so the app opens in
// it (mygo applies the stored source as Run starts). Call it from the
// main goroutine, as main does — that is mygo's documented precondition.
func ApplyThemeAtStartup() {
	ThemeMode = NormalizeThemeMode(ThemeMode)
	if current != nil {
		current.Settings.FollowSystemTheme = ThemeMode == "auto"
	}
	setThemeSource(themeSource())
}

// SetThemeMode switches to an explicit mode — "auto", "dark" or "light"
// (anything else normalizes to auto, exactly like the saved settings —
// and applies it at once. The theme slider (statusbar.go), the View
// menu's Theme submenu and the palette call it with a fixed choice.
func SetThemeMode(mode string) {
	ThemeMode = NormalizeThemeMode(mode)
	ApplyTheme()
}

// ToggleTheme cycles auto → dark → light → auto and applies the new mode
// at once. No surface cycles by default any more — every mode is one
// visible click away — but the cycle stays as a helper (and its test).
func ToggleTheme() {
	switch ThemeMode {
	case "auto":
		ThemeMode = "dark"
	case "dark":
		ThemeMode = "light"
	default:
		ThemeMode = "auto"
	}
	ApplyTheme()
}

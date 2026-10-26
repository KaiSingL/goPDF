package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

// pointSettingsAt redirects the settings file into dir for a test.
func pointSettingsAt(t *testing.T, dir string) {
	t.Helper()
	old := SettingsPath
	SettingsPath = func() (string, error) { return filepath.Join(dir, "settings.json"), nil }
	t.Cleanup(func() { SettingsPath = old })
}

// TestSettingsRoundTrip saves and loads settings through a temporary
// directory (the settingsPath seam).
func TestSettingsRoundTrip(t *testing.T) {
	pointSettingsAt(t, t.TempDir())

	// No file yet: an error, and the caller keeps the defaults.
	if f, err := LoadSettings(); err == nil {
		t.Fatalf("LoadSettings in a fresh dir: got %+v, want an error", f)
	}

	f := SettingsFile{
		Settings: Settings{
			DefaultZoomMode:   ZoomCustom,
			DefaultZoom:       1.75,
			StartWithSidebar:  false,
			DefaultSidebarTab: SidebarOutline,
			FollowSystemTheme: false,
		},
		ThemeMode: "dark",
	}
	if err := SaveSettings(f); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.ThemeMode != f.ThemeMode || got.Settings != f.Settings {
		t.Errorf("round trip: got %+v (theme %q), want %+v (theme %q)",
			got.Settings, got.ThemeMode, f.Settings, f.ThemeMode)
	}

	m := got.Merged()
	if m.DefaultZoomMode != ZoomCustom || m.DefaultZoom != 1.75 ||
		m.DefaultSidebarTab != SidebarOutline || m.StartWithSidebar {
		t.Errorf("merged: %+v", m)
	}
	if NormalizeThemeMode(got.ThemeMode) != "dark" {
		t.Errorf("theme mode %q did not survive", got.ThemeMode)
	}
}

// TestMergedFallbacks checks that missing or out-of-range values fall back
// to the NewAppState defaults.
func TestMergedFallbacks(t *testing.T) {
	m := SettingsFile{}.Merged()
	if m.DefaultZoomMode != ZoomFitWidth || m.DefaultZoom != 1.0 ||
		m.DefaultSidebarTab != SidebarThumbnails {
		t.Errorf("zero settings merged to %+v, want the defaults", m)
	}
	m = SettingsFile{Settings: Settings{
		DefaultZoomMode:   ZoomMode(9),
		DefaultZoom:       99,
		DefaultSidebarTab: SidebarTab(7),
	}}.Merged()
	if m.DefaultZoomMode != ZoomFitWidth || m.DefaultZoom != MaxZoom ||
		m.DefaultSidebarTab != SidebarThumbnails {
		t.Errorf("out-of-range settings merged to %+v", m)
	}
}

// TestSeedRecents checks the palette's recents bootstrap: newest first,
// deduplicated, capped like addRecent.
func TestSeedRecents(t *testing.T) {
	s := NewAppState()
	SeedRecents(s, []string{`C:\a.pdf`, `C:\b.pdf`})
	if len(s.Recents) != 2 || s.Recents[0] != `C:\a.pdf` {
		t.Fatalf("recents %q", s.Recents)
	}
	SeedRecents(s, []string{`C:\c.pdf`, `C:\a.pdf`})
	if s.Recents[0] != `C:\c.pdf` || s.Recents[1] != `C:\a.pdf` || len(s.Recents) != 3 {
		t.Fatalf("recents %q after reseeding", s.Recents)
	}
}

// TestThemeCycle checks the auto → dark → light → auto cycle and that
// ApplyTheme keeps FollowSystemTheme in step. Application is gated on a
// running app (mygo.App.IsReady is false in tests — a test goroutine is
// not the main thread, so the real hop would wait forever), so the
// mapping to mygo's sources is asserted through themeSource instead.
func TestThemeCycle(t *testing.T) {
	old := ThemeMode
	t.Cleanup(func() { ThemeMode = old })

	s := NewAppState()
	SetState(s)
	t.Cleanup(func() { current = nil })

	ThemeMode = "auto"
	ToggleTheme()
	if ThemeMode != "dark" || themeSource() != mygo.ThemeDark || s.Settings.FollowSystemTheme {
		t.Errorf("after auto → %q, source %v (FollowSystemTheme %v)",
			ThemeMode, themeSource(), s.Settings.FollowSystemTheme)
	}
	ToggleTheme()
	if ThemeMode != "light" || themeSource() != mygo.ThemeLight || s.Settings.FollowSystemTheme {
		t.Errorf("after dark → %q, source %v (FollowSystemTheme %v)",
			ThemeMode, themeSource(), s.Settings.FollowSystemTheme)
	}
	ToggleTheme()
	if ThemeMode != "auto" || themeSource() != mygo.ThemeSystem || !s.Settings.FollowSystemTheme {
		t.Errorf("after light → %q, source %v (FollowSystemTheme %v)",
			ThemeMode, themeSource(), s.Settings.FollowSystemTheme)
	}
}

// TestSaveCreatesDir checks SaveSettings against a missing directory.
func TestSaveCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deeper")
	pointSettingsAt(t, dir)
	if err := SaveSettings(SettingsFile{ThemeMode: "light"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatalf("settings file: %v", err)
	}
}

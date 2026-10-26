package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/app"
)

// TestSettingsMerge runs the startup merge without a window: defaults
// from NewAppState overlaid with what LoadSettings found.
func TestSettingsMerge(t *testing.T) {
	dir := t.TempDir()
	old := app.SettingsPath
	app.SettingsPath = func() (string, error) {
		return filepath.Join(dir, "settings.json"), nil
	}
	t.Cleanup(func() { app.SettingsPath = old })

	state := app.NewAppState() // defaults
	if state.Settings.DefaultZoomMode != app.ZoomFitWidth || !state.Settings.StartWithSidebar {
		t.Fatalf("defaults: %+v", state.Settings)
	}

	// No settings file yet: keep the defaults.
	if f, err := app.LoadSettings(); err == nil {
		state.Settings = f.Merged()
		t.Fatalf("fresh dir returned %+v, want an error", f)
	}

	// A saved file merges over the defaults.
	saved := app.SettingsFile{
		Settings: app.Settings{
			DefaultZoomMode:  app.ZoomCustom,
			DefaultZoom:      1.5,
			StartWithSidebar: false,
		},
		ThemeMode: "dark",
	}
	if err := app.SaveSettings(saved); err != nil {
		t.Fatal(err)
	}
	f, err := app.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	state.Settings = f.Merged()
	if state.Settings.DefaultZoomMode != app.ZoomCustom || state.Settings.DefaultZoom != 1.5 {
		t.Errorf("merged zoom: %+v", state.Settings)
	}
	// Settings merge as a whole struct: a saved false stays false.
	if state.Settings.StartWithSidebar {
		t.Errorf("saved StartWithSidebar=false lost: %+v", state.Settings)
	}
	if app.NormalizeThemeMode(f.ThemeMode) != "dark" {
		t.Errorf("theme mode %q", f.ThemeMode)
	}
}

// TestViewSmoke renders the state without panic, as the app would build
// it: the contract view of internal/app (shortcuts, chrome, viewer,
// palette). It also exercises the shell's shortcuts as keys, headless.
func TestViewSmoke(t *testing.T) {
	state := app.NewAppState()
	tt := ui.NewTester(state.View, 1200, 800)
	tt.Frame()

	// Ctrl+K opens the palette through the window shortcut; the modal is
	// up and its results show.
	tt.Key(ui.Ctrl, ui.KeyK)
	if !state.ShowPalette {
		t.Fatal("Ctrl+K did not open the palette")
	}
	tt.Frame()
	if !tt.HasText("COMMANDS") {
		t.Errorf("palette open, texts %v", tt.Texts())
	}

	// Ctrl+K again closes it (the palette rebinds it inside the modal,
	// whose backdrop takes Escape as well).
	tt.Key(ui.Ctrl, ui.KeyK)
	if state.ShowPalette {
		t.Fatal("Ctrl+K did not close the palette")
	}
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyK)
	tt.Key(0, ui.KeyEscape)
	if state.ShowPalette {
		t.Fatal("Escape did not close the palette")
	}
	tt.Frame()

	// Ctrl+F requests search focus (the toolbar's field takes it).
	tt.Key(ui.Ctrl, ui.KeyF)
	if app.SearchFocus == "" {
		t.Error("Ctrl+F set no search focus token")
	}

	// With no tabs these must be no-ops, not panics.
	tt.Key(0, ui.KeyPageDown)
	tt.Key(0, ui.KeyPageUp)
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyEnd)
	tt.Key(ui.Ctrl, ui.KeyW)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyT)
	tt.Key(ui.Ctrl, ui.KeyEqual)
	tt.Key(ui.Ctrl, ui.KeyMinus)
	tt.Key(ui.Ctrl, ui.Key0)
	tt.Key(0, ui.KeyF3)
	tt.Frame()
}

// settings.go persists the user preferences (spec §5 v1.0) as JSON in the
// user data directory, together with the manual theme mode of theme.go.
//
// Loading happens in main before App.Run (a plain file read); saving
// happens from App.OnBeforeQuit. The mygo path API (App.Path) does not
// need the running app, but it falls back to os.UserConfigDir here so the
// package also works headless (tests, `go test`).
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// SettingsFile is the JSON document written to settings.json. Settings is
// the app state's preference struct (state.go); ThemeMode is the manual
// appearance override of theme.go ("", "auto", "dark" or "light").
type SettingsFile struct {
	Settings  Settings
	ThemeMode string `json:",omitempty"`
}

// settingsFileName is the file name under the user data directory.
const settingsFileName = "settings.json"

// SettingsPath returns the path of the settings file. A variable so tests
// can point it at a temporary directory. GOPDF_SETTINGS overrides it —
// the scrolltest probe sets that to a scratch file so automated runs
// never rewrite the user's window size or recents.
var SettingsPath = func() (string, error) {
	if p := os.Getenv("GOPDF_SETTINGS"); p != "" {
		return p, nil
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		base, e := os.UserConfigDir()
		if e != nil {
			return "", e
		}
		dir = filepath.Join(base, "GoPDF")
	}
	return filepath.Join(dir, settingsFileName), nil
}

// thumbsDirName is the subdirectory of the user data directory holding
// the launcher's cached recent-document thumbnails.
const thumbsDirName = "thumbs"

// ThumbsDir returns the thumbnail cache directory, creating nothing. A
// var so tests can point it at a temporary directory; the GOPDF_SETTINGS
// directory wins, so automated runs never touch the real cache.
var ThumbsDir = func() (string, error) {
	if p := os.Getenv("GOPDF_SETTINGS"); p != "" {
		return filepath.Join(filepath.Dir(p), thumbsDirName), nil
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		base, e := os.UserConfigDir()
		if e != nil {
			return "", e
		}
		dir = filepath.Join(base, "GoPDF")
	}
	return filepath.Join(dir, thumbsDirName), nil
}

// LoadSettings reads the settings file. A missing file is not an error for
// the caller to act on beyond taking the defaults: it returns the zero
// SettingsFile and the error, which main treats as "first launch".
func LoadSettings() (SettingsFile, error) {
	p, err := SettingsPath()
	if err != nil {
		return SettingsFile{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return SettingsFile{}, err
	}
	var f SettingsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return SettingsFile{}, fmt.Errorf("gopdf: %s: %w", p, err)
	}
	return f, nil
}

// SaveSettings writes the settings file atomically-ish: it creates the
// directory if needed and writes through a temporary file, so a crash
// mid-write cannot leave a truncated document behind.
func SaveSettings(f SettingsFile) error {
	p, err := SettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), settingsFileName+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after the successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		return err
	}
	return nil
}

// Merged returns the loaded settings with sane values guaranteed: unknown
// enum values, non-positive zoom and missing files all fall back to the
// defaults of NewAppState, which the caller starts from.
func (f SettingsFile) Merged() Settings {
	s := f.Settings
	if s.DefaultZoomMode != ZoomFitWidth && s.DefaultZoomMode != ZoomFitPage && s.DefaultZoomMode != ZoomCustom {
		s.DefaultZoomMode = ZoomFitWidth
	}
	if s.DefaultZoom <= 0 {
		s.DefaultZoom = 1.0
	}
	s.DefaultZoom = clampZoom(s.DefaultZoom)
	if s.DefaultSidebarTab != SidebarThumbnails && s.DefaultSidebarTab != SidebarOutline {
		s.DefaultSidebarTab = SidebarThumbnails
	}
	return s
}

// NormalizeThemeMode returns ThemeMode as one of "auto", "dark", "light".
func NormalizeThemeMode(mode string) string {
	switch mode {
	case "dark", "light":
		return mode
	default:
		return "auto"
	}
}

// SeedRecents copies paths (newest first, as RecentDocuments returns them)
// into the state's recent documents, without opening anything. It exists
// because addRecent is internal; main seeds s.Recents from
// mygo.App.RecentDocuments at startup so the palette lists documents from
// earlier runs.
func SeedRecents(s *AppState, paths []string) {
	for i := len(paths) - 1; i >= 0; i-- { // addRecent puts its path first
		s.addRecent(paths[i])
	}
}

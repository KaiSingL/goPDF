// components.go is the seam between the four UI modules. Each function here
// is a stub that its owner replaces with a real implementation in the file
// named next to it; when you implement one, DELETE its stub here.
//
// Composition (owner B, view.go):
//
//	(s *AppState) View(c *ui.Context)
//	  └─ tabBar      (C, tabs.go)      browser-style tab strip
//	  └─ toolbar     (C, toolbar.go)   nav / search / zoom controls
//	  └─ sidebar     (C, sidebar.go)   thumbnails + outline (left, 280 DIPs)
//	  └─ viewer      (B, viewer.go)    the scrollable page area
//	  └─ statusBar   (C, statusbar.go) page X/Y, fit, zoom, theme toggle
//	  └─ paletteOverlay (D, palette.go) Ctrl+K overlay when s.ShowPalette
//
// Cross-module rules that keep the modules independent:
//
//   - Components talk to each other only through AppState actions and by
//     reading state, never by calling each other directly.
//   - The viewer follows Tab.CurPage / Tab.ReloadGen / Tab.ZoomMode changes
//     between builds (it keeps last-seen values keyed by tab id) — so the
//     sidebar, outline, toolbar and palette simply mutate state via
//     Navigate/SetZoom/etc. and the viewer reacts.
//   - When you implement a stub, delete exactly that stub from this file.
package app

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// window is the main window, set by main.go (D) immediately after creation.
var window *mygo.Window

// accentTint is the background of active chrome surfaces (active tab, the
// active Thumbnails/Outline segment): the same alpha all but disappears
// on the dark theme, so it gets more opacity there.
func accentTint(th *ui.Theme) ui.Color {
	if th.Dark {
		return th.Accent.Alpha(0.45)
	}
	return th.Accent.Alpha(0.15)
}

// accentTintSoft is the subtler variant (outline's current-section row).
func accentTintSoft(th *ui.Theme) ui.Color {
	if th.Dark {
		return th.Accent.Alpha(0.32)
	}
	return th.Accent.Alpha(0.10)
}

// SetWindow installs the main window; main.go calls it right after creating
// the window so background goroutines can hop to the UI thread via update.
func SetWindow(w *mygo.Window) { window = w }

// SearchFocus is the focus request channel for the search panel's field:
// shortcuts set it to a non-empty token (e.g. "search-<n>", unique per
// request) and the field FocusBinds to it, taking focus on the next
// build. See the mygo-maintenance skill's FocusBind pattern.
var SearchFocus string

// update runs fn on the UI thread. Safe from any goroutine. Before the
// window exists (unit tests) fn runs inline on the calling goroutine.
func update(fn func()) {
	if window != nil {
		window.Update(fn)
		return
	}
	fn()
}

// Update is the exported variant of update for package main.
func Update(fn func()) { update(fn) }

// teardownTabState releases every per-tab UI cache of a closed tab:
//
//   - viewer.go: bmpCache (page bitmaps, up to bitmapCap = 48 entries —
//     each pins a full page render's pixels, because ui.Bitmap zero-copies
//     the RGBA), inflight, viewerStates and viewerGen (all viewerMu-guarded;
//     bmpClearTab and inflightClearTab expect the caller to hold it).
//   - sidebar.go: thumbBitmaps/thumbOrder (thumbnail bitmaps, up to the
//     200-entry cap; UI-thread-only), thumbLists and thumbLastPage
//     (UI-thread-only), thumbsRequested and thumbGens (thumbMu-guarded,
//     shared with the doc workers) and outlineOpen ("tabID|path" keys).
//   - searchpanel.go: searchQueries, searchLists and searchScrolled
//     (UI-thread-only; small, but dropped here rather than waiting for the
//     panel's lazy pruning).
//
// Without this, closing a tab releases the fitz doc but the bitmaps stay
// reachable and RAM plateaus at the sum of the caches. Call from the UI
// thread (AppState.CloseTab is one).
func teardownTabState(tabID string) {
	viewerMu.Lock()
	if st := viewerStates[tabID]; st != nil && st.timer != nil {
		st.timer.Stop() // don't leave a zoom debounce firing for a dead tab
	}
	delete(viewerStates, tabID)
	delete(viewerGen, tabID)
	bmpClearTab(tabID)
	inflightClearTab(tabID)
	viewerMu.Unlock()

	delete(thumbLists, tabID)
	delete(thumbLastPage, tabID)
	delete(searchQueries, tabID)
	delete(searchLists, tabID)
	delete(searchScrolled, tabID)
	thumbMu.Lock()
	delete(thumbsRequested, tabID)
	delete(thumbGens, tabID)
	thumbMu.Unlock()
	for k := range thumbBitmaps {
		if k.tabID == tabID {
			delete(thumbBitmaps, k)
		}
	}
	for i := 0; i < len(thumbOrder); { // compact the cap order too
		if thumbOrder[i].tabID == tabID {
			thumbOrder = append(thumbOrder[:i], thumbOrder[i+1:]...)
			continue
		}
		i++
	}
	prefix := tabID + "|"
	for k := range outlineOpen {
		if strings.HasPrefix(k, prefix) {
			delete(outlineOpen, k)
		}
	}
}

// ---- C, tabs.go / toolbar.go / sidebar.go / statusbar.go ---------------
// Implemented in tabs.go (tab strip), toolbar.go (nav / search / zoom
// controls), sidebar.go (thumbnails + outline) and statusbar.go (page X/Y,
// fit, zoom, theme toggle) by Agent C.

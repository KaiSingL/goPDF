// view.go is the root view (module B). It composes the whole window from
// the components, in the order the map in components.go prescribes:
// shortcuts first so they are window-scoped, then the title bar (the
// integrated one — icon, inline menus, drag region — that replaces the
// native title bar), then the chrome, then the content row, and the
// palette overlay last so it draws above everything.
package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// slideDur is how long a side panel takes to slide in or out.
const slideDur = 200 * time.Millisecond

// slidePanel returns the animated width for a side-panel container: it
// eases between 0 (hidden) and full (shown) and keeps frames coming
// while it moves, so the panel glides instead of popping. The container
// element e must be built every frame under a stable key, so its
// animation channel and its locals survive from frame to frame; a
// container at width 0 is clipped to nothing and invisible.
//
// cond is the state the panel slides around (a document / a tab is
// open). While cond stays put, a ShowSidebar / ShowSearchPanel toggle
// glides. When cond itself changes — a document opens or closes, the
// last tab shuts — the width snaps straight to the new situation, so a
// document's panels are there as it opens and none linger, even mid-
// glide, while their document goes away (launcher tabs and no tabs
// never show panel content; TestSidebarHiddenWithoutDocument).
//
// The desktop's reduced-motion preference makes Animate go straight to
// its target, so the panels snap for those users with no extra code.
func slidePanel(e ui.Element, full float32, want, cond bool) float32 {
	w := float32(0)
	if want {
		w = full
	}
	was := ui.Local(e, "cond", func() bool { return cond })
	flipped := was != nil && cond != *was
	if was != nil {
		*was = cond
	}
	if flipped {
		return e.Jump("width", w) // the document/tab came or went: snap
	}
	return e.Animate("width", w, slideDur) // a plain toggle: glide
}

// View builds the window's interface from state. MyGo calls it on the main
// thread whenever a frame is due.
func (s *AppState) View(c *ui.Context) {
	shortcuts(c, s) // D: app-wide keyboard bindings, window scope (first)
	drainPending()  // run worker-queued updates (tests; window hops directly)

	// Fill the window: the chrome bars take their natural height and the
	// content row grows into the rest. Dividers separate the bands and the
	// sidebar from the viewer so the chrome doesn't read as one surface.
	ui.Column(c).Fill().Children(func() {
		titleBarView(c, s) // title bar: app icon, File/View/Go, drag region
		ui.Divider(c)      // ─ under the title bar
		tabBar(c, s)       // C: browser-style tab strip
		ui.Divider(c)      // ─ under the tab strip
		// The toolbar band: only for a document tab — a launcher tab shows
		// the same chrome as the initial no-tabs state (tab strip, then
		// straight to the launcher content). The go-to popover anchors to
		// the toolbar's page indicator, so it builds only here, same frame.
		indicator := toolbar(c, s) // C: back/forward, zoom, pages, search/print
		if s.ShowGoTo && indicator == (ui.Element{}) {
			s.ShowGoTo = false // the tab went launcher (or away): the field goes
		}
		gotoField(c, s, indicator)
		ui.Divider(c) // ─ under the toolbar

		// Content: sidebar (C) beside the page area (B), which fills the
		// rest, and — when toggled — the search results panel (D) on the
		// right. Both panels slide in and out: each sits in a clipped
		// container whose width eases between 0 and full (the +1 covers
		// the divider), built under a stable key so the animation
		// channel glides across frames. The inner content builds only
		// while shown or still gliding; a container at width 0 builds
		// nothing and is invisible. No document open — no tabs or a
		// launcher tab — means no sidebar or search panel: nothing to
		// thumb, outline or search (matches the initial page).
		ui.Row(c).Fill().Grow(1).Children(func() {
			// Left: thumbnails / outline. Slides only while the document
			// stays open; a document appearing or going away snaps it
			// (see slidePanel).
			hasDoc := s.Active() != nil && !s.Active().IsEmpty()
			wantSide := s.ShowSidebar && hasDoc
			side := ui.Row(c.Key("sidebar-slide")).Clip().FillHeight()
			sideW := slidePanel(side, SidebarWidth+1, wantSide, hasDoc)
			// The content rides the slide: built while the panel is
			// wanted, and while it is visibly mid-glide (between fully
			// open and shut) so it slides out with the container. A
			// frame that flips the flag while the clock stands still —
			// the Tester's does — lands on the exact full (or zero)
			// width: not sliding yet, so hidden-panel assertions see
			// the content step down at once.
			sideMid := sideW > 0.5 && sideW < SidebarWidth+1
			side.Width(sideW).Children(func() {
				if wantSide || sideMid {
					sidebar(c, s) // C: thumbnails / outline, SidebarWidth wide
					ui.Divider(c) // │ between sidebar and pages
				}
			})

			viewer(c, s) // B: the scrollable page area

			// Right: search results, the same slide, around an open tab
			// (a launcher tab keeps the panel available, as before).
			hasTab := s.Active() != nil
			wantSearch := s.ShowSearchPanel && hasTab
			search := ui.Row(c.Key("search-slide")).Clip().FillHeight()
			searchW := slidePanel(search, searchPanelWidth+1, wantSearch, hasTab)
			searchMid := searchW > 0.5 && searchW < searchPanelWidth+1
			search.Width(searchW).Children(func() {
				if wantSearch || searchMid {
					ui.Divider(c)     // │ between pages and the search panel
					searchPanel(c, s) // D: right-side search results
				}
			})
		})

		ui.Divider(c)           // ─ over the status bar
		statusBar(c, s)         // C: fit mode, zoom, theme switch
		paletteOverlay(c, s)    // D: Ctrl+K palette, drawn above everything
		propertiesOverlay(c, s) // D: Document Properties dialog (File menu)
		printAlert(c, s)        // D: a failed print names its cause
	})
}

// printAlert surfaces a printing failure (the dialog runs on a worker
// goroutine; errors it logs are invisible in a GUI app): an alert with
// the message and one OK button.
func printAlert(c *ui.Context, s *AppState) {
	if !s.ShowPrintError {
		return
	}
	if ui.AlertDialog(c, &s.ShowPrintError, "Print", s.PrintError, "OK") >= 0 {
		s.PrintError = ""
	}
}

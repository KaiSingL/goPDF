// tabs.go renders the browser-style tab strip (spec §4: "Top: tab bar like
// browser, + button"). It is one of the four chrome modules (Agent C); see
// components.go for the cross-module rules.
//
// API notes (verified against mygo v0.6.4 source):
//   - ui.ButtonBase is a focusable, clickable, hover-aware row without a
//     look — the base for each tab, so keyboard/focus/disabled behavior is
//     preserved (mygo-maintenance skill).
//   - c.Key(t.ID) before construction gives every tab stable identity, so
//     per-tab widget state (hover, focus) follows its tab across rebuilds.
//   - OnClick runs after the whole build pass, so mutating s.Tabs from it
//     is safe (deferred-action rule); an inline Clicked() during the strip
//     loop would shrink the collection being built.
//   - Middle-click close uses Element.HandleInput with InputPointerUp and
//     Button == 2 (0 = primary, 1 = secondary in the ui package).
package app

import "github.com/egoist/mygo/ui"

// chromeIcons are the small stroke icons of the chrome bars, parsed once
// at init. mygo ships no exported icon set (ui.MustParseSVG + inline SVG is
// how its own widgets build icons), so GoPDF carries the few it needs. All
// draw with currentColor, so ui.Icon tints them from the ancestor text
// color and they follow the theme.
var chromeIcons = struct {
	close, plus, minus, back, forward, reload, search, more, moon, sun, monitor, panel, printer, save *ui.SVG
	chevDown, chevUp, chevLeft, chevRight                                                             *ui.SVG
	highlighter, underline, strikeout, note, square                                                   *ui.SVG
	undo, redo                                                                                        *ui.SVG
}{
	close:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg>`)),
	plus:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg>`)),
	minus:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/></svg>`)),
	printer: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9V3h12v6"/><path d="M6 18H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2"/><rect x="6" y="14" width="12" height="7" rx="1"/></svg>`)),
	back:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>`)),
	forward: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`)),
	reload:  ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>`)),
	search:  ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-width="2"><path d="M14.5 14.5L20 20m-4-10a6 6 0 1 1-12 0a6 6 0 0 1 12 0Z"/></svg>`)),
	more:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" stroke="none"><circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/></svg>`)),
	moon:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/></svg>`)),
	sun:     ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg>`)),
	// monitor marks the follow-system theme mode (statusbar.go's theme
	// button, auto mode) — lucide's "monitor".
	monitor: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/></svg>`)),
	panel:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>`)),
	// The search panel's hit steppers (searchpanel.go), matching the
	// back/forward chevrons' style above.
	chevDown:  ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`)),
	chevUp:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m18 15-6-6-6 6"/></svg>`)),
	chevLeft:  ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>`)),
	chevRight: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`)),
	// The annotation tools (annotate.go, issue 3) — lucide's
	// "highlighter", "underline" and "strikethrough".
	highlighter: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 11-6 6v3h9l3-3"/><path d="m22 12-4.6 4.6a2 2 0 0 1-2.8 0l-5.2-5.2a2 2 0 0 1 0-2.8L14 4Z"/></svg>`)),
	underline:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 4v6a6 6 0 0 0 12 0V4"/><path d="M4 20h16"/></svg>`)),
	strikeout:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 4c-2.5 0-4.5 1.5-4.5 3.5 0 .6.2 1.2.5 1.7"/><path d="M18.5 5.5A6 6 0 0 0 7 9"/><path d="M12.5 20c3.7 0 6-1.8 6-4 0-1.1-.5-2-1.4-2.7"/><path d="M6.5 18.5A6 6 0 0 0 18 15"/><path d="M4 12h16"/></svg>`)),
	// lucide's "sticky-note", "square" and "save".
	note:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11l5-5V5a2 2 0 0 0-2-2Z"/><path d="M15 21v-4a2 2 0 0 1 2-2h4"/></svg>`)),
	square: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2"/></svg>`)),
	save:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z"/><path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7"/><path d="M7 3v4a1 1 0 0 0 1 1h7"/></svg>`)),
	// Undo/redo (undo.go, issue 6) — lucide's "undo-2" and "redo-2".
	undo: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 5.5 5.5a5.5 5.5 0 0 1-5.5 5.5H11"/></svg>`)),
	redo: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m15 14 5-5-5-5"/><path d="M20 9H9.5A5.5 5.5 0 0 0 4 14.5A5.5 5.5 0 0 0 9.5 20H13"/></svg>`)),
}

// tabBarHeight and tabHeight size the strip (spec §4: browser-style).
const (
	tabBarHeight = 40
	tabHeight    = 30
	tabMaxWidth  = 200
	titleMaxWide = 140
)

// tabBar renders the tab strip: one tab per s.Tabs (title, close button,
// active state), a "+" button opening a launcher tab (drop files or click
// open, like the empty state), double-click on empty strip space doing the
// same, and middle-click/Close on a tab calling s.CloseTab.
func tabBar(c *ui.Context, s *AppState) {
	th := c.Theme()
	ui.Row(c).Height(tabBarHeight).PaddingX(th.Space(2)).Gap(th.Space(0.5)).AlignItems(ui.Center).Children(func() {
		for _, t := range s.Tabs {
			tabButton(c, s, t)
		}
		plus := ui.Button(c, "").Label("New tab").Tooltip("Open a launcher tab: drop a PDF or click open")
		plus.Children(func() { ui.Icon(c, chromeIcons.plus).FontSize(16) })
		plus.OnClick(func() { s.NewEmptyTab() })
		// The rest of the strip: double-click opens a launcher tab too.
		rest := ui.Box(c).Grow(1).Height(tabBarHeight)
		if rest.DoubleClicked() {
			s.NewEmptyTab()
		}
	})
}

// tabButton renders one tab. Deferred OnClick actions keep mutations of
// s.Tabs out of the strip's build loop.
func tabButton(c *ui.Context, s *AppState, t *Tab) {
	th := c.Theme()
	active := s.ActiveTab >= 0 && s.ActiveTab < len(s.Tabs) && s.Tabs[s.ActiveTab].ID == t.ID

	b := ui.ButtonBase(c.Key(t.ID)).
		Height(tabHeight).MaxWidth(tabMaxWidth).
		PaddingX(th.Space(2)).Gap(th.Space(1)).
		AlignItems(ui.Center).Radius(th.Space(1.5)).
		Cursor(ui.CursorDefault)
	switch {
	case active:
		// The active tab is an accent-tinted surface (matching the
		// Thumbnails/Outline switch), not a top accent bar.
		b.Background(accentTint(th))
	case b.Hovered():
		b.Background(th.SurfaceHover)
	}
	b.Children(func() {
		title := ui.Text(c, tabLabel(t)).SingleLine().Ellipsis("…").MaxWidth(titleMaxWide).FontSize(12)
		switch {
		case active:
			title.TextColor(th.Accent)
		default:
			title.TextColor(th.TextMuted)
		}
		x := ui.ButtonBase(c).
			Size(th.Space(4), th.Space(4)).Radius(th.Space(2)).
			Label("Close " + t.Title).Tooltip("Close tab")
		if x.Hovered() {
			x.Background(th.SurfacePressed)
		}
		x.Children(func() { ui.Icon(c, chromeIcons.close).FontSize(10) })
		x.OnClick(func() { s.CloseTab(t.ID) })
	})
	b.OnClick(func() { s.SelectTabBy(t.ID) })
	// Middle-click closes, as in a browser. The Tester cannot send middle
	// clicks, so this path is covered by review only.
	b.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputPointerUp && ev.Button == 2 {
			s.CloseTab(t.ID)
			return true
		}
		return false
	})
}

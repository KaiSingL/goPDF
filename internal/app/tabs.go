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
	close, plus, back, forward, reload, search, share, comment, more, moon, sun, panel *ui.SVG
	chevDown, chevUp                                                                   *ui.SVG
}{
	close:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg>`)),
	plus:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg>`)),
	back:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>`)),
	forward: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`)),
	reload:  ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>`)),
	search:  ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-width="2"><path d="M14.5 14.5L20 20m-4-10a6 6 0 1 1-12 0a6 6 0 0 1 12 0Z"/></svg>`)),
	share:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8"/><path d="m16 6-4-4-4 4"/><path d="M12 2v13"/></svg>`)),
	comment: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>`)),
	more:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" stroke="none"><circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/></svg>`)),
	moon:    ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/></svg>`)),
	sun:     ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg>`)),
	panel:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>`)),
	// The search panel's hit steppers (searchpanel.go), matching the
	// back/forward chevrons' style above.
	chevDown: ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`)),
	chevUp:   ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m18 15-6-6-6 6"/></svg>`)),
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
		title := ui.Text(c, t.Title).SingleLine().Ellipsis("…").MaxWidth(titleMaxWide).FontSize(12)
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

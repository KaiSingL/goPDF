// password.go is the password prompt: the modal overlay an encrypted
// document's open arms (state.go openTab → pdf.ErrNeedsPassword) instead
// of failing the tab. Unlock retries through OpenTabWithPassword; a
// wrong password shows an inline error line and refocuses the field,
// three wrong entries close the prompt and surface a notice in the
// status bar, and any dismissal (Escape, backdrop click, Cancel) drops
// the pending open with no tab left behind.
//
// API notes (verified against the pinned mygo v0.6.4 source):
//   - mygo.Dialog has NO modal input prompt (files and messages only),
//     so the prompt reuses the palette's ui.Modal overlay pattern with
//     a text field of its own; ui.Modal handles Escape and
//     click-outside by clearing the open flag, which the sweep below
//     converts into a dropped pending open.
//   - Element.Password() masks a single-line text input's contents; the
//     field is ui.TextInput (not TextInputBase) because it wants the
//     standard field chrome.
//   - Focus: AutoFocus covers the first build; every fresh arm and
//     every retry stores a new token in PasswordFocus and the field
//     FocusBinds to it (the SearchFocus pattern, components.go), so the
//     caret comes back after a wrong password without rebuilding the
//     overlay.
package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// maxPasswordAttempts is how many wrong passwords the prompt takes
// before it gives up and reports through the status bar.
const maxPasswordAttempts = 3

// PasswordFocus is the focus-request token for the prompt's field (the
// SearchFocus pattern): a fresh value per arm and per wrong attempt.
var PasswordFocus string

var pwFocusSeq atomic.Uint64

// pwValue is the prompt field's storage (UI thread only, like the
// palette's palQuery); pwOpenSeen tracks the open edge so a fresh open
// starts with an empty field.
var (
	pwValue    string
	pwOpenSeen bool
)

// armPasswordPrompt points the prompt at path and shows it, resetting
// the attempt state. Re-arming for a different path (a drop while the
// prompt is up) also clears the open-edge flag so the next build starts
// a fresh field, exactly like a first open.
func (s *AppState) armPasswordPrompt(path string) {
	s.PendingPasswordPath = path
	s.ShowPasswordPrompt = true
	s.PasswordAttempts = 0
	s.PasswordError = ""
	s.StatusNotice = ""
	pwOpenSeen = false
}

// dropPendingPassword clears the prompt state after any dismissal; the
// overlay calls it when it sees the flag down while a path is still
// pending, which is every close route (Escape, backdrop, Cancel,
// give-up, success) in one place.
func (s *AppState) dropPendingPassword() {
	s.PendingPasswordPath = ""
	s.PasswordAttempts = 0
	s.PasswordError = ""
}

// passwordOverlay renders the prompt over the UI while
// s.ShowPasswordPrompt; view.go builds it last so it draws above
// everything.
func passwordOverlay(c *ui.Context, s *AppState) {
	if !s.ShowPasswordPrompt {
		if s.PendingPasswordPath != "" {
			s.dropPendingPassword() // dismissed: the pending open is gone
		}
		pwOpenSeen = false
		return
	}
	if !pwOpenSeen { // opened since the last build: fresh field + focus
		pwOpenSeen = true
		pwValue = ""
		PasswordFocus = fmt.Sprintf("pw-%d", pwFocusSeq.Add(1))
	}
	if s.PendingPasswordPath == "" { // armed without a path: nothing to prompt for
		s.ShowPasswordPrompt = false
		return
	}
	ui.Modal(c, &s.ShowPasswordPrompt, func() { passwordPanel(c, s) })
}

// passwordPanel builds the dialog: title, the file name, the masked
// field with the inline error under it, and the action row. Enter in
// the field is Unlock (TextInput's Submitted).
func passwordPanel(c *ui.Context, s *AppState) {
	th := c.Theme()
	path := s.PendingPasswordPath
	ui.Column(c).Width(440).Gap(th.Space(2)).Children(func() {
		ui.Text(c, "Password required").Bold().FontSize(th.FontSize * 1.15)
		ui.Text(c, filepath.Base(path)).TextColor(th.TextMuted).
			FontSize(th.FontSize * 0.9).SingleLine()

		in := ui.TextInput(c, &pwValue).
			Placeholder("Password").Password().AutoFocus()
		in.FocusBind(&PasswordFocus, PasswordFocus)
		if s.PasswordError != "" {
			ui.Text(c, s.PasswordError).TextColor(th.Danger).
				FontSize(th.FontSize * 0.9)
		}

		ui.Row(c).FillWidth().Justify(ui.End).Gap(th.Space(1)).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				s.ShowPasswordPrompt = false // sweep drops the pending open
			}
			if ui.PrimaryButton(c, "Unlock").Clicked() {
				submitPassword(s, path)
			}
		})
		if in.Submitted() { // Enter in the field is Unlock too
			submitPassword(s, path)
		}
	})
}

// submitPassword retries the pending open with the entered password.
// A wrong password (pdf.ErrBadPassword) retries inline until
// maxPasswordAttempts, then closes the prompt with a status-bar notice;
// any other failure closes it with the cause in the notice; success
// closes the prompt and clears the notice. Runs on the UI thread (the
// panel's callbacks). The guard absorbs a double firing (Enter while
// the Unlock button holds focus) without counting phantom attempts.
func submitPassword(s *AppState, path string) {
	if !s.ShowPasswordPrompt || s.PendingPasswordPath != path {
		return
	}
	pw := pwValue
	pwValue = ""
	if pw == "" {
		// An empty submit is not an attempt against the three; nudging
		// inline keeps the prompt up (OpenWithPassword with an empty
		// password would answer "needs password", not "wrong").
		s.PasswordError = "Enter the password."
		PasswordFocus = fmt.Sprintf("pw-%d", pwFocusSeq.Add(1))
		return
	}
	t, err := s.OpenTabWithPassword(path, pw)
	if err == nil {
		s.ShowPasswordPrompt = false
		s.PasswordError = ""
		s.StatusNotice = ""
		addSystemRecent(t.Path)
		return
	}
	if errors.Is(err, pdf.ErrBadPassword) {
		s.PasswordAttempts++
		if s.PasswordAttempts < maxPasswordAttempts {
			left := maxPasswordAttempts - s.PasswordAttempts
			unit := "attempts"
			if left == 1 {
				unit = "attempt"
			}
			s.PasswordError = fmt.Sprintf("Wrong password. %d %s left.", left, unit)
			PasswordFocus = fmt.Sprintf("pw-%d", pwFocusSeq.Add(1)) // refocus
			return
		}
		s.ShowPasswordPrompt = false // sweep drops the pending open
		s.StatusNotice = fmt.Sprintf("%s: wrong password after %d attempts",
			filepath.Base(path), maxPasswordAttempts)
		return
	}
	// Anything else (the file vanished, an unsupported build, ...):
	// close the prompt and name the cause.
	s.ShowPasswordPrompt = false
	s.StatusNotice = fmt.Sprintf("%s: %v", filepath.Base(path), err)
}

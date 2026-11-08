package pdf

// password.go is the encrypted-document surface of the open flow:
// OpenWithPassword authenticates password-protected files, and the
// permission snapshot (Permissions, CanAnnotate, CanPrint, CanCopy)
// reports what the authenticated document allows.
//
// # Opening model
//
// fitz.New returns the Document TOGETHER with ErrNeedsPassword for an
// encrypted file: the document exists but must be authenticated before
// anything else runs. OpenWithPassword authenticates on the caller's
// goroutine, BEFORE newDoc derives any cached state (page count, sizes,
// origins, outline) or snapshots permissions, so an authenticated Doc
// carries exactly the same derived state a plain Open would.
//
// A wrong password leaves NOTHING half-open: the failed fitz document is
// closed before the error returns, and the caller must retry with a
// FRESH OpenWithPassword call (the previous attempt's handle is gone).
//
// # Permissions
//
// The snapshot is read once, at open, on the goroutine that then owns
// the document. After the worker handover nobody calls HasPermission
// again: the fork's Document.Close takes no internal lock, so a read
// from another goroutine could race Close. The mask therefore follows
// the derived-state pattern (stMu-guarded, written once at open) and
// Permissions/Can* are cheap, synchronous and safe from any goroutine,
// even after Close.
//
// Semantics (pinned against the fork in password_test.go): an
// authenticated USER password grants exactly the writer's permission
// bits; the OWNER password grants everything; an unencrypted document
// grants everything. Saves never change the live document's crypt
// state, so the mask stays true for the Doc's lifetime.
//
// purego builds (no CGO): Authenticate is part of the fork's editing
// layer and returns ErrNeedsCGO there; OpenWithPassword passes that
// error through (closing the document) instead of misreporting it as a
// wrong password, and HasPermission always answers false, so the
// snapshot denies everything. The shipping Windows build is cgo.

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/gen2brain/go-fitz"
)

// Errors of the password surface. Both wrap their meaning so callers
// never import the fork to errors.Is; ErrBadPassword deliberately does
// NOT wrap fitz.ErrNeedsPassword, or every wrong password would also
// read as "this file needs a password" and the two cases the prompt
// flow switches on would collapse into one.
var (
	// ErrNeedsPassword wraps fitz.ErrNeedsPassword: the document is
	// encrypted and no usable password was given.
	ErrNeedsPassword = fmt.Errorf("pdf: document needs a password: %w", fitz.ErrNeedsPassword)
	// ErrBadPassword: a password was given and rejected.
	ErrBadPassword = fmt.Errorf("pdf: wrong password")
)

// allPermissions is the fork's full permission set, in a stable order
// (the order Permissions reports them in).
var allPermissions = []fitz.Permission{
	fitz.PermPrint, fitz.PermCopy, fitz.PermEdit, fitz.PermAnnotate,
	fitz.PermForm, fitz.PermAccessibility, fitz.PermAssemble, fitz.PermPrintHQ,
}

// snapshotPermissions reads the granted set once on the
// document-owning goroutine (see the package comment for why it must
// not run after the worker handover) into the rune-indexed snapshot.
func snapshotPermissions(fit *fitz.Document) [128]bool {
	var set [128]bool
	for _, p := range allPermissions {
		if fit.HasPermission(p) {
			set[p] = true
		}
	}
	return set
}

// OpenWithPassword opens the file at path, authenticating encrypted
// documents with password.
//
// A plain document opens exactly like Open does and the password is
// ignored (as in other viewers); the same holds for encryption with an
// empty user password, which MuPDF opens by itself. An empty password
// on a document that needs one behaves exactly like Open, except the
// error chain carries this package's ErrNeedsPassword (and through it
// the fork's fitz.ErrNeedsPassword) so callers can errors.Is without
// importing the fork.
//
// A wrong password closes the failed document and returns an error
// wrapping ErrBadPassword (which does NOT match ErrNeedsPassword);
// retrying means a fresh OpenWithPassword call, there is no half-open
// doc to feed another password. On purego builds (no CGO),
// authenticating surfaces the fork's ErrNeedsCGO through the error
// chain instead.
func OpenWithPassword(path, password string) (*Doc, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fit, err := fitz.New(abs)
	if err != nil {
		if fit == nil || !errors.Is(err, fitz.ErrNeedsPassword) {
			// A real open failure; New returns no document for these,
			// but stay leak-safe if that ever changes.
			if fit != nil {
				fit.Close()
			}
			return nil, fmt.Errorf("pdf: open %s: %w", abs, err)
		}
		// fit is the encrypted document, returned alongside
		// ErrNeedsPassword and awaiting authentication.
		if password == "" {
			fit.Close()
			return nil, fmt.Errorf("pdf: open %s: %w", abs, ErrNeedsPassword)
		}
		bits, aerr := fit.Authenticate(password)
		if aerr != nil {
			fit.Close()
			return nil, fmt.Errorf("pdf: open %s: %w", abs, aerr)
		}
		if bits == 0 {
			fit.Close() // rejected: nothing stays half-open, retry fresh
			return nil, fmt.Errorf("pdf: open %s: %w", abs, ErrBadPassword)
		}
	}
	// Authentication (when needed) already happened above, so the
	// derived state below is computed from the decrypted document.
	return newDoc(fit, abs)
}

// Permissions returns the permissions the document grants, from the
// snapshot captured at open. Synchronous, cheap and safe from any
// goroutine, including after Close; the slice is fresh, so callers may
// keep or mutate it.
func (d *Doc) Permissions() []fitz.Permission {
	d.stMu.RLock()
	set := d.perms
	d.stMu.RUnlock()
	var out []fitz.Permission
	for _, p := range allPermissions {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// hasPermission checks the open-time snapshot for one permission.
func (d *Doc) hasPermission(p fitz.Permission) bool {
	if p < 0 || p >= 128 {
		return false // not one of the fork's codes
	}
	d.stMu.RLock()
	defer d.stMu.RUnlock()
	return d.perms[p]
}

// CanAnnotate reports whether the document permits annotations. Issue 3
// (annotation tools) must check this before creating any: the edit
// facade itself does not gate on it yet, so a caller that skips the
// check relies on MuPDF refusing the operation at the PDF layer.
func (d *Doc) CanAnnotate() bool { return d.hasPermission(fitz.PermAnnotate) }

// CanPrint reports whether the document permits printing.
func (d *Doc) CanPrint() bool { return d.hasPermission(fitz.PermPrint) }

// CanCopy reports whether the document permits extracting text (the
// permission MuPDF gates search and text extraction behind).
func (d *Doc) CanCopy() bool { return d.hasPermission(fitz.PermCopy) }

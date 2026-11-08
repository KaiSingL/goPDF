package pdf

// password_test.go covers the encrypted-document open surface
// (password.go) against fixtures the fork's own Save writes (test files
// may use the fork directly): AES-256 encrypted files with known
// passwords and writer permission bits, verified through reopen
// oracles — the unencrypted original answers what derived state the
// authenticated open must produce.

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gen2brain/go-fitz"
)

// permsAll grants every writer permission bit (MuPDF's PDF_PERM_* bit
// values: print, modify, copy, annotate, form, accessibility, assemble,
// print-HQ). The fork's Save reads this int as those raw bits, NOT as
// the Permission rune values the reader side reports.
const permsAll = 1<<2 | 1<<3 | 1<<4 | 1<<5 | 1<<8 | 1<<9 | 1<<10 | 1<<11

// permsPrintOnly grants just PDF_PERM_PRINT.
const permsPrintOnly = 1 << 2

// writeEncryptedPair encrypts a generated PDF (pages of text content,
// optional outline) with AES-256, the given passwords and writer
// permission bits, and returns the plain original's path next to the
// encrypted copy's. Test fixtures may use the fork directly.
func writeEncryptedPair(t *testing.T, pages []string, toc []TOCEntry, userpw, ownerpw string, perms int) (plain, enc string) {
	t.Helper()
	dir := t.TempDir()
	plain = filepath.Join(dir, "plain.pdf")
	if err := os.WriteFile(plain, genPDFBytes(pages, nil, toc), 0o644); err != nil {
		t.Fatalf("write source pdf: %v", err)
	}
	f, err := fitz.New(plain)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer f.Close()
	enc = filepath.Join(dir, "enc.pdf")
	if err := f.Save(enc, fitz.SaveOptions{
		Encryption:    fitz.EncryptAES_256,
		Permissions:   perms,
		UserPassword:  userpw,
		OwnerPassword: ownerpw,
	}); err != nil {
		t.Fatalf("save encrypted: %v", err)
	}
	return plain, enc
}

func TestOpenEncryptedNeedsPassword(t *testing.T) {
	_, enc := writeEncryptedPair(t, []string{textContent("locked")}, nil, "userpw", "ownerpw", permsAll)

	// Open keeps its v1 behavior: refuses, and the chain still carries
	// the fork's sentinel for errors.Is.
	d, err := Open(enc)
	if err == nil || !errors.Is(err, fitz.ErrNeedsPassword) {
		t.Fatalf("Open(encrypted): err=%v, want fitz.ErrNeedsPassword chain", err)
	}
	if d != nil {
		d.Close()
		t.Fatal("Open(encrypted) returned a doc with the error")
	}

	// OpenWithPassword with an empty password: same refusal, but the
	// package sentinel rides the chain too (both identities match).
	d, err = OpenWithPassword(enc, "")
	if err == nil || !errors.Is(err, ErrNeedsPassword) {
		t.Fatalf("OpenWithPassword(enc, \"\"): err=%v, want ErrNeedsPassword", err)
	}
	if !errors.Is(err, fitz.ErrNeedsPassword) {
		t.Fatalf("OpenWithPassword(enc, \"\"): err=%v, chain lost fitz.ErrNeedsPassword", err)
	}
	if errors.Is(err, ErrBadPassword) {
		t.Fatalf("needs-password must not read as bad password: %v", err)
	}
	if d != nil {
		d.Close()
		t.Fatal("OpenWithPassword(enc, \"\") returned a doc with the error")
	}
}

func TestOpenWithWrongPassword(t *testing.T) {
	_, enc := writeEncryptedPair(t, []string{textContent("locked")}, nil, "userpw", "ownerpw", permsAll)

	d, err := OpenWithPassword(enc, "not-the-password")
	if err == nil || !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: err=%v, want ErrBadPassword", err)
	}
	if errors.Is(err, ErrNeedsPassword) || errors.Is(err, fitz.ErrNeedsPassword) {
		t.Fatalf("wrong password must not read as needs-password: %v", err)
	}
	if d != nil {
		d.Close()
		t.Fatal("wrong password returned a doc: nothing may stay half-open")
	}

	// The rejected attempt closed its document: a fresh call with the
	// right password opens cleanly right after, and the plain original
	// is untouched.
	good, err := OpenWithPassword(enc, "userpw")
	if err != nil {
		t.Fatalf("open after wrong password: %v", err)
	}
	defer good.Close()
	if good.NumPages() != 1 {
		t.Fatalf("pages after auth: %d, want 1", good.NumPages())
	}
}

func TestOpenWithPasswordPlainTextAndRender(t *testing.T) {
	toc := []TOCEntry{
		{Title: "Chapter 1", Page: 0, Level: 1},
		{Title: "Section 1.1", Page: 1, Level: 2},
	}
	plain, enc := writeEncryptedPair(t, []string{textContent("secret one"), textContent("secret two")},
		toc, "userpw", "ownerpw", permsAll)

	// Derived state must match the plain original's: auth happens before
	// the geometry walk, so nothing is derived from the locked document.
	ref, err := Open(plain)
	if err != nil {
		t.Fatalf("open plain reference: %v", err)
	}
	defer ref.Close()

	d, err := OpenWithPassword(enc, "userpw")
	if err != nil {
		t.Fatalf("open with user password: %v", err)
	}
	defer d.Close()

	if d.NumPages() != ref.NumPages() || d.NumPages() != 2 {
		t.Fatalf("NumPages after auth: %d (reference %d), want 2", d.NumPages(), ref.NumPages())
	}
	for p := 0; p < 2; p++ {
		w, h := d.PageSize(p)
		rw, rh := ref.PageSize(p)
		if w != rw || h != rh || w != 612 || h != 792 {
			t.Fatalf("PageSize(%d) after auth: %gx%g, reference %gx%g", p, w, h, rw, rh)
		}
		if x, y := d.PageOrigin(p); x != 0 || y != 0 {
			t.Fatalf("PageOrigin(%d) after auth: %g,%g, want 0,0", p, x, y)
		}
	}
	got, want := d.Outline(), ref.Outline()
	if len(got) != 1 || len(want) != 1 || got[0].Title != want[0].Title || got[0].Title != "Chapter 1" {
		t.Fatalf("Outline after auth: %+v, want the reference tree rooted at Chapter 1", got)
	}
	if len(got[0].Children) != 1 || got[0].Children[0].Title != "Section 1.1" {
		t.Fatalf("Outline children after auth: %+v", got[0].Children)
	}

	// Rendering and text extraction (copy permission granted) work
	// through the worker like on any doc.
	ch := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 72}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("render after auth: %v", err)
			ch <- nil
			return
		}
		if img.Rect.Dx() != 612 || img.Rect.Dy() != 792 {
			t.Errorf("render size after auth: %dx%d, want 612x792", img.Rect.Dx(), img.Rect.Dy())
		}
		ch <- img
	})
	if img := recvImg(t, ch, "render after auth"); img == nil {
		t.Fatal("nil render after auth")
	}
	ixCh := make(chan *Index, 1)
	d.IndexAsync(func(ix *Index, err error) {
		if err != nil {
			t.Errorf("index after auth: %v", err)
		}
		ixCh <- ix
	})
	select {
	case ix := <-ixCh:
		if ix != nil && ix.Text[0] == "" {
			t.Error("text of page 0 after auth is empty")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for index after auth")
	}
}

func TestOpenWithPasswordOnPlainDoc(t *testing.T) {
	// A password on an unencrypted document is ignored, exactly as in
	// other viewers; empty behaves like Open outright.
	path := writeTextPDF(t, "plain page")
	a, err := OpenWithPassword(path, "")
	if err != nil {
		t.Fatalf("OpenWithPassword(plain, \"\"): %v", err)
	}
	defer a.Close()
	b, err := OpenWithPassword(path, "some password")
	if err != nil {
		t.Fatalf("OpenWithPassword(plain, pw): %v", err)
	}
	defer b.Close()
	if a.NumPages() != b.NumPages() || b.NumPages() != 1 {
		t.Fatalf("pages: %d/%d, want 1", a.NumPages(), b.NumPages())
	}
}

func TestPermissionsSnapshot(t *testing.T) {
	plain, enc := writeEncryptedPair(t, []string{textContent("locked")}, nil, "userpw", "ownerpw", permsPrintOnly)

	// User password: exactly the writer's bits (print only).
	d, err := OpenWithPassword(enc, "userpw")
	if err != nil {
		t.Fatalf("open print-only with user password: %v", err)
	}
	defer d.Close()
	if got := d.Permissions(); len(got) != 1 || got[0] != fitz.PermPrint {
		t.Fatalf("Permissions with user password: %v, want [PermPrint]", got)
	}
	if !d.CanPrint() {
		t.Error("CanPrint with print-only grant: false, want true")
	}
	if d.CanCopy() {
		t.Error("CanCopy with print-only grant: true, want false")
	}
	// The hook issue 3 consumes: no annotation tools when PermAnnotate
	// is denied (the annot facade does not gate itself yet).
	if d.CanAnnotate() {
		t.Error("CanAnnotate with print-only grant: true, want false")
	}

	// Owner password: everything is granted regardless of the bits.
	o, err := OpenWithPassword(enc, "ownerpw")
	if err != nil {
		t.Fatalf("open print-only with owner password: %v", err)
	}
	defer o.Close()
	if got := o.Permissions(); len(got) != len(allPermissions) {
		t.Fatalf("Permissions with owner password: %v, want all %d", got, len(allPermissions))
	}
	for _, can := range []bool{o.CanPrint(), o.CanCopy(), o.CanAnnotate()} {
		if !can {
			t.Error("owner password must grant every permission")
		}
	}

	// Unencrypted documents grant everything.
	p, err := Open(plain)
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	defer p.Close()
	if got := p.Permissions(); len(got) != len(allPermissions) {
		t.Fatalf("Permissions of a plain doc: %v, want all %d", got, len(allPermissions))
	}

	// The snapshot survives Close and needs no document access.
	p.Close()
	if got := p.Permissions(); len(got) != len(allPermissions) || !p.CanAnnotate() {
		t.Fatalf("Permissions after Close: %v, want the open-time set", got)
	}
}

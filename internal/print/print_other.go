//go:build !windows && !darwin

// print_other.go is the printing stub for platforms without an
// implementation yet: the real pipelines are the Win32 GDI path of
// print.go + sys_windows.go (build-tagged windows) and the macOS PDFKit
// path of print_darwin.go (build-tagged darwin). Where neither builds
// (Linux and the rest), Print reports a clear error, never a silent
// no-op, so the menu item does not look dead.
package print

import (
	"fmt"

	"gopdf/internal/pdf"
)

// Print mirrors the Windows signature so callers compile unchanged.
func Print(doc *pdf.Doc, name string) (printed bool, err error) {
	_ = name
	if doc == nil || doc.NumPages() == 0 {
		return false, fmt.Errorf("print: nothing to print")
	}
	return false, fmt.Errorf("print: not yet supported on this platform (Windows only for now)")
}

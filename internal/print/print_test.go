// print_test.go pins the print dialog's wire format: PRINTDLGW must
// match the SDK's layout exactly (20 fields, 120 bytes on amd64) —
// PrintDlgW validates lStructSize and fails with PDERR_STRUCTSIZE when
// the Go struct is short, a failure that used to read as "canceled" and
// made Print look dead. Asserting the size costs nothing and needs no
// dialog.
package print

import (
	"testing"
	"unsafe"
)

func TestPrintDlgSizeMatchesSDK(t *testing.T) {
	const want = 120 // PRINTDLGW on amd64: 18 padded fields + 2 HGLOBALs
	if got := unsafe.Sizeof(printDlg{}); got != want {
		t.Fatalf("sizeof(printDlg) = %d, want %d — PrintDlgW will reject it with PDERR_STRUCTSIZE", got, want)
	}
}

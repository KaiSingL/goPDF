//go:build darwin

// print_darwin_test.go covers the guard that runs before any AppKit
// call, so the suite stays headless: with no document Print must fail
// in Go, never reach the main-queue hop (in `go test` nothing drains
// the main queue — a real print panel here would deadlock the runner).
// The panel path itself is verified by running the app and printing.
package print

import "testing"

func TestPrintNilDocFailsBeforeAppKit(t *testing.T) {
	printed, err := Print(nil, "test")
	if err == nil {
		t.Fatal("Print(nil) returned no error")
	}
	if printed {
		t.Fatal("Print(nil) reported a printed job")
	}
}

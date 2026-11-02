//go:build darwin

// print_darwin.go implements macOS printing through PDFKit — the same
// path Preview uses: the document's file URL goes into a PDFDocument,
// whose print operation drives the system print panel (printer choice,
// page range, copies, layout) with no custom UI of our own. The
// Windows pipeline renders pages itself and draws them through GDI;
// macOS instead hands the file to the framework and lets AppKit scale
// and paginate, honoring driver-side settings for free.
//
// MyGo ships no printing API (verified against the pinned v0.6.4
// source), so this is direct AppKit/PDFKit through cgo — the app
// already requires cgo for MuPDF, so it adds no toolchain burden.
//
// AppKit wants the main thread, and the main thread is busy running
// NSApplication's event loop. Print's contract (print.go) is a
// background goroutine — PrintActive spawns one — so the work hops to
// the main queue with dispatch_sync_f (the same primitive mygo's own
// darwin backend uses, watch.go) and the calling goroutine blocks on
// the thread hop until the panel is dismissed.
package print

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Quartz

#import <Cocoa/Cocoa.h>
#import <Quartz/Quartz.h>
#import <dispatch/dispatch.h>
#import <stdlib.h>
#import <string.h>

// printPDF shows the system print panel for the PDF at path and runs
// the resulting operation. YES = pages went to the printer; NO = the
// user canceled or the document could not be printed (errMsg set for
// the latter — a cancel must stay silent, as on Windows). Runs ON the
// main queue; call only via printPDFSync.
//
// Manual retain/release (cgo compiles without ARC): doc is released
// once the operation exists — its print view retains the document —
// and the operation itself rides the block's autorelease pool, which
// outlives the synchronous runOperation.
static BOOL printPDF(NSString *path, NSString **errMsg) {
	@autoreleasepool {
		PDFDocument *doc = [[PDFDocument alloc] initWithURL:[NSURL fileURLWithPath:path]];
		if (doc == nil) {
			*errMsg = @"could not open the document for printing";
			return NO;
		}
		if (doc.isLocked) {
			*errMsg = @"password-protected PDFs cannot be printed yet";
			[doc release];
			return NO;
		}
		NSPrintOperation *op = [doc printOperationForPrintInfo:[NSPrintInfo sharedPrintInfo]
													scalingMode:kPDFPrintPageScaleToFit
													autoRotate:YES];
		op.showsPrintPanel = YES;
		op.showsProgressPanel = YES;
		[doc release];
		return [op runOperation];
	}
}

// printCtx carries the call across the queue hop: Go blocks in
// dispatch_sync_f while the main thread runs printPDFTrampoline.
typedef struct {
	NSString *path;
	NSString *err;
	BOOL ran;
} printCtx;

static void printPDFTrampoline(void *v) {
	printCtx *ctx = (printCtx *)v;
	ctx->ran = printPDF(ctx->path, &ctx->err);
}

// printPDFSync hops to the main queue, prints, and returns. errMsgC is
// strdup'd on failure — the caller frees it.
static BOOL printPDFSync(const char *pathC, char **errMsgC) {
	printCtx ctx;
	ctx.path = [NSString stringWithUTF8String:pathC];
	ctx.err = nil;
	ctx.ran = NO;
	dispatch_sync_f(dispatch_get_main_queue(), &ctx, printPDFTrampoline);
	if (ctx.err != nil) {
		*errMsgC = strdup(ctx.err.UTF8String);
	}
	return ctx.ran;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"

	"gopdf/internal/pdf"
)

// busy prevents two panels at once (menu + ⌘P racing) — the same guard
// as the Windows dialog's mutex in print.go (build-tag disjoint).
var busy sync.Mutex

// Print shows the system print panel and prints the document the tab
// has open (doc.Path() feeds PDFKit directly — rendering is the
// framework's job). It returns (printed, err); printed=false with a
// nil error means the user canceled. Call from a goroutine — the panel
// blocks and the work runs on the main thread.
func Print(doc *pdf.Doc, name string) (printed bool, err error) {
	// name is the Windows queue's document title; the macOS queue names
	// the job after the app (no public per-job key in the modern SDK).
	_ = name
	if doc == nil || doc.NumPages() == 0 {
		return false, fmt.Errorf("print: nothing to print")
	}
	busy.Lock()
	defer busy.Unlock()

	var errC *C.char
	cpath := C.CString(doc.Path())
	defer C.free(unsafe.Pointer(cpath))

	ran := C.printPDFSync(cpath, &errC)
	if errC != nil {
		defer C.free(unsafe.Pointer(errC))
		return false, fmt.Errorf("print: %s", C.GoString(errC))
	}
	if !ran {
		return false, nil // canceled
	}
	return true, nil
}

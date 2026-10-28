// sys_windows.go declares the Win32 pieces printing needs: the PrintDlgW
// common dialog (comdlg32) and the GDI/winspool drawing calls (gdi32).
// Struct layouts follow PRINTDLGW on amd64 (8-byte alignment; the struct
// is 66 bytes packed per the SDK — see field offsets in the comments).
package print

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

var (
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	winspool = syscall.NewLazyDLL("winspool.drv")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procPrintDlgW          = comdlg32.NewProc("PrintDlgW")
	procCommDlgExtendedErr = comdlg32.NewProc("CommDlgExtendedError")
	procCoInitializeEx     = ole32.NewProc("CoInitializeEx")
	procCoUninitialize     = ole32.NewProc("CoUninitialize")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procGetDeviceCaps      = gdi32.NewProc("GetDeviceCaps")
	procStretchDIBits      = gdi32.NewProc("StretchDIBits")
	procStartDocW          = gdi32.NewProc("StartDocW")
	procEndDoc             = gdi32.NewProc("EndDoc")
	procStartPage          = gdi32.NewProc("StartPage")
	procEndPage            = gdi32.NewProc("EndPage")
)

// Print dialog flags (winuser/prnsetup.h subset).
const (
	pdAllPages     = 0x00000000
	pdPageNums     = 0x00000002 // From/To page selection
	pdReturnDC     = 0x00000100 // hDC of the chosen printer
	pdNoSelection  = 0x00000004 // disable the Selection radio
	pdUseDevCopies = 0x00040000 // copies through DEVMODE
)

// GetDeviceCaps indexes.
const (
	HORZRES        = 8
	VERTRES        = 10
	LOGPIXELSX     = 88
	LOGPIXELSY     = 90
	PHYSICALWIDTH  = 110
	PHYSICALHEIGHT = 111
)

// dibColors / rasterOp for StretchDIBits.
const (
	dibRGBColors = 0
	srccopy      = 0x00CC0020
)

// dialogResult carries what PrintDlgW told us.
type dialogResult struct {
	fromPage, toPage int
	copies           int
}

// PRINTDLGW for amd64. Field order is exactly the SDK's; the struct is
// passed by pointer so Go-side padding must match Windows x64 (every
// pointer/uintptr field 8-aligned — the two uint16 pairs pack into the
// holes before the following pointers).
type printDlg struct {
	lStructSize         uint32
	hwndOwner           uintptr
	hDevMode            uintptr
	hDevNames           uintptr
	hDC                 uintptr
	flags               uint32
	nFromPage           uint16
	nToPage             uint16
	nMinPage            uint16
	nMaxPage            uint16
	nCopies             uint16
	hInstance           uintptr
	lCustData           uintptr
	lpfnPrintHook       uintptr
	lpfnSetupHook       uintptr
	lpPrintTemplateName uintptr
	lpSetupTemplateName uintptr
}

// comInitialize pins the calling goroutine's thread with an STA COM
// apartment — the state the common dialogs want. A bare goroutine thread
// has no COM apartment at all, and PrintDlgW failing to open returns 0,
// which reads as "the user canceled": the dialog silently never appears.
// S_FALSE (1, already initialized on this thread) is as good as S_OK.
// coUninitialize releases the apartment; the thread stays pinned until
// printThreadDone.
func comInitialize() error {
	const coinitApartmentThreaded = 2
	r, _, e := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if int32(r) < 0 {
		return fmt.Errorf("print: CoInitializeEx: %v", e)
	}
	return nil
}

func coUninitialize() { procCoUninitialize.Call() }

func commDlgExtendedError() uint32 {
	r, _, _ := procCommDlgExtendedErr.Call()
	return uint32(r)
}

// showPrintDialog opens PrintDlgW preset to the document's page range and
// returns the result, the printer DC (when ok), whether to proceed, and
// any error. Call from the thread comInitialize pinned.
func showPrintDialog(numPages int, docName string) (res dialogResult, hdc uintptr, ok bool, err error) {
	d := &printDlg{
		lStructSize: uint32(unsafe.Sizeof(printDlg{})),
		nMinPage:    1,
		nMaxPage:    uint16(clampI(numPages, 1, 9999)),
		nFromPage:   1,
		nToPage:     uint16(clampI(numPages, 1, 9999)),
		nCopies:     1,
		flags:       pdPageNums | pdReturnDC | pdNoSelection | pdUseDevCopies,
	}
	r1, _, _ := procPrintDlgW.Call(uintptr(unsafe.Pointer(d)))
	if r1 == 0 {
		// 0 is a cancel OR a dialog that never opened; only
		// CommDlgExtendedError tells them apart, and a failure swallowed
		// here reads to the user as "the menu item does nothing".
		if ext := commDlgExtendedError(); ext != 0 {
			return dialogResult{}, 0, false, fmt.Errorf("print: the print dialog failed to open (CommDlgExtendedError 0x%x)", ext)
		}
		return dialogResult{}, 0, false, nil // plain cancel
	}
	if d.hDC == 0 {
		return dialogResult{}, 0, false, fmt.Errorf("print: dialog returned no device context")
	}
	from := int(d.nFromPage)
	to := int(d.nToPage)
	if d.flags&pdPageNums == 0 {
		from, to = 1, numPages
	}
	from = clampI(from, 1, numPages)
	to = clampI(to, from, numPages)
	return dialogResult{
		fromPage: from - 1, // 0-based
		toPage:   to - 1,
		copies:   maxI(int(d.nCopies), 1),
	}, d.hDC, true, nil
}

func deviceCaps(hdc uintptr, index int) int {
	r, _, _ := procGetDeviceCaps.Call(hdc, uintptr(index))
	return int(int32(r))
}

func deleteDC(hdc uintptr) {
	procDeleteDC.Call(hdc)
}

// docInfW is StartDocW's DOCINFOW.
type docInfW struct {
	cbSize       uint32
	lpszDocName  *uint16
	lpszOutput   *uint16
	lpszDatatype *uint16
	fwType       uint32
}

func startDoc(hdc uintptr, name string) error {
	n, err := syscall.UTF16PtrFromString(pageLabel(name))
	if err != nil {
		return err
	}
	di := &docInfW{cbSize: uint32(unsafe.Sizeof(docInfW{})), lpszDocName: n}
	r, _, e := procStartDocW.Call(hdc, uintptr(unsafe.Pointer(di)))
	if int32(r) <= 0 {
		return fmt.Errorf("print: StartDoc: %v", e)
	}
	return nil
}

func endDoc(hdc uintptr) error {
	r, _, e := procEndDoc.Call(hdc)
	if int32(r) <= 0 {
		return fmt.Errorf("print: EndDoc: %v", e)
	}
	return nil
}

func startPage(hdc uintptr) error {
	r, _, e := procStartPage.Call(hdc)
	if int32(r) <= 0 {
		return fmt.Errorf("print: StartPage: %v", e)
	}
	return nil
}

func endPage(hdc uintptr) error {
	r, _, e := procEndPage.Call(hdc)
	if int32(r) <= 0 {
		return fmt.Errorf("print: EndPage: %v", e)
	}
	return nil
}

// bitmapInfoHeader for a top-down 32bpp DIB (negative height = top-down).
type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32 // unused for BI_RGB
}

// stretchDIBits draws img into hdc at (dstX,dstY) sized (dstW,dstH),
// swapping the RGBA pixels to the BGRA a DIB expects.
func stretchDIBits(hdc uintptr, img *image.RGBA, dstX, dstY, dstW, dstH int) error {
	b := swapToBGRA(img)
	bi := &bitmapInfo{}
	bi.header.biSize = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bi.header.biWidth = int32(img.Bounds().Dx())
	bi.header.biHeight = -int32(img.Bounds().Dy()) // top-down
	bi.header.biPlanes = 1
	bi.header.biBitCount = 32
	bi.header.biCompression = 0 // BI_RGB
	bi.header.biSizeImage = uint32(len(b))

	r, _, e := procStretchDIBits.Call(
		hdc,
		uintptr(dstX), uintptr(dstY), uintptr(dstW), uintptr(dstH),
		0, 0, uintptr(img.Bounds().Dx()), uintptr(img.Bounds().Dy()),
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(unsafe.Pointer(bi)),
		dibRGBColors, srccopy,
	)
	if r == ^uintptr(0) || r == 0 { // GDI_ERROR (-1) or explicit failure
		return fmt.Errorf("print: StretchDIBits: %v", e)
	}
	return nil
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

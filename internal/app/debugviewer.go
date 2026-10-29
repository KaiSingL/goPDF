// debugviewer.go is the viewer debug mode (GOPDF_DEBUG_VIEWER=1, or
// enabled by the scrolltest harness): self-describing frames — every page
// box paints the page number and a stable id of the bitmap it shows, the
// status bar paints the frame counter — plus an in-memory event ring the
// viewer records its builds, scroll state and render completions into.
// Ctrl+Shift+D dumps the ring plus a window capture, so a live occurrence
// of the scroll-bleed bug yields ground truth in one file pair: what the
// app bound vs what the pixels showed.
package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
)

var (
	debugOn    bool          // the whole mode; off costs nothing
	debugFrame atomic.Uint64 // viewer build counter
	debugMu    sync.Mutex    // guards the ring, bitmap ids and rects
	debugRing  []string      // bounded event log
	debugBMIDs = map[*ui.Bitmap]string{}
	debugRects = map[int]ui.Rect{} // page (0-based) -> last painted rect
)

// EnableViewerDebug turns the debug mode on before the window opens.
func EnableViewerDebug() { debugOn = true }

// debugSetRect records where page's box was last painted (window
// coordinates) — scrolltest crops captures to it.
func debugSetRect(page int, r ui.Rect) {
	debugMu.Lock()
	debugRects[page] = r
	debugMu.Unlock()
}

// DebugPageRect returns the last painted rect of page (0-based).
func DebugPageRect(page int) (ui.Rect, bool) {
	debugMu.Lock()
	defer debugMu.Unlock()
	r, ok := debugRects[page]
	return r, ok
}

// debugBMID assigns a stable short id per bitmap so logs, overlay and
// captures can refer to the same buffer.
func debugBMID(bm *ui.Bitmap) string {
	if bm == nil {
		return "-"
	}
	debugMu.Lock()
	defer debugMu.Unlock()
	if id, ok := debugBMIDs[bm]; ok {
		return id
	}
	id := fmt.Sprintf("%x", len(debugBMIDs)+1)
	debugBMIDs[bm] = id
	return id
}

// debugLogf records one event in the ring (bounded; oldest drop).
func debugLogf(format string, args ...any) {
	debugMu.Lock()
	defer debugMu.Unlock()
	debugRing = append(debugRing, time.Now().Format("15:04:05.000")+" "+
		fmt.Sprintf(format, args...))
	if len(debugRing) > 4000 {
		debugRing = debugRing[len(debugRing)-4000:]
	}
}

// DebugDumpViewer writes the event ring and a window capture to
// GOPDF_DEBUG_DIR (or the temp dir). Hotkey: Ctrl+Shift+D (debug mode).
func DebugDumpViewer() {
	if !debugOn {
		return
	}
	dir := os.Getenv("GOPDF_DEBUG_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	stamp := time.Now().Format("150405.000")
	if window != nil {
		if b, err := window.CapturePage(); err == nil {
			p := filepath.Join(dir, "dump-"+stamp+".png")
			if err := os.WriteFile(p, b, 0o644); err == nil {
				log.Printf("debugviewer: capture %s", p)
			}
		}
	}
	debugMu.Lock()
	ring := slices.Clone(debugRing)
	debugMu.Unlock()
	p := filepath.Join(dir, "ring-"+stamp+".log")
	head := fmt.Sprintf("frame %d\n", debugFrame.Load())
	body := ""
	for _, line := range ring {
		body += line + "\n"
	}
	if err := os.WriteFile(p, []byte(head+body), 0o644); err == nil {
		log.Printf("debugviewer: ring %s", p)
	}
}

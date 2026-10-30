// mem.go keeps the process's resident set honest. Go's scavenger returns
// freed pages to the OS lazily, so after the app drops a large cache — a
// tab close, a reload, a zoom settling — Task Manager keeps showing the
// old peak for minutes. notifyRelease schedules one background
// GC-plus-scavenge per debounce window; the app keeps running meanwhile
// and repeated calls inside the window coalesce.
package app

import (
	"runtime/debug"
	"sync"
	"time"
)

// releaseDebounce is the minimum interval between FreeOSMemory runs: a
// full GC of a multi-hundred-MB heap is not free, so zooming must not
// trigger one per settled render.
const releaseDebounce = time.Second

var (
	releaseMu    sync.Mutex
	releaseTimer *time.Timer
)

// notifyRelease asks for a background FreeOSMemory after releaseDebounce.
// No-op before the window exists (unit tests: no process-wide GC churn
// from tab churn).
func notifyRelease() {
	if window == nil {
		return
	}
	releaseMu.Lock()
	defer releaseMu.Unlock()
	if releaseTimer != nil {
		releaseTimer.Reset(releaseDebounce)
		return
	}
	releaseTimer = time.AfterFunc(releaseDebounce, func() {
		releaseMu.Lock()
		releaseTimer = nil
		releaseMu.Unlock()
		debug.FreeOSMemory()
	})
}

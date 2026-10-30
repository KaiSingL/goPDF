// thumbs.go serves the launcher's recent-document cards (the initial
// window and every New Tab page): the first page of each recent file as a
// small bitmap.
//
// Three layers keep a cold start cheap:
//
//   - In-memory LRU (recentThumbs): what earlier launcher views already
//     showed.
//   - Disk cache (thumbs dir next to settings.json): small PNGs keyed by
//     path+size+mtime, so a warm session decodes a few tiny images and
//     never opens a PDF at startup.
//   - One serialized worker: a disk miss renders page 0 through
//     pdf.RenderThumb (a shallow open — no page-tree walk, no outline)
//     ONE DOCUMENT AT A TIME. The old code opened a full pdf.Doc per
//     visible card, so a first launch loaded up to eight complete
//     documents concurrently; now at most one transient document is open.
//
// Nothing here touches AppState; everything shared is guarded (recentMu)
// or goroutine-safe (ui.NewBitmap of an owned image).
package app

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

var (
	recentMu       sync.Mutex
	recentThumbs   = map[string]*ui.Bitmap{} // path → thumb; nil entry = tried and failed
	recentOrder    []string                  // insertion order, for the recentThumbCap
	recentInflight = map[string]bool{}       // path → queued or rendering
)

// recentThumbCap bounds the in-memory thumbnail cache so a long session
// browsing many recents cannot grow it without end. The launcher shows at
// most 8 cards at once; failures are cached as nil, exactly like
// successes, so a broken PDF is not retried every build.
const recentThumbCap = 12

// recentThumbPut stores the thumbnail under path, evicting the oldest
// entries past recentThumbCap in FIFO order. The caller holds recentMu.
func recentThumbPut(path string, bm *ui.Bitmap) {
	if _, ok := recentThumbs[path]; !ok {
		recentOrder = append(recentOrder, path)
	}
	recentThumbs[path] = bm
	for len(recentOrder) > recentThumbCap {
		delete(recentThumbs, recentOrder[0])
		recentOrder = recentOrder[1:]
	}
}

// recentThumbFor returns the cached first-page thumbnail of a recent
// document: memory, then the disk cache, else one queued render.
func recentThumbFor(path string) *ui.Bitmap {
	recentMu.Lock()
	bm, done := recentThumbs[path]
	busy := recentInflight[path]
	recentMu.Unlock()
	if done || busy {
		return bm
	}
	if img, ok := cachedThumbPNG(path); ok {
		var out *ui.Bitmap
		if img != nil {
			out = ui.NewBitmap(img)
		}
		recentMu.Lock()
		recentThumbPut(path, out)
		recentMu.Unlock()
		return out
	}
	recentMu.Lock()
	recentInflight[path] = true
	recentMu.Unlock()
	enqueueThumb(path)
	return nil
}

// thumbQueue hands paths to the single thumbWorker; thumbOnce starts both
// on the first disk miss.
var (
	thumbQueue chan string
	thumbOnce  sync.Once
)

// enqueueThumb queues one thumbnail render. The channel bound is the
// whole backlog a launcher can produce (≤ its 8 cards plus browsing);
// when full, the job is dropped and the next build re-enqueues the path.
func enqueueThumb(path string) {
	thumbOnce.Do(func() {
		thumbQueue = make(chan string, 32)
		go thumbWorker()
	})
	select {
	case thumbQueue <- path:
	default:
		recentMu.Lock()
		delete(recentInflight, path)
		recentMu.Unlock()
	}
}

// thumbWorker drains the queue: at most one transient document open at a
// time. Results land in the disk cache (successes) and the in-memory LRU;
// Invalidate wakes the launcher to show them.
func thumbWorker() {
	for path := range thumbQueue {
		img, err := pdf.RenderThumb(path)
		var out *ui.Bitmap
		if err == nil && img != nil {
			cacheThumbPNG(path, img)
			out = ui.NewBitmap(img)
		}
		recentMu.Lock()
		recentThumbPut(path, out) // nil on failure: tried and failed
		delete(recentInflight, path)
		recentMu.Unlock()
		if window != nil {
			window.Invalidate()
		}
	}
}

// ---- disk cache ---------------------------------------------------------

// thumbPNGCap bounds the thumbnail directory: past this many files the
// oldest are deleted on the next write, so the cache cannot grow forever.
const thumbPNGCap = 64

// thumbPNGPath is the disk path of path's cached first-page thumbnail.
// The key folds in the file's size and modification time, so an edited or
// replaced document is re-rendered rather than shown stale. ok is false
// when the cache directory is unknown or the document is gone.
func thumbPNGPath(path string) (p string, ok bool) {
	dir, err := ThumbsDir()
	if err != nil {
		return "", false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	key := mustAbs(path) + "|" + fi.ModTime().Format(time.RFC3339Nano) + "|" +
		filepath.Base(path)
	sum := sha1.Sum([]byte(key))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".png"), true
}

// cachedThumbPNG returns path's thumbnail from disk. ok is false on a
// miss; a corrupt entry is deleted and reported as a miss.
func cachedThumbPNG(path string) (*image.RGBA, bool) {
	p, ok := thumbPNGPath(path)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		_ = os.Remove(p)
		return nil, false
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	}
	return rgba, true
}

// cacheThumbPNG writes path's thumbnail into the disk cache. Best-effort:
// a full disk or a missing directory only costs the next launch a render.
func cacheThumbPNG(path string, img *image.RGBA) {
	p, ok := thumbPNGPath(path)
	if !ok {
		return
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		return
	}
	pruneThumbPNGs(dir)
}

// pruneThumbPNGs deletes the oldest cached thumbnails past thumbPNGCap.
func pruneThumbPNGs(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) <= thumbPNGCap {
		return
	}
	type named struct {
		path string
		mod  time.Time
	}
	files := make([]named, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err == nil {
			files = append(files, named{filepath.Join(dir, e.Name()), fi.ModTime()})
		}
	}
	slices.SortFunc(files, func(a, b named) int { return a.mod.Compare(b.mod) })
	for i := 0; i < len(files)-thumbPNGCap; i++ {
		_ = os.Remove(files[i].path)
	}
}

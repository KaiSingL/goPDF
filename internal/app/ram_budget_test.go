// ram_budget_test.go pins the memory work: the page-bitmap and thumbnail
// caches are bounded by pixel bytes (not entry counts), closing a tab
// restores the byte accounting exactly, and the launcher's recent
// thumbnails come from a disk cache served by one serialized worker.
package app

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// bmpCacheReset drops every cached page bitmap and restores bmpBudget.
func bmpCacheReset(budget int64) {
	viewerMu.Lock()
	for k := range bmpCache {
		bmpDelete(k)
	}
	viewerMu.Unlock()
	bmpBudget = budget
}

// TestBmpCacheByteBudget: bmpPut evicts oldest-first past bmpBudget,
// never the newest entry, and keeps bmpBytes exact across refreshes.
func TestBmpCacheByteBudget(t *testing.T) {
	const px = int64(4 * 4 * 4) // one 4×4 RGBA
	old := bmpBudget
	bmpBudget = 2 * px
	t.Cleanup(func() { bmpCacheReset(old) })
	bmpCacheReset(bmpBudget)

	bm := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 4, 4)))
	k1, k2, k3 := bmpKey{"t", 1, 0}, bmpKey{"t", 2, 0}, bmpKey{"t", 3, 0}

	put := func(k bmpKey, b *ui.Bitmap, dpi int) {
		viewerMu.Lock()
		bmpPut(k, b, dpi)
		viewerMu.Unlock()
	}
	snap := func() (int, int64) {
		viewerMu.Lock()
		defer viewerMu.Unlock()
		return len(bmpCache), bmpBytes
	}

	put(k1, bm, 100)
	put(k2, bm, 100)
	if n, b := snap(); n != 2 || b != 2*px {
		t.Fatalf("after 2 puts: entries=%d bytes=%d, want 2/%d", n, b, 2*px)
	}

	put(k3, bm, 100) // over budget: k1 (oldest) goes
	if n, b := snap(); n != 2 || b != 2*px {
		t.Fatalf("after 3 puts: entries=%d bytes=%d, want 2/%d", n, b, 2*px)
	}
	viewerMu.Lock()
	_, live1 := bmpCache[k1]
	viewerMu.Unlock()
	if live1 {
		t.Error("oldest entry survived eviction")
	}

	put(k2, bm, 200) // refresh: replaces bytes, total unchanged
	if n, b := snap(); n != 2 || b != 2*px {
		t.Errorf("after refresh: entries=%d bytes=%d, want 2/%d (double-counted?)", n, b, 2*px)
	}

	// An entry larger than the whole budget is kept — it is the one on
	// screen — and everything else goes first.
	big := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 32, 32)))
	put(k3, big, 100)
	if n, b := snap(); n != 1 || b != int64(32*32*4) {
		t.Fatalf("oversized newest: entries=%d bytes=%d, want 1/%d", n, b, 32*32*4)
	}
}

// TestThumbBitmapByteBudget: the sidebar's thumbnail bitmaps evict by
// bytes, and teardownTabState restores thumbBytes exactly.
func TestThumbBitmapByteBudget(t *testing.T) {
	const px = int64(4 * 4 * 4 * 4 / 3) // one 4×4 thumb incl. mipmap third
	oldBudget, oldBytes := thumbBudget, thumbBytes
	thumbBudget = 2 * px
	t.Cleanup(func() {
		for k := range thumbBitmaps {
			thumbDrop(k)
		}
		thumbBudget, thumbBytes = oldBudget, oldBytes
	})

	ta := &Tab{ID: "ram-thumb-tab"}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))

	b1 := thumbBitmapFor(ta, 1, img)
	thumbBitmapFor(ta, 2, img)
	if thumbBytes != 2*px {
		t.Fatalf("thumbBytes = %d, want %d", thumbBytes, 2*px)
	}
	thumbBitmapFor(ta, 3, img) // over budget: page 1 (oldest) goes
	if _, live := thumbBitmaps[thumbKey{ta.ID, 1}]; live {
		t.Error("oldest thumbnail survived eviction")
	}
	if got := thumbBitmapFor(ta, 2, img); got == nil {
		t.Error("surviving thumbnail missing")
	}

	// Re-render with new pixels of the same size: Update in place, bytes
	// unchanged (no double count).
	img2 := image.NewRGBA(image.Rect(0, 0, 4, 4))
	thumbBitmapFor(ta, 2, img2)
	if thumbBytes != 2*px {
		t.Errorf("thumbBytes after update = %d, want %d", thumbBytes, 2*px)
	}

	// Teardown clears the tab's entries and the accounting with them.
	teardownTabState(ta.ID)
	if thumbBytes != 0 {
		t.Errorf("thumbBytes after teardown = %d, want 0", thumbBytes)
	}
	if len(thumbBitmaps) != 0 || len(thumbOrder) != 0 {
		t.Errorf("thumb maps not empty after teardown: %d entries, %d order",
			len(thumbBitmaps), len(thumbOrder))
	}
	_ = b1
}

// TestTeardownBmpAccounting: after a tab's bitmaps are torn down, the
// bitmap byte total returns to what the other tabs hold.
func TestTeardownBmpAccounting(t *testing.T) {
	old := bmpBudget
	bmpBudget = 1 << 30
	t.Cleanup(func() { bmpCacheReset(old) })
	bmpCacheReset(bmpBudget)

	bm := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 4, 4)))
	viewerMu.Lock()
	bmpPut(bmpKey{"ram-bmp-tab", 1, 0}, bm, 100)
	bmpPut(bmpKey{"other", 1, 0}, bm, 100)
	before := bmpBytes
	viewerMu.Unlock()

	teardownTabState("ram-bmp-tab")

	viewerMu.Lock()
	after := bmpBytes
	viewerMu.Unlock()
	if after != before-int64(4*4*4) {
		t.Errorf("bmpBytes after teardown = %d, want %d", after, before-int64(4*4*4))
	}
}

// TestRecentThumbDiskCache: write → hit, mtime bump → miss, corrupt entry
// → miss and self-cleanup.
func TestRecentThumbDiskCache(t *testing.T) {
	dir := t.TempDir()
	old := ThumbsDir
	ThumbsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { ThumbsDir = old })

	path := genTestPDF(t, 2)

	if img, ok := cachedThumbPNG(path); ok {
		t.Fatalf("disk hit before any write (img=%v)", img)
	}
	img, err := pdf.RenderThumb(path)
	if err != nil {
		t.Fatalf("RenderThumb: %v", err)
	}
	cacheThumbPNG(path, img)
	got, ok := cachedThumbPNG(path)
	if !ok || got == nil {
		t.Fatal("disk cache miss after write")
	}
	if got.Rect.Dx() != img.Rect.Dx() || got.Rect.Dy() != img.Rect.Dy() {
		t.Errorf("round-trip size = %dx%d, want %dx%d",
			got.Rect.Dx(), got.Rect.Dy(), img.Rect.Dx(), img.Rect.Dy())
	}

	// An edited document (new mtime) must not serve the stale pixels.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if _, ok := cachedThumbPNG(path); ok {
		t.Error("stale hit after mtime change")
	}

	// A corrupt entry is a miss, and removes itself.
	cacheThumbPNG(path, img)
	p, ok := thumbPNGPath(path)
	if !ok {
		t.Fatal("thumbPNGPath failed for an existing file")
	}
	if err := os.WriteFile(p, []byte("not a png"), 0o644); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	if _, ok := cachedThumbPNG(path); ok {
		t.Error("corrupt entry reported as a hit")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("corrupt entry not removed")
	}

	// A missing document has no cache path at all.
	if _, ok := thumbPNGPath(filepath.Join(dir, "gone.pdf")); ok {
		t.Error("cache path returned for a missing document")
	}
}

// TestRecentThumbForServesFromDisk: a cached PNG is decoded and returned
// synchronously — no queue, no render.
func TestRecentThumbForServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	oldDir := ThumbsDir
	ThumbsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { ThumbsDir = oldDir })

	path := genTestPDF(t, 1)
	img, err := pdf.RenderThumb(path)
	if err != nil {
		t.Fatalf("RenderThumb: %v", err)
	}
	cacheThumbPNG(path, img)

	recentMu.Lock()
	delete(recentThumbs, path)
	delete(recentInflight, path)
	recentMu.Unlock()

	if bm := recentThumbFor(path); bm == nil {
		t.Fatal("recentThumbFor returned nil with the thumb on disk")
	}
	recentMu.Lock()
	busy := recentInflight[path]
	recentMu.Unlock()
	if busy {
		t.Error("disk hit still enqueued a render")
	}
}

// TestRecentThumbForQueue: a cold path ends up in the in-memory LRU via
// the serialized worker (one transient document, not one per card).
func TestRecentThumbForQueue(t *testing.T) {
	dir := t.TempDir()
	oldDir := ThumbsDir
	ThumbsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { ThumbsDir = oldDir })

	path := genTestPDF(t, 1)
	recentMu.Lock()
	delete(recentThumbs, path)
	delete(recentInflight, path)
	recentMu.Unlock()

	if bm := recentThumbFor(path); bm != nil {
		t.Fatal("cold path returned a bitmap synchronously")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		recentMu.Lock()
		bm, busy := recentThumbs[path], recentInflight[path]
		recentMu.Unlock()
		if bm != nil && !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued thumbnail never landed (bm=%v busy=%v)", bm, busy)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Second ask is served from memory.
	if bm := recentThumbFor(path); bm == nil {
		t.Error("warm recentThumbFor returned nil")
	}
}

// TestThumbPNGDirCap: writes past thumbPNGCap delete the oldest files.
func TestThumbPNGDirCap(t *testing.T) {
	dir := t.TempDir()      // the cache
	srcs := t.TempDir()     // documents live OUTSIDE the cache dir
	old := ThumbsDir
	ThumbsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { ThumbsDir = old })

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < thumbPNGCap+4; i++ {
		// Distinct source paths give distinct cache entries.
		src := filepath.Join(srcs, filepath.Base(t.TempDir())+".pdf")
		if err := os.WriteFile(src, minimalPDF([]string{"x"}), 0o644); err != nil {
			t.Fatal(err)
		}
		cacheThumbPNG(src, img)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) > thumbPNGCap {
		t.Errorf("cache dir holds %d files, want ≤ %d", len(ents), thumbPNGCap)
	}
	if len(ents) != thumbPNGCap {
		t.Errorf("cache dir holds %d files, want exactly %d (over-pruning?)",
			len(ents), thumbPNGCap)
	}
}

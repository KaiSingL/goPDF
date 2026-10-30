package pdf

import (
	"image"
	"sync"
	"testing"
)

// mkImg makes a plain RGBA of w×h pixels (4 bytes each).
func mkImg(w, h int) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

func imgBytes(w, h int) int64 { return int64(4 * w * h) }

func TestPageCachePutGet(t *testing.T) {
	c := NewPageCache(1 << 20)
	k := CacheKey{Page: 2, DPI: 150, Rotate: 90}

	if got := c.Get(k); got != nil {
		t.Errorf("Get on empty cache = %v, want nil", got)
	}
	if c.Len() != 0 || c.Bytes() != 0 {
		t.Errorf("empty cache Len=%d Bytes=%d, want 0/0", c.Len(), c.Bytes())
	}

	img := mkImg(4, 4)
	c.Put(k, img)
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	if got := c.Bytes(); got != imgBytes(4, 4) {
		t.Errorf("Bytes = %d, want %d", got, imgBytes(4, 4))
	}
	if got := c.Get(k); got != img {
		t.Error("Get did not return the stored image pointer")
	}

	// Different key misses even with the same page.
	if got := c.Get(CacheKey{Page: 2, DPI: 150, Rotate: 0}); got != nil {
		t.Errorf("Get with other rotate = non-nil, want nil")
	}

	c.Put(k, nil) // nil is a no-op
	if c.Len() != 1 {
		t.Errorf("Put(nil) changed Len to %d", c.Len())
	}
}

func TestPageCacheReplaceKey(t *testing.T) {
	c := NewPageCache(1 << 20)
	k := CacheKey{Page: 0, DPI: 72}

	small, big := mkImg(4, 4), mkImg(8, 4)
	c.Put(k, small)
	c.Put(k, big) // replaces: only the new image counts
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	if got := c.Bytes(); got != imgBytes(8, 4) {
		t.Errorf("Bytes = %d, want %d (old entry must be subtracted)", got, imgBytes(8, 4))
	}
	if got := c.Get(k); got != big {
		t.Error("Get returned the replaced image")
	}
}

func TestPageCacheLRU(t *testing.T) {
	const s = int64(4 * 4 * 4) // one 4x4 RGBA
	c := NewPageCache(3 * s)

	k1 := CacheKey{Page: 1}
	k2 := CacheKey{Page: 2}
	k3 := CacheKey{Page: 3}
	k4 := CacheKey{Page: 4}

	c.Put(k1, mkImg(4, 4))
	c.Put(k2, mkImg(4, 4))
	c.Put(k3, mkImg(4, 4))
	if c.Len() != 3 || c.Bytes() != 3*s {
		t.Fatalf("after 3 puts: Len=%d Bytes=%d, want 3/%d", c.Len(), c.Bytes(), 3*s)
	}

	c.Get(k1) // bump k1: k2 is now LRU

	c.Put(k4, mkImg(4, 4)) // must evict k2

	if got := c.Get(k2); got != nil {
		t.Error("k2 survived; expected LRU eviction of the least-recently-used key")
	}
	for _, k := range []CacheKey{k1, k3, k4} {
		if got := c.Get(k); got == nil {
			t.Errorf("k%d missing after eviction; wrong victim", k.Page)
		}
	}
	if c.Len() != 3 || c.Bytes() != 3*s {
		t.Errorf("after eviction: Len=%d Bytes=%d, want 3/%d", c.Len(), c.Bytes(), 3*s)
	}

	// Without the bump, insertion order would be the eviction order.
	c2 := NewPageCache(2 * s)
	a, b, d := CacheKey{Page: 1}, CacheKey{Page: 2}, CacheKey{Page: 3}
	c2.Put(a, mkImg(4, 4))
	c2.Put(b, mkImg(4, 4))
	c2.Put(d, mkImg(4, 4))
	if c2.Get(a) != nil {
		t.Error("oldest key a survived despite no access; LRU order wrong")
	}
	if c2.Get(b) == nil || c2.Get(d) == nil {
		t.Error("b/d should survive")
	}
}

func TestPageCacheOversized(t *testing.T) {
	c := NewPageCache(imgBytes(4, 4)) // holds exactly one 4x4
	c.Put(CacheKey{Page: 9}, mkImg(8, 8))
	if c.Len() != 0 || c.Bytes() != 0 {
		t.Errorf("oversized image stored: Len=%d Bytes=%d, want 0/0", c.Len(), c.Bytes())
	}
	if got := c.Get(CacheKey{Page: 9}); got != nil {
		t.Error("oversized image retrievable")
	}
	// Exactly-fitting image is stored.
	c.Put(CacheKey{Page: 9}, mkImg(4, 4))
	if c.Len() != 1 {
		t.Errorf("exact-fit image not stored, Len=%d", c.Len())
	}
}

func TestPageCacheEnsureMax(t *testing.T) {
	c := NewPageCache(128 << 20)
	if got := c.Max(); got != 128<<20 {
		t.Fatalf("Max = %d, want the constructor budget", got)
	}
	// Smaller asks never shrink the budget.
	if got := c.EnsureMax(64 << 20); got != 128<<20 {
		t.Errorf("EnsureMax(small) = %d, want unchanged", got)
	}
	if got := c.EnsureMax(200 << 20); got != 200<<20 {
		t.Errorf("EnsureMax(big) = %d, want raised", got)
	}
	if got := c.Max(); got != 200<<20 {
		t.Errorf("Max after raise = %d, want 200MB", got)
	}

	// The raise really holds more: three big images survive where the
	// constructor budget would have evicted all but one.
	big := mkImg(2048, 2048) // 16MB of pixels
	c2 := NewPageCache(2 * int64(len(big.Pix)))
	c2.EnsureMax(3 * int64(len(big.Pix)))
	for p := 0; p < 3; p++ {
		c2.Put(CacheKey{Page: p}, big)
	}
	if c2.Len() != 3 {
		t.Errorf("Len = %d, want 3 after EnsureMax made room", c2.Len())
	}
}

func TestPageCacheClear(t *testing.T) {
	c := NewPageCache(1 << 20)
	c.Put(CacheKey{Page: 1}, mkImg(4, 4))
	c.Put(CacheKey{Page: 2}, mkImg(4, 4))
	c.Clear()
	if c.Len() != 0 || c.Bytes() != 0 {
		t.Errorf("after clear: Len=%d Bytes=%d, want 0/0", c.Len(), c.Bytes())
	}
	if got := c.Get(CacheKey{Page: 1}); got != nil {
		t.Error("Get after Clear returned an image")
	}
	// Cache stays usable after Clear.
	c.Put(CacheKey{Page: 1}, mkImg(4, 4))
	if c.Len() != 1 {
		t.Errorf("Len after reuse = %d, want 1", c.Len())
	}
}

func TestPageCacheConcurrent(t *testing.T) {
	c := NewPageCache(1 << 18)
	keys := make([]CacheKey, 8)
	for i := range keys {
		keys[i] = CacheKey{Page: i, DPI: 150}
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				k := keys[(w+i)%len(keys)]
				c.Put(k, mkImg(8, 8))
				c.Get(k)
				c.Get(keys[w])
				_ = c.Len()
				_ = c.Bytes()
				if i%50 == 0 {
					c.Clear()
				}
			}
		}(w)
	}
	wg.Wait()
	if c.Bytes() < 0 || c.Bytes() > 1<<18 {
		t.Errorf("Bytes = %d outside [0, max]", c.Bytes())
	}
}

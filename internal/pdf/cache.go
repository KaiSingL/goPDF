package pdf

import (
	"image"
	"sync"
)

// PageCache is a thread-safe LRU cache of rendered page images, bounded by
// total pixel bytes. The UI thread reads; render callbacks write from the
// Doc worker goroutine.
type PageCache struct {
	mtx   sync.Mutex
	max   int64
	total int64
	seq   uint64
	m     map[CacheKey]*cacheEntry
}

type cacheEntry struct {
	img    *image.RGBA
	recent uint64
}

// NewPageCache returns a cache holding at most maxBytes of RGBA pixel
// data. The budget starts at defaultCacheBytes (state.go) and is raised
// adaptively by EnsureMax when renders larger than half the budget land —
// ordinary sessions never leave the default, extreme zoom keeps today's
// scroll-back warmth.
func NewPageCache(maxBytes int64) *PageCache {
	return &PageCache{max: maxBytes, m: make(map[CacheKey]*cacheEntry)}
}

// EnsureMax raises the cache budget to nBytes if that is larger than the
// current budget — only up, never down, so one call per render can lift
// the ceiling without another caller being able to shrink it. Returns the
// budget in effect. Thread-safe.
func (c *PageCache) EnsureMax(nBytes int64) int64 {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if nBytes > c.max {
		c.max = nBytes
	}
	return c.max
}

// Max returns the cache budget in bytes. Thread-safe.
func (c *PageCache) Max() int64 {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return c.max
}

// Get returns the cached image for key, or nil. Bumps recency.
func (c *PageCache) Get(key CacheKey) *image.RGBA {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil
	}
	c.seq++
	e.recent = c.seq
	return e.img
}

// Put stores img under key and evicts least-recently-used entries until the
// cache fits. Images larger than the whole cache are not stored.
func (c *PageCache) Put(key CacheKey, img *image.RGBA) {
	if img == nil {
		return
	}
	size := int64(len(img.Pix))
	if size > c.max {
		return
	}
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if old, ok := c.m[key]; ok {
		c.total -= int64(len(old.img.Pix))
		delete(c.m, key)
	}
	c.seq++
	c.m[key] = &cacheEntry{img: img, recent: c.seq}
	c.total += size
	for c.total > c.max {
		var oldestKey CacheKey
		var oldest uint64
		first := true
		for k, e := range c.m {
			if first || e.recent < oldest {
				oldestKey, oldest, first = k, e.recent, false
			}
		}
		if first {
			break
		}
		c.total -= int64(len(c.m[oldestKey].img.Pix))
		delete(c.m, oldestKey)
	}
}

// Len returns the number of cached images.
func (c *PageCache) Len() int {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return len(c.m)
}

// Bytes returns the total pixel bytes held.
func (c *PageCache) Bytes() int64 {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return c.total
}

// Clear drops everything.
func (c *PageCache) Clear() {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.m = make(map[CacheKey]*cacheEntry)
	c.total = 0
}

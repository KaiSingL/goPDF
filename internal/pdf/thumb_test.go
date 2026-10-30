package pdf

import (
	"image"
	"path/filepath"
	"testing"
)

// TestRenderThumb checks the launcher's shallow first-page render: page 0
// at ThumbDPI, fresh 0-origin image, and a clean error for missing files.
func TestRenderThumb(t *testing.T) {
	path := writeTextPDF(t, "Hello thumb", "Page two")
	img, err := RenderThumb(path)
	if err != nil {
		t.Fatalf("RenderThumb: %v", err)
	}
	if img == nil {
		t.Fatal("RenderThumb returned a nil image with nil error")
	}
	if img.Rect.Min != (image.Point{}) {
		t.Errorf("origin = %v, want 0,0", img.Rect.Min)
	}
	// The generated pages are US Letter (612×792pt); at 48 DPI that is
	// exactly 408×528.
	if got, want := img.Rect.Dx(), 408; got != want {
		t.Errorf("width = %d, want %d", got, want)
	}
	if got, want := img.Rect.Dy(), 528; got != want {
		t.Errorf("height = %d, want %d", got, want)
	}

	// A missing file errors rather than panicking.
	if _, err := RenderThumb(filepath.Join(t.TempDir(), "nope.pdf")); err == nil {
		t.Error("RenderThumb of a missing file = nil error")
	}

	// A second render is independent (no shared state between calls).
	img2, err := RenderThumb(path)
	if err != nil {
		t.Fatalf("second RenderThumb: %v", err)
	}
	if img2 == img {
		t.Error("second render returned the first image pointer")
	}
}

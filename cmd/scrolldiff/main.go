// Command scrolldiff analyzes the captures GOPDF_SCROLLTEST produced:
// it crops each PNG to the viewer's center band and flags consecutive
// frames whose centers are IDENTICAL — a settled jump to a different
// page that still shows the previous page's pixels, the scroll-bleed
// bug. It also writes cropped strips beside the captures for quick
// viewing.
//
// Usage: scrolldiff <capture-dir>
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: scrolldiff <capture-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	names, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil || len(names) == 0 {
		fmt.Fprintln(os.Stderr, "no captures in", dir)
		os.Exit(1)
	}
	sort.Strings(names)

	type cap struct {
		name string
		img  *image.Gray
	}
	var caps []cap
	for _, n := range names {
		f, err := os.Open(n)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, n, err)
			os.Exit(1)
		}
		g := image.NewGray(img.Bounds())
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, gg, b, _ := img.At(x, y).RGBA()
				l := (299*int(r>>8) + 587*int(gg>>8) + 114*int(b>>8)) / 1000
				g.SetGray(x, y, color.Gray{Y: uint8(l)})
			}
		}
		caps = append(caps, cap{filepath.Base(n), g})
	}

	// The viewer's center band: x 30..85%, y 25..80% (clears the title
	// bar, the toolbar, the sidebar and the status bar at 1200x800).
	cropOf := func(g *image.Gray) *image.Gray {
		b := g.Bounds()
		x0 := b.Min.X + b.Dx()*30/100
		x1 := b.Min.X + b.Dx()*85/100
		y0 := b.Min.Y + b.Dy()*25/100
		y1 := b.Min.Y + b.Dy()*80/100
		return g.SubImage(image.Rect(x0, y0, x1, y1)).(*image.Gray)
	}

	bad := 0
	for i := 1; i < len(caps); i++ {
		a, b := cropOf(caps[i-1].img), cropOf(caps[i].img)
		if a.Bounds() != b.Bounds() {
			fmt.Printf("%s: size changed, skip\n", caps[i].name)
			continue
		}
		maxDiff, same := 0, true
		for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y += 2 {
			for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x += 2 {
				d := int(a.GrayAt(x, y).Y) - int(b.GrayAt(x, y).Y)
				if d < 0 {
					d = -d
				}
				if d > maxDiff {
					maxDiff = d
				}
				if d > 12 {
					same = false
				}
			}
		}
		status := "changed"
		if same {
			status = "STALE (identical center — previous page's pixels?)"
			bad++
		}
		fmt.Printf("%s -> %s: maxdiff %3d  %s\n", trim(caps[i-1].name), trim(caps[i].name), maxDiff, status)
	}
	fmt.Printf("\n%d captures, %d flagged stale\n", len(caps), bad)
}

func trim(s string) string {
	return strings.TrimSuffix(s, ".png")
}

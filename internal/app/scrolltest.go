// scrolltest.go is the GPU scroll-reproduction harness, gated behind the
// GOPDF_SCROLLTEST environment variable (set to a directory): it opens a
// generated document whose pages carry giant distinct numbers, drives
// jumps and a wheel flick, waits for frames to stabilize, and matches
// each stable capture against reference renders of every page. It runs
// with the viewer debug mode ON (debugviewer.go): the captures carry the
// per-box "p<n> bm#<id>" overlay and the frame counter, and the event
// ring is dumped beside them — so a MISMATCH names the frame and the
// bitmap ids to look up in the ring, which is what decides whether the
// bug is in the app's binding or below it. The headless Tester's CPU
// renderer cannot exercise this path; the captures are from the real GPU
// renderer.
package app

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// Signature dimensions for content matching. Signatures are taken over
// the INK bounding box of the image (normalize): the viewport crop and
// the low-DPI reference renders differ in scale and position, and a raw
// box-average signature degenerated to always matching the sparsest
// reference (the digit "1") — useless verdicts. Bbox-normalized, the
// signature is the digit's shape alone.
const (
	SigW = 24
	SigH = 32
)

// DebugScrollCapture runs the scripted sequence and saves the window
// captures. It returns immediately; the run quits the app when done.
func DebugScrollCapture(dir string) {
	EnableViewerDebug()
	go run(dir)
}

// uiSync runs fn on the UI thread and WAITS for it — Window.Update only
// posts, and a snapshot raced against its closure reads zeros.
func uiSync(fn func()) {
	done := make(chan struct{})
	update(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Printf("scrolltest: uiSync timed out")
	}
}

func run(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("scrolltest: mkdir: %v", err)
		return
	}
	path := filepath.Join(dir, "pages.pdf")
	if err := os.WriteFile(path, scrollTestPDF(20), 0o644); err != nil {
		log.Printf("scrolltest: write pdf: %v", err)
		return
	}
	log.Printf("scrolltest: opening %s", path)
	OpenPath(path)

	// Wait for the tab and its document.
	var tab *Tab
	for i := 0; i < 50 && tab == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		uiSync(func() {
			if t := current.Active(); t != nil && t.Doc != nil {
				tab = t
			}
		})
	}
	if tab == nil {
		log.Printf("scrolltest: no document tab opened")
		mygo.App.Quit()
		return
	}
	refs := referenceSignatures(path, dir)
	if len(refs) == 0 {
		log.Printf("scrolltest: references never rendered")
		mygo.App.Quit()
		return
	}

	// Jumps in scrambled order: far jumps snap (viewer.go), so each step
	// settles within a frame or two of its render landing.
	steps := []int{1, 6, 3, 14, 8, 20, 5, 11, 2, 17, 9, 4, 15, 7, 19, 10, 13, 16, 12, 18}
	mismatches, idles := 0, 0
	for si, page := range steps {
		frameBefore := debugFrame.Load()
		var curPage int
		uiSync(func() {
			if t := current.Active(); t != nil {
				t.Navigate(page-1, 0)
				curPage = t.CurPage
			}
		})
		if window != nil {
			window.Invalidate() // the jump carries no user input; ask for frames
		}
		log.Printf("scrolltest: step %02d navigate -> p%02d (CurPage %d)", si, page, curPage+1)

		shown, dist, margin, blank, settled, last := settleAndMatch(dir, fmt.Sprintf("step%02d-p%02d", si, page), refs)
		if last == nil {
			log.Printf("scrolltest: step %02d target p%02d — no captures", si, page)
			continue
		}
		// A jump must build at least one frame; if the counter did not
		// move across the whole step the window stopped painting (an
		// environmental stall — e.g. the window being restored/moved on
		// the desktop) and any verdict from the stale pixels is void.
		verdict := ""
		frozen := debugFrame.Load() == frameBefore
		switch {
		case frozen:
			verdict = "APP IDLE — frames frozen, verdict void"
			idles++
		case blank:
			verdict = "INDETERMINATE (center blank)"
		case shown != page:
			verdict = fmt.Sprintf("MISMATCH (shows page %d, dist %d, margin %d)", shown, dist, margin)
			mismatches++
		default:
			verdict = fmt.Sprintf("MATCH (dist %d, margin %d)", dist, margin)
		}
		log.Printf("scrolltest: step %02d target p%02d settled=%v frame=%d shows p%02d — %s",
			si, page, settled, debugFrame.Load(), shown, verdict)

		// On a mismatch: the scroll state at the stable wrong frame —
		// Y at the target's box top means the paint lags (container);
		// Y one page short means the snap targeted the wrong box (state).
		if shown != page && !blank && !frozen {
			var y, maxYY, boxTop float32
			var cur, seen int
			for i := 0; i < 5; i++ { // snapshot until two consecutive agree
				var y2, bt, my2 float32
				var c2, s2 int
				uiSync(func() {
					if t := current.Active(); t != nil {
						c2, s2 = t.CurPage, viewerStateFor(t.ID).seenPage
						_, h := pageDipSize(t, c2, t.Zoom)
						bt = pagePadY + float32(c2)*(h+pageGap)
						y2, my2 = viewerScroll.Y, viewerScroll.MaxY
					}
				})
				if i > 0 && y2 == y && c2 == cur {
					break
				}
				y, maxYY, boxTop, cur, seen = y2, my2, bt, c2, s2
				time.Sleep(120 * time.Millisecond)
			}
			log.Printf("scrolltest: MISMATCH state: Y=%.0f expectedBoxTop=%.0f maxY=%.0f cur=%d seen=%d",
				y, boxTop, maxYY, cur, seen)
			// Freeze the frames around the wrong state before the ring
			// rotates them away.
			debugMu.Lock()
			tail := slices.Clone(debugRing)
			debugMu.Unlock()
			rp := filepath.Join(dir, fmt.Sprintf("ring-step%02d.log", si))
			if err := os.WriteFile(rp, []byte(strings.Join(tail[len(tail)-min(60, len(tail)):], "\n")), 0o644); err == nil {
				log.Printf("scrolltest: ring tail %s", rp)
			}
		}
	}

	// Wheel-flick phase: drive the scroll through the whole document fast
	// (the "when scrolling" path mostVisible tracks), then stop and check
	// the page the landed-on box shows.
	log.Printf("scrolltest: wheel flick through the document")
	for i := 0; i < tab.NumPages; i++ {
		y := float32(i) * 1000 // ≈ one page box per step at fit width
		var maxY float32
		uiSync(func() {
			viewerScroll.ScrollTo(0, y, ui.Animation{})
			maxY = viewerScroll.MaxY
		})
		if y > maxY && maxY > 0 {
			break // past the end
		}
		if window != nil {
			window.Invalidate()
		}
		time.Sleep(60 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
	shown, dist, margin, blank, _, _ := settleAndMatch(dir, "flick-settled", refs)
	if blank {
		log.Printf("scrolltest: flick settled — INDETERMINATE (center blank)")
	} else {
		log.Printf("scrolltest: flick settled shows p%02d (dist %d, margin %d)", shown, dist, margin)
	}
	log.Printf("scrolltest: done — %d mismatches, %d frozen steps over %d jump steps", mismatches, idles, len(steps))
	DebugDumpViewer()
	mygo.App.Quit()
}

// pageCrop cuts the capture to the current page's painted box, scaled to
// capture pixels — resolution- and chrome-independent. The old percent
// crop's top edge crossed the toolbar at smaller window sizes, and the
// toolbar text then dominated the ink bbox. Falls back to the percent
// crop if the rect is unknown.
func pageCrop(g *image.Gray) *image.Gray {
	var cur int
	uiSync(func() {
		if t := current.Active(); t != nil {
			cur = t.CurPage
		}
	})
	r, ok := DebugPageRect(cur)
	if !ok || deviceScale <= 0 {
		return cropViewer(g)
	}
	// The test doc draws the digit in the page's top-left (400pt glyph
	// at 8,480 — the top 40% of the page, widest page "20" ends at 75%
	// width): cut the crop to that region of the FULL box, so the page's
	// edge lines and everything below can never enter the ink bounding
	// box. The fractions must come off the unclipped box — intersecting
	// with the capture first makes them fractions of the visible part,
	// and the shorter view then slices the digit's base off.
	px := image.Rect(
		int(r.X*deviceScale), int(r.Y*deviceScale),
		int((r.X+r.W*0.8)*deviceScale), int((r.Y+r.H*0.5)*deviceScale))
	px = px.Intersect(g.Bounds())
	if px.Empty() {
		return cropViewer(g)
	}
	return g.SubImage(px.Inset(2)).(*image.Gray)
}

// settleAndMatch captures until the frame is stable AND non-blank (the
// placeholder and between-page blanks count as not settled), then matches
// the frame against the page references. It saves every capture as
// <dir>/<name>-x<k>.png and returns (page, dist, margin, blank, settled,
// last); margin is runner-up minus best — a thin margin makes a verdict
// worth doubting.
func settleAndMatch(dir, name string, refs map[int][SigW * SigH]byte) (page, dist, margin int, blank, settled bool, last *image.Gray) {
	var prevSig [SigW * SigH]byte
	stable, prevBlank := false, false
	for try := 0; try < 10; try++ {
		time.Sleep(400 * time.Millisecond)
		g := grabFrame(dir, fmt.Sprintf("%s-x%d", name, try))
		if g == nil {
			continue
		}
		last = g
		sig := signature(normalize(pageCrop(g)))
		blank = sigBlank(sig)
		if blank { // placeholder / between-page blank: keep waiting
			prevSig, prevBlank = sig, true
			continue
		}
		if sig == prevSig && !prevBlank {
			stable = true
			break
		}
		prevSig, prevBlank = sig, false
	}
	if last == nil {
		return 0, 0, 0, false, false, nil
	}
	crop := pageCrop(last)
	saveSig(dir, name+"-sig", normalize(crop)) // what the matcher saw
	shown, dist, second := bestMatch(crop, refs)
	return shown, dist, second - dist, blank, stable, last
}

// saveSig writes one normalized grayscale image into dir as <name>.png —
// the matcher's actual input, so a bad verdict can be eyeballed directly.
func saveSig(dir, name string, g *image.Gray) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, g); err != nil {
		return
	}
	p := filepath.Join(dir, name+".png")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		log.Printf("scrolltest: write %s: %v", p, err)
	}
}

// grabFrame captures the window and saves it; it returns the frame as
// grayscale for matching, or nil. CapturePage is safe off the UI thread:
// it delivers its result through the app loop.
func grabFrame(dir, name string) *image.Gray {
	if window == nil {
		return nil
	}
	b, err := window.CapturePage()
	if err != nil {
		log.Printf("scrolltest: capture %s: %v", name, err)
		return nil
	}
	p := filepath.Join(dir, name+".png")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		log.Printf("scrolltest: write %s: %v", p, err)
		return nil
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		log.Printf("scrolltest: decode %s: %v", name, err)
		return nil
	}
	g := image.NewGray(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, gg, bl, _ := img.At(x, y).RGBA()
			l := (299*int(r>>8) + 587*int(gg>>8) + 114*int(bl>>8)) / 1000
			g.SetGray(x, y, color.Gray{Y: uint8(l)})
		}
	}
	return g
}

// cropViewer cuts the viewer's area, generous on purpose — the giant
// digit must be inside whatever page fills the view: x 28..92%, y 12..92%.
func cropViewer(g *image.Gray) *image.Gray {
	b := g.Bounds()
	r := image.Rect(
		b.Min.X+b.Dx()*28/100, b.Min.Y+b.Dy()*12/100,
		b.Min.X+b.Dx()*92/100, b.Min.Y+b.Dy()*92/100)
	return g.SubImage(r.Intersect(b)).(*image.Gray)
}

// signature downscales g to SigW×SigH by box average.
func signature(g *image.Gray) [SigW * SigH]byte {
	var out [SigW * SigH]byte
	b := g.Bounds()
	for sy := 0; sy < SigH; sy++ {
		for sx := 0; sx < SigW; sx++ {
			x0 := b.Min.X + b.Dx()*sx/SigW
			x1 := b.Min.X + b.Dx()*(sx+1)/SigW
			y0 := b.Min.Y + b.Dy()*sy/SigH
			y1 := b.Min.Y + b.Dy()*(sy+1)/SigH
			sum, n := 0, 0
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					sum += int(g.GrayAt(x, y).Y)
					n++
				}
			}
			if n > 0 {
				out[sy*SigW+sx] = uint8(sum / n)
			}
		}
	}
	return out
}

// sigBlank reports a signature with no ink variation — a blank or
// near-blank view that cannot identify a page.
func sigBlank(sig [SigW * SigH]byte) bool {
	lo, hi := uint8(255), uint8(0)
	for _, v := range sig {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return int(hi)-int(lo) < 12
}

// bestMatch returns the page whose reference signature is nearest g's
// (L1 over ink-normalized signatures), the distance, and the runner-up
// distance.
func bestMatch(g *image.Gray, refs map[int][SigW * SigH]byte) (page, dist, second int) {
	sig := signature(normalize(g))
	best, bestD, secondD := 0, 1<<30, 1<<30
	for p, rs := range refs {
		d := 0
		for i := range sig {
			d += absByte(sig[i], rs[i])
		}
		if d < bestD {
			best, bestD, secondD = p, d, bestD
		} else if d < secondD {
			secondD = d
		}
	}
	return best, bestD, secondD
}

// inkBBox returns the bounding box of dark pixels, or an empty rect when
// the image has no ink at all.
func inkBBox(g *image.Gray, thresh uint8) image.Rectangle {
	b := g.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, -1, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if g.GrayAt(x, y).Y < thresh {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// normalize crops g to its ink bounding box with a small margin, so the
// signature compares the digit's SHAPE regardless of where on the window
// and at what scale the page was drawn (see the SigW/SigH comment).
func normalize(g *image.Gray) *image.Gray {
	r := inkBBox(g, 128)
	if r.Empty() {
		return g
	}
	mx := max(r.Dx()/8, 4)
	my := max(r.Dy()/8, 4)
	r = r.Add(image.Point{X: -mx, Y: -my}).Intersect(g.Bounds())
	return g.SubImage(r).(*image.Gray)
}

func absByte(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// referenceSignatures renders every page of the document at low DPI and
// returns page (1-based) → ink-normalized signature of the page. Each
// normalized reference is also saved as sigdump-ref-pNN.png for
// inspection.
func referenceSignatures(path, dir string) map[int][SigW * SigH]byte {
	doc, err := pdf.Open(path)
	if err != nil {
		log.Printf("scrolltest: reference open: %v", err)
		return nil
	}
	defer doc.Close()

	refs := map[int][SigW * SigH]byte{}
	type reply struct {
		page int
		img  *image.RGBA
	}
	ch := make(chan reply, doc.NumPages())
	for p := 0; p < doc.NumPages(); p++ {
		page := p
		doc.RenderAsync(pdf.RenderReq{Page: page, DPI: 48, Kind: pdf.KindPage},
			func(img *image.RGBA, err error) {
				if err == nil && img != nil {
					ch <- reply{page + 1, img}
				} else {
					ch <- reply{page + 1, nil}
				}
			})
	}
	for got := 0; got < doc.NumPages(); got++ {
		r := <-ch
		if r.img == nil {
			continue
		}
		g := image.NewGray(r.img.Bounds())
		for y := r.img.Bounds().Min.Y; y < r.img.Bounds().Max.Y; y++ {
			for x := r.img.Bounds().Min.X; x < r.img.Bounds().Max.X; x++ {
				c := r.img.RGBAAt(x, y)
				l := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
				g.SetGray(x, y, color.Gray{Y: uint8(l)})
			}
		}
		refs[r.page] = signature(normalize(g))
		saveSig(dir, fmt.Sprintf("sigdump-ref-p%02d", r.page), normalize(g))
	}
	return refs
}

// scrollTestPDF builds a pages-page PDF on US Letter with each page
// carrying its number as a giant digit plus a "PAGE n" label — content
// that cannot be confused between pages at a glance or by a match.
func scrollTestPDF(pages int) []byte {
	var buf []byte
	appendStr := func(s string) { buf = append(buf, s...) }
	offsets := map[int]int{}
	write := func(num int, body string) {
		offsets[num] = len(buf)
		appendStr(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", num, body))
	}

	appendStr("%PDF-1.4\n")
	kids := ""
	for i := 1; i <= pages; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+2*(i-1))
	}
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, pages))
	for i := 1; i <= pages; i++ {
		pageNum := 3 + 2*(i-1)
		contNum := pageNum + 1
		// The digit sits at the page's top-left corner, filling ~40% of
		// the page height: then the digit IS the page's ink bounding box
		// — the debug overlay label and the small PAGE caption fall
		// inside it and cannot skew the ink-bbox normalization the
		// matcher relies on (a centered digit let the label drag the
		// bbox and every capture matched the same wrong page).
		content := fmt.Sprintf(
			"BT /F1 400 Tf 8 480 Td (%d) Tj ET BT /F1 10 Tf 100 465 Td (PAGE %d) Tj ET", i, i)
		write(pageNum, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 100 0 R >> >> >>", contNum))
		write(contNum, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	fontObj := 3 + 2*pages
	write(fontObj, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	count := fontObj
	xrefAt := len(buf)
	appendStr(fmt.Sprintf("xref\n0 %d\n", count+1))
	appendStr("0000000000 65535 f \n")
	for i := 1; i <= count; i++ {
		if off, ok := offsets[i]; ok {
			appendStr(fmt.Sprintf("%010d 00000 n \n", off))
		} else {
			appendStr("0000000000 65535 f \n")
		}
	}
	appendStr(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", count+1, xrefAt))
	return buf
}

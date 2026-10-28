// selection_test.go covers the exact selection pipeline at the pdf
// layer: fz_highlight_selection quads and fz_copy_selection text over
// the structured text fz_search searches — same coordinate space, same
// exactness.
package pdf

import (
	"strings"
	"testing"
	"time"
)

// TestSelectionQuadsExact: selecting around a word search found yields
// quads that cover it and text that carries it.
func TestSelectionQuadsExact(t *testing.T) {
	path := writeTextPDF(t, "hello selection world")
	d := openTestDoc(t, path)

	type searchReply struct {
		rects []HitRect
		err   error
	}
	sch := make(chan searchReply, 1)
	d.SearchQuadsAsync(0, "selection", func(rects []HitRect, err error) {
		sch <- searchReply{rects, err}
	})
	select {
	case s := <-sch:
		if s.err != nil || len(s.rects) == 0 {
			t.Fatalf("search: rects %d err %v — nothing to anchor the selection", len(s.rects), s.err)
		}
		r := s.rects[0]

		type selReply struct {
			res SelectionResult
			err error
		}
		qch := make(chan selReply, 1)
		d.SelectionAsync(0, r.X0-5, r.Y0-5, r.X1+5, r.Y1+5, func(res SelectionResult, err error) {
			qch <- selReply{res, err}
		})
		select {
		case q := <-qch:
			if q.err != nil {
				t.Fatal(q.err)
			}
			if len(q.res.Quads) == 0 {
				t.Fatal("no selection quads around a known word")
			}
			if !strings.Contains(q.res.Text, "selection") {
				t.Fatalf("selection text %q lacks the word", q.res.Text)
			}
			covered := false
			for _, qd := range q.res.Quads {
				if r.X0 >= qd.X0-2 && r.X1 <= qd.X1+2 && r.Y0 >= qd.Y0-2 && r.Y1 <= qd.Y1+2 {
					covered = true
					break
				}
			}
			if !covered {
				t.Fatalf("search quad (%.0f,%.0f)-(%.0f,%.0f) not covered by the selection quads",
					r.X0, r.Y0, r.X1, r.Y1)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("selection never completed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("search never completed")
	}
}

// TestSelectionEmpty: two points in blank space select nothing — no
// quads, no text, no error.
func TestSelectionEmpty(t *testing.T) {
	path := writeTextPDF(t, "one line")
	d := openTestDoc(t, path)

	type selReply struct {
		res SelectionResult
		err error
	}
	qch := make(chan selReply, 1)
	d.SelectionAsync(0, 20, 700, 80, 730, func(res SelectionResult, err error) {
		qch <- selReply{res, err}
	})
	select {
	case q := <-qch:
		if q.err != nil {
			t.Fatal(q.err)
		}
		if len(q.res.Quads) != 0 || q.res.Text != "" {
			t.Fatalf("blank-space selection got %d quads, text %q", len(q.res.Quads), q.res.Text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("selection never completed")
	}
}

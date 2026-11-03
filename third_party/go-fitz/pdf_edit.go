package fitz

import "errors"

// PDF-layer editing bindings. These wrap MuPDF's pdf_* API surface: opening
// password-protected documents, saving (full, incremental, encrypted), page
// organization (insert/delete/rotate/graft), annotations, form fields,
// metadata, redaction and optional-content (layer) configurations.
//
// The implementations live in pdf_edit_cgo.go (production, static MuPDF) and
// pdf_edit_nocgo.go (purego builds, which cannot call the PDF layer: every
// method there returns ErrNeedsCGO).

var (
	// ErrNotPDF is returned when an editing operation is applied to a
	// document MuPDF parsed as a non-PDF format (EPUB, XPS, ...).
	ErrNotPDF = errors.New("fitz: not a PDF document")
	// ErrNeedsCGO is returned by the editing API on builds without cgo.
	ErrNeedsCGO = errors.New("fitz: PDF editing requires CGO_ENABLED=1")
	// ErrStaleRef is returned when an operation targets an annotation or
	// field by ID that no longer exists — it was deleted, or the document
	// was edited underneath the caller.
	ErrStaleRef = errors.New("fitz: referenced annotation or field is gone")
)

// Permission is one document access permission, checked with
// Document.HasPermission after a successful (or not required) authentication.
// The values are MuPDF's fz_permission character codes, not the PDF /Perms
// bitmask.
type Permission rune

const (
	PermPrint         Permission = 'p'
	PermCopy          Permission = 'c'
	PermEdit          Permission = 'e'
	PermAnnotate      Permission = 'n'
	PermForm          Permission = 'f'
	PermAccessibility Permission = 'y'
	PermAssemble      Permission = 'a'
	PermPrintHQ       Permission = 'h'
)

// Authentication result bits returned by Document.Authenticate. Zero means
// the password was rejected.
const (
	AuthNoPasswordNeeded = 1 << 0
	AuthUserPassword     = 1 << 1
	AuthOwnerPassword    = 1 << 2
)

// Encryption selects the algorithm pdf_save_document writes with. The values
// mirror MuPDF's pdf_encrypt_method enum.
type Encryption int

const (
	EncryptKeep Encryption = iota // keep existing encryption (passwords must be set)
	EncryptNone                   // remove encryption
	EncryptRC4_40
	EncryptRC4_128
	EncryptAES_128
	EncryptAES_256
)

// SaveOptions mirrors the subset of pdf_write_options the reader needs.
// Zero value saves a compressed, non-incremental file and keeps encryption
// state unchanged.
type SaveOptions struct {
	// Incremental appends an update section to the existing file. The file
	// must be the one the document was opened from; the original bytes stay
	// byte-for-byte intact. Ignored for ExtractPagesToFile.
	Incremental bool
	// Garbage collects unused objects: 1=gc, 2=compact xref, 3=deduplicate
	// streams, 4=check duplicate streams. 0 keeps everything.
	Garbage int
	// Appearance regenerates annotation and field appearance streams
	// (/AP) while saving. Set this after creating annotations or filling
	// fields on documents produced by other writers.
	Appearance bool
	// Sanitize strips javascript, embedded files and other active content.
	Sanitize bool
	// Pretty prints objects with whitespace.
	Pretty bool
	// ASCII hex-encodes binary streams.
	ASCII bool
	// Encryption selects the write-time encryption. EncryptKeep (the zero
	// value) preserves the document's current state; EncryptNone decrypts.
	Encryption Encryption
	// Permissions applied when writing encrypted. Ignored unless Encryption
	// is RC4_40..AES_256. Sensible default: all permissions granted.
	Permissions int
	// OwnerPassword governs permission enforcement; UserPassword governs
	// opening. Either may be empty.
	OwnerPassword string
	UserPassword  string
}

// Point is one coordinate in page space (PDF points, top-left origin —
// the same space TextSpan and HitRect use).
type Point struct{ X, Y float64 }

// Quad is one quadrilateral in page space, corners in MuPDF's fz_quad
// order (upper-left, upper-right, lower-left, lower-right).
type Quad struct{ UL, UR, LL, LR Point }

// Rect is one axis-aligned rectangle in page space.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// AnnotType is a PDF annotation subtype. Values mirror MuPDF's
// pdf_annot_type enum order exactly.
type AnnotType int

const (
	AnnotText           AnnotType = iota // 0: sticky note
	AnnotLink                            // 1
	AnnotFreeText                        // 2
	AnnotLine                            // 3
	AnnotSquare                          // 4
	AnnotCircle                          // 5
	AnnotPolygon                         // 6
	AnnotPolyLine                        // 7
	AnnotHighlight                       // 8
	AnnotUnderline                       // 9
	AnnotSquiggly                        // 10
	AnnotStrikeOut                       // 11
	AnnotRedact                          // 12
	AnnotStamp                           // 13
	AnnotCaret                           // 14
	AnnotInk                             // 15
	AnnotPopup                           // 16
	AnnotFileAttachment                  // 17
	AnnotSound                           // 18
	AnnotMovie                           // 19
	AnnotRichMedia                       // 20
	AnnotWidget                          // 21 (form field)
	AnnotScreen                          // 22
	AnnotPrinterMark                     // 23
	AnnotTrapNet                         // 24
	AnnotWatermark                       // 25
	Annot3D                              // 26
	AnnotProjection                      // 27
	AnnotUnknown        AnnotType = -1
)

// annotTypeNames maps AnnotType to the PDF /Subtype for reporting and tests.
var annotTypeNames = map[AnnotType]string{
	AnnotText: "Text", AnnotLink: "Link", AnnotFreeText: "FreeText",
	AnnotLine: "Line", AnnotSquare: "Square", AnnotCircle: "Circle",
	AnnotPolygon: "Polygon", AnnotPolyLine: "PolyLine",
	AnnotHighlight: "Highlight", AnnotUnderline: "Underline",
	AnnotSquiggly: "Squiggly", AnnotStrikeOut: "StrikeOut",
	AnnotRedact: "Redact", AnnotStamp: "Stamp", AnnotCaret: "Caret",
	AnnotInk: "Ink", AnnotPopup: "Popup",
	AnnotFileAttachment: "FileAttachment", AnnotSound: "Sound",
	AnnotMovie: "Movie", AnnotRichMedia: "RichMedia", AnnotWidget: "Widget",
	AnnotScreen: "Screen", AnnotPrinterMark: "PrinterMark",
	AnnotTrapNet: "TrapNet", AnnotWatermark: "Watermark", Annot3D: "3D",
	AnnotProjection: "Projection",
}

// String returns the PDF subtype name ("Highlight"), or "Unknown".
func (t AnnotType) String() string {
	if s, ok := annotTypeNames[t]; ok {
		return s
	}
	return "Unknown"
}

// Annot is one annotation as returned by Document.Annots.
type Annot struct {
	// ID is the PDF object number of the annotation. It is stable for the
	// lifetime of the open document and identifies the annot to the
	// Edit/Delete calls.
	ID int64
	// Type is the annotation subtype. AnnotUnknown annotates an unreadable
	// one; the annotation still shows with its rect.
	Type AnnotType
	Rect Rect
	// Contents is the annotation text (note body); Author the /T entry.
	Contents string
	Author   string
	// Color is the stroke color in the annot's own colorspace, most often
	// RGB: 3 components, each 0..1.
	Color []float64
	// Opacity is the /CA value, 0..1; 1 when absent.
	Opacity float64
	// QuadPoints carries the per-word/line highlight geometry for markup
	// annots (highlight, underline, squiggly, strikeout); empty otherwise.
	QuadPoints []Quad
}

// AnnotEdit applies a partial change to one annotation, and doubles as the
// options for CreateAnnot. Nil pointers and nil slices mean "leave as is";
// SetContents/SetAuthor distinguish an empty string from no change.
type AnnotEdit struct {
	Rect *Rect
	// QuadPoints replaces the markup geometry when non-nil.
	QuadPoints []Quad
	// Color replaces the stroke color when non-nil.
	Color []float64
	// Opacity replaces /CA when non-nil.
	Opacity *float64
	// BorderWidth replaces the border width when non-nil.
	BorderWidth *float64
	Contents    string
	SetContents bool
	Author      string
	SetAuthor   bool
}

// WidgetType is a form field kind. Values mirror MuPDF's pdf_widget_type
// enum order exactly.
type WidgetType int

const (
	WidgetUnknown     WidgetType = iota // 0
	WidgetButton                        // 1 (push button)
	WidgetCheckbox                      // 2
	WidgetCombobox                      // 3
	WidgetListbox                       // 4
	WidgetRadiobutton                   // 5
	WidgetSignature                     // 6
	WidgetText                          // 7
)

// Field is one form widget as returned by Document.Fields.
type Field struct {
	// ID is the PDF object number of the field, the handle for
	// SetFieldValue/ResetField.
	ID int64
	// Type is the field kind. Signature fields are listed but cannot be
	// filled (v1: they render only).
	Type WidgetType
	Rect Rect
	// Label is the fully-qualified field name (dotted path).
	Label string
	// Value is the current value text: text content for text fields,
	// "Yes"/"Off" (or the export value) for checkboxes and radios, the
	// chosen option for choices.
	Value string
}

// LayerConfig is one optional-content configuration (a named layer state).
type LayerConfig struct {
	Name    string
	Creator string
}

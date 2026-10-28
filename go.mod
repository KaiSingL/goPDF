module gopdf

go 1.27.3

tool github.com/egoist/mygo/cmd/mygo

require github.com/egoist/mygo v0.6.4

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/gen2brain/go-fitz v1.28.2 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// Render PDF annotations (highlights, squares, notes): patched local fork.
replace github.com/gen2brain/go-fitz => ./third_party/go-fitz

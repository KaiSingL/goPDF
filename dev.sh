# Sets up the GoPDF development environment in the current shell.
# Usage: . ./dev.sh   (source it), then: go build ./... ; go test ./... ; go tool mygo dev
#
# macOS counterpart of dev.ps1: Go comes from Homebrew (brew install go)
# and the cgo compiler is Apple clang from the Xcode CLT, both already on
# PATH — so unlike Windows, no PATH work is needed, only the cgo vars.

export CGO_ENABLED=1
export CC="${CC:-clang}"

echo "GoPDF dev environment:"
echo "  go          = $(go version)"
echo "  CC          = $CC ($($CC -dumpversion))"
echo "  CGO_ENABLED = $CGO_ENABLED"
echo ""
echo "  build:   go build ./..."
echo "  test:    go test ./...       (add -race for concurrency checks)"
echo "  run:     go tool mygo dev    (needs CGO_ENABLED=1, set above)"
echo "  package: go tool mygo build  (.app + dmg on macOS)"

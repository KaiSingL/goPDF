# Sets up the GoPDF development environment in the current PowerShell session.
# Usage: . .\dev.ps1   (dot-source), then: go build ./... ; go test ./... ; go tool mygo dev

$env:Path = 'C:\Program Files\Go\bin;C:\dev\tools\mingw\mingw64\bin;' + $env:Path
$env:CGO_ENABLED = '1'
$env:CC = 'C:\dev\tools\mingw\mingw64\bin\gcc.exe'

Write-Host "GoPDF dev environment:" -ForegroundColor Cyan
Write-Host "  go       = $(go version)"
Write-Host "  CC       = $env:CC ($(& $env:CC -dumpversion))"
Write-Host "  CGO_ENABLED = $env:CGO_ENABLED"
Write-Host ""
Write-Host "  build:  go build ./..."
Write-Host "  test:   go test ./...       (add -race for concurrency checks)"
Write-Host "  run:    go tool mygo dev    (needs CGO_ENABLED=1, set above)"
Write-Host "  package: go tool mygo build"

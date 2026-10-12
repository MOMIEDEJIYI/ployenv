# Build ployenv (portable single-binary CLI).
#   .\build.ps1              -> dist\ployenv.exe
#   .\build.ps1 -Test        -> run unit tests first
#   .\build.ps1 -Out bin     -> custom output directory
param([string]$Out = 'dist', [switch]$Test)
$ErrorActionPreference = 'Stop'
# Work from the module root: `gofmt ./cmd ./internal`, `go test ./...` and a
# relative -Out are all resolved against the current directory, so a caller that
# happens to sit elsewhere used to get `pattern ./...: directory prefix . does
# not contain main module` instead of a build (the failure looks like a test
# failure and hides the real cause).
Set-Location -LiteralPath $PSScriptRoot
New-Item -ItemType Directory -Force -Path $Out | Out-Null
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $PSScriptRoot '.gocache' }

foreach ($tool in 'go', 'gofmt') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool is not on PATH. Install Go, or add ployenv's own toolchain, e.g. <go-toolchain>\bin"
    }
}

$fmt = gofmt -l ./cmd ./internal
if ($fmt) { Write-Host "gofmt would change:"; $fmt; throw 'run: gofmt -w ./cmd ./internal' }

# Native commands do not raise on a non-zero exit in Windows PowerShell 5.1, so
# each step is checked explicitly: shipping a binary after a failed test run is
# the one outcome that must never happen (BUG-010).
function Invoke-Step([string]$What, [scriptblock]$Body) {
    & $Body
    if ($LASTEXITCODE -ne 0) { throw "$What failed (exit $LASTEXITCODE)" }
}

if ($Test) { Invoke-Step 'go test' { go test ./... } }
Invoke-Step 'go vet' { go vet ./... }
# -trimpath matters for the published artifact, not just tidiness: without it the binary
# embeds the build machine's absolute source paths (both this project's and the Go
# toolchain's) - 800+ occurrences in the 0.3.2 build were published in the npm package
# and the GitHub Release asset. Verified 2026-10-12 by the project's public-surface check.
# Do not drop it.
Invoke-Step 'go build' { go build -trimpath -ldflags '-s -w' -o "$Out\ployenv.exe" ./cmd/ployenv }

Get-Item "$Out\ployenv.exe" | Select-Object Name, Length

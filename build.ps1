# Build ployenv (portable single-binary CLI).
#   .\build.ps1              -> dist\ployenv.exe
#   .\build.ps1 -Test        -> run unit tests first
#   .\build.ps1 -Out bin     -> custom output directory
#   .\build.ps1 -Package     -> also lay out the GitHub Release assets in dist\release
#   .\build.ps1 -Version 0.3.2 -Package -> assert that this tree really is 0.3.2
param([string]$Out = 'dist', [switch]$Test, [switch]$Package, [string]$Version = '')
$ErrorActionPreference = 'Stop'
# Work from the module root: `gofmt ./cmd ./internal`, `go test ./...` and a
# relative -Out are all resolved against the current directory, so a caller that
# happens to sit elsewhere used to get `pattern ./...: directory prefix . does
# not contain main module` instead of a build (the failure looks like a test
# failure and hides the real cause).
Set-Location -LiteralPath $PSScriptRoot
# Set-Location moves PowerShell's own location (native tools are launched with it),
# but the .NET APIs used further down still read the *process* working directory,
# which may be the caller's. Everything is resolved against an absolute path here.
if (-not [System.IO.Path]::IsPathRooted($Out)) { $Out = Join-Path $PSScriptRoot $Out }
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

function Get-Sha256([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
}

if ($Package) {
    # Release packaging (owner decision 2026-10-11, option B): the published asset is the
    # binary already committed at dsh-plugin/bin/win32-x64/ployenv.exe. It is never rebuilt
    # here, because a rebuild bakes in the vcs.revision of whatever commit it runs at, which
    # is not the revision inside the committed file - and then the project's ritual
    # "deployed = bundled = built, byte for byte" silently stops holding.
    $bundledRel = 'dsh-plugin\bin\win32-x64\ployenv.exe'
    $bundled = Join-Path $PSScriptRoot $bundledRel
    if (-not (Test-Path -LiteralPath $bundled)) { throw "bundled binary not found: $bundledRel" }

    $cliSrc = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'internal\cli\cli.go') -Raw
    if ($cliSrc -notmatch 'const Version = "([^"]+)"') { throw 'cannot read const Version from internal/cli/cli.go' }
    $srcVersion = $Matches[1]

    $reported = (& $bundled --version 2>&1 | Out-String).Trim()
    if ($reported -notmatch '^ployenv ([^ ]+) \(([^)]+)\)$') { throw "unexpected --version output from the bundled binary: $reported" }
    $binVersion = $Matches[1]
    $binPlatform = $Matches[2]

    $meta = (& go version -m $bundled | Out-String)
    if ($meta -notmatch 'vcs\.modified=false') { throw "the bundled binary was built from a dirty tree (vcs.modified != false): $bundledRel" }
    $binRevision = if ($meta -match 'vcs\.revision=([0-9a-f]+)') { $Matches[1] } else { '(absent)' }

    Write-Host ''
    Write-Host '== release packaging (option B: ship the bundled binary, never rebuild it) =='
    Write-Host ("PASS bundled-exists      {0} ({1} bytes)" -f $bundledRel, (Get-Item -LiteralPath $bundled).Length)
    Write-Host ("PASS bundled-version     {0}   (internal/cli/cli.go: {1})" -f $binVersion, $srcVersion)
    Write-Host ("PASS bundled-clean-tree  vcs.revision={0}, vcs.modified=false" -f $binRevision)

    if ($binVersion -ne $srcVersion) { throw "version mismatch: bundled binary is $binVersion, internal/cli/cli.go is $srcVersion" }
    if ($binPlatform -ne 'windows/amd64') { throw "bundled binary platform is $binPlatform, expected windows/amd64" }
    if ($Version -ne '' -and $Version -ne $srcVersion) { throw "expected version $Version, but this tree says $srcVersion" }

    # The build above is a gate (the tree must compile). At a release commit it also has to
    # be byte-identical to the bundled file; that is reported, not enforced, because a commit
    # made after a release (docs, README) legitimately differs - the embedded vcs.revision
    # changes even though no code did.
    $distExe = Join-Path $Out 'ployenv.exe'
    if (Test-Path -LiteralPath $distExe) {
        $distMeta = (& go version -m $distExe | Out-String)
        $distRevision = if ($distMeta -match 'vcs\.revision=([0-9a-f]+)') { $Matches[1] } else { '(absent)' }
        if ((Get-Sha256 $distExe) -eq (Get-Sha256 $bundled)) {
            Write-Host 'PASS built-equals-bundled  the build of this tree == the bundled binary'
        } else {
            Write-Host ("INFO built-equals-bundled  differs (build of {0}, bundled {1})" -f $distRevision, $binRevision)
            Write-Host ("     expected unless this IS the release commit: refresh {0} from this build before tagging." -f $bundledRel)
        }
    }

    $relDir = Join-Path $Out 'release'
    if (Test-Path -LiteralPath $relDir) { Remove-Item -LiteralPath $relDir -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $relDir | Out-Null

    $stem = "ployenv-v$srcVersion-windows-amd64"
    $exeAsset = Join-Path $relDir "$stem.exe"
    Copy-Item -LiteralPath $bundled -Destination $exeAsset -Force
    # A stable, version-free alias so a consumer can use the fixed URL
    # https://github.com/<owner>/<repo>/releases/latest/download/ployenv-windows-amd64.exe
    # without first asking the API which version is current (REQ-003 FR-303's default source).
    # It is the same bytes as the versioned asset - never a second build.
    $aliasAsset = Join-Path $relDir 'ployenv-windows-amd64.exe'
    Copy-Item -LiteralPath $bundled -Destination $aliasAsset -Force
    $licenseAsset = Join-Path $relDir 'LICENSE'
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'LICENSE') -Destination $licenseAsset -Force

    # No zip on purpose: deflate output differs between the .NET runtimes of the dev box and
    # a CI runner, so a published hash for a zip could not be reproduced locally. The flat
    # .exe is the byte-stable artifact (and what `setup` would download and SHA-256 check).
    $sums = @()
    $sums += ("{0}  {1}" -f (Get-Sha256 $exeAsset), (Split-Path -Leaf $exeAsset))
    $sums += ("{0}  {1}" -f (Get-Sha256 $aliasAsset), (Split-Path -Leaf $aliasAsset))
    $sums += ("{0}  {1}" -f (Get-Sha256 $licenseAsset), 'LICENSE')
    $sums += ("# ployenv {0} windows/amd64 - bundled binary revision {1}, vcs.modified=false" -f $srcVersion, $binRevision)
    $sumsFile = Join-Path $relDir 'SHA256SUMS.txt'
    [IO.File]::WriteAllText($sumsFile, (($sums -join "`n") + "`n"), (New-Object Text.UTF8Encoding($false)))

    # Self-check: recompute from disk the way a consumer would.
    $checked = 0
    foreach ($line in [IO.File]::ReadAllLines($sumsFile)) {
        if ($line -eq '' -or $line.StartsWith('#')) { continue }
        if ($line -notmatch '^([0-9A-Fa-f]{64})\s+(.+)$') { throw "malformed line in SHA256SUMS.txt: $line" }
        $want = $Matches[1]; $name = $Matches[2].Trim()
        $target = Join-Path $relDir $name
        if (-not (Test-Path -LiteralPath $target)) { throw "SHA256SUMS.txt lists a missing file: $name" }
        if ((Get-Sha256 $target) -ne $want) { throw "SHA256SUMS.txt hash mismatch for $name" }
        $checked++
    }
    if ($checked -ne 3) { throw "SHA256SUMS.txt should cover 3 files, found $checked" }
    Write-Host 'PASS sha256sums-verified  3/3 recomputed from disk'

    if ((Get-Sha256 $aliasAsset) -ne (Get-Sha256 $exeAsset)) { throw 'the version-free alias is not the same bytes as the versioned asset' }
    Write-Host 'PASS alias-is-same-bytes  ployenv-windows-amd64.exe (fixed name for releases/latest/download)'

    Write-Host ''
    Write-Host "release assets in ${relDir}:"
    Get-ChildItem -LiteralPath $relDir | Select-Object Name, Length | Format-Table -AutoSize
}

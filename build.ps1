# Build ployenv (portable single-binary CLI).
#   .\build.ps1              -> dist\ployenv.exe
#   .\build.ps1 -Test        -> run unit tests first
#   .\build.ps1 -Out bin     -> custom output directory
#   .\build.ps1 -Package     -> lay out every published target's release assets in dist\release
#   .\build.ps1 -TargetOS linux -TargetArch arm64 -Package -> just that one target
#   .\build.ps1 -Version 0.3.2 -Package -> assert that this tree really is 0.3.2
#   .\build.ps1 -TargetOS linux -TargetArch arm64 -> cross-build one target (REQ-008)
param([string]$Out = 'dist', [switch]$Test, [switch]$Package, [string]$Version = '',
    [string]$TargetOS = '', [string]$TargetArch = '')
$ErrorActionPreference = 'Stop'
# The published targets (REQ-008 FR-802 / 3.1). Go's words on purpose: `amd64`, not `x64`.
$PublishedTargets = @('windows-amd64', 'windows-arm64', 'linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64')
# -Package lays out all six unless the caller asked for one specific target.
$explicitTarget = $PSBoundParameters.ContainsKey('TargetOS') -or $PSBoundParameters.ContainsKey('TargetArch')
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

# CGO is pinned off for every build (REQ-008 FR-801): the published binaries are
# meant to be static, and leaving it to the machine means a build that happens to
# find a C toolchain links libc and stops being one. Verified byte-identical on
# the 0.3.3 tree, so Windows is unaffected; it is the cross builds and any box
# with a different default that this protects.
$env:CGO_ENABLED = '0'
if ($TargetOS -eq '') { $TargetOS = (go env GOOS) }
if ($TargetArch -eq '') { $TargetArch = (go env GOARCH) }
$env:GOOS = $TargetOS
$env:GOARCH = $TargetArch
$exeName = if ($TargetOS -eq 'windows') { 'ployenv.exe' } else { 'ployenv' }
$built = Join-Path $Out $exeName

# -trimpath matters for the published artifact, not just tidiness: without it the binary
# embeds the build machine's absolute source paths (both this project's and the Go
# toolchain's) - 800+ occurrences in the 0.3.2 build were published in the npm package
# and the GitHub Release asset. Verified 2026-10-12 by the project's public-surface check.
# Do not drop it.
Invoke-Step 'go build' { go build -trimpath -ldflags '-s -w' -o $built ./cmd/ployenv }

Write-Host ("target: {0}/{1}  ->  {2}" -f $TargetOS, $TargetArch, $built)
Get-Item $built | Select-Object Name, Length

function Get-Sha256([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
}

# A cross-built binary cannot be executed on this host (and this host cannot always run the
# target's format), so the one thing that CAN be asserted about it is that it is the right kind
# of image: a Windows target must be a PE, a Linux target an ELF, a macOS target a Mach-O.
function Assert-BinaryKind([string]$Path, [string]$OS) {
    $head = [byte[]]::new(4)
    $stream = [IO.File]::OpenRead($Path)
    try { [void]$stream.Read($head, 0, 4) } finally { $stream.Dispose() }
    $hex = (($head | ForEach-Object { $_.ToString('X2') }) -join ' ')
    $kind = switch ($OS) {
        'windows' { if ($hex -like '4D 5A*') { 'PE (MZ)' } else { throw "expected a PE image for windows, got $hex" } }
        'linux'   { if ($hex -eq '7F 45 4C 46') { 'ELF' } else { throw "expected an ELF image for linux, got $hex" } }
        'darwin'  { if (@('CF FA ED FE', 'CE FA ED FE', 'FE ED FA CF') -contains $hex) { 'Mach-O' } else { throw "expected a Mach-O image for darwin, got $hex" } }
        default   { throw "unknown target OS: $OS" }
    }
    Write-Host ("PASS binary-kind         {0} {1}" -f $OS, $kind)
}

if ($Package) {
    # Release packaging (REQ-008 FR-802).
    #
    # windows/amd64 keeps the owner decision of 2026-10-11 (option B): its published asset IS the
    # binary committed at dsh-plugin/bin/win32-x64/ployenv.exe and is never rebuilt here, because a
    # rebuild bakes in the vcs.revision of whatever commit it runs at - which is not the revision
    # inside the committed file - and the ritual "deployed = bundled = built, byte for byte" then
    # silently stops holding.
    #
    # No other target has a committed binary, so there the published asset is a build of this tree.
    # What this repository stands behind for those targets is therefore: the same clean tree at the
    # same commit, built by this same command with CGO_ENABLED=0 -trimpath -ldflags '-s -w'. The vcs
    # stamp is deliberately kept because it is the provenance of the asset, and that is exactly why
    # the comparison only holds on a clean tree (vcs.modified=false) at the release commit: a dirty
    # tree changes the stamp and therefore the bytes, and so would -buildvcs=false on either side.
    # Every comparison here and in the release pipeline builds both sides with these flags.
    #
    # Assets stay flat, never zipped: deflate output differs between the .NET runtimes of a dev box
    # and a CI runner, so a published hash could not be reproduced locally. Consequence worth
    # knowing: a flat asset carries no mode, so on Unix the consumer has to set the execute bit
    # itself - REQ-008 3.1 fixes the flat name, so that part is by contract.
    $targets = if ($explicitTarget) { @("$TargetOS-$TargetArch") } else { $PublishedTargets }

    $relDir = Join-Path $Out 'release'
    if (Test-Path -LiteralPath $relDir) { Remove-Item -LiteralPath $relDir -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $relDir | Out-Null

    $cliSrc = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'internal\cli\cli.go') -Raw
    if ($cliSrc -notmatch 'const Version = "([^"]+)"') { throw 'cannot read const Version from internal/cli/cli.go' }
    $srcVersion = $Matches[1]
    if ($Version -ne '' -and $Version -ne $srcVersion) { throw "expected version $Version, but this tree says $srcVersion" }

    $licenseAsset = Join-Path $relDir 'LICENSE'
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'LICENSE') -Destination $licenseAsset -Force

    $names = @()
    $provenance = @()
    foreach ($target in $targets) {
        if ($PublishedTargets -notcontains $target) { throw "not a published target: $target (expected one of $($PublishedTargets -join ', '))" }
        $parts = $target.Split('-')
        $osName = $parts[0]
        $arch = $parts[1]
        $ext = if ($osName -eq 'windows') { '.exe' } else { '' }
        $versioned = Join-Path $relDir ("ployenv-v$srcVersion-$target$ext")
        $alias = Join-Path $relDir ("ployenv-$target$ext")

        if ($target -eq 'windows-amd64') {
            $bundledRel = 'dsh-plugin\bin\win32-x64\ployenv.exe'
            $bundled = Join-Path $PSScriptRoot $bundledRel
            if (-not (Test-Path -LiteralPath $bundled)) { throw "bundled binary not found: $bundledRel" }

            $reported = (& $bundled --version 2>&1 | Out-String).Trim()
            if ($reported -notmatch '^ployenv ([^ ]+) \(([^)]+)\)$') { throw "unexpected --version output from the bundled binary: $reported" }
            $binVersion = $Matches[1]
            $binPlatform = $Matches[2]

            $meta = (& go version -m $bundled | Out-String)
            if ($meta -notmatch 'vcs\.modified=false') { throw "the bundled binary was built from a dirty tree (vcs.modified != false): $bundledRel" }
            $binRevision = if ($meta -match 'vcs\.revision=([0-9a-f]+)') { $Matches[1] } else { '(absent)' }

            Write-Host ''
            Write-Host '== release packaging (option B: windows/amd64 ships the bundled binary, never rebuilds it) =='
            Write-Host ("PASS bundled-exists      {0} ({1} bytes)" -f $bundledRel, (Get-Item -LiteralPath $bundled).Length)
            # Do NOT spell out the file extension here. A problem matcher on the runner reads
            # "<something>.go: <message>" as a compiler diagnostic, so a line like
            # "PASS bundled-version  0.3.3   (internal/cli/cli.go: 0.3.3)" is published as a
            # failure-level annotation whose message is just "0.3.3)" - a green run that looks red.
            # Measured 2026-10-14: the annotation is attributed a path and a line number, i.e. it
            # comes from a matcher, not from anything this script prints (I-26).
            Write-Host ("PASS bundled-version     {0}   (version constant in internal/cli: {1})" -f $binVersion, $srcVersion)
            Write-Host ("PASS bundled-clean-tree  vcs.revision={0}, vcs.modified=false" -f $binRevision)

            if ($binVersion -ne $srcVersion) { throw "version mismatch: bundled binary is $binVersion, internal/cli/cli.go is $srcVersion" }
            if ($binPlatform -ne 'windows/amd64') { throw "bundled binary platform is $binPlatform, expected windows/amd64" }
            Copy-Item -LiteralPath $bundled -Destination $versioned -Force
            $provenance += ("windows/amd64 shipped from {0} (revision {1}, vcs.modified=false)" -f $bundledRel, $binRevision)

            # The build above is a gate (the tree must compile). At a release commit it also has to
            # be byte-identical to the bundled file; that is reported, not enforced, because a commit
            # made after a release (docs, README) legitimately differs - the embedded vcs.revision
            # changes even though no code did.
            if (Test-Path -LiteralPath $built) {
                $distMeta = (& go version -m $built | Out-String)
                $distRevision = if ($distMeta -match 'vcs\.revision=([0-9a-f]+)') { $Matches[1] } else { '(absent)' }
                if ((Get-Sha256 $built) -eq (Get-Sha256 $bundled)) {
                    Write-Host 'PASS built-equals-bundled  the build of this tree == the bundled binary'
                } else {
                    Write-Host ("INFO built-equals-bundled  differs (build of {0}, bundled {1})" -f $distRevision, $binRevision)
                    Write-Host ("     expected unless this IS the release commit: refresh {0} from this build before tagging." -f $bundledRel)
                }
            }
        } else {
            $stage = Join-Path $Out ('pkg\' + $target)
            New-Item -ItemType Directory -Force -Path $stage | Out-Null
            $staged = Join-Path $stage ("ployenv" + $ext)
            $env:GOOS = $osName
            $env:GOARCH = $arch
            $env:CGO_ENABLED = '0'
            Invoke-Step "go build $target" { go build -trimpath -ldflags '-s -w' -o $staged ./cmd/ployenv }
            $meta = (& go version -m $staged | Out-String)
            if ($meta -notmatch 'vcs\.modified=false') { throw "the $target build comes from a dirty tree (vcs.modified != false); clean up or commit before packaging" }
            $rev = if ($meta -match 'vcs\.revision=([0-9a-f]+)') { $Matches[1] } else { '(absent)' }
            Assert-BinaryKind -Path $staged -OS $osName
            Copy-Item -LiteralPath $staged -Destination $versioned -Force
            Write-Host ("PASS built-from-tree     {0} (revision {1}, vcs.modified=false)" -f $target, $rev)
            $provenance += ("$target built from this tree (revision $rev, vcs.modified=false)")
        }

        # A stable, version-free alias so a consumer can use the fixed URL
        # https://github.com/<owner>/<repo>/releases/latest/download/ployenv-<os>-<arch>[.exe]
        # without first asking the API which version is current (REQ-003 FR-303's default source and
        # REQ-008 FR-802's naming contract). Same bytes as the versioned asset - never a second build.
        Copy-Item -LiteralPath $versioned -Destination $alias -Force
        if ((Get-Sha256 $alias) -ne (Get-Sha256 $versioned)) { throw "the version-free alias of $target is not the same bytes as the versioned asset" }
        Write-Host ("PASS alias-is-same-bytes {0} (fixed name for releases/latest/download)" -f (Split-Path -Leaf $alias))
        $names += (Split-Path -Leaf $versioned)
        $names += (Split-Path -Leaf $alias)
    }

    # One SHA256SUMS.txt for the whole release, listing every asset (REQ-008 FR-802 / 3.1, v1.4):
    # whichever single asset a consumer has, its own line is in this file.
    $sums = @()
    foreach ($p in $provenance) { $sums += ("# ployenv {0} - {1}" -f $srcVersion, $p) }
    foreach ($name in $names) { $sums += ("{0}  {1}" -f (Get-Sha256 (Join-Path $relDir $name)), $name) }
    $sums += ("{0}  LICENSE" -f (Get-Sha256 $licenseAsset))
    $sumsFile = Join-Path $relDir 'SHA256SUMS.txt'
    [IO.File]::WriteAllText($sumsFile, (($sums -join "`n") + "`n"), (New-Object Text.UTF8Encoding($false)))

    # Self-check: recompute from disk the way a consumer would.
    $checked = 0
    foreach ($line in [IO.File]::ReadAllLines($sumsFile)) {
        if ($line -eq '' -or $line.StartsWith('#')) { continue }
        if ($line -notmatch '^([0-9A-Fa-f]{64})\s+(.+)$') { throw "malformed line in SHA256SUMS.txt: $line" }
        $want = $Matches[1]; $name = $Matches[2].Trim()
        $targetPath = Join-Path $relDir $name
        if (-not (Test-Path -LiteralPath $targetPath)) { throw "SHA256SUMS.txt lists a missing file: $name" }
        if ((Get-Sha256 $targetPath) -ne $want) { throw "SHA256SUMS.txt hash mismatch for $name" }
        $checked++
    }
    $expected = 2 * $targets.Count + 1
    if ($checked -ne $expected) { throw "SHA256SUMS.txt should cover $expected files, found $checked" }
    Write-Host ("PASS sha256sums-verified  {0}/{0} recomputed from disk (one file, every asset listed)" -f $checked)

    Write-Host ''
    Write-Host ("release assets in {0}  (targets: {1})" -f $relDir, ($targets -join ', '))
    Get-ChildItem -LiteralPath $relDir | Select-Object Name, Length | Format-Table -AutoSize
}

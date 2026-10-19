# Maven checks that cannot be expressed as one table row (REQ-009); the Windows
# counterpart of maven.sh for a Windows leg of the runtime matrix.
$ErrorActionPreference = 'Stop'

$bin = $env:PLOYENV_BIN
if (-not $bin) { throw 'the job must set PLOYENV_BIN' }
$work = $env:RUNNER_TEMP
if (-not $work) { $work = $env:TEMP }

if ($env:MAVEN_HOME) { throw "env must not set MAVEN_HOME (got $($env:MAVEN_HOME))" }
$m2 = Join-Path $env:USERPROFILE '.m2'
if (Test-Path -LiteralPath $m2) { Remove-Item -LiteralPath $m2 -Recurse -Force }

$out = (& mvn -v | Out-String)
$out
if ($out -notmatch '(?m)^Apache Maven 3\.[0-9]+\.[0-9]+ \(') { throw 'maven is not a stable 3.x release' }
if ($out -match '(?m)^Apache Maven 4\.') { throw 'a Maven release candidate was installed' }
if (-not $env:JAVA_HOME) { throw 'ployenv env must export JAVA_HOME for maven' }
if (-not $out.Contains($env:JAVA_HOME)) { throw "mvn does not run on the JDK that ployenv activated ($($env:JAVA_HOME))" }
if (Test-Path -LiteralPath $m2) { throw 'mvn wrote ~/.m2' }
$mvnCmd = (Get-Command mvn -ErrorAction SilentlyContinue).Source
$managed = Join-Path $env:PLOYENV_ROOT 'versions\maven'
if (-not $mvnCmd -or -not $mvnCmd.StartsWith($managed, [StringComparison]::OrdinalIgnoreCase)) {
  throw "mvn resolves to '$mvnCmd', not a ployenv-managed install"
}

$refusals = Join-Path $work 'mvn-release.txt'
& $bin install maven@release *> $refusals
if ($LASTEXITCODE -eq 0) { throw '@release must be refused while upstream <release> is 4.0.0-rc-7' }
if (-not ((Get-Content -LiteralPath $refusals -Raw).Contains('4.0.0-rc-7'))) { throw 'the refusal does not name the release candidate' }

Write-Host 'PASS maven: stable 3.x on the managed JDK, no ~/.m2, @release refused'
# The refusal above is an expected non-zero exit; leave the step successful.
$LASTEXITCODE = 0

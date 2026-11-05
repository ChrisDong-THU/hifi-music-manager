$ErrorActionPreference = 'Stop'
. (Join-Path (Split-Path -Parent $PSScriptRoot) 'manager-local-lifecycle.ps1')
$tempBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$testRoot = [IO.Path]::GetFullPath((Join-Path $tempBase ('hifi-manager-lifecycle-' + [Guid]::NewGuid().ToString('N'))))
if (-not $testRoot.StartsWith($tempBase.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe test directory' }
New-Item -ItemType Directory -Path $testRoot | Out-Null
function Assert-Equal($Actual, $Expected) {
    if ($Actual -ne $Expected) { throw "Expected '$Expected', got '$Actual'" }
}
try {
    $source = Join-Path $testRoot 'new.bin'
    $target = Join-Path $testRoot 'running.bin'
    $armSource = Join-Path $testRoot 'new-arm.bin'
    $armTarget = Join-Path $testRoot 'running-arm.bin'
    [IO.File]::WriteAllText($source, 'new-program')
    [IO.File]::WriteAllText($armSource, 'new-arm')
    $artifacts = @(@{Source=$source;Target=$target}, @{Source=$armSource;Target=$armTarget})
    $observed = @{ Started=0; Restored=0 }

    # Successful paired update starts only after both binaries have been installed.
    [IO.File]::WriteAllText($target, 'old-program')
    [IO.File]::WriteAllText($armTarget, 'old-arm')
    Install-ManagerArtifacts -Artifacts $artifacts -StartNew {
        Assert-Equal ([IO.File]::ReadAllText($target)) 'new-program'
        Assert-Equal ([IO.File]::ReadAllText($armTarget)) 'new-arm'
        $observed.Started++
    }
    Assert-Equal $observed.Started 1

    # Failure to start restores the entire previous pair, then restarts it.
    [IO.File]::WriteAllText($target, 'old-program')
    [IO.File]::WriteAllText($armTarget, 'old-arm')
    try {
        Install-ManagerArtifacts -Artifacts $artifacts -StartNew { throw 'synthetic startup failure' } -RestartPrevious {
            Assert-Equal ([IO.File]::ReadAllText($target)) 'old-program'
            Assert-Equal ([IO.File]::ReadAllText($armTarget)) 'old-arm'
            $observed.Restored++
        }
        throw 'Expected startup failure'
    } catch { if ($_.Exception.Message -notmatch 'previous artifacts restored:.*synthetic startup failure') { throw } }
    Assert-Equal $observed.Restored 1

    # An open executable is not overwritten; it becomes available after release.
    $held = [IO.File]::Open($target, 'Open', 'ReadWrite', 'None')
    try {
        try { Wait-ManagerFileReleased -Path $target -TimeoutMilliseconds 20; throw 'Expected a locked-file failure' }
        catch { if ($_.Exception.Message -notmatch 'Executable is still in use') { throw } }
    } finally { $held.Dispose() }
    Wait-ManagerFileReleased -Path $target
    Assert-Equal ([IO.File]::ReadAllText($target)) 'old-program'

    # Missing output is detected before changing either installed artifact.
    try {
        Install-ManagerArtifacts -Artifacts @($artifacts[0], @{Source=(Join-Path $testRoot 'missing.bin');Target=$armTarget}) -StartNew { throw 'must not start' }
        throw 'Expected missing build output'
    } catch { if ($_.Exception.Message -notmatch 'Missing build output') { throw } }
    Assert-Equal ([IO.File]::ReadAllText($target)) 'old-program'

    # A failure halfway through copying cannot leave a mixed pair installed.
    try {
        Install-ManagerArtifacts -Artifacts @($artifacts[0], @{Source=$armSource;Target=(Join-Path $testRoot 'missing-directory/arm.bin')}) -StartNew { throw 'must not start' }
        throw 'Expected partial publication failure'
    } catch { if ($_.Exception.Message -notmatch 'previous artifacts restored') { throw } }
    Assert-Equal ([IO.File]::ReadAllText($target)) 'old-program'
    Assert-Equal @(Get-ChildItem -LiteralPath $testRoot -Filter '*.rollback-*').Count 0
    Write-Output '5 local manager lifecycle checks passed.'
} finally {
    # Only remove this test's checked, uniquely named temporary directory.
    if ($testRoot.StartsWith($tempBase.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($testRoot).StartsWith('hifi-manager-lifecycle-')) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}

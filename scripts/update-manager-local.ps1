param(
    [ValidateRange(1024,65535)][int]$Port = 8787,
    [string]$DataDirectory = '',
    [switch]$FixedLocalStorage
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
if (-not $DataDirectory) { $DataDirectory = Join-Path $projectRoot 'downloads/manager-local' }
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory)
$program = Join-Path $projectRoot 'downloads/hifi-manager.exe'
$stage = Join-Path $projectRoot 'downloads/manager-build'
. (Join-Path $PSScriptRoot 'manager-local-lifecycle.ps1')
# Finish all checks before touching the running program or service.
& (Join-Path $PSScriptRoot 'build-manager.ps1') -OutputDirectory $stage
$artifacts = @(
    @{ Source = (Join-Path $stage 'hifi-manager.exe'); Target = $program },
    @{ Source = (Join-Path $stage 'hifi-manager-linux-arm64'); Target = (Join-Path $projectRoot 'downloads/hifi-manager-linux-arm64') }
)
foreach ($item in $artifacts) {
    if (-not (Test-Path -LiteralPath $item.Source -PathType Leaf)) { throw "Missing build output: $($item.Source)" }
}
$existing = @(Get-CimInstance Win32_Process -Filter "Name='hifi-manager.exe'" | Where-Object {
    $_.ExecutablePath -eq $program -and $_.CommandLine -match ('(?:^|\s)-listen\s+"?127\.0\.0\.1:' + $Port + '"?(?:\s|$)')
})
if ($existing.Count -gt 1) { throw 'More than one manager process matches this address' }
if ($existing.Count -eq 1) {
    $homePattern = '(?:^|\s)-home\s+(?:"' + [regex]::Escape($DataDirectory) + '"|' + [regex]::Escape($DataDirectory) + ')(?:\s|$)'
    $diskManaged = $existing[0].CommandLine -match '(?:^|\s)-data-disks(?:\s|$)'
    if ($existing[0].CommandLine -notmatch $homePattern -or $diskManaged -eq [bool]$FixedLocalStorage) {
        throw 'The running manager uses different storage settings; stop it explicitly before changing modes or directories'
    }
    $state = Invoke-RestMethod "http://127.0.0.1:$Port/api/state" -TimeoutSec 5
    if (@($state.jobs | Where-Object { $_.kind -ne 'sync' -and $_.state -in @('queued','running') }).Count -or $state.active_mutations -gt 0) {
        throw 'Wait for uploads and file tasks to finish before updating'
    }
    Stop-Process -Id $existing[0].ProcessId
    Wait-Process -Id $existing[0].ProcessId -ErrorAction SilentlyContinue
}
$startLocal = {
    & (Join-Path $PSScriptRoot 'start-manager-local.ps1') -Port $Port -DataDirectory $DataDirectory -FixedLocalStorage:$FixedLocalStorage -Background
}
$restoreLocal = if ($existing.Count -eq 1) { $startLocal } else { $null }
Install-ManagerArtifacts -Artifacts $artifacts -StartNew $startLocal -RestartPrevious $restoreLocal

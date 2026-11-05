param([ValidateRange(1024,65535)][int]$Port = 8787, [string]$DataDirectory = '', [switch]$FixedLocalStorage, [switch]$Background)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
if (-not $DataDirectory) { $DataDirectory = Join-Path $projectRoot 'downloads/manager-local' }
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory)
$program = Join-Path $projectRoot 'downloads/hifi-manager.exe'
if (-not (Test-Path -LiteralPath $program)) {
    & (Join-Path $PSScriptRoot 'build-manager.ps1')
}
Write-Output "Local music manager: http://127.0.0.1:$Port"
if ($FixedLocalStorage) {
    Write-Output "Local test data: $DataDirectory"
} else {
    Write-Output "Local configuration: $DataDirectory"
    Write-Output 'USB data disk management is enabled. Select a disk in the home page settings.'
}
if ($Background) { Write-Output 'Starting in background. No connection to the Nano is made.' }
else { Write-Output 'Press Ctrl+C to stop. No connection to the Nano is made.' }
$managerArguments = @('-listen', "127.0.0.1:$Port", '-home', $DataDirectory)
if (-not $FixedLocalStorage) { $managerArguments += '-data-disks' }
if ($Background) {
    $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $Port)
    try { $probe.Start() } finally { $probe.Stop() }
    New-Item -ItemType Directory -Force -Path $DataDirectory | Out-Null
    $managerArguments[3] = '"' + $DataDirectory + '"'
    $process = Start-Process -FilePath $program -ArgumentList $managerArguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $DataDirectory 'server.stdout.log') -RedirectStandardError (Join-Path $DataDirectory 'server.stderr.log')
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        if ($process.HasExited) { throw "Manager exited. See $DataDirectory/server.stderr.log" }
        try {
            $state = Invoke-RestMethod "http://127.0.0.1:$Port/api/state" -TimeoutSec 2
            if ($state.csrf -and $null -ne $state.stats) { Write-Output "Ready (PID $($process.Id))"; return }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    Stop-Process -Id $process.Id -ErrorAction SilentlyContinue
    throw "Manager did not become ready. See $DataDirectory/server.stderr.log"
}
& $program @managerArguments
if ($LASTEXITCODE) { throw 'Music manager stopped with an error' }

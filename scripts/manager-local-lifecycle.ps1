# Local executable replacement. No data directories or device services are changed.
function Wait-ManagerFileReleased {
    param([Parameter(Mandatory)][string]$Path, [int]$TimeoutMilliseconds = 10000)
    $elapsed = [Diagnostics.Stopwatch]::StartNew()
    while (Test-Path -LiteralPath $Path) {
        try {
            $handle = [IO.File]::Open($Path, 'Open', 'ReadWrite', 'None')
            $handle.Dispose()
            return
        } catch [IO.IOException] {
            if ($elapsed.ElapsedMilliseconds -ge $TimeoutMilliseconds) {
                throw "Executable is still in use: $Path"
            }
            Start-Sleep -Milliseconds 100
        }
    }
}

function Install-ManagerArtifacts {
    param(
        [Parameter(Mandatory)][hashtable[]]$Artifacts,
        [Parameter(Mandatory)][scriptblock]$StartNew,
        [scriptblock]$RestartPrevious
    )
    # Validate the complete build before replacing any files.
    foreach ($item in $Artifacts) {
        if (-not (Test-Path -LiteralPath $item.Source -PathType Leaf)) {
            throw "Missing build output: $($item.Source)"
        }
    }
    $saved = @()
    try {
        foreach ($item in $Artifacts) {
            Wait-ManagerFileReleased -Path $item.Target
            $backup = $item.Target + '.rollback-' + [Guid]::NewGuid().ToString('N')
            $existed = Test-Path -LiteralPath $item.Target
            if ($existed) { Copy-Item -LiteralPath $item.Target -Destination $backup -ErrorAction Stop }
            $saved += @{ Target = $item.Target; Backup = $backup; Existed = $existed }
            Copy-Item -LiteralPath $item.Source -Destination $item.Target -Force -ErrorAction Stop
        }
        & $StartNew
    } catch {
        $failure = $_
        try {
            foreach ($item in $saved) {
                Wait-ManagerFileReleased -Path $item.Target
                if ($item.Existed) {
                    Copy-Item -LiteralPath $item.Backup -Destination $item.Target -Force -ErrorAction Stop
                } elseif (Test-Path -LiteralPath $item.Target) {
                    Remove-Item -LiteralPath $item.Target -ErrorAction Stop
                }
            }
            if ($RestartPrevious) { & $RestartPrevious }
        } catch {
            # Keep backups for recovery if rollback itself fails.
            throw "Update failed: $failure. Recovery failed: $_. Backups were retained."
        }
        foreach ($item in $saved) {
            if (Test-Path -LiteralPath $item.Backup) { Remove-Item -LiteralPath $item.Backup -ErrorAction Stop }
        }
        throw "Update failed; previous artifacts restored: $failure"
    }
    foreach ($item in $saved) {
        if (Test-Path -LiteralPath $item.Backup) { Remove-Item -LiteralPath $item.Backup -ErrorAction Stop }
    }
}

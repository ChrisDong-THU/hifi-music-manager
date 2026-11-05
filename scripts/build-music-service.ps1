param(
    [ValidateSet('arm64', 'amd64')][string]$TargetArchitecture = 'arm64',
    [string]$OutputDirectory = '',
    [switch]$BuildTargetTests
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$compiler = Join-Path $projectRoot 'downloads/go-admin/go/bin/go.exe'
if (-not (Test-Path -LiteralPath $compiler)) { $compiler = (Get-Command go -ErrorAction Stop).Source }
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $projectRoot 'downloads' }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$artifactNames = @('hifi-music-service')
if ($BuildTargetTests) { $artifactNames += @('hifi-music-service.test', 'hifi-browsebridge.test') }
$buildNonce = [Guid]::NewGuid().ToString('N')
$stages = @{}
foreach ($name in $artifactNames) { $stages[$name] = Join-Path $OutputDirectory ($name + '-' + $buildNonce + '.next') }
$names = @('GOOS', 'GOARCH', 'CGO_ENABLED', 'GOPATH', 'GOCACHE')
$previous = @{}
foreach ($name in $names) { $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
Push-Location (Join-Path $projectRoot 'music-service')
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    $env:GOPATH = Join-Path $projectRoot 'downloads/go-manager'
    $env:GOCACHE = Join-Path $projectRoot 'downloads/go-manager-cache'
    Push-Location (Join-Path $projectRoot 'shared-library')
    try {
        & $compiler vet ./...
        if ($LASTEXITCODE) { throw 'Shared library vet failed' }
        & $compiler test -count=1 -timeout=90s ./...
        if ($LASTEXITCODE) { throw 'Shared library tests failed' }
    } finally { Pop-Location }
    & $compiler vet ./...
    if ($LASTEXITCODE) { throw 'Go vet failed' }
    & $compiler test -count=1 -timeout=90s ./...
    if ($LASTEXITCODE) { throw 'Music service tests failed' }
    $env:GOOS = 'linux'; $env:GOARCH = $TargetArchitecture
    & $compiler vet ./...
    if ($LASTEXITCODE) { throw 'Linux target vet failed' }
    & $compiler build -trimpath -ldflags='-s -w' -o $stages['hifi-music-service'] .
    if ($LASTEXITCODE) { throw 'Linux build failed' }
    if ($BuildTargetTests) {
        & $compiler test -c -o $stages['hifi-music-service.test'] .
        if ($LASTEXITCODE) { throw 'Linux test build failed' }
        & $compiler test -c -o $stages['hifi-browsebridge.test'] ./browsebridge
        if ($LASTEXITCODE) { throw 'UPnP test build failed' }
    }
    foreach ($name in $artifactNames) { Copy-Item -LiteralPath $stages[$name] -Destination (Join-Path $OutputDirectory $name) -Force }
    Write-Output "Music service checks and Linux/$TargetArchitecture build passed. No deployment performed."
    if ($BuildTargetTests) { Write-Output 'Target test binaries were compiled only; execute them on Linux for platform-specific verification.' }
} finally {
    foreach ($stage in $stages.Values) { if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Force } }
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process') }
    Pop-Location
}

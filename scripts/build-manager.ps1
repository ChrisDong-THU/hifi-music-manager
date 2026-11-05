param(
    [switch]$SkipTests,
    [string]$OutputDirectory = ''
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $projectRoot 'downloads' }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$stageID = [Guid]::NewGuid().ToString('N')
$windowsStage = Join-Path $OutputDirectory "hifi-manager-$stageID.exe"
$linuxStage = Join-Path $OutputDirectory "hifi-manager-$stageID-linux-arm64"
$compiler = Join-Path $projectRoot 'downloads/go-admin/go/bin/go.exe'
if (-not (Test-Path -LiteralPath $compiler)) { $compiler = (Get-Command go -ErrorAction Stop).Source }
$names = @('GOOS', 'GOARCH', 'CGO_ENABLED', 'GOPATH', 'GOCACHE')
$previous = @{}
foreach ($name in $names) { $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
Push-Location (Join-Path $projectRoot 'manager-service')
try {
    $frontend = Join-Path $projectRoot 'manager-service/frontend'
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { throw 'Node.js/npm is required to build the frontend; the running Go service does not require Node.js.' }
    & npm run deps:check --prefix $frontend -- (Join-Path $projectRoot 'downloads/npm-manager-cache')
    if ($LASTEXITCODE) { throw 'Frontend dependencies failed' }
    if (-not $SkipTests) {
        & (Join-Path $PSScriptRoot 'tests/manager-local-lifecycle.test.ps1')
        & npm test --prefix $frontend
        if ($LASTEXITCODE) { throw 'Frontend tests failed' }
    }
    & npm run build --prefix $frontend
    if ($LASTEXITCODE) { throw 'Frontend build failed' }
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    $env:GOPATH = Join-Path $projectRoot 'downloads/go-manager'
    $env:GOCACHE = Join-Path $projectRoot 'downloads/go-manager-cache'
    Push-Location (Join-Path $projectRoot 'shared-library')
    try {
        & $compiler vet ./...
        if ($LASTEXITCODE) { throw 'Shared library vet failed' }
        if (-not $SkipTests) {
            & $compiler test -count=1 -timeout=90s ./...
            if ($LASTEXITCODE) { throw 'Shared library tests failed' }
        }
    } finally { Pop-Location }
    & $compiler vet ./...
    if ($LASTEXITCODE) { throw 'Manager vet failed' }
    if (-not $SkipTests) {
        & $compiler test -count=1 -timeout=90s ./...
        if ($LASTEXITCODE) { throw 'Manager tests failed' }
    }
    & $compiler build -trimpath -ldflags='-s -w' -o $windowsStage .
    if ($LASTEXITCODE) { throw 'Windows build failed' }
    # Compilation only. This script never connects to or deploys to the Nano.
    $env:GOOS = 'linux'; $env:GOARCH = 'arm64'
    & $compiler build -trimpath -ldflags='-s -w' -o $linuxStage .
    if ($LASTEXITCODE) { throw 'ARM64 cross-build failed' }
    # Publish only after both targets compile. An isolated output directory lets
    # review/CI builds leave the running local executable untouched.
    try {
        Copy-Item -LiteralPath $windowsStage -Destination (Join-Path $OutputDirectory 'hifi-manager.exe') -Force
        Copy-Item -LiteralPath $linuxStage -Destination (Join-Path $OutputDirectory 'hifi-manager-linux-arm64') -Force
    } catch { throw "Cannot publish build output to $OutputDirectory. Stop the local manager or use -OutputDirectory for an isolated build." }
    Write-Output 'Go manager checks and builds passed. No deployment performed.'
} finally {
    foreach ($stage in @($windowsStage, $linuxStage)) {
        if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage }
    }
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process') }
    Pop-Location
}

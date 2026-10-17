param([ValidateSet('Debug','Release')][string]$Configuration='Release')
$ErrorActionPreference='Stop'
$desktopProjectRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$desktopDist=Join-Path $desktopProjectRoot 'dist'
New-Item -ItemType Directory -Path $desktopDist -Force | Out-Null
Push-Location (Join-Path $desktopProjectRoot 'web')
try { bun run build; if($LASTEXITCODE -ne 0){throw 'Frontend build failed'} } finally { Pop-Location }
$desktopOldCGO=$env:CGO_ENABLED
$desktopOldCC=$env:CC
Push-Location (Join-Path $desktopProjectRoot 'backend')
try {
    $env:CGO_ENABLED='1'
    if(-not $env:CC -and (Test-Path -LiteralPath 'C:\msys64\ucrt64\bin\gcc.exe')){. (Join-Path $PSScriptRoot 'prepare-cgo.ps1')}
    go build -trimpath -o (Join-Path $desktopDist 'canvas-host.exe') ./cmd/desktop
    if($LASTEXITCODE -ne 0){throw 'Host build failed'}
    if(Test-Path -LiteralPath 'cmd/canvas-mcp/main.go'){
        go build -trimpath -o (Join-Path $desktopDist 'canvas-mcp.exe') ./cmd/canvas-mcp
        if($LASTEXITCODE -ne 0){throw 'MCP build failed'}
    }
} finally { $env:CGO_ENABLED=$desktopOldCGO; $env:CC=$desktopOldCC; Pop-Location }

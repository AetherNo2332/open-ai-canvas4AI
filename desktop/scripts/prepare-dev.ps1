$ErrorActionPreference='Stop'
$desktopDevRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$desktopDevResources=Join-Path $desktopDevRoot '.local/desktop-dev-resources'
New-Item -ItemType Directory -Path (Join-Path $desktopDevResources 'bin') -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $desktopDevRoot 'dist/canvas-host.exe') -Destination (Join-Path $desktopDevResources 'bin/canvas-host.exe') -Force
New-Item -ItemType Directory -Path (Join-Path $desktopDevResources 'web') -Force | Out-Null
Copy-Item -Path (Join-Path $desktopDevRoot 'web/dist/*') -Destination (Join-Path $desktopDevResources 'web') -Recurse -Force
# The supplied release has no default_app.asar CLI launcher. Register ONLY our
# source main as the development application in the isolated extracted runtime.
$desktopDevLauncher=Join-Path $desktopDevRoot '.local/electron-runtime/resources/app'
New-Item -ItemType Directory -Path $desktopDevLauncher -Force | Out-Null
@{name='infinite-canvas-desktop-dev';version='0.1.0';main='main.cjs'} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $desktopDevLauncher 'package.json') -Encoding utf8
$desktopDevMain=(Join-Path $desktopDevRoot 'desktop/main.cjs') | ConvertTo-Json -Compress
('console.log("desktop_source_launcher"); require('+$desktopDevMain+');') | Set-Content -LiteralPath (Join-Path $desktopDevLauncher 'main.cjs') -Encoding utf8

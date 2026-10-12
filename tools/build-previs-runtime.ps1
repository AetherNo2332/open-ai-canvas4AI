param([string]$Image = 'canvas-previs-renderer:verify')
$ErrorActionPreference = 'Stop'
$previsRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$previsContext = [IO.Path]::GetFullPath((Join-Path $previsRoot '.local\previs-runtime-build'))
if (-not $previsContext.StartsWith($previsRoot + [IO.Path]::DirectorySeparatorChar)) { throw '构建目录越界' }
New-Item -ItemType Directory -Force $previsContext | Out-Null
foreach ($relative in @('renderer\src','renderer\public','web\dist-previs')) {
    $source = Join-Path $previsRoot $relative
    $destination = Join-Path $previsContext $relative
    if (-not (Test-Path -LiteralPath $source)) { throw "缺少已验证构建产物：$relative" }
    New-Item -ItemType Directory -Force $destination | Out-Null
    Copy-Item -Path (Join-Path $source '*') -Destination $destination -Recurse -Force
}
Copy-Item -LiteralPath (Join-Path $previsRoot 'renderer\package.json') -Destination (Join-Path $previsContext 'renderer\package.json') -Force
@'
FROM node:22.19.0-alpine3.22
RUN apk add --no-cache chromium=142.0.7444.59-r0 ffmpeg=6.1.2-r2 && mkdir -p /data/jobs && chown -R node:node /data
RUN apk add --no-cache chromium-swiftshader=142.0.7444.59-r0
WORKDIR /app/renderer
COPY renderer/src ./src
COPY renderer/public ./public
COPY renderer/package.json ./
COPY web/dist-previs /app/web/dist-previs
ENV CHROME_BIN=/usr/bin/chromium-browser PREVIS_DATA_DIR=/data/jobs PORT=8081
USER node
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s --retries=3 CMD node -e "fetch('http://127.0.0.1:8081/health').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"
CMD ["node","src/server.mjs"]
'@ | Set-Content -LiteralPath (Join-Path $previsContext 'Dockerfile') -Encoding utf8
docker build -t $Image $previsContext
exit $LASTEXITCODE

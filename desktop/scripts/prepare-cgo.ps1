# Stage compiler subprocesses beside their DLLs. Some Windows installations do
# not resolve MSYS2 DLLs for executables in lib/gcc or target/bin through PATH.
param([string]$Gcc='C:\msys64\ucrt64\bin\gcc.exe')
$ErrorActionPreference='Stop'
$desktopCompilerRoot=Split-Path $Gcc
$desktopCompilerVersion=(& $Gcc -dumpversion).Trim()
if($LASTEXITCODE -ne 0){throw 'Cannot query GCC version'}
$desktopCompilerLib=[IO.Path]::GetFullPath((Join-Path $desktopCompilerRoot "../lib/gcc/x86_64-w64-mingw32/$desktopCompilerVersion"))
$desktopCompilerStage=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../.local/cgo-tools'))
New-Item -ItemType Directory -Path $desktopCompilerStage -Force | Out-Null
foreach($desktopTool in @('cc1.exe','collect2.exe','lto-wrapper.exe','lto1.exe')) {
    Copy-Item -LiteralPath (Join-Path $desktopCompilerLib $desktopTool) -Destination $desktopCompilerStage
}
foreach($desktopTool in @('as.exe','ld.exe','ar.exe','ranlib.exe')) {
    Copy-Item -LiteralPath (Join-Path $desktopCompilerRoot $desktopTool) -Destination $desktopCompilerStage
}
Get-ChildItem -LiteralPath $desktopCompilerRoot -Filter '*.dll' | Copy-Item -Destination $desktopCompilerStage
$env:CC='"'+$Gcc+'" -B "'+$desktopCompilerStage+'"'
$env:CGO_ENABLED='1'

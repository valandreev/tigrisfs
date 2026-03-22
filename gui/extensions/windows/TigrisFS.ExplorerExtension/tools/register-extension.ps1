param(
    [string]$DllPath = "",
    [switch]$Unregister
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($DllPath)) {
    $DllPath = Join-Path $PSScriptRoot "..\bin\Release\net48\TigrisFS.ExplorerExtension.dll"
}
$DllPath = [System.IO.Path]::GetFullPath($DllPath)

if (-not (Test-Path $DllPath)) {
    throw "Extension DLL not found: $DllPath"
}

$framework64 = Join-Path $env:WINDIR "Microsoft.NET\Framework64\v4.0.30319\RegAsm.exe"
$framework32 = Join-Path $env:WINDIR "Microsoft.NET\Framework\v4.0.30319\RegAsm.exe"

if (Test-Path $framework64) {
    $regasm = $framework64
} elseif (Test-Path $framework32) {
    $regasm = $framework32
} else {
    throw "RegAsm.exe not found. Install .NET Framework 4.x developer tools."
}

if ($Unregister) {
    & $regasm /nologo /u "$DllPath"
    Write-Host "Unregistered TigrisFS Explorer extension: $DllPath"
} else {
    & $regasm /nologo /codebase "$DllPath"
    Write-Host "Registered TigrisFS Explorer extension: $DllPath"
}

Write-Host "Restart Explorer (or sign out/in) to refresh shell extension state."

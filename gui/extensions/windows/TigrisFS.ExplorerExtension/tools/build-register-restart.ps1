param(
    [string]$Configuration = "Release",
    [string]$ProjectPath = "..\TigrisFS.ExplorerExtension.csproj",
    [string]$DllPath = ""
)

$ErrorActionPreference = "Stop"

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-Administrator)) {
    throw "Run this script from an elevated (Administrator) PowerShell prompt."
}

$projectFullPath = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot $ProjectPath))
if (-not (Test-Path $projectFullPath)) {
    throw "Project not found: $projectFullPath"
}

Write-Host "Building Explorer extension..."
dotnet build $projectFullPath -c $Configuration

if ([string]::IsNullOrWhiteSpace($DllPath)) {
    $projectDir = Split-Path -Parent $projectFullPath
    $DllPath = Join-Path $projectDir "bin\$Configuration\net48\TigrisFS.ExplorerExtension.dll"
}
$DllPath = [System.IO.Path]::GetFullPath($DllPath)

if (-not (Test-Path $DllPath)) {
    throw "Built DLL not found: $DllPath"
}

Write-Host "Registering extension DLL..."
& "$PSScriptRoot\register-extension.ps1" -DllPath $DllPath

Write-Host "Restarting Explorer..."
Get-Process explorer -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Process explorer.exe

Write-Host "Done. Explorer shell extension has been rebuilt, registered, and Explorer restarted."

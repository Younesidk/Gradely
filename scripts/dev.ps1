<#
.SYNOPSIS
  Runs Gradly locally with a throw-away or persistent local data folder. Local use only.
.DESCRIPTION
  Sets the first-run administrator variables (if not already set in your environment) and starts the app
  with `dotnet run`. If ADMIN_PASSWORD is not set, a random one is generated and printed ONCE.
  Nothing is deployed and nothing leaves your machine.
.EXAMPLE
  ./scripts/dev.ps1
  ./scripts/dev.ps1 -DataDir C:\temp\gradly-data -Port 5103
#>
param(
    [string]$DataDir = (Join-Path $PSScriptRoot "..\src\Gradly\App_Data"),
    [int]$Port = 5103
)

$ErrorActionPreference = "Stop"
$project = Join-Path $PSScriptRoot "..\src\Gradly"

if (-not $env:ADMIN_EMAIL)    { $env:ADMIN_EMAIL = "admin@gradly.local" }
if (-not $env:ADMIN_USERNAME) { $env:ADMIN_USERNAME = "admin" }
if (-not $env:ADMIN_PASSWORD) {
    $bytes = New-Object byte[] 12
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    $env:ADMIN_PASSWORD = "Gr-" + ([Convert]::ToBase64String($bytes) -replace '[^A-Za-z0-9]', 'x') + "1"
    Write-Host "Generated admin password (shown once, only used if the admin doesn't exist yet):" -ForegroundColor Yellow
    Write-Host "  $($env:ADMIN_USERNAME) / $($env:ADMIN_PASSWORD)" -ForegroundColor Yellow
}
$env:GRADLY_DATA_DIR = (New-Item -ItemType Directory -Force -Path $DataDir).FullName
$env:APP_BASE_URL = "http://localhost:$Port"
$env:ASPNETCORE_ENVIRONMENT = "Development"

Write-Host "Data directory : $($env:GRADLY_DATA_DIR)"
Write-Host "Listening on   : http://localhost:$Port"
dotnet run --project $project --no-launch-profile --urls "http://localhost:$Port"

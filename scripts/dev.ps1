<#
.SYNOPSIS
  Runs Gradely locally: the Go API on :8080 and (optionally) the Vite dev server with hot reload on :5173.
.DESCRIPTION
  Sets the first-run administrator variables (if not already set in your environment) and starts the backend.
  If ADMIN_PASSWORD is not set, a random one is generated and printed ONCE.
  Nothing is deployed and nothing leaves your machine.
.EXAMPLE
  ./scripts/dev.ps1              # build the frontend, run the Go server on http://localhost:8080
  ./scripts/dev.ps1 -Watch       # also start Vite on http://localhost:5173 (proxies /api to :8080)
#>
param(
    [string]$DataDir = (Join-Path $PSScriptRoot "..\backend\data"),
    [int]$Port = 8080,
    [switch]$Watch
)

$ErrorActionPreference = "Stop"
$root = Join-Path $PSScriptRoot ".."

if (-not $env:ADMIN_EMAIL)    { $env:ADMIN_EMAIL = "admin@gradely.local" }
if (-not $env:ADMIN_USERNAME) { $env:ADMIN_USERNAME = "admin" }
if (-not $env:ADMIN_PASSWORD) {
    $bytes = New-Object byte[] 12
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    $env:ADMIN_PASSWORD = "Gr-" + ([Convert]::ToBase64String($bytes) -replace '[^A-Za-z0-9]', 'x') + "1"
    Write-Host "Generated admin password (shown once, only used if the admin doesn't exist yet):" -ForegroundColor Yellow
    Write-Host "  $($env:ADMIN_USERNAME) / $($env:ADMIN_PASSWORD)" -ForegroundColor Yellow
}
$env:GRADELY_DATA_DIR = (New-Item -ItemType Directory -Force -Path $DataDir).FullName
$env:GRADELY_ADDR = ":$Port"
$env:APP_BASE_URL = if ($Watch) { "http://localhost:5173" } else { "http://localhost:$Port" }

if ($Watch) {
    Start-Process -NoNewWindow -FilePath "npm" -ArgumentList "run", "dev" -WorkingDirectory (Join-Path $root "frontend")
} else {
    Push-Location (Join-Path $root "frontend")
    try { npm install; npm run build } finally { Pop-Location }
}

Write-Host "Data directory : $($env:GRADELY_DATA_DIR)"
Write-Host "API listening  : http://localhost:$Port"
Push-Location (Join-Path $root "backend")
try { go run . } finally { Pop-Location }

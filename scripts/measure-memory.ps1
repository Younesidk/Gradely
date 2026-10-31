<#
.SYNOPSIS
  Samples the memory of a running Gradly process (local measurement only).
.DESCRIPTION
  Finds the process listening on the given port (default 5103) - or use -ProcessId - and prints
  Working Set (RAM currently resident), Private Bytes (committed private memory) and GC heap figures
  every few seconds. Run the app first (scripts/dev.ps1 or `dotnet run`), use it for a while, then run this.
  Optionally -Warmup issues a few requests so you also see a "serving traffic" figure.
.EXAMPLE
  ./scripts/measure-memory.ps1 -Samples 6 -IntervalSeconds 5
#>
param(
    [int]$Port = 5103,
    [int]$ProcessId = 0,
    [int]$Samples = 5,
    [int]$IntervalSeconds = 3,
    [switch]$Warmup
)

if ($ProcessId -eq 0) {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $conn) { Write-Error "Nothing is listening on port $Port. Start Gradly first."; exit 1 }
    $ProcessId = $conn.OwningProcess
}
$p = Get-Process -Id $ProcessId
Write-Host "Process: $($p.ProcessName) (PID $($p.Id)), started $($p.StartTime)"

if ($Warmup) {
    1..10 | ForEach-Object { try { Invoke-WebRequest -UseBasicParsing "http://localhost:$Port/account/login" | Out-Null } catch { } }
}

"{0,-10} {1,14} {2,16} {3,14}" -f "Time", "WorkingSet(MB)", "PrivateBytes(MB)", "Threads"
for ($i = 0; $i -lt $Samples; $i++) {
    $p.Refresh()
    "{0,-10} {1,14:N1} {2,16:N1} {3,14}" -f (Get-Date -Format "HH:mm:ss"),
        ($p.WorkingSet64 / 1MB), ($p.PrivateMemorySize64 / 1MB), $p.Threads.Count
    if ($i -lt $Samples - 1) { Start-Sleep -Seconds $IntervalSeconds }
}

# Opt-in real Windows/WSL regression; creates only synthetic test files.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Distribution,
    [Parameter(Mandatory)][string]$OutputDirectory,
    [Parameter(Mandatory)][ValidatePattern('^/[^\x00\r\n]*$')][string]$LinuxOutputDirectory,
    [ValidateRange(12, 180)][int]$HoldSeconds = 90
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT' -or -not [IO.Path]::IsPathRooted($OutputDirectory)) {
    throw 'Windows and an absolute, new OutputDirectory are required.'
}
$directory = [IO.Path]::GetFullPath($OutputDirectory)
$null = New-Item -ItemType Directory -Path $directory
$utf8 = New-Object Text.UTF8Encoding($false)
$launcher = Join-Path $PSScriptRoot 'start-wsl-soak.ps1'
$observer = Join-Path $PSScriptRoot 'observe-wsl-soak.ps1'
$windowsPowerShell = Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function Literal([string]$Value) { "'" + $Value.Replace("'", "''") + "'" }
function Start-DetachedFixture([string]$FileName, [string]$RunDirectory) {
    $linuxFile = $LinuxOutputDirectory.TrimEnd('/') + '/' + $FileName
    $command = '& ' + (Literal $launcher) + ' -Distribution ' + (Literal $Distribution) +
        ' -LinuxScript ' + (Literal $linuxFile) + ' -OutputDirectory ' + (Literal $RunDirectory)
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    # The actual launching shell exits before any lifecycle assertions below.
    & $windowsPowerShell -NoProfile -NonInteractive -ExecutionPolicy Bypass -OutputFormat Text -EncodedCommand $encoded | Out-Null
    Assert-True ($LASTEXITCODE -eq 0) 'Launching shell failed.'
    $manifest = Get-Content -LiteralPath (Join-Path $RunDirectory 'manifest.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-True ($null -eq (Get-Process -Id $manifest.launcher_pid -ErrorAction SilentlyContinue)) 'Launching shell remained alive.'
    return $manifest
}
function Read-Status([string]$RunDirectory) {
    # Opening a concurrently replaced file can transiently fail on this host.
    # Retry the open only, never malformed JSON or a stale cached status.
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        try {
            $raw = [IO.File]::ReadAllText((Join-Path $RunDirectory 'status.json'), [Text.Encoding]::UTF8)
            return ($raw | ConvertFrom-Json)
        } catch [IO.IOException] {
            if ($attempt -eq 19) { throw }
            Start-Sleep -Milliseconds 25
        }
    }
}

$hold = @'
#!/bin/sh
set -eu
i=0
while [ "$i" -lt HOLD_SECONDS ]; do
  printf 'heartbeat=%s\n' "$i"
  i=$((i + 1))
  sleep 1
done
printf 'completed=%s\n' "$i"
'@
$holdName = "hold 'quote.sh"
[IO.File]::WriteAllText((Join-Path $directory $holdName), $hold.Replace('HOLD_SECONDS', [string]$HoldSeconds).Replace("`r`n", "`n"), $utf8)
$burst = "#!/bin/sh`nset -eu`nhead -c 2097152 /dev/zero`nhead -c 2097152 /dev/zero >&2`nexit 31`n"
[IO.File]::WriteAllText((Join-Path $directory 'burst.sh'), $burst, $utf8)
$holdRun = Join-Path $directory "run 'quoted"
$burstRun = Join-Path $directory 'burst'
$holdManifest = Start-DetachedFixture $holdName $holdRun
$burstManifest = Start-DetachedFixture 'burst.sh' $burstRun
$initial = Read-Status $holdRun
$midRun = $null
$deadline = [DateTime]::UtcNow.AddSeconds($HoldSeconds + 30)
do {
    $status = Read-Status $holdRun
    $lines = @(Get-Content -LiteralPath (Join-Path $holdRun 'wsl.stdout.log') -ErrorAction SilentlyContinue)
    if ($null -eq $midRun -and $status.state -eq 'running' -and $status.observed_at -ne $initial.observed_at -and $lines.Count -ge 2) {
        Assert-True ($null -ne (Get-Process -Id $status.supervisor_pid -ErrorAction SilentlyContinue)) 'Supervisor disappeared.'
        Assert-True ($null -ne (Get-Process -Id $status.wsl_pid -ErrorAction SilentlyContinue)) 'WSL client disappeared.'
        $midRun = $status
        $liveObservation = (& $observer -OutputDirectory $holdRun) | ConvertFrom-Json
        Assert-True ($liveObservation.state -eq 'handles_live' -and $liveObservation.test_acceptance -eq 'not_evaluated') 'Native process identities were not verified.'
    }
    Assert-True ($status.state -ne 'supervisor_failed') 'Supervisor failed; inspect its logs.'
    if ($status.state -eq 'exited') { break }
    Start-Sleep -Milliseconds 100
} while ([DateTime]::UtcNow -lt $deadline)
Assert-True ($null -ne $midRun) 'No live heartbeat after launching shell exit.'
Assert-True ($status.state -eq 'exited' -and $status.exit_code -eq 0) 'Hold fixture did not exit successfully.'
$lines = @(Get-Content -LiteralPath (Join-Path $holdRun 'wsl.stdout.log'))
Assert-True (@($lines | Where-Object { $_ -like 'heartbeat=*' }).Count -eq $HoldSeconds) 'Heartbeat count mismatch.'
Assert-True ($lines[-1] -eq "completed=$HoldSeconds") 'Final log not complete at terminal status.'
$failed = Read-Status $burstRun
Assert-True ($failed.state -eq 'exited' -and $failed.exit_code -eq 31) 'Fast nonzero exit was lost.'
$terminalHash = (Get-FileHash -LiteralPath (Join-Path $holdRun 'status.json')).Hash
$terminalObservation = (& $observer -OutputDirectory $holdRun) | ConvertFrom-Json
$failedObservation = (& $observer -OutputDirectory $burstRun) | ConvertFrom-Json
Assert-True ($terminalObservation.state -eq 'exited_recorded' -and $terminalObservation.exit_code -eq 0 -and $terminalObservation.test_acceptance -eq 'not_evaluated') 'Observer inferred acceptance from zero exit.'
Assert-True ($failedObservation.state -eq 'exited_recorded' -and $failedObservation.exit_code -eq 31) 'Observer lost nonzero exit.'
Assert-True ($terminalHash -eq (Get-FileHash -LiteralPath (Join-Path $holdRun 'status.json')).Hash) 'Observer modified terminal status.'
$stdoutBytes = (Get-Item -LiteralPath (Join-Path $burstRun 'wsl.stdout.log')).Length
$stderrBytes = (Get-Item -LiteralPath (Join-Path $burstRun 'wsl.stderr.log')).Length
Assert-True ($stdoutBytes -eq 2097152 -and $stderrBytes -eq 2097152) 'Concurrent pipe output was truncated.'
$originalHash = (Get-FileHash -LiteralPath (Join-Path $holdRun 'manifest.json')).Hash
$rejected = $false
try { & $launcher -Distribution $Distribution -LinuxScript '/unused.sh' -OutputDirectory $holdRun | Out-Null } catch { $rejected = $true }
Assert-True ($rejected -and $originalHash -eq (Get-FileHash -LiteralPath (Join-Path $holdRun 'manifest.json')).Hash) 'Existing run was overwritten.'
Assert-True ($status.test_acceptance -eq 'not_evaluated' -and $failed.test_acceptance -eq 'not_evaluated') 'Launcher declared acceptance.'
$result = [ordered]@{
    format = 'myprobe.wsl-soak-regression.v1'
    supervisor_sha256 = (Get-FileHash -LiteralPath $launcher).Hash.ToLowerInvariant()
    hold_seconds = $HoldSeconds; heartbeat_count = $HoldSeconds
    launching_shells_exited = $true; mid_run = $midRun; final_status = $status
    nonzero_status = $failed; stdout_bytes = $stdoutBytes; stderr_bytes = $stderrBytes
    existing_directory_rejected = $true; passed = $true
    live_observation = $liveObservation; terminal_observation = $terminalObservation; failed_observation = $failedObservation
}
[IO.File]::WriteAllText((Join-Path $directory 'result.json'), ($result | ConvertTo-Json -Depth 8), $utf8)
$result | ConvertTo-Json -Depth 8

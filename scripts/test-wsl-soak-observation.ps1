# Deterministic native PowerShell regression; no WSL or process mutation.
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'lib/wsl-soak-observation.ps1')
$now = [DateTime]::Parse('2026-10-17T00:00:00Z').ToUniversalTime()
$began = $now.AddMinutes(-10)
$manifest = [pscustomobject]@{ format = 'myprobe.wsl-soak-launch.v1'; run_id = ('a' * 32); launched_at = $began.ToString('o') }
function New-Status {
    [pscustomobject]@{ format = 'myprobe.wsl-soak-status.v1'; run_id = ('a' * 32); state = 'running'; started_at = $began.ToString('o'); observed_at = $now.AddSeconds(-5).ToString('o'); supervisor_pid = 100; wsl_pid = 101; supervisor_process_started_at = $began.AddSeconds(-1).ToString('o'); wsl_process_started_at = $began.AddSeconds(1).ToString('o'); ended_at = $null; exit_code = $null }
}
function New-Lookup([int]$Identifier, [string]$Name, [DateTime]$Created, [int]$Parent = 100) {
    [pscustomobject]@{ status = 'observed'; process = [pscustomobject]@{ ProcessId = $Identifier; Name = $Name; CreationDate = $Created; ParentProcessId = $Parent } }
}
$supervisor = New-Lookup 100 'powershell.exe' $began.AddSeconds(-1) 90
$client = New-Lookup 101 'wsl.exe' $began.AddSeconds(1)
$missing = [pscustomobject]@{ status = 'missing' }
$unavailable = [pscustomobject]@{ status = 'unavailable' }
$checks = 0
function Assert-State($Status, $Supervisor, $Client, $Boot, [string]$State, [string]$Reason) {
    $actual = Get-WSLSoakObservation $manifest $Status $Supervisor $Client $Boot $now
    if ($actual.state -ne $State -or $actual.reason -ne $Reason -or $actual.test_acceptance -ne 'not_evaluated') { throw ($actual | ConvertTo-Json) }
    $script:checks++
}
Assert-State (New-Status) $supervisor $client $began.AddHours(-1) 'handles_live' 'process_identity_and_parent_verified'
$record = New-Status; $record.observed_at = $now.AddSeconds(-31).ToString('o')
Assert-State $record $supervisor $client $null 'handles_live_observation_stale' 'heartbeat_stale'
Assert-State (New-Status) $missing $missing $began.AddMinutes(1) 'interrupted' 'host_restarted_since_run'
Assert-State (New-Status) $missing $missing $null 'handles_missing' 'recorded_running_is_not_live'
Assert-State (New-Status) $missing $client $null 'partial_handles' 'linux_result_unknown'
Assert-State (New-Status) $supervisor $missing $null 'partial_handles' 'linux_result_unknown'
Assert-State (New-Status) $unavailable $client $null 'observation_unavailable' 'process_identity_unavailable'
$reused = New-Lookup 100 'powershell.exe' $now.AddSeconds(-1) 90
Assert-State (New-Status) $reused $client $null 'observation_unavailable' 'recorded_pid_identity_changed'
$other = New-Lookup 100 'unrelated.exe' $began.AddSeconds(-1) 90
Assert-State (New-Status) $other $client $null 'observation_unavailable' 'recorded_pid_identity_changed'
$otherParent = New-Lookup 101 'wsl.exe' $began.AddSeconds(1) 999
Assert-State (New-Status) $supervisor $otherParent $null 'observation_unavailable' 'client_parent_mismatch'
$legacy = New-Status; $legacy.PSObject.Properties.Remove('supervisor_process_started_at')
Assert-State $legacy $supervisor $client $null 'observation_unavailable' 'process_identity_unavailable'
$record = New-Status; $record.observed_at = $now.AddSeconds(6).ToString('o')
Assert-State $record $supervisor $client $null 'observation_unavailable' 'clock_inconsistent'
Assert-State (New-Status) $supervisor $client $now.AddSeconds(6) 'observation_unavailable' 'clock_inconsistent'
$record = New-Status; $record.observed_at = $began.AddSeconds(-1).ToString('o')
Assert-State $record $supervisor $client $null 'observation_unavailable' 'clock_inconsistent'
$record = New-Status; $record.observed_at = $now.AddSeconds(-30).ToString('o')
Assert-State $record $supervisor $client $null 'handles_live' 'process_identity_and_parent_verified'
$record = New-Status; $record.state = 'exited'; $record.exit_code = 0; $record.ended_at = $now.AddSeconds(-1).ToString('o')
Assert-State $record $missing $missing $now.AddSeconds(-1) 'exited_recorded' 'recorded_client_exit_only'
$record.exit_code = 31
Assert-State $record $missing $missing $null 'exited_recorded' 'recorded_client_exit_only'
foreach ($kind in @('mismatched_run','invalid_pid','invalid_exit','invalid_time','invalid_start')) {
    $record = New-Status
    if ($kind -eq 'mismatched_run') { $record.run_id = 'b' * 32 }
    if ($kind -eq 'invalid_time') { $record.observed_at = 'not-a-time' }
    if ($kind -eq 'invalid_start') { $record.started_at = 'not-a-time' }
    if ($kind -eq 'invalid_exit') { $record.state = 'exited'; $record.exit_code = $null }
    if ($kind -eq 'invalid_pid') {
        $record.supervisor_pid = 'not-a-process'
        Assert-State $record $supervisor $client $null 'observation_unavailable' 'process_identity_unavailable'
        continue
    }
    $rejected = $false
    try { Get-WSLSoakObservation $manifest $record $supervisor $client $null $now | Out-Null } catch { $rejected = $true }
    if (-not $rejected) { throw "Accepted $kind" }
    $checks++
}
$scratch = Join-Path ([IO.Path]::GetTempPath()) ('myprobe-soak-observer-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $scratch
$manifestFile = Join-Path $scratch 'manifest.json'
$statusFile = Join-Path $scratch 'status.json'
$utf8 = New-Object Text.UTF8Encoding($false)
try {
    [IO.File]::WriteAllText($manifestFile, ($manifest | ConvertTo-Json), $utf8)
    foreach ($badRecord in @('{', (' ' * 65537))) {
        [IO.File]::WriteAllText($statusFile, $badRecord, $utf8)
        $before = (Get-FileHash -LiteralPath $statusFile).Hash
        $rejected = $false
        try { & (Join-Path $PSScriptRoot 'observe-wsl-soak.ps1') -OutputDirectory $scratch | Out-Null } catch { $rejected = $true }
        if (-not $rejected -or $before -ne (Get-FileHash -LiteralPath $statusFile).Hash) { throw 'CLI accepted or changed invalid record.' }
        $checks++
    }
} finally {
    # Only the two explicitly created files, then an empty directory; no recursive
    # deletion, path enumeration or modification of a real run is involved.
    Remove-Item -LiteralPath $statusFile -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $manifestFile -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $scratch -ErrorAction SilentlyContinue
}
[pscustomobject]@{ format = 'myprobe.wsl-soak-observation-regression.v1'; checks = $checks; passed = $true; wsl_started = $false; run_modified = $false } | ConvertTo-Json

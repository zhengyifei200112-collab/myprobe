# Pure evaluation only. Never start/stop WSL, mutate a run or infer test success.
function Get-SoakValue($Record, [string]$Name) {
    if ($null -eq $Record) { return $null }
    $property = $Record.PSObject.Properties[$Name]
    if ($null -ne $property) { return $property.Value }
    return $null
}
function ConvertTo-SoakTime($Value) {
    if ($null -eq $Value -or [string]::IsNullOrWhiteSpace([string]$Value)) { return $null }
    if ($Value -is [DateTime]) { return $Value.ToUniversalTime() }
    try {
        if ([string]$Value -notmatch '(Z|[+-]\d{2}:\d{2})$') { return $null }
        return ([DateTimeOffset]::Parse([string]$Value, [Globalization.CultureInfo]::InvariantCulture)).UtcDateTime
    } catch { return $null }
}
function Get-SoakProcessIdentity($Lookup, $ExpectedID, $ExpectedStart, [string]$ExpectedName) {
    if ($null -eq $ExpectedID) { return 'not_recorded' }
    if ([string]$ExpectedID -notmatch '^[1-9][0-9]{0,9}$' -or [long]$ExpectedID -gt [int]::MaxValue) { return 'identity_invalid' }
    if ($null -eq $Lookup -or (Get-SoakValue $Lookup 'status') -eq 'unavailable') { return 'lookup_unavailable' }
    if ((Get-SoakValue $Lookup 'status') -eq 'missing') { return 'missing' }
    if ((Get-SoakValue $Lookup 'status') -ne 'observed') { return 'lookup_unavailable' }
    $process = Get-SoakValue $Lookup 'process'
    $created = ConvertTo-SoakTime (Get-SoakValue $process 'CreationDate')
    $expected = ConvertTo-SoakTime $ExpectedStart
    if ($null -eq $created -or $null -eq $expected) { return 'identity_unavailable' }
    if ((Get-SoakValue $process 'ProcessId') -ne $ExpectedID -or (Get-SoakValue $process 'Name') -ne $ExpectedName -or [Math]::Abs(($created - $expected).TotalMilliseconds) -gt 1) { return 'identity_mismatch' }
    return 'verified'
}
function Get-WSLSoakObservation($Manifest, $Status, $SupervisorLookup, $ClientLookup, $BootTime, [DateTime]$Now = [DateTime]::UtcNow) {
    $runID = Get-SoakValue $Manifest 'run_id'
    $recordedState = Get-SoakValue $Status 'state'
    $recordedStart = Get-SoakValue $Status 'started_at'
    $started = ConvertTo-SoakTime $recordedStart
    if ($null -eq $recordedStart) { $started = ConvertTo-SoakTime (Get-SoakValue $Manifest 'launched_at') }
    $observed = ConvertTo-SoakTime (Get-SoakValue $Status 'observed_at')
    if ((Get-SoakValue $Manifest 'format') -ne 'myprobe.wsl-soak-launch.v1' -or (Get-SoakValue $Status 'format') -ne 'myprobe.wsl-soak-status.v1' -or [string]$runID -notmatch '^[a-f0-9]{32}$' -or $runID -ne (Get-SoakValue $Status 'run_id') -or $null -eq $started -or $null -eq $observed -or $recordedState -notin @('launching','starting','running','exited','supervisor_failed')) { throw 'Invalid or inconsistent soak records.' }
    $supervisor = Get-SoakProcessIdentity $SupervisorLookup (Get-SoakValue $Status 'supervisor_pid') (Get-SoakValue $Status 'supervisor_process_started_at') 'powershell.exe'
    $client = Get-SoakProcessIdentity $ClientLookup (Get-SoakValue $Status 'wsl_pid') (Get-SoakValue $Status 'wsl_process_started_at') 'wsl.exe'
    $boot = ConvertTo-SoakTime $BootTime
    $result = [ordered]@{
        format = 'myprobe.wsl-soak-observation.v1'; run_id = $runID
        observed_at = $Now.ToUniversalTime().ToString('o'); recorded_state = $recordedState
        recorded_observed_at = $observed.ToString('o')
        state = 'observation_unavailable'; reason = 'process_identity_unavailable'
        supervisor = $supervisor; wsl_client = $client
        host_restart_observed = ($null -ne $boot -and $boot -gt $started)
        exit_code = $null; test_acceptance = 'not_evaluated'
    }
    if ($recordedState -eq 'exited') {
        $ended = ConvertTo-SoakTime (Get-SoakValue $Status 'ended_at')
        $code = Get-SoakValue $Status 'exit_code'
        if ($null -eq $ended -or $ended -lt $started -or $ended -gt $Now.AddSeconds(5) -or ($code -isnot [int] -and $code -isnot [long]) -or $code -lt [int]::MinValue -or $code -gt [int]::MaxValue) { throw 'Invalid terminal soak record.' }
        $result.state = 'exited_recorded'; $result.reason = 'recorded_client_exit_only'; $result.exit_code = $code
    } elseif ($recordedState -eq 'supervisor_failed') {
        $result.state = 'supervisor_failed'; $result.reason = 'linux_result_unknown'
    } elseif ($observed -gt $Now.AddSeconds(5) -or $started -gt $Now.AddSeconds(5) -or $observed -lt $started -or ($null -ne $boot -and $boot -gt $Now.AddSeconds(5))) {
        $result.host_restart_observed = $false; $result.reason = 'clock_inconsistent'
    } elseif ($result.host_restart_observed) {
        $result.state = 'interrupted'; $result.reason = 'host_restarted_since_run'
    } elseif ($supervisor -eq 'missing' -and $client -eq 'missing') {
        $result.state = 'handles_missing'; $result.reason = 'recorded_running_is_not_live'
    } elseif ($supervisor -eq 'missing' -or $client -eq 'missing') {
        $result.state = 'partial_handles'; $result.reason = 'linux_result_unknown'
    } elseif ($supervisor -eq 'identity_mismatch' -or $client -eq 'identity_mismatch') {
        $result.reason = 'recorded_pid_identity_changed'
    } elseif ($supervisor -eq 'verified' -and $client -eq 'verified') {
        $parent = Get-SoakValue (Get-SoakValue $ClientLookup 'process') 'ParentProcessId'
        if ($parent -ne (Get-SoakValue $Status 'supervisor_pid')) {
            $result.reason = 'client_parent_mismatch'
        } elseif ((ConvertTo-SoakTime (Get-SoakValue (Get-SoakValue $SupervisorLookup 'process') 'CreationDate')) -gt $Now.AddSeconds(5) -or (ConvertTo-SoakTime (Get-SoakValue (Get-SoakValue $ClientLookup 'process') 'CreationDate')) -gt $Now.AddSeconds(5)) {
            $result.reason = 'clock_inconsistent'
        } elseif (($Now - $observed).TotalSeconds -gt 30) {
            $result.state = 'handles_live_observation_stale'; $result.reason = 'heartbeat_stale'
        } else {
            $result.state = 'handles_live'; $result.reason = 'process_identity_and_parent_verified'
        }
    }
    return [pscustomobject]$result
}

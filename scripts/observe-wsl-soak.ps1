# Read-only native observation; never invokes wsl.exe or edits the run directory.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$OutputDirectory)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT' -or -not [IO.Path]::IsPathRooted($OutputDirectory)) { throw 'Windows and an absolute run directory are required.' }
. (Join-Path $PSScriptRoot 'lib/wsl-soak-observation.ps1')
function Read-SoakRecord([string]$Path) {
    for ($attempt = 0; $attempt -lt 4; $attempt++) {
        try {
            # Bound the allocation before reading. Status is atomically replaced;
            # retry only transient file access, never invalid/malformed JSON.
            $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete)
            try {
                if ($stream.Length -gt 65536) { throw 'Soak record exceeds 64 KiB.' }
                $buffer = New-Object byte[] 65537
                $length = 0
                while ($length -lt $buffer.Length) {
                    $read = $stream.Read($buffer, $length, $buffer.Length - $length)
                    if ($read -eq 0) { break }
                    $length += $read
                }
                if ($length -gt 65536) { throw 'Soak record exceeds 64 KiB.' }
                $raw = [Text.Encoding]::UTF8.GetString($buffer, 0, $length)
            } finally { $stream.Dispose() }
            break
        } catch [IO.IOException] {
            if ($attempt -eq 3) { throw }
            Start-Sleep -Milliseconds 25
        }
    }
    return ($raw | ConvertFrom-Json)
}
function Read-SoakProcess($Identifier) {
    if ($null -eq $Identifier -or [string]$Identifier -notmatch '^[1-9][0-9]{0,9}$' -or [long]$Identifier -gt [int]::MaxValue) { return $null }
    try {
        $process = Get-CimInstance Win32_Process -Filter ("ProcessId=" + [int]$Identifier) -ErrorAction Stop
        if ($null -eq $process) { return [pscustomobject]@{ status = 'missing' } }
        # Only these fields are evaluated; command lines/credentials never enter output.
        return [pscustomobject]@{ status = 'observed'; process = $process }
    } catch { return [pscustomobject]@{ status = 'unavailable' } }
}
$directory = [IO.Path]::GetFullPath($OutputDirectory)
$manifest = Read-SoakRecord (Join-Path $directory 'manifest.json')
$status = Read-SoakRecord (Join-Path $directory 'status.json')
$supervisor = Read-SoakProcess (Get-SoakValue $status 'supervisor_pid')
$client = Read-SoakProcess (Get-SoakValue $status 'wsl_pid')
$boot = $null
try { $boot = (Get-CimInstance Win32_OperatingSystem -ErrorAction Stop).LastBootUpTime } catch { }
Get-WSLSoakObservation $manifest $status $supervisor $client $boot | ConvertTo-Json -Depth 4

# Windows-only lifecycle supervision for a trusted, prewritten WSL test script.
# A live status is observation, never a declaration that the test passed.
[CmdletBinding(DefaultParameterSetName = 'Launch')]
param(
    [Parameter(Mandatory, ParameterSetName = 'Launch')]
    [ValidatePattern('^[\p{L}\p{N}_.-]{1,128}$')]
    [string]$Distribution,

    [Parameter(Mandatory, ParameterSetName = 'Launch')]
    [ValidatePattern('^/[^\x00\r\n]*$')]
    [string]$LinuxScript,

    [Parameter(Mandatory, ParameterSetName = 'Launch')]
    [string]$OutputDirectory,

    [Parameter(Mandatory, ParameterSetName = 'Worker')]
    [string]$RunManifest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ($env:OS -ne 'Windows_NT') { throw 'This launcher requires Windows and wsl.exe.' }
$utf8 = New-Object System.Text.UTF8Encoding($false)

function Write-Status([string]$Directory, [System.Collections.IDictionary]$Value) {
    $next = Join-Path $Directory 'status.next.json'
    $destination = Join-Path $Directory 'status.json'
    [IO.File]::WriteAllText($next, ($Value | ConvertTo-Json -Depth 8), $utf8)
    if ([IO.File]::Exists($destination)) {
        # Windows PowerShell 5.1 coerces $null to an empty string for a .NET
        # string parameter; NullString passes the required null backup path.
        [IO.File]::Replace($next, $destination, [System.Management.Automation.Language.NullString]::Value)
    } else {
        [IO.File]::Move($next, $destination)
    }
}

# Start-Process joins ArgumentList into one Windows command line. Quote each
# argument explicitly, including embedded quotes and trailing backslashes.
function Quote-WindowsArgument([string]$Value) {
    # wsl.exe parses its own options before the command; preserve bare options.
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') { return $Value }
    '"' + (($Value -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
}

if ($PSCmdlet.ParameterSetName -eq 'Launch') {
    if (-not [IO.Path]::IsPathRooted($OutputDirectory)) { throw 'OutputDirectory must be absolute.' }
    $directory = [IO.Path]::GetFullPath($OutputDirectory)
    # Refuse an existing run directory, including one left by an interrupted job.
    $null = New-Item -ItemType Directory -Path $directory -ErrorAction Stop
    $manifestPath = Join-Path $directory 'manifest.json'
    $frozenSupervisor = Join-Path $directory 'supervisor.ps1'
    Copy-Item -LiteralPath $PSCommandPath -Destination $frozenSupervisor
    $manifest = [ordered]@{
        format = 'myprobe.wsl-soak-launch.v1'
        run_id = [Guid]::NewGuid().ToString('N')
        launched_at = [DateTime]::UtcNow.ToString('o')
        launcher_pid = $PID
        distribution = $Distribution
        linux_script = $LinuxScript
        supervisor_sha256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    [IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json), $utf8)
    Write-Status $directory ([ordered]@{
        format = 'myprobe.wsl-soak-status.v1'; run_id = $manifest.run_id
        state = 'launching'; observed_at = [DateTime]::UtcNow.ToString('o')
        test_acceptance = 'not_evaluated'
    })
    $escapedScript = $frozenSupervisor.Replace("'", "''")
    $escapedManifest = $manifestPath.Replace("'", "''")
    $command = "& '$escapedScript' -RunManifest '$escapedManifest'"
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    try {
        # Windows Start-Process detaches from the launching shell. The worker
        # holds a foreground wsl.exe for the entire Linux job; no systemd-only hold.
        $windowsPowerShell = Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'
        $worker = Start-Process -FilePath $windowsPowerShell `
            -ArgumentList "-NoProfile -NonInteractive -OutputFormat Text -ExecutionPolicy Bypass -EncodedCommand $encoded" `
            -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput (Join-Path $directory 'supervisor.stdout.log') `
            -RedirectStandardError (Join-Path $directory 'supervisor.stderr.log')
    } catch {
        Write-Status $directory ([ordered]@{
            format = 'myprobe.wsl-soak-status.v1'; run_id = $manifest.run_id
            state = 'supervisor_failed'; observed_at = [DateTime]::UtcNow.ToString('o')
            failure = 'supervisor_start_failed'; test_acceptance = 'not_evaluated'
        })
        throw
    }
    [pscustomobject]@{ run_id = $manifest.run_id; supervisor_pid = $worker.Id; directory = $directory }
    return
}

$manifestPath = (Resolve-Path -LiteralPath $RunManifest).Path
$directory = Split-Path -Parent $manifestPath
$manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($manifest.format -ne 'myprobe.wsl-soak-launch.v1') { throw 'Unsupported run manifest.' }
$status = [ordered]@{
    format = 'myprobe.wsl-soak-status.v1'; run_id = $manifest.run_id
    state = 'starting'; supervisor_pid = $PID; wsl_pid = $null
    started_at = [DateTime]::UtcNow.ToString('o'); observed_at = [DateTime]::UtcNow.ToString('o')
    ended_at = $null; exit_code = $null; test_acceptance = 'not_evaluated'
}
$child = $null
$stdoutFile = $null
$stderrFile = $null
try {
    Write-Status $directory $status
    $arguments = @('--distribution', $manifest.distribution, '--user', 'root', '--exec', '/bin/sh', $manifest.linux_script)
    $commandLine = ($arguments | ForEach-Object { Quote-WindowsArgument $_ }) -join ' '
    # Own the process handle from Start, including a WSL client which fails
    # immediately. Windows PowerShell Start-Process can otherwise lose ExitCode.
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = Join-Path $env:SystemRoot 'System32/wsl.exe'
    $startInfo.Arguments = $commandLine
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $child = New-Object System.Diagnostics.Process
    $child.StartInfo = $startInfo
    $stdoutFile = [IO.FileStream]::new((Join-Path $directory 'wsl.stdout.log'), [IO.FileMode]::CreateNew,
        [IO.FileAccess]::Write, [IO.FileShare]::Read, 1, [IO.FileOptions]::Asynchronous)
    $stderrFile = [IO.FileStream]::new((Join-Path $directory 'wsl.stderr.log'), [IO.FileMode]::CreateNew,
        [IO.FileAccess]::Write, [IO.FileShare]::Read, 1, [IO.FileOptions]::Asynchronous)
    if (-not $child.Start()) { throw 'WSL client did not start.' }
    # Copy both pipes concurrently without PowerShell event callbacks/runspaces,
    # preventing a full stdout or stderr pipe from blocking the Linux process.
    $stdoutCopy = $child.StandardOutput.BaseStream.CopyToAsync($stdoutFile)
    $stderrCopy = $child.StandardError.BaseStream.CopyToAsync($stderrFile)
    $status.wsl_pid = $child.Id
    $status.state = 'running'
    do {
        $status.observed_at = [DateTime]::UtcNow.ToString('o')
        Write-Status $directory $status
    } while (-not $child.WaitForExit(5000))
    # WaitForExit without a timeout completes redirected stream handling too.
    $child.WaitForExit()
    $child.Refresh()
    $status.exit_code = $child.ExitCode
    $stdoutCopy.GetAwaiter().GetResult()
    $stderrCopy.GetAwaiter().GetResult()
    $stdoutFile.Flush()
    $stderrFile.Flush()
    $status.state = 'exited'
} catch {
    # Do not restart, kill other WSL instances, or hide an unknown Linux outcome.
    $status.state = 'supervisor_failed'
    $status['failure'] = 'supervision_error'
    throw
} finally {
    $status.observed_at = [DateTime]::UtcNow.ToString('o')
    $status.ended_at = $status.observed_at
    Write-Status $directory $status
    if ($null -ne $child) { $child.Dispose() }
    if ($null -ne $stdoutFile) { $stdoutFile.Dispose() }
    if ($null -ne $stderrFile) { $stderrFile.Dispose() }
}

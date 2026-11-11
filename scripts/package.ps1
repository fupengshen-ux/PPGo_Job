[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$')]
    [string]$Version,

    [ValidateNotNullOrEmpty()]
    [string]$Ref = 'HEAD'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-Checked {
    param([string]$File, [string[]]$Arguments)
    # Native stderr (for example Go download progress) is not an exit status.
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $File @Arguments
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousPreference
    }
    if ($exitCode -ne 0) { throw "Command failed ($exitCode): $File" }
}

function Write-Utf8 {
    param([string]$Path, [string]$Text)
    [IO.File]::WriteAllText($Path, $Text.Replace("`r`n", "`n"), (New-Object Text.UTF8Encoding($false)))
}

function Assert-Directory {
    param([string]$Path)
    if (Test-Path -LiteralPath $Path) {
        $item = Get-Item -LiteralPath $Path -Force
        if (!$item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "Expected a normal directory, not a file or reparse point: $Path"
        }
    }
}

function Assert-ChildPath {
    param([string]$Path, [string]$Parent)
    $absolute = [IO.Path]::GetFullPath($Path)
    $prefix = [IO.Path]::GetFullPath($Parent).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (!$absolute.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path escaped its intended parent: $Path"
    }
}

function Assert-LinuxBinary {
    param([string]$Path, [string]$Go)
    $stream = [IO.File]::OpenRead($Path)
    try {
        $header = New-Object byte[] 20
        if ($stream.Read($header, 0, 20) -ne 20 -or
            $header[0] -ne 0x7f -or $header[1] -ne 0x45 -or $header[2] -ne 0x4c -or $header[3] -ne 0x46 -or
            $header[4] -ne 2 -or $header[5] -ne 1 -or [BitConverter]::ToUInt16($header, 18) -ne 62) {
            throw "Expected an ELF64 amd64 binary: $Path"
        }
    } finally { $stream.Dispose() }
    $info = (Invoke-Checked -File $Go -Arguments @('version', '-m', $Path)) -join "`n"
    foreach ($setting in @('GOOS=linux', 'GOARCH=amd64', 'CGO_ENABLED=0')) {
        if (!$info.Contains($setting)) { throw "Missing binary build setting: $setting" }
    }
}

$repoRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$git = (Get-Command git -CommandType Application -ErrorAction Stop).Source
$go = (Get-Command go -CommandType Application -ErrorAction Stop).Source
$tar = (Get-Command tar -CommandType Application -ErrorAction Stop).Source
$gitRoot = ((Invoke-Checked -File $git -Arguments @('-C', $repoRoot, 'rev-parse', '--show-toplevel')) -join '').Trim()
if ([IO.Path]::GetFullPath($gitRoot) -ne $repoRoot) { throw 'Run the packaging script from its original repository.' }
$commit = ((Invoke-Checked -File $git -Arguments @('-C', $repoRoot, 'rev-parse', '--verify', '--end-of-options', ($Ref + '^{commit}'))) -join '').Trim()
if ($commit -notmatch '^[0-9a-f]{40}$') { throw 'The source ref did not resolve to a commit.' }
$goVersion = ((Invoke-Checked -File $go -Arguments @('version')) -join '').Trim()
if ($goVersion -notmatch 'go([0-9]+)\.([0-9]+)' -or [int]$matches[1] -lt 1 -or
    ([int]$matches[1] -eq 1 -and [int]$matches[2] -lt 22)) { throw 'Go 1.22 or newer is required.' }

$outputRoot = Join-Path $repoRoot 'PPGo_Job-package'
$releaseDir = Join-Path $outputRoot $Version
$buildRoot = Join-Path $repoRoot 'PPGo_Job-build'
Assert-Directory $outputRoot
Assert-Directory $buildRoot
if (Test-Path -LiteralPath $releaseDir) { throw "Release already exists; refusing to overwrite: $releaseDir" }
Assert-ChildPath $releaseDir $outputRoot

$workspace = Join-Path $buildRoot ('package-' + [Guid]::NewGuid().ToString('N'))
$createdWorkspace = $false
$savedEnvironment = @{}
foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

try {
    New-Item -ItemType Directory -Path $workspace -ErrorAction Stop | Out-Null
    $createdWorkspace = $true
    $source = Join-Path $workspace 'source'
    $payloads = Join-Path $workspace 'payloads'
    $publish = Join-Path $workspace 'publish'
    New-Item -ItemType Directory -Path $source, $payloads, $publish | Out-Null
    Write-Host "Source: $commit (uncommitted working-tree changes are excluded)"
    Write-Host "Target: linux/amd64; version: $Version"

    # Reject Git links/submodules before exporting to avoid traversing outside
    # the disposable source directory while copying runtime assets.
    $tree = @(Invoke-Checked -File $git -Arguments @('-C', $repoRoot, 'ls-tree', '-r', $commit))
    if ($tree | Where-Object { $_ -match '^(120000|160000) ' }) { throw 'This packager requires regular tracked files, without symlinks or submodules.' }
    $sourceTar = Join-Path $workspace 'source.tar'
    Invoke-Checked -File $git -Arguments @('-C', $repoRoot, 'archive', '--format=tar', '-o', $sourceTar, $commit)
    Invoke-Checked -File $tar -Arguments @('-xf', $sourceTar, '-C', $source)
    $migrations = @('20261109_task_search.sql', '20261110_server_monitor.sql')
    $documents = @('task-search.md', 'server-monitor.md')
    $required = @('go.mod', 'go.sum', 'main.go', 'agent/main.go', 'views', 'static', 'LICENSE') +
        @($migrations | ForEach-Object { 'migrations/' + $_ }) + @($documents | ForEach-Object { 'docs/' + $_ })
    foreach ($entry in $required) {
        if (!(Test-Path -LiteralPath (Join-Path $source $entry))) { throw "Selected commit lacks required upgrade content: $entry" }
    }

    $controllerExample = @'
# EXAMPLE ONLY. Keep the existing conf/app.conf when upgrading.
AppName = PPGo_Job2
HTTPPort = 8080
RunMode = prod
SessionOn = true
version = __VERSION__
jobs.pool = 1000
site.name = PPGo Job
notify.type = 0

# Use the existing database. This package does not initialize an empty database.
db.host = 127.0.0.1
db.user = CHANGE_ME
db.password = CHANGE_ME
db.port = 3306
db.name = CHANGE_ME
db.prefix = pp_
db.timezone = Asia/Shanghai

# Configure and validate mail separately; resource alerts are disabled by default.
email.host =
email.port = 465
email.from =
email.user =
email.password =
email.pool = 10
msg.url =
msg.pool = 10
dingtalk.url =
dingtalk.pool = 10
wechat.url =
wechat.pool = 10
'@
    $agentExample = @'
# EXAMPLE ONLY. Keep the existing config/conf.ini when upgrading.
AppMode = prod
Version = __VERSION__
LogLevel = INFO
ServerName = auto
# Written by registration; never ship another Agent's instance ID.
ServerId = 0
TcpPort = 1564
TcpIp = auto
IpType = 0
GroupId = 1
# Replace CHANGE_ME with the controller address before using this example.
RegisterUrl = http://CHANGE_ME:8080/server/apisave
UpdateStatusUrl = http://CHANGE_ME:8080/server/apistatus
# Filesystem containing the task output directory, for example /data.
MonitorDiskPath = /
'@
    $upgrade = @'
# PPGo Job __VERSION__ - existing-installation upgrade only

Source commit: __COMMIT__
Target: Linux amd64 (uname -m: x86_64)

This is not an empty-database installer. No production configuration, private
keys, default administrator account, old initialization SQL, or test data is included.
Do not copy .example files over an existing configuration.

## Controller
1. Back up the existing database, binaries, configuration, and private keys.
   Use a maintenance window and the existing service manager to stop scheduling.
2. Apply migrations/20261109_task_search.sql if the search upgrade has not been
   applied, then migrations/20261110_server_monitor.sql. Both are rerunnable.
   For a non-default prefix set @ppgo_prefix in the SAME database connection.
   Do not import the old ppgo_job2.sql and do not use mysql --force.
3. Extract to a new directory, then replace PPGo_Job, views/, and static/ in the
   existing installation. Keep the existing conf/app.conf and private keys.
4. On Linux run: chmod 0755 PPGo_Job
   Restart using the existing service manager and working directory. Never run
   old and new controllers simultaneously. Update the display version in the
   existing configuration manually if desired.
5. Resource alerts default to disabled. Verify permissions, task controls,
   Agent metrics, and real SMTP recipients before explicitly enabling alerts.

## Agent
1. Wait for running work to finish and stop the specific Agent with its existing
   service manager. Keep config/conf.ini and its registration identity.
2. Replace ppgo_agent and run: chmod 0755 ppgo_agent
3. Optionally add MonitorDiskPath=/data to the EXISTING configuration, selecting
   the actual output filesystem. Without this setting the default is /.
4. Restart using the existing working directory/configuration. Verify one host
   first, then upgrade the remaining Agents. Keep the Agent port restricted to
   the controller. CPU usage needs two valid consecutive samples.

The controller archive contains migrations and full upgrade/monitoring docs.
Both programs are already compiled; the target hosts do not need Go.
Windows archive permission mapping varies: always apply the chmod step above.

## Verification and rollback
With both archives and SHA256SUMS in one directory run: sha256sum -c SHA256SUMS
Packaging checks do not replace live Linux Agent and real-email acceptance.
To roll back, stop the new process and restore the previous binaries and pages,
retaining the existing configuration and additive database fields/tables.
'@
    $upgrade = $upgrade.Replace('__VERSION__', $Version).Replace('__COMMIT__', $commit)
    $buildInfo = [ordered]@{
        version = $Version
        source_commit = $commit
        source_ref = $Ref
        target = 'linux/amd64'
        go_version = $goVersion
        cgo_enabled = $false
        build_flags = @('-trimpath', '-mod=readonly', '-buildvcs=false')
        built_at_utc = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
        package_kind = 'existing-installation-upgrade'
    } | ConvertTo-Json -Depth 3

    [Environment]::SetEnvironmentVariable('GOOS', 'linux', 'Process')
    [Environment]::SetEnvironmentVariable('GOARCH', 'amd64', 'Process')
    [Environment]::SetEnvironmentVariable('CGO_ENABLED', '0', 'Process')
    $checksums = @()
    foreach ($binaryName in @('PPGo_Job', 'ppgo_agent')) {
        $folderName = "$binaryName-$Version-linux-amd64"
        $bundle = Join-Path $payloads $folderName
        New-Item -ItemType Directory -Path $bundle | Out-Null
        $binary = Join-Path $bundle $binaryName
        $package = if ($binaryName -eq 'PPGo_Job') { '.' } else { './agent' }
        Push-Location -LiteralPath $source
        try {
            Invoke-Checked -File $go -Arguments @('build', '-trimpath', '-mod=readonly', '-buildvcs=false', '-o', $binary, $package)
        } finally { Pop-Location }
        Assert-LinuxBinary $binary $go
        Copy-Item -LiteralPath (Join-Path $source 'LICENSE') -Destination (Join-Path $bundle 'LICENSE')
        Write-Utf8 (Join-Path $bundle 'BUILDINFO.json') ($buildInfo + "`n")
        Write-Utf8 (Join-Path $bundle 'UPGRADE.md') ($upgrade + "`n")

        if ($binaryName -eq 'PPGo_Job') {
            New-Item -ItemType Directory -Path (Join-Path $bundle 'conf'), (Join-Path $bundle 'migrations'), (Join-Path $bundle 'docs') | Out-Null
            Write-Utf8 (Join-Path $bundle 'conf/app.conf.example') ($controllerExample.Replace('__VERSION__', $Version) + "`n")
            foreach ($name in @('views', 'static')) { Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $bundle $name) -Recurse }
            foreach ($name in $migrations) { Copy-Item -LiteralPath (Join-Path $source ('migrations/' + $name)) -Destination (Join-Path $bundle ('migrations/' + $name)) }
            foreach ($name in $documents) { Copy-Item -LiteralPath (Join-Path $source ('docs/' + $name)) -Destination (Join-Path $bundle ('docs/' + $name)) }
        } else {
            New-Item -ItemType Directory -Path (Join-Path $bundle 'config') | Out-Null
            Write-Utf8 (Join-Path $bundle 'config/conf.ini.example') ($agentExample.Replace('__VERSION__', $Version) + "`n")
        }

        $files = @(Get-ChildItem -LiteralPath $bundle -Recurse -File)
        if (Get-ChildItem -LiteralPath $bundle -Recurse -Force | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }) {
            throw 'A package contains a reparse point.'
        }
        foreach ($file in $files) {
            $relative = $file.FullName.Substring($bundle.Length + 1).Replace('\', '/')
            if ($relative -match '(^|/)(app\.conf|conf\.ini|ppgo_job2\.sql|access\.txt|source-demo\.sql)$|(^|/)(tests|tmp|\.git)(/|$)|\.(exe|log|pem|key|pfx)$') {
                throw "Unexpected runtime or sensitive file: $relative"
            }
        }

        $archiveName = $folderName + '.tar.gz'
        $archive = Join-Path $publish $archiveName
        Invoke-Checked -File $tar -Arguments @('-czf', $archive, '-C', $payloads, $folderName)
        $entries = @(Invoke-Checked -File $tar -Arguments @('-tzf', $archive))
        foreach ($entry in $entries) {
            $normal = $entry.Replace('\', '/')
            if (!$normal.StartsWith($folderName + '/', [StringComparison]::Ordinal) -or $normal -match '(^|/)\.\.(/|$)') {
                throw "Unexpected archive member: $entry"
            }
        }
        $verify = Join-Path $workspace ('verify-' + $binaryName)
        New-Item -ItemType Directory -Path $verify | Out-Null
        Invoke-Checked -File $tar -Arguments @('-xzf', $archive, '-C', $verify)
        $extracted = Join-Path $verify $folderName
        if (@(Get-ChildItem -LiteralPath $extracted -Recurse -File).Count -ne $files.Count) { throw 'Archive file count mismatch.' }
        foreach ($file in $files) {
            $relative = $file.FullName.Substring($bundle.Length + 1)
            $copy = Join-Path $extracted $relative
            if (!(Test-Path -LiteralPath $copy -PathType Leaf) -or (Get-FileHash -LiteralPath $file.FullName).Hash -ne (Get-FileHash -LiteralPath $copy).Hash) {
                throw "Archive extraction mismatch: $relative"
            }
        }
        $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
        $checksums += $digest + '  ' + $archiveName
        Write-Host "Verified: $archiveName ($($files.Count) files)"
    }

    Write-Utf8 (Join-Path $publish 'SHA256SUMS') (($checksums -join "`n") + "`n")
    foreach ($line in [IO.File]::ReadAllLines((Join-Path $publish 'SHA256SUMS'))) {
        if ($line -notmatch '^([0-9a-f]{64})  ([A-Za-z0-9_.-]+\.tar\.gz)$') { throw 'Invalid checksum manifest.' }
        if ((Get-FileHash -LiteralPath (Join-Path $publish $matches[2])).Hash.ToLowerInvariant() -ne $matches[1]) { throw 'Checksum verification failed.' }
    }

    Assert-Directory $outputRoot
    if (!(Test-Path -LiteralPath $outputRoot)) { New-Item -ItemType Directory -Path $outputRoot | Out-Null }
    Assert-ChildPath (Resolve-Path -LiteralPath $publish).Path $workspace
    Assert-ChildPath $releaseDir (Resolve-Path -LiteralPath $outputRoot).Path
    # Directory.Move fails if the target appeared concurrently; no overwriting
    # and no partially populated version directory are permitted.
    [IO.Directory]::Move($publish, $releaseDir)
    Write-Host "Release packages ready: $releaseDir"
    Get-ChildItem -LiteralPath $releaseDir -File | Select-Object Name, Length
} finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
    if ($createdWorkspace -and (Test-Path -LiteralPath $workspace)) {
        Assert-Directory $buildRoot
        Assert-Directory $workspace
        $resolved = (Resolve-Path -LiteralPath $workspace).Path
        if ($resolved -ne [IO.Path]::GetFullPath($workspace)) { throw 'Unexpected cleanup target.' }
        Assert-ChildPath $resolved (Resolve-Path -LiteralPath $buildRoot).Path
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}

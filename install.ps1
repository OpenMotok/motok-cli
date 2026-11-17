[CmdletBinding()]
param(
    [string]$Version = $env:MOTOK_VERSION,
    [string]$BinDir = $env:MOTOK_BIN_DIR,
    [string]$Repo = $env:MOTOK_REPO,
    [string]$ReleaseBaseUrl = $env:MOTOK_RELEASE_BASE_URL,
    [string]$FromDir,
    [switch]$NoPathUpdate,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'
if ($Help) {
    Write-Output 'Install Motok CLI on Windows without administrator privileges.'
    Write-Output 'Usage: .\install.ps1 [-Version v0.1.0] [-BinDir DIRECTORY] [-FromDir DIRECTORY] [-NoPathUpdate]'
    Write-Output 'Downloads the matching GitHub Release and verifies SHA256SUMS before installation.'
    return
}
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on macOS or Linux.' }
if (-not $Version) { $Version = 'latest' }
if (-not $Repo) { $Repo = 'OpenMotok/motok-cli' }
if ($Repo -notmatch '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$') { throw 'Invalid repository; expected OWNER/NAME.' }
if (-not $BinDir) {
    if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is required.' }
    $BinDir = Join-Path $env:LOCALAPPDATA 'Programs\Motok\bin'
}
$BinDir = [IO.Path]::GetFullPath($BinDir)
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

function Get-InstallFile([string]$Url, [string]$Destination) {
    $uri = [Uri]$Url
    if ($uri.Scheme -ne 'https' -or $uri.UserInfo -or $uri.Fragment) { throw 'Download URLs must use HTTPS without credentials.' }
    Invoke-WebRequest -UseBasicParsing -Uri $uri -OutFile $Destination -Headers @{ 'User-Agent' = 'Motok-Installer' } | Out-Null
}

if ($Version -eq 'latest') {
    if ($FromDir -or $ReleaseBaseUrl) { throw 'Specify -Version when installing from a local directory or mirror.' }
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ 'User-Agent' = 'Motok-Installer' }
    $Version = $release.tag_name
}
if ($Version -notmatch '^[A-Za-z0-9._-]+$') { throw 'Invalid release version.' }
$architecture = $env:PROCESSOR_ARCHITEW6432
if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
if (-not $architecture) { throw 'Cannot determine the Windows CPU architecture.' }
switch ($architecture.ToUpperInvariant()) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { throw 'Only Windows AMD64 and ARM64 are supported.' }
}
$archive = "motok_${Version}_windows_${arch}.zip"
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('motok-install-' + [Guid]::NewGuid().ToString('N'))
$staged = $null
try {
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    $archivePath = Join-Path $tempDir $archive
    $manifestPath = Join-Path $tempDir 'SHA256SUMS'
    if ($FromDir) {
        Copy-Item -LiteralPath (Join-Path $FromDir $archive) -Destination $archivePath
        Copy-Item -LiteralPath (Join-Path $FromDir 'SHA256SUMS') -Destination $manifestPath
    } else {
        if (-not $ReleaseBaseUrl) { $ReleaseBaseUrl = "https://github.com/$Repo/releases/download/$Version" }
        $ReleaseBaseUrl = $ReleaseBaseUrl.TrimEnd('/')
        Get-InstallFile "$ReleaseBaseUrl/$archive" $archivePath
        Get-InstallFile "$ReleaseBaseUrl/SHA256SUMS" $manifestPath
    }
    $pattern = '^([A-Fa-f0-9]{64})\s+\*?' + [Regex]::Escape($archive) + '$'
    $checksums = @(Get-Content -LiteralPath $manifestPath | ForEach-Object {
        if ($_ -match $pattern) { $Matches[1] }
    })
    if ($checksums.Count -ne 1) { throw 'Missing or duplicate artifact checksum.' }
    $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash
    if ($actual -ine $checksums[0]) { throw 'Checksum mismatch; installation aborted.' }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($archivePath)
    try {
        if ($zip.Entries.Count -ne 1 -or $zip.Entries[0].FullName -cne 'motok.exe') { throw 'Invalid binary archive.' }
    } finally { $zip.Dispose() }
    $unpack = Join-Path $tempDir 'unpack'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $unpack
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    $staged = Join-Path $BinDir ('.motok-' + [Guid]::NewGuid().ToString('N') + '.exe')
    Copy-Item -LiteralPath (Join-Path $unpack 'motok.exe') -Destination $staged
    $destination = Join-Path $BinDir 'motok.exe'
    if (Test-Path -LiteralPath $destination) {
        [IO.File]::Replace($staged, $destination, [System.Management.Automation.Language.NullString]::Value)
    } else {
        [IO.File]::Move($staged, $destination)
    }
    $staged = $null
    if (-not $NoPathUpdate) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @($userPath -split ';' | Where-Object { $_ })
        if ($entries -inotcontains $BinDir) {
            [Environment]::SetEnvironmentVariable('Path', (($entries + $BinDir) -join ';'), 'User')
        }
        if (($env:Path -split ';') -inotcontains $BinDir) { $env:Path = "$BinDir;$env:Path" }
    }
    Write-Output "Installed motok $Version in $BinDir."
    Write-Output 'Next: motok init, then fill in your API URL, key and model IDs.'
} finally {
    if ($staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
    if (Test-Path -LiteralPath $tempDir) { Remove-Item -LiteralPath $tempDir -Recurse -Force }
}

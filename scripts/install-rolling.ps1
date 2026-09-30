# agentsview rolling-build installer for Windows
#
# Installs the newest build from the rolling "latest" prerelease that
# .github/workflows/rolling-release.yml publishes on every push to main.
# The workflow attaches this script to that release as install.ps1 with
# the repository filled in:
#
#   powershell -ExecutionPolicy ByPass -c "irm https://github.com/<owner>/<repo>/releases/download/latest/install.ps1 | iex"
#
# Environment overrides:
#   AGENTSVIEW_REPO            owner/repo that publishes the rolling release
#   AGENTSVIEW_INSTALL_DIR     install directory (default: %USERPROFILE%\.local\bin)
#   AGENTSVIEW_NO_MODIFY_PATH  set to 1 to leave the user PATH alone
#   AGENTSVIEW_SKIP_CHECKSUM   set to 1 to skip SHA256SUMS verification

$ErrorActionPreference = 'Stop'
# The progress bar makes Invoke-WebRequest very slow in Windows PowerShell 5.x.
$ProgressPreference = 'SilentlyContinue'

# The release workflow replaces the placeholder when it publishes this
# script. Running the unpublished copy requires AGENTSVIEW_REPO.
$defaultRepo = '@AGENTSVIEW_REPO@'
$releaseTag = 'latest'
$binaryName = 'agentsview.exe'
$assetName = 'agentsview-windows-amd64.exe'

function Write-Info($msg) { Write-Host $msg -ForegroundColor Green }
function Write-Warn($msg) { Write-Host $msg -ForegroundColor Yellow }
function Write-Err($msg) { Write-Host $msg -ForegroundColor Red }

function Test-EnvBool($name) {
    $val = [Environment]::GetEnvironmentVariable($name)
    return ($val -match '^(1|true|yes)$')
}

function Resolve-Repo {
    $repo = $env:AGENTSVIEW_REPO
    if (-not $repo) { $repo = $defaultRepo }
    if (-not $repo -or $repo.Contains('@')) {
        Write-Err "Error: Repository unknown. Set AGENTSVIEW_REPO=<owner>/<repo>."
        exit 1
    }
    return $repo
}

function Get-Architecture {
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
        return 'arm64'
    }
    if ([System.Environment]::Is64BitOperatingSystem) {
        return 'amd64'
    }
    return '386'
}

function Get-InstallDir {
    if ($env:AGENTSVIEW_INSTALL_DIR) {
        return $env:AGENTSVIEW_INSTALL_DIR
    }
    # Same place as ~/.local/bin on Linux.
    return Join-Path (Join-Path $env:USERPROFILE '.local') 'bin'
}

function Invoke-Download {
    param([string]$Uri, [string]$OutFile)

    $params = @{ Uri = $Uri; OutFile = $OutFile }
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        $params.UseBasicParsing = $true
    }
    Invoke-WebRequest @params
}

function Test-ChecksumMatch {
    param([string]$File, [string]$ChecksumFile, [string]$Name)

    $expected = @()
    foreach ($line in Get-Content $ChecksumFile) {
        $parts = $line.Trim() -split '\s+', 2
        if ($parts.Count -lt 2) { continue }
        $filename = $parts[1] -replace '^\*', ''
        if ($filename -eq $Name) { $expected += $parts[0] }
    }

    if ($expected.Count -ne 1) {
        Write-Err "Error: Expected one checksum for $Name in SHA256SUMS, found $($expected.Count)"
        exit 1
    }

    $actual = (Get-FileHash -Path $File -Algorithm SHA256).Hash
    if ($actual -ne $expected[0]) {
        Write-Err "Error: Checksum verification failed!"
        Write-Err "Expected: $($expected[0])"
        Write-Err "Got:      $actual"
        exit 1
    }
    Write-Info "Checksum verified"
}

function Test-PathListContains {
    param([string]$PathList, [string]$Dir)

    if (-not $PathList) { return $false }
    $normalized = $Dir.TrimEnd('\', '/')
    foreach ($entry in $PathList -split ';') {
        if ($entry -and ([Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\', '/') -ieq $normalized)) {
            return $true
        }
    }
    return $false
}

# Adds the directory to the user PATH. Returns $true when the change
# only reaches terminals opened afterwards.
function Update-UserPath {
    param([string]$Dir)

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (Test-PathListContains $userPath $Dir) {
        if (Test-PathListContains $env:Path $Dir) {
            Write-Info "$Dir is already in PATH"
            return $false
        }
        Write-Warn "$Dir is in your user PATH, but this terminal has not loaded it yet."
        $env:Path = "$env:Path;$Dir"
        return $true
    }

    if (Test-EnvBool 'AGENTSVIEW_NO_MODIFY_PATH') {
        Write-Warn "$Dir is not in your user PATH. Add it in System Properties > Environment Variables."
        return $false
    }

    if ($userPath) {
        $newPath = "$($userPath.TrimEnd(';'));$Dir"
    } else {
        $newPath = $Dir
    }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$env:Path;$Dir"
    Write-Info "Added $Dir to your user PATH"
    return $true
}

# Replaces the installed binary. Windows cannot delete a running .exe but
# can rename it, so a running copy is moved aside first.
function Install-Binary {
    param([string]$Source, [string]$Dest)

    if (Test-Path $Dest) {
        try {
            Remove-Item $Dest -Force
        } catch {
            $old = "$Dest.old"
            Remove-Item $old -Force -ErrorAction SilentlyContinue
            try {
                Move-Item $Dest $old -Force
            } catch {
                Write-Err "Error: Could not replace $Dest. Stop agentsview and try again."
                exit 1
            }
            Write-Warn "The running agentsview was moved to $old; restart it to use the new build."
        }
    }
    Move-Item $Source $Dest -Force
}

function Install-AgentsviewRolling {
    Write-Info "Installing the agentsview rolling build..."
    Write-Host ""

    $arch = Get-Architecture
    if ($arch -eq '386') {
        Write-Err "Error: 32-bit Windows is not supported."
        exit 1
    }
    if ($arch -eq 'arm64') {
        Write-Warn "Rolling builds are amd64 only; installing windows/amd64 (runs under emulation)."
    }

    $repo = Resolve-Repo
    $installDir = Get-InstallDir
    $baseUrl = "https://github.com/$repo/releases/download/$releaseTag"
    $destPath = Join-Path $installDir $binaryName

    Write-Info "Release: $repo ($releaseTag)"
    Write-Info "Install directory: $installDir"
    Write-Host ""

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "agentsview-install-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    try {
        $assetPath = Join-Path $tmpDir $assetName

        Write-Info "Downloading $assetName..."
        try {
            Invoke-Download -Uri "$baseUrl/$assetName" -OutFile $assetPath
        } catch {
            Write-Err "Error: Download failed: $_"
            Write-Err "Check https://github.com/$repo/releases/tag/$releaseTag"
            exit 1
        }

        if (Test-EnvBool 'AGENTSVIEW_SKIP_CHECKSUM') {
            Write-Warn "Checksum verification skipped (AGENTSVIEW_SKIP_CHECKSUM is set)"
        } else {
            $checksumFile = Join-Path $tmpDir 'SHA256SUMS'
            try {
                Invoke-Download -Uri "$baseUrl/SHA256SUMS" -OutFile $checksumFile
            } catch {
                Write-Err "Error: Could not download SHA256SUMS: $_"
                Write-Err "Set AGENTSVIEW_SKIP_CHECKSUM=1 to bypass verification (not recommended)"
                exit 1
            }
            Test-ChecksumMatch -File $assetPath -ChecksumFile $checksumFile -Name $assetName
        }

        if (-not (Test-Path $installDir)) {
            New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        }
        Install-Binary -Source $assetPath -Dest $destPath
        Write-Info "Installed $destPath"
        Write-Host ""

        $needsRestart = Update-UserPath -Dir $installDir

        $found = Get-Command 'agentsview' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($found -and ($found.Source -ne $destPath)) {
            Write-Warn "Another agentsview at $($found.Source) comes first in PATH and will run instead."
        }

        Write-Host ""
        Write-Info "Installation complete!"
        if ($needsRestart) {
            Write-Host ""
            Write-Warn "Open a new terminal so other windows pick up the PATH change."
        }
        Write-Host ""
        Write-Host "Get started:"
        Write-Host "  agentsview serve    # Start the server and open browser"
        Write-Host ""
        Write-Host "To update to the newest rolling build later:"
        Write-Host "  agentsview update"
    } finally {
        if (Test-Path $tmpDir) {
            Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

# Guard: dot-sourcing (as the tests do) only defines the functions.
if ($MyInvocation.InvocationName -ne '.') {
    Install-AgentsviewRolling
}

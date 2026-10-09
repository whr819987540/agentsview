# agentsview rolling-build installer for Windows
#
# Installs the newest build that .github/workflows/rolling-release.yml
# publishes on every push to main. The workflow attaches this script to
# the rolling "latest" prerelease as install.ps1 with the repository
# filled in:
#
#   powershell -ExecutionPolicy ByPass -c "irm https://github.com/<owner>/<repo>/releases/download/latest/install.ps1 | iex"
#
# "latest" is updated in place on every push, so the installer reads
# only its BUILD_TAG file there. SHA256SUMS and the binary come from the
# immutable build-YYYYMMDD-<sha> release that BUILD_TAG names, so a push
# during the install cannot mix files from two builds.
#
# Environment overrides:
#   AGENTSVIEW_REPO            owner/repo that publishes the rolling release
#   AGENTSVIEW_INSTALL_DIR     install directory (default: %USERPROFILE%\.local\bin,
#                              or %USERPROFILE%\.agentsview\bin when an earlier
#                              install is there and none is in .local\bin)
#   AGENTSVIEW_NO_MODIFY_PATH  set to 1 to leave the user PATH alone
#   AGENTSVIEW_SKIP_CHECKSUM   set to 1 to skip SHA256SUMS verification

$ErrorActionPreference = 'Stop'
# The progress bar makes Invoke-WebRequest very slow in Windows PowerShell 5.x.
$ProgressPreference = 'SilentlyContinue'

# The release workflow replaces the placeholder when it publishes this
# script. Running the unpublished copy requires AGENTSVIEW_REPO.
$defaultRepo = '@AGENTSVIEW_REPO@'
$latestTag = 'latest'
# Snapshot tags look like build-20260102-0123abcd. Used with -cmatch so
# uppercase hex is rejected; \z also rejects a trailing newline.
$buildTagPattern = '^build-[0-9]{8}-[0-9a-f]{7,40}\z'
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
    $dir = Join-Path (Join-Path $env:USERPROFILE '.local') 'bin'
    # The stable installer and earlier rolling installers used
    # %USERPROFILE%\.agentsview\bin. Update such an install where it is,
    # so the old copy there does not keep running.
    $legacyDir = Join-Path (Join-Path $env:USERPROFILE '.agentsview') 'bin'
    if ((Test-Path -LiteralPath (Join-Path $legacyDir $binaryName) -PathType Leaf) -and
        -not (Test-Path -LiteralPath (Join-Path $dir $binaryName))) {
        Write-Info "Found an existing install in $legacyDir; updating it in place."
        return $legacyDir
    }
    return $dir
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

# Returns the snapshot tag named by the rolling release's BUILD_TAG file.
# Exits when the file is missing or invalid, which can happen while the
# workflow is republishing "latest". There is deliberately no fallback
# to the assets on "latest".
function Resolve-BuildTag {
    param([string]$Repo, [string]$TmpDir)

    $uri = "https://github.com/$Repo/releases/download/$latestTag/BUILD_TAG"
    $retry = 'The rolling release may be mid-publish; try again in a few minutes.'
    $file = Join-Path $TmpDir 'BUILD_TAG'
    try {
        Invoke-Download -Uri $uri -OutFile $file | Out-Null
    } catch {
        Write-Err "Error: Could not read the current build from ${uri}: $_"
        Write-Err $retry
        exit 1
    }

    $tag = [string](Get-Content -LiteralPath $file -Raw)
    $tag = $tag.Trim()
    if ($tag -cnotmatch $buildTagPattern) {
        Write-Err "Error: BUILD_TAG on the rolling release does not name a build."
        Write-Err $retry
        exit 1
    }
    return $tag
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

# Adds the directory to the front of the user PATH, so it wins over
# other copies listed there. Returns $true when the change only reaches
# terminals opened afterwards.
function Update-UserPath {
    param([string]$Dir)

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (Test-PathListContains $userPath $Dir) {
        if (Test-PathListContains $env:Path $Dir) {
            Write-Info "$Dir is already in PATH"
            return $false
        }
        Write-Warn "$Dir is in your user PATH, but this terminal has not loaded it yet."
        $env:Path = "$Dir;$env:Path"
        return $true
    }

    if (Test-EnvBool 'AGENTSVIEW_NO_MODIFY_PATH') {
        Write-Warn "$Dir is not in your user PATH. Add it in System Properties > Environment Variables."
        return $false
    }

    if ($userPath) {
        $newPath = "$Dir;$($userPath.TrimStart(';'))"
    } else {
        $newPath = $Dir
    }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$Dir;$env:Path"
    Write-Info "Added $Dir to the front of your user PATH"
    return $true
}

# Returns the system (Machine) and user PATH values. New terminals use
# the system PATH followed by the user PATH.
function Get-PersistedPath {
    return @{
        Machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
        User = [Environment]::GetEnvironmentVariable('Path', 'User')
    }
}

# Returns the first agentsview command in a ;-separated PATH list,
# trying each PATHEXT extension in every folder the way Windows does.
function Find-AgentsviewOnPath {
    param([string]$PathList)

    if (-not $PathList) { return $null }
    $exts = $env:PATHEXT
    if (-not $exts) { $exts = '.COM;.EXE;.BAT;.CMD' }
    foreach ($entry in $PathList -split ';') {
        $folder = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"'))
        if (-not $folder) { continue }
        foreach ($ext in $exts -split ';') {
            if (-not $ext) { continue }
            try {
                $candidate = [System.IO.Path]::Combine($folder, 'agentsview' + $ext.ToLowerInvariant())
            } catch {
                # Invalid characters in a PATH entry; Windows skips it too.
                continue
            }
            if ([System.IO.File]::Exists($candidate)) { return $candidate }
        }
    }
    return $null
}

function Test-SamePath {
    param([string]$A, [string]$B)
    try {
        return ([System.IO.Path]::GetFullPath($A) -ieq [System.IO.Path]::GetFullPath($B))
    } catch {
        return ($A -ieq $B)
    }
}

# Warns when new terminals will run another agentsview instead of Dest,
# and says how to fix it. Returns $true in that case.
function Test-OtherCopyFirst {
    param([string]$Dest, [string]$InstallDir)

    $persisted = Get-PersistedPath
    $winner = Find-AgentsviewOnPath "$($persisted.Machine);$($persisted.User)"
    if ($winner -and -not (Test-SamePath $winner $Dest)) {
        $winnerDir = Split-Path -Parent $winner
        Write-Warn "Another agentsview at $winner comes first in PATH, so new terminals will run it instead of $Dest."
        if (Test-PathListContains $persisted.Machine $winnerDir) {
            Write-Warn "To fix it, delete or rename $winner, or remove $winnerDir from the system PATH. Both may need an administrator; the system PATH is searched before your user PATH."
        } else {
            Write-Warn "To fix it, delete or rename $winner, or move $InstallDir above $winnerDir in your user PATH (System Properties > Environment Variables)."
        }
        return $true
    }

    # New terminals will find Dest, but this one may still find another.
    $current = Find-AgentsviewOnPath $env:Path
    if ($winner -and $current -and -not (Test-SamePath $current $Dest)) {
        Write-Warn "This terminal still finds $current first; open a new terminal to use the new build."
    }
    return $false
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
    $destPath = Join-Path $installDir $binaryName

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "agentsview-install-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    try {
        Write-Info "Release: $repo ($latestTag)"
        $buildTag = Resolve-BuildTag -Repo $repo -TmpDir $tmpDir
        $baseUrl = "https://github.com/$repo/releases/download/$buildTag"
        Write-Info "Build: $buildTag"
        Write-Info "Install directory: $installDir"
        Write-Host ""

        $assetPath = Join-Path $tmpDir $assetName

        Write-Info "Downloading $assetName..."
        try {
            Invoke-Download -Uri "$baseUrl/$assetName" -OutFile $assetPath
        } catch {
            Write-Err "Error: Download failed: $_"
            Write-Err "Check https://github.com/$repo/releases/tag/$buildTag"
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
        Write-Info "Installed $destPath ($buildTag)"
        Write-Host ""

        $needsRestart = Update-UserPath -Dir $installDir
        $shadowed = Test-OtherCopyFirst -Dest $destPath -InstallDir $installDir

        Write-Host ""
        if ($shadowed) {
            Write-Warn "Installed $destPath, but new terminals will run the other agentsview until you fix PATH (see above)."
        } else {
            Write-Info "Installation complete!"
        }
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

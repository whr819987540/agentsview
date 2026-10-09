# Tests for install-rolling.ps1. Each case runs the real installer in a
# child PowerShell process with downloads served from a local fixture
# directory. Windows-only cases cover the user PATH and replacing a
# running binary.
$ErrorActionPreference = 'Stop'

$installer = Join-Path $PSScriptRoot 'install-rolling.ps1'
$pwshExe = (Get-Process -Id $PID).Path
$onWindows = [System.Environment]::OSVersion.Platform -eq 'Win32NT'

$script:pass = 0
$script:fail = 0

function Pass($desc) { Write-Host "  PASS: $desc"; $script:pass++ }
function Fail($desc, $detail) {
    Write-Host "  FAIL: $desc"
    if ($detail) { Write-Host "    $detail" }
    $script:fail++
}
function Assert-Eq($desc, $expected, $actual) {
    if ($expected -ceq $actual) { Pass $desc } else { Fail $desc "expected '$expected', got '$actual'" }
}
function Assert-Contains($desc, $needle, $haystack) {
    if ($haystack.Contains($needle)) { Pass $desc } else { Fail $desc "missing '$needle' in: $haystack" }
}
function Assert-NotContains($desc, $needle, $haystack) {
    if (-not $haystack.Contains($needle)) { Pass $desc } else { Fail $desc "unexpected '$needle' in: $haystack" }
}
function Assert-Missing($desc, $path) {
    if (-not (Test-Path $path)) { Pass $desc } else { Fail $desc "unexpected file $path" }
}
# Reads a file for an assertion; a missing file fails the assertion
# instead of stopping the run.
function Read-Text($path) {
    if (-not (Test-Path -LiteralPath $path)) { return "<missing $path>" }
    return (Get-Content -LiteralPath $path -Raw)
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) "agentsview-rolling-test-$(Get-Random)"
# One subdirectory per release tag, mirroring the download URL layout.
$releases = Join-Path $work 'releases'
$latest = Join-Path $releases 'latest'
New-Item -ItemType Directory -Path $latest -Force | Out-Null
$assetName = 'agentsview-windows-amd64.exe'
$baseUrl = 'https://github.com/example/agentsview/releases/download'
$build1 = 'build-20260101-1111aaaa'
$build2 = 'build-20260102-2222bbbb'

# Publishes a snapshot release whose binary holds the given text, then
# copies its assets to "latest" the way the release workflow does.
function Publish-Build($tag, $content) {
    $dir = Join-Path $releases $tag
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    $bin = Join-Path $dir $assetName
    Set-Content -Path $bin -Value $content -NoNewline
    $hash = (Get-FileHash -Path $bin -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path (Join-Path $dir 'SHA256SUMS') -Value "$hash  $assetName"
    Set-Content -Path (Join-Path $dir 'BUILD_TAG') -Value $tag
    Copy-Item -Path (Join-Path $dir '*') -Destination $latest -Force
}

function Get-RequestedUrls {
    $file = Join-Path $work 'urls'
    if (-not (Test-Path $file)) { return '' }
    return (@(Get-Content $file) -join "`n")
}

# The child dot-sources the installer, swaps in a fixture-backed
# download, and runs the real install function. TEST_MACHINE_PATH and
# TEST_USER_PATH stand in for the registry PATH values, which only
# exist on Windows.
$runner = @'
. $env:TEST_INSTALLER
function Invoke-Download {
    param([string]$Uri, [string]$OutFile)
    Add-Content -Path (Join-Path $env:TEST_WORK 'urls') -Value $Uri
    $marker = '/releases/download/'
    $src = Join-Path $env:TEST_RELEASES $Uri.Substring($Uri.IndexOf($marker) + $marker.Length)
    if (-not (Test-Path $src)) { throw "404 Not Found: $Uri" }
    Copy-Item $src $OutFile
}
if ($env:TEST_MACHINE_PATH -or $env:TEST_USER_PATH) {
    function Get-PersistedPath {
        return @{ Machine = $env:TEST_MACHINE_PATH; User = $env:TEST_USER_PATH }
    }
}
Install-AgentsviewRolling
'@

function Invoke-Installer {
    param([hashtable]$Vars = @{}, [string]$Script = $installer)

    # PowerShell names are case-insensitive, so this must not be $vars.
    $childEnv = @{
        TEST_INSTALLER = $Script
        TEST_WORK = $work
        TEST_RELEASES = $releases
        AGENTSVIEW_REPO = 'example/agentsview'
        AGENTSVIEW_INSTALL_DIR = $null
        AGENTSVIEW_NO_MODIFY_PATH = '1'
        AGENTSVIEW_SKIP_CHECKSUM = $null
        TEST_MACHINE_PATH = $null
        TEST_USER_PATH = $null
    }
    foreach ($key in $Vars.Keys) { $childEnv[$key] = $Vars[$key] }

    Remove-Item (Join-Path $work 'urls') -Force -ErrorAction SilentlyContinue
    $saved = @{}
    foreach ($key in $childEnv.Keys) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key)
        [Environment]::SetEnvironmentVariable($key, $childEnv[$key])
    }
    try {
        $output = & $pwshExe -NoProfile -NonInteractive -Command $runner 2>&1 | Out-String
        return [pscustomobject]@{ Status = $LASTEXITCODE; Output = $output }
    } finally {
        foreach ($key in $saved.Keys) {
            [Environment]::SetEnvironmentVariable($key, $saved[$key])
        }
    }
}

Write-Host "=== install-rolling.ps1 ==="

try {
    $dir = Join-Path $work 'bin'
    $dest = Join-Path $dir 'agentsview.exe'

    Publish-Build $build1 'build-1'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $dir }
    Assert-Eq 'install succeeds' 0 $r.Status
    Assert-Eq 'binary installed as agentsview.exe' 'build-1' (Read-Text $dest)
    Assert-Contains 'names the build being installed' "Build: $build1" $r.Output
    Assert-Eq 'reads BUILD_TAG from latest, then assets from the snapshot' `
        "$baseUrl/latest/BUILD_TAG`n$baseUrl/$build1/$assetName`n$baseUrl/$build1/SHA256SUMS" `
        (Get-RequestedUrls)
    Assert-Contains 'prints manual PATH guidance when opted out' 'is not in your user PATH' $r.Output

    Publish-Build $build2 'build-2'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $dir }
    Assert-Eq 'reinstall succeeds' 0 $r.Status
    Assert-Eq 'reinstall replaces binary' 'build-2' (Read-Text $dest)

    # A push can replace the assets on "latest" before BUILD_TAG moves.
    # The installer still installs a consistent build from the snapshot.
    Set-Content -Path (Join-Path $latest $assetName) -Value 'build-3' -NoNewline
    Set-Content -Path (Join-Path $latest 'SHA256SUMS') -Value 'not a checksum list'
    $midDir = Join-Path $work 'mid-publish'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $midDir }
    Assert-Eq 'install during a publish succeeds' 0 $r.Status
    Assert-Eq 'install during a publish uses the snapshot binary' 'build-2' `
        (Read-Text (Join-Path $midDir 'agentsview.exe'))
    Publish-Build $build2 'build-2'

    # While "latest" is being recreated its BUILD_TAG is missing. The
    # installer stops instead of falling back to other assets.
    $noTagDir = Join-Path $work 'no-build-tag'
    $heldTag = Join-Path $work 'held-build-tag'
    Move-Item (Join-Path $latest 'BUILD_TAG') $heldTag
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $noTagDir }
    Assert-Eq 'missing BUILD_TAG fails' 1 $r.Status
    Assert-Contains 'missing BUILD_TAG suggests retrying' 'may be mid-publish; try again' $r.Output
    Assert-Eq 'missing BUILD_TAG downloads nothing else' "$baseUrl/latest/BUILD_TAG" (Get-RequestedUrls)
    Assert-Missing 'no binary without BUILD_TAG' (Join-Path $noTagDir 'agentsview.exe')
    Move-Item $heldTag (Join-Path $latest 'BUILD_TAG')

    # BUILD_TAG must name a snapshot release.
    $badTagDir = Join-Path $work 'bad-build-tag'
    $badTags = @(
        ''
        'latest'
        'v0.31.0'
        'build-2026010-1111aaaa'
        'build-20260101-111aaa'
        'build-20260101-1111AAAA'
        'build-20260101-1111aaaa extra'
        ('build-20260101-' + ('a' * 41))
        "build-20260101-1111aaaa`nbuild-20260102-2222bbbb"
        '../build-20260101-1111aaaa'
        '<html>Not Found</html>'
    )
    foreach ($bad in $badTags) {
        Set-Content -Path (Join-Path $latest 'BUILD_TAG') -Value $bad
        $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $badTagDir }
        Assert-Eq "BUILD_TAG '$bad' is rejected" 1 $r.Status
        Assert-Contains "BUILD_TAG '$bad' suggests retrying" 'may be mid-publish; try again' $r.Output
        Assert-Eq "BUILD_TAG '$bad' downloads nothing else" "$baseUrl/latest/BUILD_TAG" (Get-RequestedUrls)
    }
    Assert-Missing 'no binary after invalid BUILD_TAG' (Join-Path $badTagDir 'agentsview.exe')

    # Surrounding whitespace and CRLF endings are trimmed; a full SHA works.
    $longTag = 'build-20260103-' + ('0123456789' * 4)
    $longDir = Join-Path $releases $longTag
    New-Item -ItemType Directory -Path $longDir -Force | Out-Null
    Copy-Item -Path (Join-Path (Join-Path $releases $build1) '*') -Destination $longDir
    Set-Content -Path (Join-Path $latest 'BUILD_TAG') -Value "  $longTag `r`n`r`n" -NoNewline
    $paddedDir = Join-Path $work 'padded-build-tag'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $paddedDir }
    Assert-Eq 'padded BUILD_TAG with a full SHA succeeds' 0 $r.Status
    Assert-Eq 'padded BUILD_TAG installs its snapshot' 'build-1' (Read-Text (Join-Path $paddedDir 'agentsview.exe'))
    Assert-Contains 'padded BUILD_TAG is trimmed in the download URL' "$baseUrl/$longTag/$assetName" (Get-RequestedUrls)
    Publish-Build $build2 'build-2'

    # Without an override the binary lands in %USERPROFILE%\.local\bin.
    $profileDir = Join-Path $work 'profile'
    $r = Invoke-Installer @{ USERPROFILE = $profileDir }
    Assert-Eq 'default dir install succeeds' 0 $r.Status
    $defaultDest = Join-Path (Join-Path (Join-Path $profileDir '.local') 'bin') 'agentsview.exe'
    Assert-Eq 'default dir is USERPROFILE\.local\bin' 'build-2' (Read-Text $defaultDest)

    # An install in the older %USERPROFILE%\.agentsview\bin is updated in
    # place, so the old copy does not keep running from there.
    $legacyProfile = Join-Path $work 'legacy-profile'
    $legacyDest = Join-Path (Join-Path (Join-Path $legacyProfile '.agentsview') 'bin') 'agentsview.exe'
    $legacyNewDest = Join-Path (Join-Path (Join-Path $legacyProfile '.local') 'bin') 'agentsview.exe'
    New-Item -ItemType Directory -Path (Split-Path -Parent $legacyDest) -Force | Out-Null
    Set-Content -Path $legacyDest -Value 'old-build' -NoNewline
    $r = Invoke-Installer @{ USERPROFILE = $legacyProfile }
    Assert-Eq 'legacy install update succeeds' 0 $r.Status
    Assert-Eq 'legacy install updated in place' 'build-2' (Read-Text $legacyDest)
    Assert-Missing 'no second copy in .local\bin' $legacyNewDest
    Assert-Contains 'says the legacy install is updated in place' `
        "Found an existing install in $(Split-Path -Parent $legacyDest); updating it in place." $r.Output

    # Once .local\bin has a copy it is the install to update.
    $bothProfile = Join-Path $work 'both-profile'
    $bothLegacy = Join-Path (Join-Path (Join-Path $bothProfile '.agentsview') 'bin') 'agentsview.exe'
    $bothNew = Join-Path (Join-Path (Join-Path $bothProfile '.local') 'bin') 'agentsview.exe'
    New-Item -ItemType Directory -Path (Split-Path -Parent $bothLegacy) -Force | Out-Null
    New-Item -ItemType Directory -Path (Split-Path -Parent $bothNew) -Force | Out-Null
    Set-Content -Path $bothLegacy -Value 'old-build' -NoNewline
    Set-Content -Path $bothNew -Value 'old-build' -NoNewline
    $r = Invoke-Installer @{ USERPROFILE = $bothProfile }
    Assert-Eq 'install with both folders succeeds' 0 $r.Status
    Assert-Eq 'both folders: .local\bin updated' 'build-2' (Read-Text $bothNew)
    Assert-Eq 'both folders: legacy copy untouched' 'old-build' (Read-Text $bothLegacy)

    # An explicit folder wins over a legacy install.
    $overrideDir = Join-Path $work 'override'
    Set-Content -Path $legacyDest -Value 'old-build' -NoNewline
    $r = Invoke-Installer @{ USERPROFILE = $legacyProfile; AGENTSVIEW_INSTALL_DIR = $overrideDir }
    Assert-Eq 'override with legacy install succeeds' 0 $r.Status
    Assert-Eq 'override folder gets the build' 'build-2' (Read-Text (Join-Path $overrideDir 'agentsview.exe'))
    Assert-Eq 'override leaves legacy copy alone' 'old-build' (Read-Text $legacyDest)

    # Another agentsview that new terminals find first is reported with
    # its location and a fix, instead of an unqualified success.
    $otherMachine = Join-Path $work 'other-machine'
    New-Item -ItemType Directory -Path $otherMachine -Force | Out-Null
    $otherMachineExe = Join-Path $otherMachine 'agentsview.exe'
    Set-Content -Path $otherMachineExe -Value 'other' -NoNewline
    $shadowDir = Join-Path $work 'shadow'
    $shadowDest = Join-Path $shadowDir 'agentsview.exe'
    $env:TEST_OTHER_MACHINE = $otherMachine
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $shadowDir; TEST_MACHINE_PATH = '%TEST_OTHER_MACHINE%'; TEST_USER_PATH = $shadowDir }
    Remove-Item Env:TEST_OTHER_MACHINE
    Assert-Eq 'install shadowed by the system PATH still succeeds' 0 $r.Status
    Assert-Contains 'names the copy on the system PATH' `
        "Another agentsview at $otherMachineExe comes first in PATH, so new terminals will run it instead of $shadowDest." $r.Output
    Assert-Contains 'system PATH fix' "remove $otherMachine from the system PATH" $r.Output
    Assert-NotContains 'no unqualified success when the system PATH wins' 'Installation complete!' $r.Output
    Assert-Contains 'qualified success when the system PATH wins' "Installed $shadowDest, but new terminals will run the other agentsview" $r.Output

    # A .cmd shim earlier in the user PATH also counts.
    $otherUser = Join-Path $work 'other-user'
    New-Item -ItemType Directory -Path $otherUser -Force | Out-Null
    $otherUserCmd = Join-Path $otherUser 'agentsview.cmd'
    Set-Content -Path $otherUserCmd -Value '@echo other'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $shadowDir; TEST_MACHINE_PATH = (Join-Path $work 'empty'); TEST_USER_PATH = "$otherUser;$shadowDir" }
    Assert-Contains 'names the shim on the user PATH' "Another agentsview at $otherUserCmd comes first in PATH" $r.Output
    Assert-Contains 'user PATH fix' "move $shadowDir above $otherUser in your user PATH" $r.Output
    Assert-NotContains 'no unqualified success when the user PATH wins' 'Installation complete!' $r.Output

    # No warning when the install folder comes first.
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $shadowDir; TEST_MACHINE_PATH = (Join-Path $work 'empty'); TEST_USER_PATH = "$shadowDir;$otherUser" }
    Assert-NotContains 'no warning when the install folder comes first' 'Another agentsview' $r.Output
    Assert-Contains 'unqualified success when the install folder comes first' 'Installation complete!' $r.Output

    # Lookup order follows the PATH list and skips folders without a copy.
    . $installer
    Assert-Eq 'lookup finds the first folder with a copy' $otherUserCmd `
        (Find-AgentsviewOnPath "$(Join-Path $work 'empty');$otherUser;$otherMachine")
    Assert-Eq 'lookup accepts quoted folders' $otherMachineExe `
        (Find-AgentsviewOnPath "`"$otherMachine`";$otherUser")
    Assert-Eq 'lookup returns nothing without a copy' $null (Find-AgentsviewOnPath (Join-Path $work 'empty'))

    # A corrupted download is rejected before anything is installed.
    $badDir = Join-Path $work 'bad'
    $snapshotAsset = Join-Path (Join-Path $releases $build2) $assetName
    Set-Content -Path $snapshotAsset -Value 'tampered' -NoNewline
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $badDir }
    Assert-Eq 'checksum mismatch fails' 1 $r.Status
    Assert-Contains 'reports checksum failure' 'Checksum verification failed' $r.Output
    Assert-Missing 'no binary after checksum failure' (Join-Path $badDir 'agentsview.exe')

    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $badDir; AGENTSVIEW_SKIP_CHECKSUM = '1' }
    Assert-Eq 'skip-checksum install succeeds' 0 $r.Status
    Assert-Eq 'skip-checksum installs download' 'tampered' (Read-Text (Join-Path $badDir 'agentsview.exe'))
    Assert-Eq 'skip-checksum still reads the snapshot' `
        "$baseUrl/latest/BUILD_TAG`n$baseUrl/$build2/$assetName" (Get-RequestedUrls)
    Publish-Build $build2 'build-2'

    # BUILD_TAG names a snapshot whose binary is missing.
    $held = Join-Path $work 'held'
    Move-Item $snapshotAsset $held
    $missingDir = Join-Path $work 'missing'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $missingDir }
    Assert-Eq 'missing asset fails' 1 $r.Status
    Assert-Contains 'reports download failure' 'Download failed' $r.Output
    Assert-Contains 'points at the snapshot release' "releases/tag/$build2" $r.Output
    Assert-Missing 'no binary after missing asset' (Join-Path $missingDir 'agentsview.exe')
    Move-Item $held $snapshotAsset

    # The unpublished script has no repository until the workflow fills it in.
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = (Join-Path $work 'no-repo'); AGENTSVIEW_REPO = $null }
    Assert-Eq 'unresolved repository fails' 1 $r.Status
    Assert-Contains 'asks for AGENTSVIEW_REPO' 'Set AGENTSVIEW_REPO' $r.Output

    # The release workflow's substitution yields a working default.
    $published = Join-Path $work 'install.ps1'
    (Get-Content $installer -Raw).Replace('@AGENTSVIEW_REPO@', 'example/published') |
        Set-Content -Path $published -NoNewline
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = (Join-Path $work 'published'); AGENTSVIEW_REPO = $null } -Script $published
    Assert-Eq 'published script installs without AGENTSVIEW_REPO' 0 $r.Status
    Assert-Contains 'published script uses substituted repository' `
        'https://github.com/example/published/releases/download/latest/BUILD_TAG' (Get-RequestedUrls)

    # PATH entry matching tolerates case, trailing slashes and variables.
    . $installer
    $env:TEST_PATH_HOME = 'C:\Users\Example'
    Assert-Eq 'PATH match ignores case and trailing slash' $true `
        (Test-PathListContains 'C:\Windows;c:\users\example\.agentsview\bin\;' 'C:\Users\Example\.agentsview\bin')
    Assert-Eq 'PATH match expands variables' $true `
        (Test-PathListContains '%TEST_PATH_HOME%\.agentsview\bin' 'C:\Users\Example\.agentsview\bin')
    Assert-Eq 'PATH match rejects prefixes' $false `
        (Test-PathListContains 'C:\Users\Example\.agentsview' 'C:\Users\Example\.agentsview\bin')
    Assert-Eq 'PATH match handles empty list' $false (Test-PathListContains '' 'C:\bin')
    Remove-Item Env:TEST_PATH_HOME

    if ($onWindows) {
        # Writes the real user PATH, then restores it.
        $savedUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $pathDir = Join-Path $work 'path-bin'
        try {
            # Another copy already in the user PATH, as an older install
            # in %USERPROFILE%\.agentsview\bin would be.
            $olderDir = Join-Path $work 'older-bin'
            New-Item -ItemType Directory -Path $olderDir -Force | Out-Null
            Set-Content -Path (Join-Path $olderDir 'agentsview.exe') -Value 'older' -NoNewline
            if ($savedUserPath) {
                [Environment]::SetEnvironmentVariable('Path', "$olderDir;$savedUserPath", 'User')
            } else {
                [Environment]::SetEnvironmentVariable('Path', $olderDir, 'User')
            }

            $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $pathDir; AGENTSVIEW_NO_MODIFY_PATH = $null }
            Assert-Eq 'PATH install succeeds' 0 $r.Status
            $userEntries = @([Environment]::GetEnvironmentVariable('Path', 'User') -split ';')
            $entries = @($userEntries | Where-Object { $_ -ieq $pathDir })
            Assert-Eq 'user PATH gains install dir' 1 $entries.Count
            Assert-Eq 'install dir goes first in the user PATH' $pathDir $userEntries[0]
            Assert-NotContains 'prepended install dir wins over the older copy' 'Another agentsview' $r.Output
            Assert-Contains 'asks for a new terminal' 'Open a new terminal' $r.Output

            $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $pathDir; AGENTSVIEW_NO_MODIFY_PATH = $null }
            $entries = @(([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') | Where-Object { $_ -ieq $pathDir })
            Assert-Eq 'reinstall keeps one user PATH entry' 1 $entries.Count
            Assert-Contains 'reinstall notes terminal has not loaded PATH' 'has not loaded it yet' $r.Output
        } finally {
            [Environment]::SetEnvironmentVariable('Path', $savedUserPath, 'User')
        }

        # A running agentsview.exe is moved aside instead of blocking the update.
        $runDir = Join-Path $work 'running'
        New-Item -ItemType Directory -Path $runDir -Force | Out-Null
        $runDest = Join-Path $runDir 'agentsview.exe'
        Copy-Item (Join-Path $env:SystemRoot 'System32\ping.exe') $runDest
        $proc = Start-Process -FilePath $runDest -ArgumentList '-n', '60', '127.0.0.1' -PassThru -WindowStyle Hidden
        try {
            $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $runDir }
            Assert-Eq 'install over running binary succeeds' 0 $r.Status
            Assert-Eq 'running binary replaced' 'build-2' (Read-Text $runDest)
            Assert-Eq 'running binary moved aside' $true (Test-Path "$runDest.old")
        } finally {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        }
    } else {
        Write-Host "  SKIP: user PATH and running-binary cases need Windows"
    }
} finally {
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Results: $($script:pass) passed, $($script:fail) failed"
if ($script:fail -gt 0) { exit 1 }

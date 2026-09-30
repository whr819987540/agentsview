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
function Assert-Missing($desc, $path) {
    if (-not (Test-Path $path)) { Pass $desc } else { Fail $desc "unexpected file $path" }
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) "agentsview-rolling-test-$(Get-Random)"
$release = Join-Path $work 'release'
New-Item -ItemType Directory -Path $release -Force | Out-Null
$asset = Join-Path $release 'agentsview-windows-amd64.exe'

function Publish-Build($content) {
    Set-Content -Path $asset -Value $content -NoNewline
    $hash = (Get-FileHash -Path $asset -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path (Join-Path $release 'SHA256SUMS') -Value "$hash  agentsview-windows-amd64.exe"
}

# The child dot-sources the installer, swaps in a fixture-backed
# download, and runs the real install function.
$runner = @'
. $env:TEST_INSTALLER
function Invoke-Download {
    param([string]$Uri, [string]$OutFile)
    Add-Content -Path (Join-Path $env:TEST_WORK 'urls') -Value $Uri
    $src = Join-Path $env:TEST_RELEASE $Uri.Substring($Uri.LastIndexOf('/') + 1)
    if (-not (Test-Path $src)) { throw "404 Not Found: $Uri" }
    Copy-Item $src $OutFile
}
Install-AgentsviewRolling
'@

function Invoke-Installer {
    param([hashtable]$Vars = @{}, [string]$Script = $installer)

    $vars = @{
        TEST_INSTALLER = $Script
        TEST_WORK = $work
        TEST_RELEASE = $release
        AGENTSVIEW_REPO = 'example/agentsview'
        AGENTSVIEW_INSTALL_DIR = $null
        AGENTSVIEW_NO_MODIFY_PATH = '1'
        AGENTSVIEW_SKIP_CHECKSUM = $null
    }
    foreach ($key in $Vars.Keys) { $vars[$key] = $Vars[$key] }

    $saved = @{}
    foreach ($key in $vars.Keys) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key)
        [Environment]::SetEnvironmentVariable($key, $vars[$key])
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

    Publish-Build 'build-1'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $dir }
    Assert-Eq 'install succeeds' 0 $r.Status
    Assert-Eq 'binary installed as agentsview.exe' 'build-1' (Get-Content $dest -Raw)
    Assert-Contains 'downloads from the rolling release' `
        'https://github.com/example/agentsview/releases/download/latest/agentsview-windows-amd64.exe' `
        (Get-Content (Join-Path $work 'urls') -Raw)
    Assert-Contains 'prints manual PATH guidance when opted out' 'is not in your user PATH' $r.Output

    Publish-Build 'build-2'
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $dir }
    Assert-Eq 'reinstall succeeds' 0 $r.Status
    Assert-Eq 'reinstall replaces binary' 'build-2' (Get-Content $dest -Raw)

    # Without an override the binary lands in %USERPROFILE%\.local\bin.
    $profileDir = Join-Path $work 'profile'
    $r = Invoke-Installer @{ USERPROFILE = $profileDir }
    Assert-Eq 'default dir install succeeds' 0 $r.Status
    $defaultDest = Join-Path (Join-Path (Join-Path $profileDir '.local') 'bin') 'agentsview.exe'
    Assert-Eq 'default dir is USERPROFILE\.local\bin' 'build-2' (Get-Content $defaultDest -Raw)

    # A corrupted download is rejected before anything is installed.
    $badDir = Join-Path $work 'bad'
    Set-Content -Path $asset -Value 'tampered' -NoNewline
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $badDir }
    Assert-Eq 'checksum mismatch fails' 1 $r.Status
    Assert-Contains 'reports checksum failure' 'Checksum verification failed' $r.Output
    Assert-Missing 'no binary after checksum failure' (Join-Path $badDir 'agentsview.exe')

    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $badDir; AGENTSVIEW_SKIP_CHECKSUM = '1' }
    Assert-Eq 'skip-checksum install succeeds' 0 $r.Status
    Assert-Eq 'skip-checksum installs download' 'tampered' (Get-Content (Join-Path $badDir 'agentsview.exe') -Raw)
    Publish-Build 'build-2'

    # Missing release asset.
    $held = Join-Path $work 'held'
    Move-Item $asset $held
    $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = (Join-Path $work 'missing') }
    Assert-Eq 'missing asset fails' 1 $r.Status
    Assert-Contains 'reports download failure' 'Download failed' $r.Output
    Move-Item $held $asset

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
    Assert-Contains 'published script uses substituted repository' 'example/published' $r.Output

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
            $r = Invoke-Installer @{ AGENTSVIEW_INSTALL_DIR = $pathDir; AGENTSVIEW_NO_MODIFY_PATH = $null }
            Assert-Eq 'PATH install succeeds' 0 $r.Status
            $entries = @(([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') | Where-Object { $_ -ieq $pathDir })
            Assert-Eq 'user PATH gains install dir' 1 $entries.Count
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
            Assert-Eq 'running binary replaced' 'build-2' (Get-Content $runDest -Raw)
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

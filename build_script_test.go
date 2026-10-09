package agentsview_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevBackendBuildRestoresPricingSnapshotBeforeBuild(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	installRepoFile(t, root, "scripts/dev-backend-build.sh", 0o755)
	stubs := installUnixBuildStubs(t, root)

	out, err := runInWorkspace(t, root, stubs.env(), "bash", "scripts/dev-backend-build.sh")
	require.NoError(t, err, "%s", out)

	events := stubs.events(t)
	assertEventOrder(t, events,
		"go run ./internal/pricing/cmd/litellm-snapshot -restore",
		"go build -tags fts5",
	)
	require.FileExists(t, filepath.Join(root, "tmp", "agentsview"))
}

func TestDevBackendBuildStopsWhenPricingSnapshotRestoreFails(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	installRepoFile(t, root, "scripts/dev-backend-build.sh", 0o755)
	stubs := installUnixBuildStubs(t, root)

	out, err := runInWorkspace(
		t,
		root,
		stubs.env("RESTORE_FAIL=1"),
		"bash",
		"scripts/dev-backend-build.sh",
	)
	require.Error(t, err, "script should fail when snapshot restore fails: %s", out)

	events := stubs.events(t)
	assertEventContains(t, events,
		"go run ./internal/pricing/cmd/litellm-snapshot -restore")
	assertNoEventContains(t, events, "go build")
}

func TestE2EServerRestoresPricingSnapshotBeforeServerBuild(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	installRepoFile(t, root, "scripts/e2e-server.sh", 0o755)
	writeE2EWorkspace(t, root)
	fixture := writeFixtureBinary(t, root)
	stubs := installUnixBuildStubs(t, root)

	out, err := runInWorkspace(
		t,
		root,
		stubs.env("E2E_PREBUILT_FIXTURE="+fixture),
		"bash",
		"scripts/e2e-server.sh",
	)
	require.NoError(t, err, "%s", out)

	assertEventOrder(t, stubs.events(t),
		"fixture -out",
		"go run ./internal/pricing/cmd/litellm-snapshot -restore",
		"npm run build",
		"go build -tags fts5,kit_posthog_disabled",
		"built-binary serve --port 8090 --no-browser",
	)
}

func TestE2EServerRestoresPricingSnapshotBeforeFixtureBuild(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	installRepoFile(t, root, "scripts/e2e-server.sh", 0o755)
	writeE2EWorkspace(t, root)
	server := writeServerBinary(t, root)
	stubs := installUnixBuildStubs(t, root)

	out, err := runInWorkspace(
		t,
		root,
		stubs.env("E2E_PREBUILT_SERVER="+server),
		"bash",
		"scripts/e2e-server.sh",
	)
	require.NoError(t, err, "%s", out)

	assertEventOrder(t, stubs.events(t),
		"go run ./internal/pricing/cmd/litellm-snapshot -restore",
		"go build -tags fts5,kit_posthog_disabled",
		"built-binary -out",
		"server serve --port 8090 --no-browser",
	)
}

func TestE2EServerStopsWhenPricingSnapshotRestoreFails(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	installRepoFile(t, root, "scripts/e2e-server.sh", 0o755)
	writeE2EWorkspace(t, root)
	fixture := writeFixtureBinary(t, root)
	stubs := installUnixBuildStubs(t, root)

	out, err := runInWorkspace(
		t,
		root,
		stubs.env("E2E_PREBUILT_FIXTURE="+fixture, "RESTORE_FAIL=1"),
		"bash",
		"scripts/e2e-server.sh",
	)
	require.Error(t, err, "script should fail when snapshot restore fails: %s", out)

	events := stubs.events(t)
	assertEventContains(t, events,
		"go run ./internal/pricing/cmd/litellm-snapshot -restore")
	assertNoEventContains(t, events, "go build")
}

func TestDesktopDevPowerShellStopsWhenPricingSnapshotRestoreFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell native command failure handling is Windows-specific")
	}
	requireCommand(t, "pwsh")

	root := t.TempDir()
	installRepoFile(t, root, "scripts/desktop-dev.ps1", 0o755)
	writeDesktopDevWorkspace(t, root)
	stubs := installWindowsBuildStubs(t, root)

	out, err := runInWorkspace(
		t,
		root,
		stubs.env("RESTORE_FAIL=1"),
		"pwsh",
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		filepath.Join(root, "scripts", "desktop-dev.ps1"),
	)
	require.Error(t, err, "script should fail when snapshot restore fails: %s", out)

	events := stubs.events(t)
	assertEventContains(t, events,
		"go run ./internal/pricing/cmd/litellm-snapshot -restore")
	assertNoEventContains(t, events, "go build")
}

// rollingBuildTag mirrors the permanent per-build tag the rolling release
// workflow creates next to the moving "latest" tag.
const rollingBuildTag = "build-20260101-abcdef12"

func TestMakefileVersionIgnoresRollingReleaseTags(t *testing.T) {
	requireUnixShell(t)
	requireCommand(t, "make")

	root := t.TempDir()
	want := initRollingTaggedRepo(t, root)
	installRepoFile(t, root, "Makefile", 0o644)

	// A dry run prints the build recipe with VERSION expanded.
	out, err := runInWorkspace(
		t,
		root,
		isolatedGitEnv(t, os.Environ()),
		"make",
		"-n",
		"build",
	)
	require.NoError(t, err, "%s", out)

	assert.Equal(t, want, tokenAfter(string(out), "-X main.version="))
}

func TestDevBackendBuildVersionIgnoresRollingReleaseTags(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	want := initRollingTaggedRepo(t, root)
	installRepoFile(t, root, "scripts/dev-backend-build.sh", 0o755)
	stubs := installUnixBuildStubs(t, root)
	removeGitStubs(t, stubs)

	out, err := runInWorkspace(
		t,
		root,
		isolatedGitEnv(t, stubs.env()),
		"bash",
		"scripts/dev-backend-build.sh",
	)
	require.NoError(t, err, "%s", out)

	assert.Equal(t, want, tokenAfter(
		strings.Join(stubs.events(t), "\n"), "-X main.version="))
}

func TestPrepareSidecarVersionIgnoresRollingReleaseTags(t *testing.T) {
	requireUnixShell(t)

	root := t.TempDir()
	want := initRollingTaggedRepo(t, root)
	installRepoFile(t, root, "desktop/scripts/prepare-sidecar.sh", 0o755)
	writeDesktopDevWorkspace(t, root)
	stubs := installUnixBuildStubs(t, root)
	removeGitStubs(t, stubs)

	// Empty overrides send the script to git for the version and to the
	// rustc stub for the target triple.
	env := isolatedGitEnv(t, stubs.env(
		"AGENTSVIEW_VERSION=",
		"TAURI_ENV_TARGET_TRIPLE=",
		"CARGO_BUILD_TARGET=",
	))
	out, err := runInWorkspace(
		t,
		root,
		env,
		"bash",
		"desktop/scripts/prepare-sidecar.sh",
	)
	require.NoError(t, err, "%s", out)

	assert.Equal(t, want, tokenAfter(
		strings.Join(stubs.events(t), "\n"), "-X main.version="))
	conf, err := os.ReadFile(
		filepath.Join(root, "desktop", "src-tauri", "tauri.conf.json"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"version": "1.0.0-dev.1"}`, string(conf))
}

func TestScreenshotsVersionIgnoresRollingReleaseTags(t *testing.T) {
	requireUnixShell(t)

	src := t.TempDir()
	want := initRollingTaggedRepo(t, src)

	root := t.TempDir()
	installRepoFile(t, root, "docs/screenshots/run.sh", 0o755)
	screenshotsDir := filepath.Join(root, "docs", "screenshots")
	require.NoError(t, os.WriteFile(
		filepath.Join(screenshotsDir, "Dockerfile"),
		[]byte("FROM scratch\n"),
		0o644,
	))
	writeExecutable(t, filepath.Join(screenshotsDir, "extract-db.sh"),
		"#!/bin/sh\nexit 0\n")
	sourceDB := filepath.Join(root, "sessions.db")
	require.NoError(t, os.WriteFile(sourceDB, nil, 0o644))

	stubs := newBuildStubs(t, root)
	writeExecutable(t, filepath.Join(stubs.binDir, "docker"), `#!/bin/sh
printf 'docker %s\n' "$*" >> "$CALL_LOG"
exit 0
`)
	for _, name := range []string{"rsync", "sqlite3"} {
		writeExecutable(t, filepath.Join(stubs.binDir, name), "#!/bin/sh\nexit 0\n")
	}

	out, err := runInWorkspace(
		t,
		root,
		isolatedGitEnv(t, stubs.env(
			"AGENTSVIEW_SRC="+src,
			"SOURCE_DB="+sourceDB,
		)),
		"bash",
		"docs/screenshots/run.sh",
	)
	require.NoError(t, err, "%s", out)

	assert.Equal(t, want, tokenAfter(
		strings.Join(stubs.events(t), "\n"), "--build-arg AV_VERSION="))
}

func TestDesktopDevPowerShellVersionIgnoresRollingReleaseTags(t *testing.T) {
	requireCommand(t, "pwsh")

	root := t.TempDir()
	want := initRollingTaggedRepo(t, root)
	installRepoFile(t, root, "scripts/desktop-dev.ps1", 0o755)
	writeDesktopDevWorkspace(t, root)
	stubs := installWindowsBuildStubs(t, root)
	removeGitStubs(t, stubs)

	out, err := runInWorkspace(
		t,
		root,
		isolatedGitEnv(t, stubs.env()),
		"pwsh",
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		filepath.Join(root, "scripts", "desktop-dev.ps1"),
	)
	require.NoError(t, err, "%s", out)

	assert.Equal(t, want, tokenAfter(
		strings.Join(stubs.events(t), "\n"), "-X main.version="))
	// The script restores tauri.conf.json after cargo exits, so its
	// report is the remaining evidence of the version it patched in.
	assert.Contains(t, string(out),
		"Patched tauri.conf.json version to 1.0.0-dev.1")
}

// initRollingTaggedRepo makes root a git checkout shaped like this fork
// after a rolling release: release tag v1.0.0 sits one commit behind
// HEAD, HEAD carries the "latest" and build-* tags, and a tracked file
// has an uncommitted edit. It returns the version that git describe
// limited to v* tags reports for that checkout.
func initRollingTaggedRepo(t *testing.T, root string) string {
	t.Helper()
	requireCommand(t, "git")

	env := append(isolatedGitEnv(t, os.Environ()),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	notes := filepath.Join(root, "notes.txt")
	writeNotes := func(text string) {
		require.NoError(t, os.WriteFile(notes, []byte(text+"\n"), 0o644))
	}

	runGit(t, root, env, "init", "-q")
	writeNotes("release")
	runGit(t, root, env, "add", "notes.txt")
	runGit(t, root, env, "commit", "-q", "-m", "release")
	runGit(t, root, env, "tag", "v1.0.0")
	writeNotes("main")
	runGit(t, root, env, "commit", "-q", "-a", "-m", "main")
	runGit(t, root, env, "tag", "latest")
	runGit(t, root, env, "tag", rollingBuildTag)
	writeNotes("local edit")

	head := runGit(t, root, env, "rev-parse", "--short", "HEAD")
	return "v1.0.0-1-g" + head + "-dirty"
}

// isolatedGitEnv drops inherited GIT_* variables, such as the GIT_DIR a
// git hook exports, and points git at empty config so the developer's
// hooks, signing, and abbreviation settings do not apply.
func isolatedGitEnv(t *testing.T, env []string) []string {
	t.Helper()

	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(globalConfig, nil, 0o644))
	isolated := make([]string, 0, len(env)+2)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "GIT_") {
			isolated = append(isolated, entry)
		}
	}
	return append(isolated,
		"GIT_CONFIG_GLOBAL="+globalConfig,
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), stderr.String())
	return strings.TrimSpace(string(out))
}

// removeGitStubs lets scripts run the real git against a scratch repo.
func removeGitStubs(t *testing.T, stubs buildStubs) {
	t.Helper()

	for _, name := range []string{"git", "git.ps1", "git.cmd"} {
		err := os.Remove(filepath.Join(stubs.binDir, name))
		if !errors.Is(err, os.ErrNotExist) {
			require.NoError(t, err)
		}
	}
}

// tokenAfter returns the whitespace-delimited word that follows the first
// marker in text, or "" when the marker is absent.
func tokenAfter(text, marker string) string {
	_, rest, found := strings.Cut(text, marker)
	if !found {
		return ""
	}
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		return rest[:end]
	}
	return rest
}

type buildStubs struct {
	binDir  string
	logPath string
}

func installUnixBuildStubs(t *testing.T, root string) buildStubs {
	t.Helper()

	stubs := newBuildStubs(t, root)
	writeExecutable(t, filepath.Join(stubs.binDir, "go"), `#!/bin/sh
set -eu
printf 'go %s\n' "$*" >> "$CALL_LOG"
if [ "${1:-}" = "run" ] && [ "${2:-}" = "./internal/pricing/cmd/litellm-snapshot" ]; then
  if [ "${RESTORE_FAIL:-}" = "1" ]; then exit 42; fi
  exit 0
fi
if [ "${1:-}" = "build" ]; then
  out=""
  prev=""
  for arg in "$@"; do
    if [ "$prev" = "-o" ]; then
      out="$arg"
      break
    fi
    prev="$arg"
  done
  if [ -n "$out" ]; then
    mkdir -p "$(dirname "$out")"
    cat > "$out" <<'EOS'
#!/bin/sh
printf 'built-binary %s\n' "$*" >> "$CALL_LOG"
exit 0
EOS
    chmod +x "$out"
  fi
  exit 0
fi
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "npm"), `#!/bin/sh
set -eu
printf 'npm %s\n' "$*" >> "$CALL_LOG"
if [ "${1:-}" = "run" ] && [ "${2:-}" = "build" ]; then
  mkdir -p dist
  printf 'ok\n' > dist/index.html
fi
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "git"), `#!/bin/sh
set -eu
printf 'git %s\n' "$*" >> "$CALL_LOG"
case " $* " in
  *" describe "*) printf 'v1.2.3-4-gabcdef\n' ;;
  *" rev-parse "*) printf 'abcdef1\n' ;;
esac
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "rustc"), `#!/bin/sh
set -eu
if [ "${1:-}" = "-vV" ]; then
  printf 'host: x86_64-unknown-linux-gnu\n'
fi
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "cargo"), `#!/bin/sh
set -eu
printf 'cargo %s\n' "$*" >> "$CALL_LOG"
exit 0
`)
	return stubs
}

func installWindowsBuildStubs(t *testing.T, root string) buildStubs {
	t.Helper()

	stubs := newBuildStubs(t, root)
	writeExecutable(t, filepath.Join(stubs.binDir, "go.ps1"), `Add-Content -Path $env:CALL_LOG -Value ("go " + ($args -join " "))
if ($args.Length -gt 0 -and $args[0] -eq "run") {
  if ($env:RESTORE_FAIL -eq "1") { exit 42 }
  exit 0
}
if ($args.Length -gt 0 -and $args[0] -eq "build") {
  $out = $null
  for ($i = 0; $i -lt $args.Length - 1; $i++) {
    if ($args[$i] -eq "-o") {
      $out = $args[$i+1]
      break
    }
  }
  if ($out) {
    $dir = [System.IO.Path]::GetDirectoryName($out)
    if ($dir) {
      New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    Set-Content -Path $out -Value @'
@echo off
echo built-binary %*>> "%CALL_LOG%"
exit /b 0
'@ -NoNewline
  }
  exit 0
}
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "go.cmd"), `@echo off
echo go %*>> "%CALL_LOG%"
if "%1"=="run" (
  if "%RESTORE_FAIL%"=="1" exit /b 42
  exit /b 0
)
if "%1"=="build" (
  set "out="
  set "next="
:go_loop
  if "%~1"=="" goto go_after
  if defined next (
    set "out=%~1"
    set "next="
  ) else if "%~1"=="-o" (
    set "next=1"
  )
  shift
  goto go_loop
:go_after
  if not "%out%"=="" (
    echo @echo off>"%out%"
    echo echo built-binary %%*^>^> "%%CALL_LOG%%">>"%out%"
    echo exit /b 0>>"%out%"
  )
  exit /b 0
)
exit /b 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "npm.ps1"), `Add-Content -Path $env:CALL_LOG -Value ("npm " + ($args -join " "))
if ($args.Length -ge 2 -and $args[0] -eq "run" -and $args[1] -eq "build") {
  New-Item -ItemType Directory -Path dist -Force | Out-Null
  Set-Content -Path "dist/index.html" -Value "ok" -NoNewline
}
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "npm.cmd"), `@echo off
echo npm %*>> "%CALL_LOG%"
if "%1"=="run" if "%2"=="build" (
  if not exist dist mkdir dist
  echo ok>dist\index.html
)
exit /b 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "git.ps1"), `$joined = $args -join " "
Add-Content -Path $env:CALL_LOG -Value ("git " + $joined)
if ($joined -like "*describe*") {
  Write-Output "v1.2.3-4-gabcdef"
  exit 0
}
if ($joined -like "*rev-parse*") {
  Write-Output "abcdef1"
  exit 0
}
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "git.cmd"), `@echo off
echo git %*>> "%CALL_LOG%"
echo %* | findstr /C:"describe" >nul && (
  echo v1.2.3-4-gabcdef
  exit /b 0
)
echo %* | findstr /C:"rev-parse" >nul && (
  echo abcdef1
  exit /b 0
)
exit /b 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "rustc.ps1"), `if ($args.Length -gt 0 -and $args[0] -eq "-vV") {
  Write-Output "host: x86_64-pc-windows-msvc"
}
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "rustc.cmd"), `@echo off
if "%1"=="-vV" echo host: x86_64-pc-windows-msvc
exit /b 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "cargo.ps1"), `Add-Content -Path $env:CALL_LOG -Value ("cargo " + ($args -join " "))
exit 0
`)
	writeExecutable(t, filepath.Join(stubs.binDir, "cargo.cmd"), `@echo off
echo cargo %*>> "%CALL_LOG%"
exit /b 0
`)
	return stubs
}

func newBuildStubs(t *testing.T, root string) buildStubs {
	t.Helper()

	binDir := filepath.Join(root, "stub-bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	logPath := filepath.Join(root, "calls.log")
	require.NoError(t, os.WriteFile(logPath, nil, 0o644))
	return buildStubs{binDir: binDir, logPath: logPath}
}

func (s buildStubs) env(extra ...string) []string {
	env := envWithout("PATH", "CALL_LOG")
	env = append(env, "PATH="+s.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = append(env, "CALL_LOG="+s.logPath)
	return append(env, extra...)
}

func (s buildStubs) events(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(s.logPath)
	require.NoError(t, err)
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	return strings.FieldsFunc(normalized, func(r rune) bool { return r == '\n' })
}

func writeE2EWorkspace(t *testing.T, root string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "frontend"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "parser"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "web"), 0o755))

	var registry strings.Builder
	registry.WriteString("package parser\n\nvar agentDirs = []struct{ EnvVar string }{\n")
	for i := range 12 {
		fmt.Fprintf(&registry, "\t{EnvVar: \"AGENT_%02d_DIR\"},\n", i)
	}
	registry.WriteString("}\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "internal", "parser", "types.go"),
		[]byte(registry.String()),
		0o644,
	))
}

func writeDesktopDevWorkspace(t *testing.T, root string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "frontend"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "web"), 0o755))
	tauriDir := filepath.Join(root, "desktop", "src-tauri")
	require.NoError(t, os.MkdirAll(tauriDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tauriDir, "tauri.conf.json"),
		[]byte(`{"version": "0.0.0"}`),
		0o644,
	))
}

func writeFixtureBinary(t *testing.T, root string) string {
	t.Helper()

	fixture := filepath.Join(root, "fixture")
	writeExecutable(t, fixture, `#!/bin/sh
set -eu
printf 'fixture %s\n' "$*" >> "$CALL_LOG"
while [ "$#" -gt 0 ]; do
  case "$1" in
    -out|-duckdb-out)
      shift
      mkdir -p "$(dirname "$1")"
      printf 'fixture\n' > "$1"
      ;;
  esac
  shift || true
done
exit 0
`)
	return fixture
}

func writeServerBinary(t *testing.T, root string) string {
	t.Helper()

	server := filepath.Join(root, "server")
	writeExecutable(t, server, `#!/bin/sh
set -eu
printf 'server %s\n' "$*" >> "$CALL_LOG"
exit 0
`)
	return server
}

func installRepoFile(t *testing.T, root, rel string, mode os.FileMode) {
	t.Helper()

	data, err := os.ReadFile(rel)
	require.NoError(t, err)
	dst := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	require.NoError(t, os.WriteFile(dst, data, mode))
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
}

func runInWorkspace(
	t *testing.T,
	root string,
	env []string,
	name string,
	args ...string,
) ([]byte, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = root
	cmd.Env = env
	return cmd.CombinedOutput()
}

func requireUnixShell(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("bash script behavior tests run on Unix")
	}
	requireCommand(t, "bash")
}

func requireCommand(t *testing.T, name string) string {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found on PATH", name)
	}
	return path
}

func assertEventOrder(t *testing.T, events []string, wants ...string) {
	t.Helper()

	start := 0
	for _, want := range wants {
		found := -1
		for i := start; i < len(events); i++ {
			if strings.Contains(events[i], want) {
				found = i
				break
			}
		}
		require.NotEqual(t, -1, found,
			"event containing %q not found after index %d in %v", want, start, events)
		start = found + 1
	}
}

func assertEventContains(t *testing.T, events []string, want string) {
	t.Helper()

	for _, event := range events {
		if strings.Contains(event, want) {
			return
		}
	}
	assert.Failf(t, "missing event", "event containing %q not found in %v", want, events)
}

func assertNoEventContains(t *testing.T, events []string, unwanted string) {
	t.Helper()

	for _, event := range events {
		assert.NotContains(t, event, unwanted)
	}
}

func envWithout(keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[key] = struct{}{}
	}

	var env []string
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			continue
		}
		if _, found := drop[key]; found {
			continue
		}
		env = append(env, entry)
	}
	return env
}

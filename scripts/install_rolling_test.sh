#!/bin/bash
# Tests for install-rolling.sh. Sources the installer and runs its real
# main function with downloads served from a local fixture directory.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=install-rolling.sh
source "$SCRIPT_DIR/install-rolling.sh"
set +e

PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        pass "$desc"
    else
        fail "$desc"
        echo "    expected: '$expected'"
        echo "    actual:   '$actual'"
    fi
}

assert_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        pass "$desc"
    else
        fail "$desc"
        echo "    missing: '$needle'"
        echo "    in:      '$haystack'"
    fi
}

assert_missing() {
    local desc="$1" path="$2"
    if [ ! -e "$path" ]; then
        pass "$desc"
    else
        fail "$desc"
        echo "    unexpected file: $path"
    fi
}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
RELEASE="$WORK/release"
mkdir -p "$RELEASE"

# Serve downloads from $RELEASE and record the requested URLs.
mock_download() {
    echo "$1" >> "$WORK/urls"
    local name="${1##*/}"
    [ -f "$RELEASE/$name" ] || return 22
    cp "$RELEASE/$name" "$2"
}
download() { mock_download "$@"; }

# Pretend to be linux/amd64 regardless of the machine running the test.
uname() {
    case "$1" in
        -s) echo Linux ;;
        -m) echo x86_64 ;;
        *) command uname "$@" ;;
    esac
}

publish_build() {
    printf '%s\n' "$1" > "$RELEASE/agentsview-linux-amd64"
    (cd "$RELEASE" && sha256sum agentsview-linux-amd64 > SHA256SUMS)
}

# Runs main in a subshell with a fresh HOME and a PATH that does not
# include the install directory. Extra NAME=value arguments set env vars.
run_install() {
    local home="$1"; shift
    OUTPUT=$(
        export HOME="$home"
        export PATH="/usr/bin:/bin"
        export AGENTSVIEW_REPO="example/agentsview"
        unset ZDOTDIR XDG_CONFIG_HOME AGENTSVIEW_INSTALL_DIR AGENTSVIEW_NO_MODIFY_PATH AGENTSVIEW_SKIP_CHECKSUM
        for kv in "$@"; do export "${kv?}"; done
        set -e
        main 2>&1
    )
    STATUS=$?
}

new_home() {
    local home="$WORK/home-$1"
    mkdir -p "$home"
    echo "$home"
}

echo "=== install-rolling.sh ==="

publish_build "build-1"

# zsh user without ~/.local/bin in PATH or .zshrc.
home=$(new_home zsh)
run_install "$home" SHELL=/bin/zsh
assert_eq "zsh install succeeds" 0 "$STATUS"
assert_eq "binary installed as agentsview" "build-1" "$(cat "$home/.local/bin/agentsview")"
assert_eq "binary is executable" "yes" "$([ -x "$home/.local/bin/agentsview" ] && echo yes)"
assert_eq "zshrc adds install dir with \$HOME" \
    'export PATH="$HOME/.local/bin:$PATH"' "$(tail -1 "$home/.zshrc")"
assert_contains "tells user to reload zshrc" "source $home/.zshrc" "$OUTPUT"
assert_contains "downloads from the rolling release" \
    "https://github.com/example/agentsview/releases/download/latest/agentsview-linux-amd64" "$(cat "$WORK/urls")"

# Rerun replaces the binary and does not repeat the PATH line.
publish_build "build-2"
run_install "$home" SHELL=/bin/zsh
assert_eq "reinstall succeeds" 0 "$STATUS"
assert_eq "reinstall replaces binary" "build-2" "$(cat "$home/.local/bin/agentsview")"
assert_eq "zshrc PATH line written once" 1 "$(grep -c 'agentsview installer' "$home/.zshrc")"
assert_contains "reinstall still asks for reload" "source $home/.zshrc" "$OUTPUT"

# ZDOTDIR moves the zsh startup file.
home=$(new_home zdotdir)
run_install "$home" SHELL=/usr/bin/zsh ZDOTDIR="$home/zsh"
assert_eq "ZDOTDIR install succeeds" 0 "$STATUS"
assert_contains "zshrc written under ZDOTDIR" '.local/bin' "$(cat "$home/zsh/.zshrc")"
assert_missing "no zshrc in HOME when ZDOTDIR is set" "$home/.zshrc"

# bash user whose .bashrc already configures the directory.
home=$(new_home bash-configured)
printf 'export PATH="~/.local/bin:$PATH"\n' > "$home/.bashrc"
run_install "$home" SHELL=/bin/bash
assert_eq "configured bash install succeeds" 0 "$STATUS"
assert_eq "existing bashrc left unchanged" 1 "$(wc -l < "$home/.bashrc" | tr -d ' ')"
assert_contains "configured bash asks for reload" "source $home/.bashrc" "$OUTPUT"

# Directory already in the running PATH: nothing to change or reload.
home=$(new_home in-path)
run_install "$home" SHELL=/bin/bash PATH="$home/.local/bin:/usr/bin:/bin"
assert_eq "in-PATH install succeeds" 0 "$STATUS"
assert_missing "no bashrc written when already in PATH" "$home/.bashrc"
assert_contains "reports install dir already in PATH" "already in PATH" "$OUTPUT"
if [[ "$OUTPUT" == *"Refresh your shell"* ]]; then
    fail "no reload prompt when already in PATH"
else
    pass "no reload prompt when already in PATH"
fi

# fish uses its own syntax and config file.
home=$(new_home fish)
run_install "$home" SHELL=/usr/bin/fish
assert_eq "fish install succeeds" 0 "$STATUS"
assert_eq "fish config uses fish_add_path" \
    'fish_add_path "$HOME/.local/bin"' "$(tail -1 "$home/.config/fish/config.fish")"

# Unknown shells fall back to .profile.
home=$(new_home sh)
run_install "$home" SHELL=/bin/dash
assert_eq "dash install succeeds" 0 "$STATUS"
assert_contains "profile gets PATH line" '$HOME/.local/bin' "$(cat "$home/.profile")"

# Opting out of startup-file changes still installs and prints guidance.
home=$(new_home no-modify)
run_install "$home" SHELL=/bin/zsh AGENTSVIEW_NO_MODIFY_PATH=1
assert_eq "no-modify install succeeds" 0 "$STATUS"
assert_missing "no zshrc written when opted out" "$home/.zshrc"
assert_contains "prints manual PATH line" 'export PATH="$HOME/.local/bin:$PATH"' "$OUTPUT"

# Custom install directory outside HOME is written literally.
home=$(new_home custom-dir)
run_install "$home" SHELL=/bin/zsh AGENTSVIEW_INSTALL_DIR="$WORK/opt/bin"
assert_eq "custom dir install succeeds" 0 "$STATUS"
assert_eq "binary installed in custom dir" "build-2" "$(cat "$WORK/opt/bin/agentsview")"
assert_eq "zshrc uses literal custom dir" \
    "export PATH=\"$WORK/opt/bin:\$PATH\"" "$(tail -1 "$home/.zshrc")"

# A corrupted download is rejected before anything is installed.
home=$(new_home bad-checksum)
printf 'tampered\n' > "$RELEASE/agentsview-linux-amd64"
run_install "$home" SHELL=/bin/zsh
assert_eq "checksum mismatch fails" 1 "$STATUS"
assert_contains "reports checksum failure" "Checksum verification failed" "$OUTPUT"
assert_missing "no binary after checksum failure" "$home/.local/bin/agentsview"
assert_missing "no zshrc after checksum failure" "$home/.zshrc"

# Skipping verification installs the downloaded file as-is.
run_install "$home" SHELL=/bin/zsh AGENTSVIEW_SKIP_CHECKSUM=1
assert_eq "skip-checksum install succeeds" 0 "$STATUS"
assert_eq "skip-checksum installs download" "tampered" "$(cat "$home/.local/bin/agentsview")"
publish_build "build-2"

# Missing release asset.
home=$(new_home missing)
mv "$RELEASE/agentsview-linux-amd64" "$WORK/held"
run_install "$home" SHELL=/bin/zsh
assert_eq "missing asset fails" 1 "$STATUS"
assert_contains "reports download failure" "Download failed" "$OUTPUT"
mv "$WORK/held" "$RELEASE/agentsview-linux-amd64"

# The unpublished script has no repository until the workflow fills it in.
home=$(new_home no-repo)
OUTPUT=$(
    export HOME="$home" PATH="/usr/bin:/bin" SHELL=/bin/zsh
    unset AGENTSVIEW_REPO
    set -e
    main 2>&1
)
STATUS=$?
assert_eq "unresolved repository fails" 1 "$STATUS"
assert_contains "asks for AGENTSVIEW_REPO" "Set AGENTSVIEW_REPO" "$OUTPUT"

# The release workflow's substitution yields a working default.
home=$(new_home published)
sed "s|@AGENTSVIEW_REPO@|example/published|" "$SCRIPT_DIR/install-rolling.sh" > "$WORK/install.sh"
OUTPUT=$(
    export HOME="$home" PATH="/usr/bin:/bin" SHELL=/bin/zsh
    unset AGENTSVIEW_REPO
    # shellcheck disable=SC1091
    source "$WORK/install.sh"
    download() { mock_download "$@"; }
    main 2>&1
)
STATUS=$?
assert_eq "published script installs without AGENTSVIEW_REPO" 0 "$STATUS"
assert_contains "published script uses substituted repository" "example/published" "$OUTPUT"

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]

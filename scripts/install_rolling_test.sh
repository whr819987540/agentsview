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

assert_not_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "$haystack" != *"$needle"* ]]; then
        pass "$desc"
    else
        fail "$desc"
        echo "    unexpected: '$needle'"
        echo "    in:         '$haystack'"
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
# One subdirectory per release tag, mirroring the download URL layout.
RELEASES="$WORK/releases"
mkdir -p "$RELEASES/latest"

BASE_URL="https://github.com/example/agentsview/releases/download"
BUILD1="build-20260101-1111aaaa"
BUILD2="build-20260102-2222bbbb"

# Serve downloads from $RELEASES/<tag>/<file> and record the requested URLs.
mock_download() {
    echo "$1" >> "$WORK/urls"
    local rel="${1#https://github.com/*/releases/download/}"
    [ -f "$RELEASES/$rel" ] || return 22
    cp "$RELEASES/$rel" "$2"
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

# Publishes a snapshot release whose binary holds the given text, then
# copies its assets to "latest" the way the release workflow does.
publish_build() {
    local tag="$1" content="$2"
    mkdir -p "$RELEASES/$tag"
    printf '%s\n' "$content" > "$RELEASES/$tag/agentsview-linux-amd64"
    (cd "$RELEASES/$tag" && sha256sum agentsview-linux-amd64 > SHA256SUMS)
    printf '%s\n' "$tag" > "$RELEASES/$tag/BUILD_TAG"
    cp "$RELEASES/$tag"/* "$RELEASES/latest/"
}

# Runs main in a subshell with a fresh HOME and a PATH that does not
# include the install directory. Extra NAME=value arguments set env vars.
# The URLs the run requested are left in $WORK/urls.
run_install() {
    local home="$1"; shift
    rm -f "$WORK/urls"
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

requested_urls() { cat "$WORK/urls" 2>/dev/null; }

new_home() {
    local home="$WORK/home-$1"
    mkdir -p "$home"
    echo "$home"
}

echo "=== install-rolling.sh ==="

publish_build "$BUILD1" "build-1"

# zsh user without ~/.local/bin in PATH or .zshrc.
home=$(new_home zsh)
run_install "$home" SHELL=/bin/zsh
assert_eq "zsh install succeeds" 0 "$STATUS"
assert_eq "binary installed as agentsview" "build-1" "$(cat "$home/.local/bin/agentsview")"
assert_eq "binary is executable" "yes" "$([ -x "$home/.local/bin/agentsview" ] && echo yes)"
assert_eq "zshrc adds install dir with \$HOME" \
    'export PATH="$HOME/.local/bin:$PATH"' "$(tail -1 "$home/.zshrc")"
assert_contains "tells user to reload zshrc" "source $home/.zshrc" "$OUTPUT"
assert_contains "names the build being installed" "Build: $BUILD1" "$OUTPUT"
assert_eq "reads BUILD_TAG from latest, then assets from the snapshot" \
    "$BASE_URL/latest/BUILD_TAG
$BASE_URL/$BUILD1/agentsview-linux-amd64
$BASE_URL/$BUILD1/SHA256SUMS" "$(requested_urls)"

# Rerun replaces the binary and does not repeat the PATH line.
publish_build "$BUILD2" "build-2"
run_install "$home" SHELL=/bin/zsh
assert_eq "reinstall succeeds" 0 "$STATUS"
assert_eq "reinstall replaces binary" "build-2" "$(cat "$home/.local/bin/agentsview")"
assert_eq "zshrc PATH line written once" 1 "$(grep -c 'agentsview installer' "$home/.zshrc")"
assert_contains "reinstall still asks for reload" "source $home/.zshrc" "$OUTPUT"

# A push can replace the assets on "latest" before BUILD_TAG moves.
# The installer still installs a consistent build from the snapshot.
home=$(new_home mid-publish)
printf 'build-3\n' > "$RELEASES/latest/agentsview-linux-amd64"
printf 'not a checksum list\n' > "$RELEASES/latest/SHA256SUMS"
run_install "$home" SHELL=/bin/zsh
assert_eq "install during a publish succeeds" 0 "$STATUS"
assert_eq "install during a publish uses the snapshot binary" "build-2" "$(cat "$home/.local/bin/agentsview")"
publish_build "$BUILD2" "build-2"

# While "latest" is being recreated its BUILD_TAG is missing. The
# installer stops instead of falling back to other assets.
home=$(new_home no-build-tag)
mv "$RELEASES/latest/BUILD_TAG" "$WORK/held-build-tag"
run_install "$home" SHELL=/bin/zsh
assert_eq "missing BUILD_TAG fails" 1 "$STATUS"
assert_contains "missing BUILD_TAG suggests retrying" "may be mid-publish; try again" "$OUTPUT"
assert_eq "missing BUILD_TAG downloads nothing else" "$BASE_URL/latest/BUILD_TAG" "$(requested_urls)"
assert_missing "no binary without BUILD_TAG" "$home/.local/bin/agentsview"
assert_missing "no zshrc without BUILD_TAG" "$home/.zshrc"
mv "$WORK/held-build-tag" "$RELEASES/latest/BUILD_TAG"

# BUILD_TAG must name a snapshot release.
home=$(new_home bad-build-tag)
bad_tags=(
    ""
    "latest"
    "v0.31.0"
    "build-2026010-1111aaaa"
    "build-20260101-111aaa"
    "build-20260101-1111AAAA"
    "build-20260101-1111aaaa extra"
    "build-20260101-$(printf 'a%.0s' {1..41})"
    $'build-20260101-1111aaaa\nbuild-20260102-2222bbbb'
    "../build-20260101-1111aaaa"
    "<html>Not Found</html>"
)
for bad in "${bad_tags[@]}"; do
    printf '%s\n' "$bad" > "$RELEASES/latest/BUILD_TAG"
    run_install "$home" SHELL=/bin/zsh
    assert_eq "BUILD_TAG '$bad' is rejected" 1 "$STATUS"
    assert_contains "BUILD_TAG '$bad' suggests retrying" "may be mid-publish; try again" "$OUTPUT"
    assert_eq "BUILD_TAG '$bad' downloads nothing else" "$BASE_URL/latest/BUILD_TAG" "$(requested_urls)"
done
assert_missing "no binary after invalid BUILD_TAG" "$home/.local/bin/agentsview"

# Surrounding whitespace and CRLF endings are trimmed; a full SHA works.
home=$(new_home spaced-build-tag)
long_tag="build-20260103-$(printf '0123456789%.0s' 1 2 3 4)"
mkdir -p "$RELEASES/$long_tag"
cp "$RELEASES/$BUILD1"/* "$RELEASES/$long_tag/"
printf '  %s \r\n\n' "$long_tag" > "$RELEASES/latest/BUILD_TAG"
run_install "$home" SHELL=/bin/zsh
assert_eq "padded BUILD_TAG with a full SHA succeeds" 0 "$STATUS"
assert_eq "padded BUILD_TAG installs its snapshot" "build-1" "$(cat "$home/.local/bin/agentsview")"
assert_contains "padded BUILD_TAG is trimmed in the download URL" \
    "$BASE_URL/$long_tag/agentsview-linux-amd64" "$(requested_urls)"
publish_build "$BUILD2" "build-2"

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

# Only a real PATH element counts as configured. Each case writes one
# startup file line; @HOME@ stands for the test HOME.
rc_case() {
    local expected="$1" line="$2"
    local home="$WORK/rc-home"
    mkdir -p "$home"
    printf '%s\n' "${line//@HOME@/$home}" > "$home/rc"
    local got=no
    if (HOME="$home"; rc_mentions_dir "$home/rc" "$home/.local/bin"); then
        got=yes
    fi
    assert_eq "startup line counts as configured=$expected: $line" "$expected" "$got"
}
rc_case yes 'export PATH="$HOME/.local/bin:$PATH"'
rc_case yes 'export PATH="${HOME}/.local/bin:$PATH"'
rc_case yes 'export PATH=~/.local/bin:$PATH'
rc_case yes 'PATH="$PATH:$HOME/.local/bin"'
rc_case yes 'PATH=$PATH:$HOME/.local/bin'
rc_case yes 'export PATH="$HOME/.local/bin/:$PATH"'
rc_case yes '  export PATH="$HOME/.local/bin:$PATH"  # my tools'
rc_case yes 'export PATH="@HOME@/.local/bin:$PATH"'
rc_case yes 'fish_add_path "$HOME/.local/bin"'
rc_case yes 'fish_add_path ~/.local/bin'
rc_case yes 'fish_add_path -g ~/.local/bin/'
rc_case yes 'set -gx PATH $HOME/.local/bin $PATH'
rc_case no '# old: ~/.local/bin'
rc_case no '    # export PATH="$HOME/.local/bin:$PATH"'
rc_case no 'export EDITOR=vim # see ~/.local/bin'
rc_case no 'export PATH="$HOME/.local/binaries:$PATH"'
rc_case no 'export PATH="$HOME/.local/bin-old:$PATH"'
rc_case no 'export PATH="/srv@HOME@/.local/bin:$PATH"'
rc_case no 'export PATH="$HOME/.local/bin/extra:$PATH"'
rc_case no '# fish_add_path ~/.local/bin'
rc_case no 'fish_add_path ~/.local/binaries'
rc_case no 'set -gx PATH $HOME/.local/bin_old $PATH'

# A final line without a newline still counts.
home="$WORK/rc-home"
printf 'export PATH="$HOME/.local/bin:$PATH"' > "$home/rc"
got=no
if (HOME="$home"; rc_mentions_dir "$home/rc" "$home/.local/bin"); then got=yes; fi
assert_eq "unterminated last startup line counts as configured" yes "$got"

# A startup file that only mentions the directory in a comment still
# gets a PATH line.
home=$(new_home bash-comment)
printf '# old: ~/.local/bin\n' > "$home/.bashrc"
run_install "$home" SHELL=/bin/bash
assert_eq "commented bashrc install succeeds" 0 "$STATUS"
assert_eq "commented bashrc gains PATH line" \
    'export PATH="$HOME/.local/bin:$PATH"' "$(tail -1 "$home/.bashrc")"

# A fish config that adds a longer path still gets a PATH line.
home=$(new_home fish-longer)
mkdir -p "$home/.config/fish"
printf 'fish_add_path ~/.local/binaries\n' > "$home/.config/fish/config.fish"
run_install "$home" SHELL=/usr/bin/fish
assert_eq "fish longer-path install succeeds" 0 "$STATUS"
assert_eq "fish config with a longer path gains PATH line" \
    'fish_add_path "$HOME/.local/bin"' "$(tail -1 "$home/.config/fish/config.fish")"

# Directory already in the running PATH: nothing to change or reload.
home=$(new_home in-path)
run_install "$home" SHELL=/bin/bash PATH="$home/.local/bin:/usr/bin:/bin"
assert_eq "in-PATH install succeeds" 0 "$STATUS"
assert_missing "no bashrc written when already in PATH" "$home/.bashrc"
assert_contains "reports install dir already in PATH" "already in PATH" "$OUTPUT"
assert_not_contains "no reload prompt when already in PATH" "Refresh your shell" "$OUTPUT"

# Another agentsview, as in /usr/local/bin, judged against the PATH that
# new shells will see.
other_dir="$WORK/other/bin"
mkdir -p "$other_dir"
printf '#!/bin/sh\n' > "$other_dir/agentsview"
chmod 755 "$other_dir/agentsview"

# The installer just put its directory first in the startup file.
home=$(new_home other-prepended)
run_install "$home" SHELL=/bin/bash PATH="$other_dir:/usr/bin:/bin"
assert_eq "install beside another copy succeeds" 0 "$STATUS"
assert_not_contains "no warning when the new PATH line comes first" "Another agentsview" "$OUTPUT"
assert_contains "unqualified success when the new PATH line comes first" "Installation complete!" "$OUTPUT"

# The directory is already in PATH, after the other copy.
home=$(new_home other-first)
run_install "$home" SHELL=/bin/bash PATH="$other_dir:$home/.local/bin:/usr/bin:/bin"
assert_eq "shadowed install still succeeds" 0 "$STATUS"
assert_contains "warns that the other copy runs first" \
    "Another agentsview at $other_dir/agentsview comes first in PATH and will run instead." "$OUTPUT"
assert_contains "says how to fix the order" "put $home/.local/bin before $other_dir in PATH" "$OUTPUT"
assert_not_contains "no unqualified success when shadowed" "Installation complete!" "$OUTPUT"

# The directory is already in PATH, before the other copy.
home=$(new_home other-later)
run_install "$home" SHELL=/bin/bash PATH="$home/.local/bin:$other_dir:/usr/bin:/bin"
assert_not_contains "no warning when the install dir comes first" "Another agentsview" "$OUTPUT"
assert_contains "unqualified success when the install dir comes first" "Installation complete!" "$OUTPUT"

# A symlink to the new binary earlier in PATH is not another copy.
home=$(new_home other-symlink)
mkdir -p "$home/.local/bin" "$home/links"
ln -s "$home/.local/bin/agentsview" "$home/links/agentsview"
run_install "$home" SHELL=/bin/bash PATH="$home/links:$home/.local/bin:/usr/bin:/bin"
assert_not_contains "no warning for a symlink to the new binary" "Another agentsview" "$OUTPUT"

# The startup file adds the directory but is not loaded, so the order in
# new shells is unknown and the warning is conditional.
home=$(new_home other-unloaded)
printf 'export PATH="$PATH:$HOME/.local/bin"\n' > "$home/.bashrc"
run_install "$home" SHELL=/bin/bash PATH="$other_dir:/usr/bin:/bin"
assert_contains "conditional warning when the order is unknown" \
    "Another agentsview is at $other_dir/agentsview. If $other_dir comes before $home/.local/bin in PATH, that copy will run instead." "$OUTPUT"
assert_not_contains "no certain warning when the order is unknown" "comes first in PATH" "$OUTPUT"

home=$(new_home other-no-modify)
run_install "$home" SHELL=/bin/bash PATH="$other_dir:/usr/bin:/bin" AGENTSVIEW_NO_MODIFY_PATH=1
assert_contains "conditional warning when PATH is left alone" \
    "If $other_dir comes before $home/.local/bin in PATH" "$OUTPUT"
assert_not_contains "no certain warning when PATH is left alone" "comes first in PATH" "$OUTPUT"

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
printf 'tampered\n' > "$RELEASES/$BUILD2/agentsview-linux-amd64"
run_install "$home" SHELL=/bin/zsh
assert_eq "checksum mismatch fails" 1 "$STATUS"
assert_contains "reports checksum failure" "Checksum verification failed" "$OUTPUT"
assert_missing "no binary after checksum failure" "$home/.local/bin/agentsview"
assert_missing "no zshrc after checksum failure" "$home/.zshrc"

# Skipping verification installs the downloaded file as-is.
run_install "$home" SHELL=/bin/zsh AGENTSVIEW_SKIP_CHECKSUM=1
assert_eq "skip-checksum install succeeds" 0 "$STATUS"
assert_eq "skip-checksum installs download" "tampered" "$(cat "$home/.local/bin/agentsview")"
assert_eq "skip-checksum still reads the snapshot" \
    "$BASE_URL/latest/BUILD_TAG
$BASE_URL/$BUILD2/agentsview-linux-amd64" "$(requested_urls)"
publish_build "$BUILD2" "build-2"

# BUILD_TAG names a snapshot whose binary is missing.
home=$(new_home missing)
mv "$RELEASES/$BUILD2/agentsview-linux-amd64" "$WORK/held"
run_install "$home" SHELL=/bin/zsh
assert_eq "missing asset fails" 1 "$STATUS"
assert_contains "reports download failure" "Download failed" "$OUTPUT"
assert_contains "points at the snapshot release" "releases/tag/$BUILD2" "$OUTPUT"
assert_missing "no binary after missing asset" "$home/.local/bin/agentsview"
mv "$WORK/held" "$RELEASES/$BUILD2/agentsview-linux-amd64"

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
rm -f "$WORK/urls"
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
assert_contains "published script uses substituted repository" \
    "https://github.com/example/published/releases/download/latest/BUILD_TAG" "$(requested_urls)"

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]

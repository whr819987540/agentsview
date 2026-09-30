#!/bin/bash
# agentsview rolling-build installer for Linux
#
# Installs the newest build from the rolling "latest" prerelease that
# .github/workflows/rolling-release.yml publishes on every push to main.
# The workflow attaches this script to that release as install.sh with
# the repository filled in:
#
#   curl -fsSL https://github.com/<owner>/<repo>/releases/download/latest/install.sh | bash
#
# Environment overrides:
#   AGENTSVIEW_REPO            owner/repo that publishes the rolling release
#   AGENTSVIEW_INSTALL_DIR     install directory (default: ~/.local/bin)
#   AGENTSVIEW_NO_MODIFY_PATH  set to 1 to leave shell startup files alone
#   AGENTSVIEW_SKIP_CHECKSUM   set to 1 to skip SHA256SUMS verification

set -euo pipefail

# The release workflow replaces the placeholder when it publishes this
# script. Running the unpublished copy requires AGENTSVIEW_REPO.
DEFAULT_REPO="@AGENTSVIEW_REPO@"
RELEASE_TAG="latest"
BINARY_NAME="agentsview"
ASSET_NAME="agentsview-linux-amd64"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${GREEN}$1${NC}"; }
warn() { echo -e "${YELLOW}$1${NC}"; }
error() { echo -e "${RED}$1${NC}" >&2; exit 1; }

resolve_repo() {
    local repo="${AGENTSVIEW_REPO:-$DEFAULT_REPO}"
    case "$repo" in
        *@*|"") error "Repository unknown. Set AGENTSVIEW_REPO=<owner>/<repo>." ;;
    esac
    echo "$repo"
}

check_platform() {
    if [ "$(uname -s)" != "Linux" ]; then
        error "Rolling builds are published for Linux only. Use install.sh for other platforms."
    fi
    case "$(uname -m)" in
        x86_64|amd64) ;;
        *) error "Rolling builds are published for linux/amd64 only (found $(uname -m))." ;;
    esac
}

download() {
    local url="$1"
    local output="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL "$url" -o "$output"
    elif command -v wget &>/dev/null; then
        wget -q "$url" -O "$output"
    else
        error "Neither curl nor wget found"
    fi
}

verify_checksum() {
    local file="$1"
    local checksums_file="$2"
    local filename="$3"

    local expected
    expected=$(awk -v f="$filename" '{gsub(/^\*/, "", $2); if ($2==f) {print $1; exit}}' "$checksums_file")
    if [ -z "$expected" ]; then
        error "No checksum found for $filename in SHA256SUMS"
    fi

    local actual
    if command -v sha256sum &>/dev/null; then
        actual=$(sha256sum "$file" | cut -d' ' -f1)
    elif command -v shasum &>/dev/null; then
        actual=$(shasum -a 256 "$file" | cut -d' ' -f1)
    else
        error "No sha256 tool available. Install coreutils or set AGENTSVIEW_SKIP_CHECKSUM=1 to bypass."
    fi

    if [ "$expected" != "$actual" ]; then
        error "Checksum verification failed!\n  Expected: $expected\n  Actual:   $actual"
    fi

    info "Checksum verified"
}

# Prints the startup file for the user's login shell. $SHELL is used
# rather than the running shell because `curl ... | bash` always runs
# bash, even for zsh or fish users.
shell_rc_file() {
    case "$(basename "${SHELL:-}")" in
        zsh) echo "${ZDOTDIR:-$HOME}/.zshrc" ;;
        bash) echo "$HOME/.bashrc" ;;
        fish) echo "${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish" ;;
        *) echo "$HOME/.profile" ;;
    esac
}

# Prints the directory as it should appear in a startup file, using
# $HOME instead of an absolute home path when possible.
portable_dir() {
    local dir="$1"
    case "$dir" in
        "$HOME"/*) echo "\$HOME/${dir#"$HOME"/}" ;;
        *) echo "$dir" ;;
    esac
}

path_contains() {
    case ":$PATH:" in
        *":$1:"*|*":$1/:"*) return 0 ;;
        *) return 1 ;;
    esac
}

rc_mentions_dir() {
    local rc="$1"
    local dir="$2"
    [ -f "$rc" ] || return 1
    grep -qF "$dir" "$rc" && return 0
    case "$dir" in
        "$HOME"/*)
            local rel="${dir#"$HOME"/}"
            grep -qF "\$HOME/$rel" "$rc" && return 0
            grep -qF "\${HOME}/$rel" "$rc" && return 0
            grep -qF "~/$rel" "$rc" && return 0
            ;;
    esac
    return 1
}

# Makes sure new shells find the install directory. Sets RC_TO_RELOAD
# when the user must reload a startup file to pick up the change.
ensure_path() {
    local dir="$1"
    local rc
    rc=$(shell_rc_file)

    if path_contains "$dir"; then
        info "$dir is already in PATH"
        return 0
    fi

    if rc_mentions_dir "$rc" "$dir"; then
        warn "$rc already adds $dir to PATH, but this shell has not loaded it yet."
        RC_TO_RELOAD="$rc"
        return 0
    fi

    if [ "${AGENTSVIEW_NO_MODIFY_PATH:-0}" = "1" ]; then
        warn "$dir is not in PATH. Add it to $rc:"
        echo "  export PATH=\"$(portable_dir "$dir"):\$PATH\""
        return 0
    fi

    local line
    if [ "$(basename "${SHELL:-}")" = "fish" ]; then
        line="fish_add_path \"$(portable_dir "$dir")\""
    else
        line="export PATH=\"$(portable_dir "$dir"):\$PATH\""
    fi

    mkdir -p "$(dirname "$rc")"
    printf '\n# Added by the agentsview installer\n%s\n' "$line" >> "$rc"
    info "Added $dir to PATH in $rc"
    RC_TO_RELOAD="$rc"
}

install_binary() {
    local src="$1"
    local install_dir="$2"

    mkdir -p "$install_dir"
    # Copy beside the destination, then rename, so a running agentsview
    # keeps its old file and the new one appears atomically.
    local tmp
    tmp=$(mktemp "$install_dir/.${BINARY_NAME}.tmp.XXXXXX")
    if ! cp "$src" "$tmp" || ! chmod 755 "$tmp" || ! mv -f "$tmp" "$install_dir/$BINARY_NAME"; then
        rm -f "$tmp"
        error "Failed to install $BINARY_NAME to $install_dir"
    fi
}

main() {
    info "Installing the agentsview rolling build..."
    echo

    check_platform
    local repo
    repo=$(resolve_repo)
    local install_dir="${AGENTSVIEW_INSTALL_DIR:-$HOME/.local/bin}"
    local base_url="https://github.com/${repo}/releases/download/${RELEASE_TAG}"

    info "Release: ${repo} (${RELEASE_TAG})"
    info "Install directory: ${install_dir}"
    echo

    local tmpdir
    tmpdir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmpdir'" EXIT

    info "Downloading ${ASSET_NAME}..."
    if ! download "${base_url}/${ASSET_NAME}" "$tmpdir/$ASSET_NAME"; then
        error "Download failed. Check https://github.com/${repo}/releases/tag/${RELEASE_TAG}"
    fi

    if [ "${AGENTSVIEW_SKIP_CHECKSUM:-0}" = "1" ]; then
        warn "Checksum verification skipped (AGENTSVIEW_SKIP_CHECKSUM=1)"
    else
        if ! download "${base_url}/SHA256SUMS" "$tmpdir/SHA256SUMS"; then
            error "Failed to download SHA256SUMS. Set AGENTSVIEW_SKIP_CHECKSUM=1 to bypass."
        fi
        verify_checksum "$tmpdir/$ASSET_NAME" "$tmpdir/SHA256SUMS" "$ASSET_NAME"
    fi

    install_binary "$tmpdir/$ASSET_NAME" "$install_dir"
    info "Installed ${install_dir}/${BINARY_NAME}"
    echo

    RC_TO_RELOAD=""
    ensure_path "$install_dir"

    local found
    found=$(command -v "$BINARY_NAME" 2>/dev/null || true)
    if [ -n "$found" ] && [ "$found" != "$install_dir/$BINARY_NAME" ]; then
        warn "Another agentsview at $found comes first in PATH and will run instead."
    fi

    echo
    info "Installation complete!"
    if [ -n "$RC_TO_RELOAD" ]; then
        echo
        warn "Refresh your shell to use agentsview:"
        echo "  source $RC_TO_RELOAD    # or open a new terminal"
    fi
    echo
    echo "Get started:"
    echo "  agentsview serve    # Start the server and open browser"
    echo
    echo "To update, run this installer again. 'agentsview update' installs"
    echo "the upstream stable release instead of this rolling build."
}

# Guard: only run main when executed directly, not when sourced.
# ${BASH_SOURCE[0]-} defaults to empty when piped via stdin
# (curl ... | bash), which we treat as direct execution.
if [[ "${BASH_SOURCE[0]-}" == "${0}" || -z "${BASH_SOURCE[0]-}" ]]; then
    main "$@"
fi

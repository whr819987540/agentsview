#!/bin/bash
# agentsview rolling-build installer for Linux
#
# Installs the newest build that .github/workflows/rolling-release.yml
# publishes on every push to main. The workflow attaches this script to
# the rolling "latest" prerelease as install.sh with the repository
# filled in:
#
#   curl -fsSL https://github.com/<owner>/<repo>/releases/download/latest/install.sh | bash
#
# "latest" is updated in place on every push, so the installer reads
# only its BUILD_TAG file there. SHA256SUMS and the binary come from the
# immutable build-YYYYMMDD-<sha> release that BUILD_TAG names, so a push
# during the install cannot mix files from two builds.
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
LATEST_TAG="latest"
# Snapshot tags look like build-20260102-0123abcd. Explicit character
# lists keep locale collation from widening the ranges.
BUILD_TAG_PATTERN='^build-[0123456789]{8}-[0123456789abcdef]{7,40}$'
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

# Prints the snapshot tag named by the rolling release's BUILD_TAG file.
# Exits when the file is missing or invalid, which can happen while the
# workflow is republishing "latest". There is deliberately no fallback
# to the assets on "latest".
resolve_build_tag() {
    local repo="$1"
    local tmpdir="$2"
    local url="https://github.com/${repo}/releases/download/${LATEST_TAG}/BUILD_TAG"
    local retry="The rolling release may be mid-publish; try again in a few minutes."

    if ! download "$url" "$tmpdir/BUILD_TAG"; then
        error "Could not read the current build from $url\n$retry"
    fi

    local tag
    tag=$(cat "$tmpdir/BUILD_TAG")
    # Trim leading and trailing whitespace, including CRLF line endings.
    tag="${tag#"${tag%%[![:space:]]*}"}"
    tag="${tag%"${tag##*[![:space:]]}"}"
    if ! [[ "$tag" =~ $BUILD_TAG_PATTERN ]]; then
        error "BUILD_TAG on the rolling release does not name a build.\n$retry"
    fi
    echo "$tag"
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

# Prints the ways a startup file might spell the directory, one per line.
dir_spellings() {
    local dir="$1"
    [ "$dir" = "/" ] || dir="${dir%/}"
    echo "$dir"
    case "$dir" in
        "$HOME"/*)
            local rel="${dir#"$HOME"/}"
            echo "\$HOME/$rel"
            echo "\${HOME}/$rel"
            echo "~/$rel"
            ;;
    esac
}

# Reports whether a startup file line uses the spelling as a whole PATH
# element: after a start of line, whitespace, quote, ":" or "=", and
# before an optional "/" and then an end of line, whitespace, quote or
# ":". Comments do not count, and neither do longer paths that start
# with the spelling, such as ~/.local/binaries.
line_names_dir() {
    local line="$1"
    local spelling="$2"
    # Skip comment lines, then drop any trailing comment.
    line="${line#"${line%%[![:space:]]*}"}"
    case "$line" in
        "#"*) return 1 ;;
    esac
    line="${line%%[[:space:]]#*}"
    # Pad so the start and end of the line count as separators.
    line=" $line "
    [[ "$line" == *[[:space:]:=\"\']"$spelling"[[:space:]:\"\']* ]] && return 0
    [[ "$line" == *[[:space:]:=\"\']"$spelling"/[[:space:]:\"\']* ]] && return 0
    return 1
}

rc_mentions_dir() {
    local rc="$1"
    local dir="$2"
    [ -f "$rc" ] || return 1
    local spellings=()
    local spelling
    while IFS= read -r spelling; do
        spellings+=("$spelling")
    done < <(dir_spellings "$dir")
    local line
    while IFS= read -r line || [ -n "$line" ]; do
        for spelling in "${spellings[@]}"; do
            line_names_dir "$line" "$spelling" && return 0
        done
    done < "$rc"
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

    local tmpdir
    tmpdir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmpdir'" EXIT

    info "Release: ${repo} (${LATEST_TAG})"
    local build_tag
    build_tag=$(resolve_build_tag "$repo" "$tmpdir")
    local base_url="https://github.com/${repo}/releases/download/${build_tag}"
    info "Build: ${build_tag}"
    info "Install directory: ${install_dir}"
    echo

    info "Downloading ${ASSET_NAME}..."
    if ! download "${base_url}/${ASSET_NAME}" "$tmpdir/$ASSET_NAME"; then
        error "Download failed. Check https://github.com/${repo}/releases/tag/${build_tag}"
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
    info "Installed ${install_dir}/${BINARY_NAME} (${build_tag})"
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
    echo "To update to the newest rolling build later:"
    echo "  agentsview update"
}

# Guard: only run main when executed directly, not when sourced.
# ${BASH_SOURCE[0]-} defaults to empty when piped via stdin
# (curl ... | bash), which we treat as direct execution.
if [[ "${BASH_SOURCE[0]-}" == "${0}" || -z "${BASH_SOURCE[0]-}" ]]; then
    main "$@"
fi

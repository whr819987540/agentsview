#!/bin/bash
# Produces the CJK full-text search sidecar archive for a rolling build.
#
#   scripts/rolling-sidecar.sh <owner/repo> <output.tar.gz>
#
# The archive holds the simple SQLite tokenizer and its cppjieba
# dictionaries as scripts/build-simple-fts.sh lays them out, plus a
# BUILD_INPUTS file. The rolling installer and `agentsview update`
# unpack it where the daemon looks for the sidecar.
#
# internal/db fingerprints the sidecar's library and dictionaries and
# rebuilds its CJK index whenever they change. The C++ build is not
# byte-for-byte reproducible, so building on every push would make every
# update reindex. This script therefore reuses the archive of the current
# rolling build when BUILD_INPUTS shows it came from the same inputs, and
# builds a new one only when they changed.
#
# When GITHUB_OUTPUT is set, it records built=true or built=false there.
set -euo pipefail

# Bump when the archive layout changes without build-simple-fts.sh
# changing, so the next build packs a fresh archive.
PACKAGE_VERSION=1
ASSET_NAME="agentsview-simple-linux-amd64.tar.gz"
BUILD_TAG_PATTERN='^build-[0123456789]{8}-[0123456789abcdef]{7,40}$'
# The binary is built on manylinux_2_28; the sidecar must not need more.
MAX_GLIBC="2.28"
SIDECAR_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Prints what the archive is built from.
sidecar_inputs() {
    local script_sum
    script_sum=$(sha256sum "$SIDECAR_SCRIPT_DIR/build-simple-fts.sh" | cut -d' ' -f1)
    printf 'build-simple-fts.sh %s package %s\n' "$script_sum" "$PACKAGE_VERSION"
}

fetch() {
    curl -fsSL --retry 3 "$1" -o "$2"
}

# Copies the current rolling build's archive to out when it verifies
# against that build's SHA256SUMS and was built from inputs. Fails, with
# the reason on stdout, otherwise.
reuse_previous() {
    local repo="$1" inputs="$2" out="$3" work="$4"
    local root="https://github.com/$repo/releases/download"

    if ! fetch "$root/latest/BUILD_TAG" "$work/BUILD_TAG"; then
        echo "No rolling build to take a sidecar from."
        return 1
    fi
    local tag
    tag=$(tr -d '[:space:]' < "$work/BUILD_TAG")
    if ! [[ "$tag" =~ $BUILD_TAG_PATTERN ]]; then
        echo "latest/BUILD_TAG does not name a build."
        return 1
    fi
    if ! fetch "$root/$tag/SHA256SUMS" "$work/SHA256SUMS"; then
        echo "Could not read SHA256SUMS of $tag."
        return 1
    fi
    local expected
    expected=$(awk -v f="$ASSET_NAME" '{n=$2; sub(/^\*/, "", n); if (n==f) {print $1; exit}}' "$work/SHA256SUMS")
    if [ -z "$expected" ]; then
        echo "$tag has no sidecar."
        return 1
    fi
    if ! fetch "$root/$tag/$ASSET_NAME" "$work/$ASSET_NAME"; then
        echo "Could not download the sidecar of $tag."
        return 1
    fi
    if [ "$(sha256sum "$work/$ASSET_NAME" | cut -d' ' -f1)" != "$expected" ]; then
        echo "The sidecar of $tag does not match its SHA256SUMS."
        return 1
    fi
    local previous
    previous=$(tar -xzOf "$work/$ASSET_NAME" BUILD_INPUTS 2>/dev/null) || previous=""
    if [ "$previous" != "$inputs" ]; then
        echo "The sidecar of $tag was built from other inputs: ${previous:-unknown}."
        return 1
    fi
    cp "$work/$ASSET_NAME" "$out"
    echo "Reusing the sidecar of $tag."
}

build_sidecar() {
    bash "$SIDECAR_SCRIPT_DIR/build-simple-fts.sh" "$1"
}

# Fails when the library needs a newer glibc than the binary does.
check_glibc() {
    local highest
    highest=$(objdump -T "$1" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sed 's/GLIBC_//' | sort -V | tail -1)
    echo "Highest glibc symbol in $(basename "$1"): ${highest:-none} (max allowed: $MAX_GLIBC)"
    if [ -n "$highest" ] && [ "$(printf '%s\n%s\n' "$MAX_GLIBC" "$highest" | sort -V | tail -1)" != "$MAX_GLIBC" ]; then
        echo "The sidecar needs glibc $highest; the binary supports $MAX_GLIBC." >&2
        return 1
    fi
}

# Packs dir into out. Sorted names, fixed times and owners, and gzip -n
# keep the archive identical for identical files.
package_sidecar() {
    local dir="$1" inputs="$2" out="$3"
    local required=(libsimple.so VERSIONS dict licenses)
    local name
    for name in "${required[@]}"; do
        if [ ! -e "$dir/$name" ]; then
            echo "Sidecar build is missing $name." >&2
            return 1
        fi
    done
    printf '%s\n' "$inputs" > "$dir/BUILD_INPUTS"
    (cd "$dir" && tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
        -cf - libsimple.so dict licenses VERSIONS BUILD_INPUTS) | gzip -9n > "$out"
}

record_output() {
    if [ -n "${GITHUB_OUTPUT:-}" ]; then
        echo "built=$1" >> "$GITHUB_OUTPUT"
    fi
}

main() {
    if [ "$#" -ne 2 ]; then
        echo "usage: $0 <owner/repo> <output.tar.gz>" >&2
        return 2
    fi
    local repo="$1" out="$2"
    local work
    work=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$work'" RETURN

    local inputs
    inputs=$(sidecar_inputs)
    echo "Sidecar inputs: $inputs"
    if reuse_previous "$repo" "$inputs" "$out" "$work"; then
        record_output false
        return 0
    fi

    echo "Building a new sidecar."
    build_sidecar "$work/simple" || return 1
    package_sidecar "$work/simple" "$inputs" "$out" || return 1
    if ! check_glibc "$work/simple/libsimple.so"; then
        rm -f "$out"
        return 1
    fi
    record_output true
    echo "Packed $out."
}

if [[ "${BASH_SOURCE[0]-}" == "${0}" ]]; then
    main "$@"
fi

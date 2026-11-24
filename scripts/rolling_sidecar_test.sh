#!/bin/bash
# Tests for rolling-sidecar.sh. Sources the script and runs its real main
# function with releases served from a local fixture directory and a
# stand-in for the C++ build.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=rolling-sidecar.sh
source "$SCRIPT_DIR/rolling-sidecar.sh"
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

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
RELEASES="$WORK/releases"
PREV="build-20260101-1111aaaa"
INPUTS=$(sidecar_inputs)

# Serve downloads from $RELEASES/<tag>/<file>.
fetch() {
    local rel="${1#https://github.com/example/agentsview/releases/download/}"
    [ -f "$RELEASES/$rel" ] || return 22
    cp "$RELEASES/$rel" "$2"
}

# Stands in for build-simple-fts.sh and counts its runs.
fresh_build() {
    echo build >> "$WORK/builds"
    mkdir -p "$1/dict" "$1/licenses"
    printf 'fresh lib\n' > "$1/libsimple.so"
    printf 'fresh dict\n' > "$1/dict/jieba.dict.utf8"
    printf 'license\n' > "$1/licenses/simple-LICENSE"
    printf 'simple fresh\n' > "$1/VERSIONS"
}
build_sidecar() { fresh_build "$1"; }
check_glibc() { :; }

# Publishes PREV as the rolling build, with a sidecar whose BUILD_INPUTS
# holds the given text. An empty text publishes no sidecar.
publish_previous() {
    local build_inputs="$1"
    rm -rf "$RELEASES"
    mkdir -p "$RELEASES/latest" "$RELEASES/$PREV" "$WORK/prev"
    printf '%s\n' "$PREV" > "$RELEASES/latest/BUILD_TAG"
    printf 'binary\n' > "$RELEASES/$PREV/agentsview-linux-amd64"
    if [ -n "$build_inputs" ]; then
        printf 'previous lib\n' > "$WORK/prev/libsimple.so"
        printf '%s\n' "$build_inputs" > "$WORK/prev/BUILD_INPUTS"
        tar -czf "$RELEASES/$PREV/$ASSET_NAME" -C "$WORK/prev" libsimple.so BUILD_INPUTS
        (cd "$RELEASES/$PREV" && sha256sum agentsview-linux-amd64 "$ASSET_NAME" > SHA256SUMS)
    else
        (cd "$RELEASES/$PREV" && sha256sum agentsview-linux-amd64 > SHA256SUMS)
    fi
}

# Runs main and leaves its output, status, build count, and GITHUB_OUTPUT.
run_sidecar() {
    rm -f "$WORK/builds" "$WORK/out.tar.gz" "$WORK/github-output"
    OUTPUT=$(GITHUB_OUTPUT="$WORK/github-output" main example/agentsview "$WORK/out.tar.gz" 2>&1)
    STATUS=$?
    BUILDS=$(wc -l < "$WORK/builds" 2>/dev/null || echo 0)
    BUILDS=$((BUILDS + 0))
    GH_OUTPUT=$(cat "$WORK/github-output" 2>/dev/null)
}

archive_listing() { tar -tzf "$WORK/out.tar.gz" | LC_ALL=C sort; }

echo "=== rolling-sidecar.sh ==="

# The previous build's sidecar came from the same inputs: reuse its bytes.
publish_previous "$INPUTS"
run_sidecar
assert_eq "reuse succeeds" 0 "$STATUS"
assert_eq "reuse does not build" 0 "$BUILDS"
assert_eq "reused archive is byte-identical" \
    "$(sha256sum < "$RELEASES/$PREV/$ASSET_NAME")" "$(sha256sum < "$WORK/out.tar.gz")"
assert_eq "reuse is recorded" "built=false" "$GH_OUTPUT"
assert_contains "names the reused build" "Reusing the sidecar of $PREV." "$OUTPUT"

# Every reason not to reuse leads to a fresh build.
declare -A cases=(
    ["other inputs"]="other inputs"
    ["no sidecar in the previous build"]="no sidecar"
    ["previous sidecar fails its checksum"]="tampered"
    ["previous sidecar is missing"]="missing"
    ["no rolling build yet"]="no latest"
    ["BUILD_TAG names no build"]="bad tag"
)
for desc in "${!cases[@]}"; do
    case "${cases[$desc]}" in
        "other inputs") publish_previous "build-simple-fts.sh 0000 package 1" ;;
        "no sidecar") publish_previous "" ;;
        "tampered")
            publish_previous "$INPUTS"
            printf 'tampered\n' >> "$RELEASES/$PREV/$ASSET_NAME"
            ;;
        "missing")
            publish_previous "$INPUTS"
            rm "$RELEASES/$PREV/$ASSET_NAME"
            ;;
        "no latest")
            publish_previous "$INPUTS"
            rm "$RELEASES/latest/BUILD_TAG"
            ;;
        "bad tag")
            publish_previous "$INPUTS"
            printf '<html>Not Found</html>\n' > "$RELEASES/latest/BUILD_TAG"
            ;;
    esac
    run_sidecar
    assert_eq "$desc: succeeds" 0 "$STATUS"
    assert_eq "$desc: builds once" 1 "$BUILDS"
    assert_eq "$desc: records the build" "built=true" "$GH_OUTPUT"
    assert_eq "$desc: packs the fresh build" "fresh lib" "$(tar -xzOf "$WORK/out.tar.gz" libsimple.so)"
done

# A fresh archive holds the sidecar layout and its inputs.
publish_previous ""
run_sidecar
assert_eq "fresh archive lists the sidecar files" "BUILD_INPUTS
VERSIONS
dict/
dict/jieba.dict.utf8
libsimple.so
licenses/
licenses/simple-LICENSE" "$(archive_listing)"
assert_eq "fresh archive records its inputs" "$INPUTS" "$(tar -xzOf "$WORK/out.tar.gz" BUILD_INPUTS)"

# Files with other timestamps pack to the same bytes, so a rebuild that
# produces identical files keeps the archive checksum.
first=$(sha256sum < "$WORK/out.tar.gz")
build_sidecar() {
    fresh_build "$1"
    find "$1" -exec touch -d '2001-02-03 04:05:06' {} +
}
run_sidecar
assert_eq "packing ignores file times" "$first" "$(sha256sum < "$WORK/out.tar.gz")"

# A build that lacks a required file is not packed.
build_sidecar() {
    echo build >> "$WORK/builds"
    mkdir -p "$1/dict" "$1/licenses"
    printf 'fresh dict\n' > "$1/dict/jieba.dict.utf8"
    printf 'simple fresh\n' > "$1/VERSIONS"
}
run_sidecar
assert_eq "incomplete build fails" 1 "$STATUS"
assert_contains "names the missing file" "Sidecar build is missing libsimple.so." "$OUTPUT"
assert_eq "incomplete build writes no archive" "no" "$([ -e "$WORK/out.tar.gz" ] && echo yes || echo no)"

# main needs both arguments.
OUTPUT=$(main example/agentsview 2>&1)
assert_eq "missing argument fails with usage" 2 "$?"
assert_contains "prints usage" "usage:" "$OUTPUT"

echo
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]

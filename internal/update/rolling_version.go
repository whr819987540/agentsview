package update

import (
	"regexp"
	"strconv"
)

// rollingVersionPattern matches the clean `git describe --tags --match 'v*'`
// output that rolling builds embed: vX.Y.Z on a tagged commit, or
// vX.Y.Z-N-g<hex> for a commit N commits past that tag. The leading "v" is
// optional, as it is for the other version helpers in this package.
var rollingVersionPattern = regexp.MustCompile(
	`^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?$`,
)

// rollingPosition orders a rolling build: its tag's semver base, then the
// number of commits since that tag.
type rollingPosition [4]uint64

func parseRollingVersion(v string) (rollingPosition, bool) {
	m := rollingVersionPattern.FindStringSubmatch(v)
	if m == nil {
		return rollingPosition{}, false
	}
	var pos rollingPosition
	for i, field := range m[1:] {
		if field == "" {
			// A plain vX.Y.Z is the tagged commit itself: N is 0.
			continue
		}
		n, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return rollingPosition{}, false
		}
		pos[i] = n
	}
	return pos, true
}

// IsRollingBuildVersion reports whether v is a version IsNewerRollingBuild
// can order: vX.Y.Z or vX.Y.Z-N-g<hex>. Dirty builds, prerelease tags, bare
// commit hashes, and other strings are not.
func IsRollingBuildVersion(v string) bool {
	_, ok := parseRollingVersion(v)
	return ok
}

// IsNewerRollingBuild reports whether the current rolling build is strictly
// newer than the running one. It compares the semver base first and then the
// commit count since that tag, so v0.44.0-40-g... is newer than v0.44.0 and
// v0.44.0-10-g... is newer than v0.44.0-9-g.... Semver ordering cannot be
// used here because it treats v0.44.0-40-g... as a prerelease of v0.44.0.
// It returns false when either version fails IsRollingBuildVersion or both
// name the same position.
func IsNewerRollingBuild(current, running string) bool {
	cur, ok := parseRollingVersion(current)
	if !ok {
		return false
	}
	run, ok := parseRollingVersion(running)
	if !ok {
		return false
	}
	for i := range cur {
		if cur[i] != run[i] {
			return cur[i] > run[i]
		}
	}
	return false
}

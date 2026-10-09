package update

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// rollingRepo is the owner/repo whose rolling "latest" prerelease this
// binary updates from. The rolling-release workflow sets it with
// -ldflags "-X go.kenn.io/agentsview/internal/update.rollingRepo=owner/repo".
// When empty, updates come from the upstream stable releases.
var rollingRepo string

const (
	rollingReleaseTag     = "latest"
	rollingBuildTagAsset  = "BUILD_TAG"
	rollingVersionAsset   = "VERSION"
	rollingChecksumsAsset = "SHA256SUMS"
	rollingCacheFileName  = "update_check_rolling.json"
	maxRollingTextSize    = 256
)

// rollingBuildTagPattern matches the immutable snapshot release that
// every main build is published to, such as build-20261008-1a2b3c4d.
var rollingBuildTagPattern = regexp.MustCompile(
	`^build-[0-9]{8}-[0-9a-f]{7,40}$`,
)

// RollingRepo returns the repository this binary updates from when it
// is a rolling build, or "" when it follows upstream stable releases.
func RollingRepo() string {
	return rollingRepo
}

// rollingDownloadRoot is the release download root of repo. Release
// assets live at <root>/<tag>/<asset>.
func rollingDownloadRoot(repo string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download", repo)
}

// rollingAssetName is the raw binary the rolling release publishes for
// a platform, such as agentsview-linux-amd64.
func rollingAssetName(goos, goarch string) string {
	name := fmt.Sprintf("agentsview-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// checkRollingUpdate compares the running version with the newest
// rolling build published under downloadRoot. Any difference counts as
// an update because the rolling release always holds the newest main
// build.
//
// The mutable "latest" release is read only for its BUILD_TAG asset,
// which names the immutable build-YYYYMMDD-<sha> snapshot of that
// build. VERSION, SHA256SUMS, and the binary all come from that
// snapshot, so a push that republishes "latest" in between cannot mix
// files from two builds.
//
// A non-forced check reads only BUILD_TAG and VERSION and caches the
// result for devCacheDuration, because the channel publishes on every
// push. When an update exists it returns display-only info whose
// NeedsRefetch is true. A forced check, which installs use, also reads
// the checksum and size of this platform's binary.
func checkRollingUpdate(ctx context.Context,
	downloadRoot, currentVersion string,
	forceCheck bool,
	cacheDir string,
) (*UpdateInfo, error) {
	if !forceCheck {
		if cached, err := loadCacheFile(cacheDir, rollingCacheFileName); err == nil &&
			cached.Version != "" &&
			time.Since(cached.CheckedAt) < devCacheDuration {
			return rollingDisplayInfo(currentVersion, cached.Version), nil
		}
	}

	buildTag, err := fetchRollingBuildTag(ctx,
		downloadRoot+"/"+rollingReleaseTag+"/"+rollingBuildTagAsset,
	)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	snapshotBase := downloadRoot + "/" + buildTag

	latestVersion, err := fetchRollingVersion(ctx,
		snapshotBase+"/"+rollingVersionAsset,
	)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	saveCacheFile(latestVersion, cacheDir, rollingCacheFileName)

	info := rollingDisplayInfo(currentVersion, latestVersion)
	if info == nil || !forceCheck {
		return info, nil
	}
	return withRollingDownload(ctx, info, snapshotBase)
}

// rollingDisplayInfo describes an update to latestVersion without the
// download details an install needs, or returns nil when latestVersion
// is the running build.
func rollingDisplayInfo(currentVersion, latestVersion string) *UpdateInfo {
	if latestVersion == currentVersion {
		return nil
	}
	return &UpdateInfo{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		rawBinary:      true,
		cacheOnly:      true,
	}
}

// withRollingDownload completes display-only info with the checksum and
// size of this platform's binary in the snapshot at snapshotBase.
func withRollingDownload(ctx context.Context,
	info *UpdateInfo, snapshotBase string,
) (*UpdateInfo, error) {
	assetName := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	checksum, err := fetchChecksumFromFile(ctx,
		snapshotBase+"/"+rollingChecksumsAsset, assetName,
	)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	if checksum == "" {
		return nil, fmt.Errorf(
			"no rolling build for %s/%s", runtime.GOOS, runtime.GOARCH,
		)
	}

	downloadURL := snapshotBase + "/" + assetName
	size, err := fetchContentLength(ctx, downloadURL)
	if err != nil {
		return nil, fmt.Errorf(
			"no rolling build for %s/%s: %w",
			runtime.GOOS, runtime.GOARCH, err,
		)
	}

	full := *info
	full.DownloadURL = downloadURL
	full.AssetName = assetName
	full.Size = size
	full.Checksum = checksum
	full.cacheOnly = false
	return &full, nil
}

// fetchRollingBuildTag reads the one-line BUILD_TAG asset of the
// "latest" release, which names the snapshot release of its build.
// Without it there is no consistent set of files to read, so the check
// fails instead of falling back to the mutable "latest" assets.
func fetchRollingBuildTag(ctx context.Context, url string) (string, error) {
	body, err := fetchSmallAsset(ctx, url, maxRollingTextSize)
	if err == nil {
		tag := strings.TrimSpace(body)
		if rollingBuildTagPattern.MatchString(tag) {
			return tag, nil
		}
		err = fmt.Errorf("invalid build tag %q", tag)
	}
	return "", fmt.Errorf(
		"read rolling build tag (the rolling release may be "+
			"mid-publish; try again shortly): %w", err,
	)
}

// fetchRollingVersion reads the one-line VERSION asset that names the
// build in a rolling snapshot release.
func fetchRollingVersion(ctx context.Context, url string) (string, error) {
	body, err := fetchSmallAsset(ctx, url, maxRollingTextSize)
	if err != nil {
		return "", fmt.Errorf("fetch rolling version: %w", err)
	}
	version := strings.TrimSpace(body)
	if version == "" || strings.ContainsAny(version, " \t\r\n") {
		return "", fmt.Errorf("invalid rolling version %q", version)
	}
	return version, nil
}

// installRawBinaryTo verifies a downloaded binary and installs it at
// dstPath. Rolling releases publish bare binaries, not archives.
func installRawBinaryTo(
	binaryPath, expectedChecksum, dstPath, precomputedChecksum string,
) error {
	if err := verifyChecksum(
		binaryPath, expectedChecksum, precomputedChecksum,
	); err != nil {
		return err
	}
	return installBinaryTo(binaryPath, dstPath)
}

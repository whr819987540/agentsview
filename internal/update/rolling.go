package update

import (
	"context"
	"fmt"
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
	rollingVersionAsset   = "VERSION"
	rollingCacheFileName  = "update_check_rolling.json"
	maxRollingVersionSize = 256
)

// RollingRepo returns the repository this binary updates from when it
// is a rolling build, or "" when it follows upstream stable releases.
func RollingRepo() string {
	return rollingRepo
}

func rollingDownloadBase(repo string) string {
	return fmt.Sprintf(
		"https://github.com/%s/releases/download/%s", repo, rollingReleaseTag,
	)
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

// checkRollingUpdate compares the running version with the VERSION
// asset of the rolling release at baseURL. Any difference counts as an
// update because the rolling release always holds the newest main build.
func checkRollingUpdate(ctx context.Context,
	baseURL, currentVersion string,
	forceCheck bool,
	cacheDir string,
) (*UpdateInfo, error) {
	if !forceCheck {
		if cached, err := loadCacheFile(cacheDir, rollingCacheFileName); err == nil &&
			time.Since(cached.CheckedAt) < cacheDuration &&
			cached.Version == currentVersion {
			return nil, nil
		}
	}

	latestVersion, err := fetchRollingVersion(ctx, baseURL+"/"+rollingVersionAsset)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	saveCacheFile(latestVersion, cacheDir, rollingCacheFileName)

	if latestVersion == currentVersion {
		return nil, nil
	}

	assetName := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	checksum, err := fetchChecksumFromFile(ctx, baseURL+"/SHA256SUMS", assetName)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	if checksum == "" {
		return nil, fmt.Errorf(
			"no rolling build for %s/%s", runtime.GOOS, runtime.GOARCH,
		)
	}

	downloadURL := baseURL + "/" + assetName
	size, err := fetchContentLength(ctx, downloadURL)
	if err != nil {
		return nil, fmt.Errorf(
			"no rolling build for %s/%s: %w",
			runtime.GOOS, runtime.GOARCH, err,
		)
	}

	return &UpdateInfo{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		DownloadURL:    downloadURL,
		AssetName:      assetName,
		Size:           size,
		Checksum:       checksum,
		rawBinary:      true,
	}, nil
}

// fetchRollingVersion reads the one-line VERSION asset that names the
// build in the rolling release.
func fetchRollingVersion(ctx context.Context, url string) (string, error) {
	body, err := fetchSmallAsset(ctx, url, maxRollingVersionSize)
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

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The rolling release publishes the CJK full-text search sidecar beside the
// binary: the simple SQLite tokenizer and its cppjieba dictionaries, packed
// as agentsview-simple-<os>-<arch>.tar.gz. internal/db loads it at startup
// from AGENTSVIEW_SIMPLE_DIR, else from an agentsview-simple directory beside
// the executable, else from ../lib/agentsview/simple. A rolling update keeps
// the sidecar in step with the binary it installs.

const (
	// sidecarDirEnv names a sidecar the user manages. Updates leave it alone.
	sidecarDirEnv = "AGENTSVIEW_SIMPLE_DIR"
	// sidecarSkipEnv set to 1 makes updates leave the sidecar alone.
	sidecarSkipEnv = "AGENTSVIEW_SKIP_CJK_FTS"
	// sidecarMarkerName records the checksum of the archive an update or
	// the rolling installer unpacked. A directory without it was installed
	// some other way, such as make install-cjk-fts, and is left alone.
	sidecarMarkerName = ".agentsview-archive-sha256"
	// maxSidecarSize bounds a sidecar download. The archive is about 5 MB.
	maxSidecarSize = 64 << 20
	// sidecarDownloadTimeout bounds the whole sidecar download.
	sidecarDownloadTimeout = 10 * time.Minute
)

// sidecarAsset is the sidecar archive a rolling snapshot publishes for this
// platform.
type sidecarAsset struct {
	URL      string
	Name     string
	Checksum string
}

// rollingSidecarAssetName is the sidecar archive a rolling release publishes
// for a platform, such as agentsview-simple-linux-amd64.tar.gz.
func rollingSidecarAssetName(goos, goarch string) string {
	return fmt.Sprintf("agentsview-simple-%s-%s.tar.gz", goos, goarch)
}

// sidecarLibraryName is the tokenizer library file internal/db loads.
func sidecarLibraryName(goos string) string {
	switch goos {
	case "darwin":
		return "libsimple.dylib"
	case "windows":
		return "simple.dll"
	default:
		return "libsimple.so"
	}
}

// sidecarTarget returns the directory the daemon started from exePath loads
// the sidecar from when AGENTSVIEW_SIMPLE_DIR is unset: an existing
// agentsview-simple entry beside the executable, else
// ../lib/agentsview/simple, the directory make install-cjk-fts uses.
func sidecarTarget(exePath string) string {
	dir := filepath.Dir(exePath)
	beside := filepath.Join(dir, "agentsview-simple")
	if _, err := os.Lstat(beside); err == nil {
		return beside
	}
	return filepath.Clean(filepath.Join(dir, "..", "lib", "agentsview", "simple"))
}

// installedSidecar reports what sits at target: nothing, a directory an
// update installed (with the archive checksum it recorded), or a directory
// installed some other way.
func installedSidecar(target string) (present, managed bool, checksum string, err error) {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, "", nil
	}
	if err != nil {
		return false, false, "", fmt.Errorf("check %s: %w", target, err)
	}
	if !info.IsDir() {
		return true, false, "", fmt.Errorf("%s is not a directory", target)
	}
	data, err := os.ReadFile(filepath.Join(target, sidecarMarkerName))
	if errors.Is(err, os.ErrNotExist) {
		return true, false, "", nil
	}
	if err != nil {
		return true, false, "", fmt.Errorf("read %s marker: %w", target, err)
	}
	return true, true, strings.TrimSpace(string(data)), nil
}

// installSidecar brings the sidecar used by the executable at exePath up to
// date with asset. It returns a one-line status for the user, or "" when the
// installed sidecar already matches.
//
// The archive is verified and unpacked into a sibling directory, then
// renamed into place. internal/db refuses to start with a sidecar directory
// that is incomplete, while a missing one only disables CJK search, so the
// target is never left partly written.
func installSidecar(ctx context.Context, asset *sidecarAsset, exePath string) (string, error) {
	if os.Getenv(sidecarSkipEnv) == "1" {
		return fmt.Sprintf("Skipped the CJK search extension (%s=1).", sidecarSkipEnv), nil
	}
	if dir := os.Getenv(sidecarDirEnv); dir != "" {
		return fmt.Sprintf(
			"Left the CJK search extension in %s alone (%s is set).",
			dir, sidecarDirEnv,
		), nil
	}

	target := sidecarTarget(exePath)
	present, managed, installed, err := installedSidecar(target)
	if err != nil {
		return "", err
	}
	if present && !managed {
		return fmt.Sprintf(
			"Left the CJK search extension in %s alone "+
				"(it was not installed by an update).", target,
		), nil
	}
	if managed && strings.EqualFold(installed, asset.Checksum) {
		return "", nil
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", parent, err)
	}
	staged, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".new-*")
	if err != nil {
		return "", fmt.Errorf("stage CJK search extension: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(staged)
		}
	}()

	archive := staged + ".tar.gz"
	defer os.Remove(archive)
	if err := downloadSidecar(ctx, asset.URL, archive, asset.Checksum); err != nil {
		return "", err
	}
	if err := extractTarGz(archive, staged); err != nil {
		return "", fmt.Errorf("extract %s: %w", asset.Name, err)
	}
	if err := checkSidecarLayout(staged); err != nil {
		return "", fmt.Errorf("%s: %w", asset.Name, err)
	}
	if err := os.WriteFile(
		filepath.Join(staged, sidecarMarkerName),
		[]byte(strings.ToLower(asset.Checksum)+"\n"), 0o644,
	); err != nil {
		return "", fmt.Errorf("record CJK search extension: %w", err)
	}
	if err := replaceDir(staged, target); err != nil {
		return "", err
	}
	keep = true

	if managed {
		return fmt.Sprintf("Updated the CJK search extension in %s.", target), nil
	}
	return fmt.Sprintf(
		"Installed the CJK search extension in %s. The next start indexes "+
			"existing messages for Chinese, Japanese, and Korean search.",
		target,
	), nil
}

// downloadSidecar fetches url to dest and checks its SHA-256 against
// expected while it streams.
func downloadSidecar(ctx context.Context, url, dest, expected string) error {
	ctx, cancel := context.WithTimeout(ctx, sidecarDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download CJK search extension: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download CJK search extension: GET %s: %s", url, resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, maxSidecarSize+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("download CJK search extension: %w", err)
	}
	if n > maxSidecarSize {
		return fmt.Errorf("download CJK search extension: larger than %d bytes", maxSidecarSize)
	}
	return verifyChecksum(dest, expected, hex.EncodeToString(h.Sum(nil)))
}

// checkSidecarLayout makes sure an unpacked archive has the library and
// dictionaries internal/db loads before it replaces a working sidecar.
func checkSidecarLayout(dir string) error {
	library := sidecarLibraryName(runtime.GOOS)
	if info, err := os.Stat(filepath.Join(dir, library)); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("archive has no %s", library)
	}
	if info, err := os.Stat(filepath.Join(dir, "dict")); err != nil || !info.IsDir() {
		return errors.New("archive has no dict directory")
	}
	return nil
}

// replaceDir renames staged to target. An existing target is moved aside
// first and restored if the second rename fails.
func replaceDir(staged, target string) error {
	aside := ""
	if _, err := os.Lstat(target); err == nil {
		aside = fmt.Sprintf("%s.old-%d", staged, time.Now().UnixNano())
		if err := os.Rename(target, aside); err != nil {
			return fmt.Errorf("replace %s: %w", target, err)
		}
	}
	if err := os.Rename(staged, target); err != nil {
		if aside != "" {
			if restoreErr := os.Rename(aside, target); restoreErr != nil {
				return fmt.Errorf(
					"replace %s: %w (restoring the previous copy also failed: %w)",
					target, err, restoreErr,
				)
			}
		}
		return fmt.Errorf("replace %s: %w", target, err)
	}
	if aside != "" {
		os.RemoveAll(aside)
	}
	return nil
}

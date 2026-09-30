package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rollingRelease serves a fake rolling release. Assets missing from
// the map return 404.
type rollingRelease struct {
	assets   map[string]string
	requests atomic.Int32
}

func (r *rollingRelease) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			r.requests.Add(1)
			body, ok := r.assets[filepath.Base(req.URL.Path)]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			if req.Method != http.MethodHead {
				_, _ = w.Write([]byte(body))
			}
		},
	))
	t.Cleanup(srv.Close)
	return srv.URL + "/releases/download/latest"
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newRollingRelease publishes binary for the current platform and an
// unrelated platform, with version as the VERSION asset.
func newRollingRelease(version, binary string) *rollingRelease {
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	other := rollingAssetName("plan9", "mips")
	return &rollingRelease{assets: map[string]string{
		"VERSION": version + "\n",
		asset:     binary,
		other:     "other",
		"SHA256SUMS": sha256Hex("other") + "  " + other + "\n" +
			sha256Hex(binary) + "  " + asset + "\n",
	}}
}

func TestRollingAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "agentsview-linux-amd64"},
		{"windows", "amd64", "agentsview-windows-amd64.exe"},
		{"darwin", "arm64", "agentsview-darwin-arm64"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, rollingAssetName(tt.goos, tt.goarch))
	}
}

func TestRollingDownloadBase(t *testing.T) {
	assert.Equal(t,
		"https://github.com/example/agentsview/releases/download/latest",
		rollingDownloadBase("example/agentsview"),
	)
}

func TestCheckRollingUpdateNewBuild(t *testing.T) {
	release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "new-binary")
	base := release.serve(t)
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)

	info, err := checkRollingUpdate(t.Context(),
		base, "v0.44.0-28-gaaaaaaaa", false, t.TempDir(),
	)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "v0.44.0-28-gaaaaaaaa", info.CurrentVersion)
	assert.Equal(t, "v0.44.0-30-gbbbbbbbb", info.LatestVersion)
	assert.Equal(t, base+"/"+asset, info.DownloadURL)
	assert.Equal(t, asset, info.AssetName)
	assert.Equal(t, int64(len("new-binary")), info.Size)
	assert.Equal(t, sha256Hex("new-binary"), info.Checksum)
	assert.False(t, info.IsDevBuild, "rolling updates are real updates, not dev notices")
	assert.True(t, info.rawBinary)
	assert.False(t, info.NeedsRefetch())
}

func TestCheckRollingUpdateSameBuild(t *testing.T) {
	release := newRollingRelease("v0.44.0-28-gaaaaaaaa", "binary")
	base := release.serve(t)

	info, err := checkRollingUpdate(t.Context(),
		base, "v0.44.0-28-gaaaaaaaa", false, t.TempDir(),
	)
	require.NoError(t, err)
	assert.Nil(t, info)
}

func TestCheckRollingUpdateErrors(t *testing.T) {
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{
			name:    "missing VERSION",
			mutate:  func(a map[string]string) { delete(a, "VERSION") },
			wantErr: "404",
		},
		{
			name:    "empty VERSION",
			mutate:  func(a map[string]string) { a["VERSION"] = " \n" },
			wantErr: "invalid rolling version",
		},
		{
			name:    "multi-word VERSION",
			mutate:  func(a map[string]string) { a["VERSION"] = "v1 v2\n" },
			wantErr: "invalid rolling version",
		},
		{
			name:    "missing SHA256SUMS",
			mutate:  func(a map[string]string) { delete(a, "SHA256SUMS") },
			wantErr: "failed to fetch checksums",
		},
		{
			name: "no checksum for this platform",
			mutate: func(a map[string]string) {
				a["SHA256SUMS"] = sha256Hex("x") + "  agentsview-plan9-mips\n"
			},
			wantErr: "no rolling build for " + runtime.GOOS + "/" + runtime.GOARCH,
		},
		{
			name:    "missing binary",
			mutate:  func(a map[string]string) { delete(a, asset) },
			wantErr: "no rolling build for " + runtime.GOOS + "/" + runtime.GOARCH,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "binary")
			tt.mutate(release.assets)
			base := release.serve(t)

			info, err := checkRollingUpdate(t.Context(),
				base, "v0.44.0-28-gaaaaaaaa", true, t.TempDir(),
			)
			require.Error(t, err)
			assert.Nil(t, info)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCheckRollingUpdateCache(t *testing.T) {
	const current = "v0.44.0-28-gaaaaaaaa"

	t.Run("fresh check of current build skips network", func(t *testing.T) {
		release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "binary")
		base := release.serve(t)
		cacheDir := t.TempDir()
		saveCacheFile(current, cacheDir, rollingCacheFileName)

		info, err := checkRollingUpdate(t.Context(), base, current, false, cacheDir)
		require.NoError(t, err)
		assert.Nil(t, info)
		assert.Zero(t, release.requests.Load())
	})

	t.Run("force bypasses fresh cache", func(t *testing.T) {
		release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "binary")
		base := release.serve(t)
		cacheDir := t.TempDir()
		saveCacheFile(current, cacheDir, rollingCacheFileName)

		info, err := checkRollingUpdate(t.Context(), base, current, true, cacheDir)
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "v0.44.0-30-gbbbbbbbb", info.LatestVersion)
	})

	t.Run("cached newer build refetches download details", func(t *testing.T) {
		release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "binary")
		base := release.serve(t)
		cacheDir := t.TempDir()
		saveCacheFile("v0.44.0-30-gbbbbbbbb", cacheDir, rollingCacheFileName)

		info, err := checkRollingUpdate(t.Context(), base, current, false, cacheDir)
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, sha256Hex("binary"), info.Checksum)
	})

	t.Run("expired cache refetches", func(t *testing.T) {
		release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "binary")
		base := release.serve(t)
		cacheDir := t.TempDir()
		data := fmt.Sprintf(`{"checked_at":%q,"version":%q}`,
			time.Now().Add(-2*cacheDuration).Format(time.RFC3339), current)
		require.NoError(t, os.WriteFile(
			filepath.Join(cacheDir, rollingCacheFileName), []byte(data), 0o600,
		))

		info, err := checkRollingUpdate(t.Context(), base, current, false, cacheDir)
		require.NoError(t, err)
		require.NotNil(t, info)
	})

	t.Run("check records the rolling version separately", func(t *testing.T) {
		release := newRollingRelease("v0.44.0-30-gbbbbbbbb", "binary")
		base := release.serve(t)
		cacheDir := t.TempDir()
		saveCache("v0.44.0", cacheDir)

		_, err := checkRollingUpdate(t.Context(), base, current, true, cacheDir)
		require.NoError(t, err)

		rolling, err := loadCacheFile(cacheDir, rollingCacheFileName)
		require.NoError(t, err)
		assert.Equal(t, "v0.44.0-30-gbbbbbbbb", rolling.Version)
		stable, err := loadCache(cacheDir)
		require.NoError(t, err)
		assert.Equal(t, "v0.44.0", stable.Version, "stable cache left untouched")
	})
}

func TestCheckForUpdateUsesRollingRepo(t *testing.T) {
	// Both caches are fresh, so neither path reaches the network. The
	// stable path would report a dev-build notice from its cache; the
	// rolling path sees that the running build is current.
	old := rollingRepo
	rollingRepo = "example/agentsview"
	t.Cleanup(func() { rollingRepo = old })

	cacheDir := t.TempDir()
	saveCache("v0.45.0", cacheDir)
	saveCacheFile("v0.44.0-28-gaaaaaaaa", cacheDir, rollingCacheFileName)

	info, err := CheckForUpdate(t.Context(), "v0.44.0-28-gaaaaaaaa", false, cacheDir)
	require.NoError(t, err)
	assert.Nil(t, info, "fresh rolling cache for the running build means up to date")
}

func TestInstallRawBinaryTo(t *testing.T) {
	t.Run("installs verified binary", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "download")
		dst := filepath.Join(dir, "agentsview")
		require.NoError(t, os.WriteFile(src, []byte("new"), 0o600))
		require.NoError(t, os.WriteFile(dst, []byte("old"), 0o755))

		require.NoError(t, installRawBinaryTo(src, sha256Hex("new"), dst, ""))

		got, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, "new", string(got))
		if runtime.GOOS != "windows" {
			st, err := os.Stat(dst)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o755), st.Mode().Perm())
		}
	})

	t.Run("rejects checksum mismatch and keeps old binary", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "download")
		dst := filepath.Join(dir, "agentsview")
		require.NoError(t, os.WriteFile(src, []byte("tampered"), 0o600))
		require.NoError(t, os.WriteFile(dst, []byte("old"), 0o755))

		err := installRawBinaryTo(src, sha256Hex("new"), dst, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")

		got, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, "old", string(got))
	})

	t.Run("refuses empty checksum", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "download")
		require.NoError(t, os.WriteFile(src, []byte("new"), 0o600))

		err := installRawBinaryTo(src, "", filepath.Join(dir, "agentsview"), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing unverified binary")
	})

	t.Run("trusts precomputed download checksum", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "download")
		dst := filepath.Join(dir, "agentsview")
		require.NoError(t, os.WriteFile(src, []byte("new"), 0o600))

		err := installRawBinaryTo(src, sha256Hex("new"), dst, sha256Hex("other"))
		require.Error(t, err, "precomputed hash of the streamed download wins")
		assert.Contains(t, err.Error(), "checksum mismatch")
	})
}

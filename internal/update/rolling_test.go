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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	runningBuild  = "v0.44.0-28-gaaaaaaaa"
	snapshotBuild = "v0.44.0-30-gbbbbbbbb"
	snapshotTag   = "build-20261008-bbbbbbbb"
)

// rollingRelease serves fake GitHub releases at
// /releases/download/<tag>/<asset>. Missing tags and assets return 404.
type rollingRelease struct {
	mu       sync.Mutex
	releases map[string]map[string]string
	requests []string
	agents   []string
}

func (r *rollingRelease) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			r.mu.Lock()
			defer r.mu.Unlock()
			path := strings.TrimPrefix(req.URL.Path, "/releases/download/")
			r.requests = append(r.requests, req.Method+" "+path)
			r.agents = append(r.agents, req.UserAgent())
			tag, asset, _ := strings.Cut(path, "/")
			body, ok := r.releases[tag][asset]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if req.Method != http.MethodHead {
				_, _ = w.Write([]byte(body))
			}
		},
	))
	t.Cleanup(srv.Close)
	return srv.URL + "/releases/download"
}

// update changes the published releases between requests.
func (r *rollingRelease) update(fn func(map[string]map[string]string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r.releases)
}

func (r *rollingRelease) seen() (requests, agents []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...), append([]string(nil), r.agents...)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// releaseAssets publishes binary for the current platform and an
// unrelated platform, with version as the VERSION asset.
func releaseAssets(version, binary string) map[string]string {
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	other := rollingAssetName("plan9", "mips")
	return map[string]string{
		"VERSION": version + "\n",
		asset:     binary,
		other:     "other",
		"SHA256SUMS": sha256Hex("other") + "  " + other + "\n" +
			sha256Hex(binary) + "  " + asset + "\n",
	}
}

// newRollingRelease publishes snapshotBuild with binary as the
// snapshotTag release. The "latest" release names that snapshot in
// BUILD_TAG but already carries a newer build's files, as it does while
// a later push republishes it, so a client that mixes the two releases
// sees the wrong version or checksum.
func newRollingRelease(binary string) *rollingRelease {
	latest := releaseAssets("v0.44.0-31-gcccccccc", "republished-binary")
	latest["BUILD_TAG"] = snapshotTag + "\n"
	return &rollingRelease{releases: map[string]map[string]string{
		"latest":    latest,
		snapshotTag: releaseAssets(snapshotBuild, binary),
	}}
}

// writeRollingCache records version as the rolling check made age ago.
func writeRollingCache(t *testing.T, dir, version string, age time.Duration) {
	t.Helper()
	data := fmt.Sprintf(`{"checked_at":%q,"version":%q}`,
		time.Now().Add(-age).Format(time.RFC3339Nano), version)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, rollingCacheFileName), []byte(data), 0o600,
	))
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

func TestRollingDownloadRoot(t *testing.T) {
	assert.Equal(t,
		"https://github.com/example/agentsview/releases/download",
		rollingDownloadRoot("example/agentsview"),
	)
}

func TestCheckRollingUpdateForcedReadsOneSnapshot(t *testing.T) {
	release := newRollingRelease("new-binary")
	root := release.serve(t)
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)

	info, err := checkRollingUpdate(t.Context(), root, runningBuild, true, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, runningBuild, info.CurrentVersion)
	assert.Equal(t, snapshotBuild, info.LatestVersion)
	assert.Equal(t, root+"/"+snapshotTag+"/"+asset, info.DownloadURL)
	assert.Equal(t, asset, info.AssetName)
	assert.Equal(t, int64(len("new-binary")), info.Size)
	assert.Equal(t, sha256Hex("new-binary"), info.Checksum)
	assert.False(t, info.IsDevBuild, "rolling updates are real updates, not dev notices")
	assert.True(t, info.rawBinary)
	assert.False(t, info.NeedsRefetch())

	requests, agents := release.seen()
	assert.Equal(t, []string{
		"GET latest/BUILD_TAG",
		"GET " + snapshotTag + "/VERSION",
		"GET " + snapshotTag + "/SHA256SUMS",
		"HEAD " + snapshotTag + "/" + asset,
	}, requests)
	for _, agent := range agents {
		assert.Equal(t, updateUserAgent, agent)
	}
}

func TestCheckRollingUpdateDisplayOnly(t *testing.T) {
	release := newRollingRelease("new-binary")
	root := release.serve(t)

	info, err := checkRollingUpdate(t.Context(), root, runningBuild, false, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, runningBuild, info.CurrentVersion)
	assert.Equal(t, snapshotBuild, info.LatestVersion)
	assert.True(t, info.NeedsRefetch(), "display-only info lacks install details")
	assert.Empty(t, info.Checksum)
	assert.Empty(t, info.DownloadURL)
	assert.Zero(t, info.Size)

	requests, _ := release.seen()
	assert.Equal(t, []string{
		"GET latest/BUILD_TAG",
		"GET " + snapshotTag + "/VERSION",
	}, requests)
}

func TestCheckRollingUpdateSameBuild(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			release := newRollingRelease("binary")
			root := release.serve(t)

			info, err := checkRollingUpdate(t.Context(), root, snapshotBuild, force, t.TempDir())
			require.NoError(t, err)
			assert.Nil(t, info)

			requests, _ := release.seen()
			assert.Len(t, requests, 2, "no download details for the running build")
		})
	}
}

func TestCheckRollingUpdateBuildTag(t *testing.T) {
	const longTag = "build-20261008-0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name     string
		buildTag *string
		wantTag  string
		wantErr  string
	}{
		{name: "missing", wantErr: "404"},
		{name: "empty", buildTag: new(""), wantErr: `invalid build tag ""`},
		{name: "rolling tag", buildTag: new("latest"), wantErr: "invalid build tag"},
		{name: "short date", buildTag: new("build-2026108-bbbbbbbb"), wantErr: "invalid build tag"},
		{name: "upper-case sha", buildTag: new("build-20261008-BBBBBBBB"), wantErr: "invalid build tag"},
		{name: "six-digit sha", buildTag: new("build-20261008-bbbbbb"), wantErr: "invalid build tag"},
		{name: "path traversal", buildTag: new("../" + snapshotTag), wantErr: "invalid build tag"},
		{name: "two tags", buildTag: new(snapshotTag + "\n" + snapshotTag), wantErr: "invalid build tag"},
		{name: "surrounding whitespace", buildTag: new(" " + snapshotTag + "\r\n"), wantTag: snapshotTag},
		{name: "full sha", buildTag: new(longTag), wantTag: longTag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := newRollingRelease("binary")
			release.update(func(r map[string]map[string]string) {
				if tt.buildTag == nil {
					delete(r["latest"], "BUILD_TAG")
				} else {
					r["latest"]["BUILD_TAG"] = *tt.buildTag
				}
				if tt.wantTag != "" {
					r[tt.wantTag] = r[snapshotTag]
				}
			})
			root := release.serve(t)

			info, err := checkRollingUpdate(t.Context(), root, runningBuild, false, t.TempDir())
			requests, _ := release.seen()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Nil(t, info)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Contains(t, err.Error(), "may be mid-publish; try again shortly")
				assert.Equal(t, []string{"GET latest/BUILD_TAG"}, requests,
					"never fall back to the mutable latest assets")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, snapshotBuild, info.LatestVersion)
			assert.Equal(t, "GET "+tt.wantTag+"/VERSION", requests[len(requests)-1])
		})
	}
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
			release := newRollingRelease("binary")
			release.update(func(r map[string]map[string]string) {
				tt.mutate(r[snapshotTag])
			})
			root := release.serve(t)

			info, err := checkRollingUpdate(t.Context(), root, runningBuild, true, t.TempDir())
			require.Error(t, err)
			assert.Nil(t, info)
			assert.Contains(t, err.Error(), tt.wantErr)

			requests, _ := release.seen()
			for _, req := range requests[1:] {
				assert.NotContains(t, req, " latest/",
					"only BUILD_TAG is read from the latest release")
			}
		})
	}
}

func TestCheckRollingUpdateCache(t *testing.T) {
	tests := []struct {
		name         string
		cached       string
		age          time.Duration
		force        bool
		wantLatest   string
		wantRequests int
		wantRefetch  bool
	}{
		{
			name:   "fresh check of running build skips network",
			cached: runningBuild,
			age:    time.Minute,
		},
		{
			name:        "fresh check of newer build skips network",
			cached:      "v0.44.0-29-gdddddddd",
			age:         time.Minute,
			wantLatest:  "v0.44.0-29-gdddddddd",
			wantRefetch: true,
		},
		{
			name:         "entry older than the rolling window refetches",
			cached:       runningBuild,
			age:          devCacheDuration + time.Minute,
			wantLatest:   snapshotBuild,
			wantRequests: 2,
			wantRefetch:  true,
		},
		{
			name:         "entry without a version refetches",
			cached:       "",
			age:          time.Minute,
			wantLatest:   snapshotBuild,
			wantRequests: 2,
			wantRefetch:  true,
		},
		{
			name:         "force bypasses fresh cache",
			cached:       runningBuild,
			age:          time.Minute,
			force:        true,
			wantLatest:   snapshotBuild,
			wantRequests: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := newRollingRelease("binary")
			root := release.serve(t)
			cacheDir := t.TempDir()
			writeRollingCache(t, cacheDir, tt.cached, tt.age)

			info, err := checkRollingUpdate(t.Context(), root, runningBuild, tt.force, cacheDir)
			require.NoError(t, err)
			requests, _ := release.seen()
			assert.Len(t, requests, tt.wantRequests)
			if tt.wantLatest == "" {
				assert.Nil(t, info)
				return
			}
			require.NotNil(t, info)
			assert.Equal(t, runningBuild, info.CurrentVersion)
			assert.Equal(t, tt.wantLatest, info.LatestVersion)
			assert.Equal(t, tt.wantRefetch, info.NeedsRefetch())
			assert.True(t, info.rawBinary)
		})
	}

	t.Run("check records the rolling version separately", func(t *testing.T) {
		release := newRollingRelease("binary")
		root := release.serve(t)
		cacheDir := t.TempDir()
		saveCache("v0.44.0", cacheDir)

		_, err := checkRollingUpdate(t.Context(), root, runningBuild, false, cacheDir)
		require.NoError(t, err)

		rolling, err := loadCacheFile(cacheDir, rollingCacheFileName)
		require.NoError(t, err)
		assert.Equal(t, snapshotBuild, rolling.Version)
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
	saveCacheFile(runningBuild, cacheDir, rollingCacheFileName)

	info, err := CheckForUpdate(t.Context(), runningBuild, false, cacheDir)
	require.NoError(t, err)
	assert.Nil(t, info, "fresh rolling cache for the running build means up to date")
}

func TestRollingUpdateSurvivesRepublish(t *testing.T) {
	// A push republishes "latest" between the check and the download.
	// The install still gets the checked build from its snapshot.
	release := newRollingRelease("checked-binary")
	root := release.serve(t)
	exe := filepath.Join(t.TempDir(), rollingAssetName(runtime.GOOS, runtime.GOARCH))
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755))
	fakeExecutable(t, exe, nil)

	info, err := checkRollingUpdate(t.Context(), root, runningBuild, true, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, info)

	release.update(func(r map[string]map[string]string) {
		r["latest"] = releaseAssets("v0.44.0-32-geeeeeeee", "pushed-binary")
		r["latest"]["BUILD_TAG"] = "build-20261009-eeeeeeee\n"
	})

	require.NoError(t, PerformUpdate(t.Context(), info, nil))
	got, err := os.ReadFile(exe)
	require.NoError(t, err)
	assert.Equal(t, "checked-binary", string(got))
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

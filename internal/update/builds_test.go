package update

import (
	"context"
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	switchRepo   = "owner/repo"
	olderTag     = "build-20261126-ae921ad3"
	newerTag     = "build-20261127-b168b550"
	olderBinary  = "older agentsview build"
	newerBinary  = "newer agentsview build"
	olderVersion = 129
)

// buildHost serves fake release assets, release listings, and source
// files, and records every request it answers.
type buildHost struct {
	mu       sync.Mutex
	assets   map[string]map[string]string // tag -> asset -> body
	releases []string                     // tags, newest first
	sources  map[string]string            // commit -> internal/db/db.go
	requests []string
}

func (h *buildHost) serve(t *testing.T) buildSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.requests = append(h.requests, req.Method+" "+req.URL.RequestURI())
		path := req.URL.Path
		switch {
		case strings.HasPrefix(path, "/download/"):
			tag, asset, _ := strings.Cut(strings.TrimPrefix(path, "/download/"), "/")
			body, ok := h.assets[tag][asset]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if req.Method != http.MethodHead {
				_, _ = w.Write([]byte(body))
			}
		case path == "/api/repos/"+switchRepo+"/releases":
			page, _ := strconv.Atoi(req.URL.Query().Get("page"))
			per, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
			start := min((page-1)*per, len(h.releases))
			end := min(start+per, len(h.releases))
			var items []string
			for _, tag := range h.releases[start:end] {
				items = append(items, fmt.Sprintf(`{"tag_name":%q}`, tag))
			}
			_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
		case strings.HasPrefix(path, "/raw/"+switchRepo+"/"):
			commit, file, _ := strings.Cut(strings.TrimPrefix(path, "/raw/"+switchRepo+"/"), "/")
			body, ok := h.sources[commit]
			if !ok || file != dataVersionSourcePath {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	return buildSource{
		repo:         switchRepo,
		downloadRoot: srv.URL + "/download",
		apiBase:      srv.URL + "/api",
		rawBase:      srv.URL + "/raw",
	}
}

func (h *buildHost) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.requests...)
}

func (h *buildHost) forget() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = nil
}

// snapshotAssets publishes binary for this platform, plus another
// platform's binary that switch must ignore.
func snapshotAssets(binary string) map[string]string {
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	other := rollingAssetName("plan9", "mips")
	return map[string]string{
		asset: binary,
		other: "other",
		rollingChecksumsAsset: sha256Hex("other") + "  " + other + "\n" +
			sha256Hex(binary) + "  " + asset + "\n",
	}
}

// dbSource is a db.go excerpt that declares version, shaped like the real
// file: the constant sits among other declarations and comments.
func dbSource(version int) string {
	return "package db\n\n// dataVersion tracks parser changes.\n" +
		"// (128: an older note mentioning const dataVersion = 1)\n" +
		fmt.Sprintf("const dataVersion = %d\n\nconst other = 7\n", version)
}

func newBuildHost() *buildHost {
	return &buildHost{
		assets: map[string]map[string]string{
			olderTag: snapshotAssets(olderBinary),
			newerTag: snapshotAssets(newerBinary),
			"latest": {rollingBuildTagAsset: newerTag + "\n"},
		},
		releases: []string{"latest", newerTag, olderTag},
		sources:  map[string]string{"ae921ad3": dbSource(olderVersion), "b168b550": dbSource(130)},
	}
}

// testSwitcher returns a Switcher on host whose binaries report no data
// version, as builds before the data_version field do.
func testSwitcher(t *testing.T, host *buildHost) *Switcher {
	t.Helper()
	withoutAria2(t)
	sw := newSwitcher(host.serve(t), filepath.Join(t.TempDir(), "lib", "agentsview", "builds"))
	sw.report = func(context.Context, string) (BinaryReport, error) {
		return BinaryReport{}, nil
	}
	return sw
}

func requestsWithPrefix(requests []string, prefix string) []string {
	var out []string
	for _, r := range requests {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func TestBuildCommit(t *testing.T) {
	tests := []struct {
		name, version, commit, want string
	}{
		{"commit hash", "v0.44.0-426-gae921ad3", "AE921AD3", "ae921ad3"},
		{"version when commit is unknown", "v0.44.0-426-gae921ad3", "unknown", "ae921ad3"},
		{"tagged release names no commit", "v0.44.0", "unknown", ""},
		{"dirty build names no commit", "v0.44.0-426-gae921ad3-dirty", "", ""},
		{"too short to be a commit", "", "ae92", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildCommit(tt.version, tt.commit))
		})
	}
}

func TestInstallPathFor(t *testing.T) {
	prefix := t.TempDir()
	installed := filepath.Join(prefix, "bin", binaryName())
	require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
	require.NoError(t, os.WriteFile(installed, []byte("installed"), 0o755))
	kept := filepath.Join(BuildsDir(installed), olderTag, rollingAssetName(runtime.GOOS, runtime.GOARCH))

	got, err := InstallPathFor(installed)
	require.NoError(t, err)
	assert.Equal(t, installed, got, "the installed binary switches itself")

	got, err = InstallPathFor(kept)
	require.NoError(t, err)
	assert.Equal(t, installed, got, "a kept build switches the install it belongs to")

	elsewhere := filepath.Join(prefix, "lib", "agentsview", "builds", "not-a-tag", "agentsview")
	got, err = InstallPathFor(elsewhere)
	require.NoError(t, err)
	assert.Equal(t, elsewhere, got, "a directory that is not a build tag is not a kept build")

	require.NoError(t, os.Remove(installed))
	_, err = InstallPathFor(kept)
	require.Error(t, err, "a kept build with no install beside it must not guess")
}

func TestBuildsDirSitsBesideTheSidecar(t *testing.T) {
	installed := filepath.Join(t.TempDir(), "bin", "agentsview")
	assert.Equal(t,
		filepath.Join(filepath.Dir(sidecarTarget(installed)), "builds"),
		BuildsDir(installed),
	)
}

func TestSwitcherResolve(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)

	tests := []struct {
		target string
		want   string
	}{
		{"latest", newerTag},
		{olderTag, olderTag},
		{"ae921ad3", olderTag},
		{"ae921ad", olderTag},
		{"ae921ad3c0ffee00112233445566778899aabbcc", olderTag},
		{"v0.44.0-426-gae921ad3", olderTag},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got, err := sw.Resolve(t.Context(), tt.target)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
	assert.Len(t, requestsWithPrefix(host.seen(), "GET /api/"), 1,
		"one release listing must answer every commit lookup")
}

func TestSwitcherResolveErrors(t *testing.T) {
	host := newBuildHost()
	host.releases = append(host.releases, "build-20261128-ae921ad9")
	sw := testSwitcher(t, host)

	_, err := sw.Resolve(t.Context(), "ae921ad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches several builds")

	_, err = sw.Resolve(t.Context(), "deadbeef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https://github.com/"+switchRepo+"/releases")

	_, err = sw.Resolve(t.Context(), "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not name a build")
}

func TestSwitcherResolvePagesThroughReleases(t *testing.T) {
	host := newBuildHost()
	var releases []string
	for i := range releasesPerPage {
		releases = append(releases, fmt.Sprintf("build-20261201-%08x", i+0x10000000))
	}
	releases = append(releases, olderTag)
	host.releases = releases
	sw := testSwitcher(t, host)

	got, err := sw.Resolve(t.Context(), "ae921ad3")
	require.NoError(t, err)
	assert.Equal(t, olderTag, got)
	assert.Len(t, requestsWithPrefix(host.seen(), "GET /api/"), 2)
}

func TestSwitcherEnsureDownloadsVerifiesAndKeeps(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)

	b, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	assert.Equal(t, olderTag, b.Tag)
	assert.Equal(t, olderVersion, b.DataVersion, "the data version comes from the build's source")
	data, err := os.ReadFile(b.BinaryPath)
	require.NoError(t, err)
	assert.Equal(t, olderBinary, string(data))
	info, err := os.Stat(b.BinaryPath)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.NotZero(t, info.Mode()&0o111, "a kept binary must be executable")
	}

	host.forget()
	again, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	assert.Equal(t, b.BinaryPath, again.BinaryPath)
	assert.Equal(t, olderVersion, again.DataVersion)
	assert.Empty(t, host.seen(), "a kept build must switch without network requests")
}

func TestSwitcherEnsureReplacesTamperedKeptBuild(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	b, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(b.BinaryPath, []byte("truncated"), 0o755))

	host.forget()
	b, err = sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	data, err := os.ReadFile(b.BinaryPath)
	require.NoError(t, err)
	assert.Equal(t, olderBinary, string(data))
	assert.NotEmpty(t, requestsWithPrefix(host.seen(), "GET /download/"+olderTag+"/"),
		"a kept binary that fails its checksum must be downloaded again")
}

func TestSwitcherEnsureRejectsChecksumMismatch(t *testing.T) {
	host := newBuildHost()
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	host.assets[olderTag][asset] = "corrupted in transit"
	sw := testSwitcher(t, host)

	_, err := sw.Ensure(t.Context(), olderTag, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
	assert.Empty(t, sw.keptTags(), "a failed download must not leave a build behind")
	entries, _ := os.ReadDir(sw.BuildsDir())
	assert.Empty(t, entries, "a failed download must clean up its staging directory")
}

func TestSwitcherEnsureMissingBuild(t *testing.T) {
	sw := testSwitcher(t, newBuildHost())
	_, err := sw.Ensure(t.Context(), "build-20260101-00000000", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build-20260101-00000000")
}

func TestSwitcherEnsurePrefersTheBinaryReport(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	sw.report = func(_ context.Context, path string) (BinaryReport, error) {
		return BinaryReport{DataVersion: 131}, nil
	}

	b, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	assert.Equal(t, 131, b.DataVersion)
	assert.Empty(t, requestsWithPrefix(host.seen(), "GET /raw/"),
		"a build that reports its data version needs no source lookup")
}

func TestSwitcherEnsureFailsWithoutDataVersion(t *testing.T) {
	host := newBuildHost()
	host.sources["ae921ad3"] = "package db\n\nvar dataVersion = 129\n"
	sw := testSwitcher(t, host)

	_, err := sw.Ensure(t.Context(), olderTag, nil)
	require.Error(t, err, "switch must not guess a data version it cannot read")
	assert.Contains(t, err.Error(), "data version")
}

func TestSwitcherKeep(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	installed := filepath.Join(t.TempDir(), "agentsview")
	require.NoError(t, os.WriteFile(installed, []byte(newerBinary), 0o755))

	b, err := sw.Keep(t.Context(), newerTag, installed, 130)
	require.NoError(t, err)
	assert.Equal(t, 130, b.DataVersion)
	data, err := os.ReadFile(b.BinaryPath)
	require.NoError(t, err)
	assert.Equal(t, newerBinary, string(data))
	assert.Equal(t, newerTag, sw.KeptTagOf(installed))

	tag, err := sw.TagForCommit(t.Context(), "b168b550")
	require.NoError(t, err)
	assert.Equal(t, newerTag, tag)
}

func TestSwitcherKeepRefusesUnpublishedBinary(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	dirty := filepath.Join(t.TempDir(), "agentsview")
	require.NoError(t, os.WriteFile(dirty, []byte("locally modified build"), 0o755))

	_, err := sw.Keep(t.Context(), newerTag, dirty, 130)
	require.ErrorIs(t, err, ErrNotPublishedBuild)
	assert.Empty(t, sw.List())
	assert.Empty(t, sw.KeptTagOf(dirty))
}

func TestSwitcherTagForCommitUsesKeptBuildsFirst(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	_, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)

	host.forget()
	tag, err := sw.TagForCommit(t.Context(), "ae921ad3")
	require.NoError(t, err)
	assert.Equal(t, olderTag, tag)
	assert.Empty(t, host.seen(), "a kept build must answer a commit lookup without the API")
}

func TestSwitcherList(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	_, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	_, err = sw.Ensure(t.Context(), newerTag, nil)
	require.NoError(t, err)
	// A leftover staging directory from an interrupted switch is not a build.
	require.NoError(t, os.MkdirAll(filepath.Join(sw.BuildsDir(), "."+olderTag+".new-123"), 0o755))

	builds := sw.List()
	require.Len(t, builds, 2)
	assert.Equal(t, newerTag, builds[0].Tag, "newest build first")
	assert.Equal(t, 130, builds[0].DataVersion)
	assert.Equal(t, olderTag, builds[1].Tag)
	assert.Equal(t, olderVersion, builds[1].DataVersion)
}

func TestInstallKeptBuild(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	b, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "bin", "agentsview")
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	require.NoError(t, os.WriteFile(dst, []byte(newerBinary), 0o755))

	_, err = InstallKeptBuild(t.Context(), b, dst)
	require.NoError(t, err)
	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, olderBinary, string(data))
	_, err = os.Stat(b.BinaryPath)
	assert.NoError(t, err, "installing must leave the kept copy in place")
}

func TestInstallKeptBuildRefusesTamperedBinary(t *testing.T) {
	host := newBuildHost()
	sw := testSwitcher(t, host)
	b, err := sw.Ensure(t.Context(), olderTag, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(b.BinaryPath, []byte("swapped after the check"), 0o755))
	dst := filepath.Join(t.TempDir(), "agentsview")
	require.NoError(t, os.WriteFile(dst, []byte(newerBinary), 0o755))

	_, err = InstallKeptBuild(t.Context(), b, dst)
	require.Error(t, err)
	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, newerBinary, string(data), "a failed install must leave the installed binary alone")
}

func TestNewSwitcherNeedsRollingBuild(t *testing.T) {
	old := rollingRepo
	t.Cleanup(func() { rollingRepo = old })
	rollingRepo = ""

	_, err := NewSwitcher(filepath.Join(t.TempDir(), "agentsview"))
	require.ErrorIs(t, err, ErrNotRollingBuild)
}

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sidecarFiles is a sidecar archive's content for one build. It carries
// the library under every platform's name so the same fixture installs on
// the Linux and Windows test runners.
func sidecarFiles(build string) map[string]string {
	return map[string]string{
		"libsimple.so":         "so " + build,
		"libsimple.dylib":      "dylib " + build,
		"simple.dll":           "dll " + build,
		"dict/jieba.dict.utf8": "dict " + build,
		"VERSIONS":             "simple " + build + "\n",
	}
}

// sidecarArchive packs files into a .tar.gz the way the release workflow
// does, without directory entries.
func sidecarArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(files[name])),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(files[name]))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.String()
}

// sidecarServer serves archives by name and counts the requests it gets.
type sidecarServer struct {
	url      string
	requests atomic.Int32
}

func serveSidecars(t *testing.T, archives map[string]string) *sidecarServer {
	t.Helper()
	s := &sidecarServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		body, ok := archives[r.URL.Path[1:]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *sidecarServer) asset(name, archive string) *sidecarAsset {
	return &sidecarAsset{URL: s.url + "/" + name, Name: name, Checksum: sha256Hex(archive)}
}

// clearSidecarEnv unsets the variables that make updates skip the sidecar.
func clearSidecarEnv(t *testing.T) {
	t.Helper()
	t.Setenv(sidecarDirEnv, "")
	t.Setenv(sidecarSkipEnv, "")
}

// readTree returns every file under dir by slash-separated relative path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	require.NoError(t, err)
	return files
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// installedFiles is what a managed install of an archive leaves on disk.
func installedFiles(files map[string]string, archive string) map[string]string {
	want := map[string]string{sidecarMarkerName: sha256Hex(archive) + "\n"}
	for name, body := range files {
		want[name] = body
	}
	return want
}

// newInstallRoot makes <root>/bin/agentsview and returns root and the
// executable path.
func newInstallRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "bin", "agentsview")
	require.NoError(t, os.MkdirAll(filepath.Dir(exe), 0o755))
	require.NoError(t, os.WriteFile(exe, []byte("binary"), 0o755))
	return root, exe
}

func TestRollingSidecarAssetName(t *testing.T) {
	assert.Equal(t, "agentsview-simple-linux-amd64.tar.gz",
		rollingSidecarAssetName("linux", "amd64"))
	assert.Equal(t, "agentsview-simple-windows-amd64.tar.gz",
		rollingSidecarAssetName("windows", "amd64"))
}

func TestInstallSidecarFreshInstall(t *testing.T) {
	clearSidecarEnv(t)
	root, exe := newInstallRoot(t)
	v1 := sidecarArchive(t, sidecarFiles("v1"))
	srv := serveSidecars(t, map[string]string{"v1.tar.gz": v1})

	status, err := installSidecar(t.Context(), srv.asset("v1.tar.gz", v1), exe)
	require.NoError(t, err)

	target := filepath.Join(root, "lib", "agentsview", "simple")
	assert.Equal(t, "Installed the CJK search extension in "+target+
		". The next start indexes existing messages for Chinese, "+
		"Japanese, and Korean search.", status)
	assert.Equal(t, installedFiles(sidecarFiles("v1"), v1), readTree(t, target))
	assert.Equal(t, []string{"simple"}, entryNames(t, filepath.Dir(target)),
		"no staging or archive files are left beside the sidecar")
	assert.Equal(t, int32(1), srv.requests.Load())
}

func TestInstallSidecarSkipsMatchingInstall(t *testing.T) {
	clearSidecarEnv(t)
	_, exe := newInstallRoot(t)
	v1 := sidecarArchive(t, sidecarFiles("v1"))
	srv := serveSidecars(t, map[string]string{"v1.tar.gz": v1})
	_, err := installSidecar(t.Context(), srv.asset("v1.tar.gz", v1), exe)
	require.NoError(t, err)

	status, err := installSidecar(t.Context(), srv.asset("v1.tar.gz", v1), exe)
	require.NoError(t, err)

	assert.Empty(t, status)
	assert.Equal(t, int32(1), srv.requests.Load(), "a matching install downloads nothing")
}

func TestInstallSidecarReplacesManagedInstall(t *testing.T) {
	clearSidecarEnv(t)
	root, exe := newInstallRoot(t)
	oldFiles := sidecarFiles("v1")
	oldFiles["dict/retired.utf8"] = "only in v1"
	v1 := sidecarArchive(t, oldFiles)
	v2 := sidecarArchive(t, sidecarFiles("v2"))
	srv := serveSidecars(t, map[string]string{"v1.tar.gz": v1, "v2.tar.gz": v2})
	_, err := installSidecar(t.Context(), srv.asset("v1.tar.gz", v1), exe)
	require.NoError(t, err)

	status, err := installSidecar(t.Context(), srv.asset("v2.tar.gz", v2), exe)
	require.NoError(t, err)

	target := filepath.Join(root, "lib", "agentsview", "simple")
	assert.Equal(t, "Updated the CJK search extension in "+target+".", status)
	assert.Equal(t, installedFiles(sidecarFiles("v2"), v2), readTree(t, target),
		"the new archive replaces the whole directory")
	assert.Equal(t, []string{"simple"}, entryNames(t, filepath.Dir(target)))
}

func TestInstallSidecarUpdatesCopyBesideExecutable(t *testing.T) {
	clearSidecarEnv(t)
	root, exe := newInstallRoot(t)
	beside := filepath.Join(root, "bin", "agentsview-simple")
	v1 := sidecarArchive(t, sidecarFiles("v1"))
	v2 := sidecarArchive(t, sidecarFiles("v2"))
	srv := serveSidecars(t, map[string]string{"v2.tar.gz": v2})
	require.NoError(t, os.MkdirAll(beside, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(beside, sidecarMarkerName), []byte(sha256Hex(v1)+"\n"), 0o644,
	))

	_, err := installSidecar(t.Context(), srv.asset("v2.tar.gz", v2), exe)
	require.NoError(t, err)

	assert.Equal(t, installedFiles(sidecarFiles("v2"), v2), readTree(t, beside))
	assert.NoDirExists(t, filepath.Join(root, "lib"),
		"the daemon loads the copy beside the executable first")
}

func TestInstallSidecarLeavesOtherInstallsAlone(t *testing.T) {
	v1 := sidecarArchive(t, sidecarFiles("v1"))
	tests := []struct {
		name       string
		env        map[string]string
		manual     bool
		wantStatus func(target string) string
	}{
		{
			name:   "directory installed without an update",
			manual: true,
			wantStatus: func(target string) string {
				return "Left the CJK search extension in " + target +
					" alone (it was not installed by an update)."
			},
		},
		{
			name: "AGENTSVIEW_SIMPLE_DIR names the sidecar",
			env:  map[string]string{sidecarDirEnv: "/opt/simple"},
			wantStatus: func(string) string {
				return "Left the CJK search extension in /opt/simple alone " +
					"(AGENTSVIEW_SIMPLE_DIR is set)."
			},
		},
		{
			name: "AGENTSVIEW_SKIP_CJK_FTS opts out",
			env:  map[string]string{sidecarSkipEnv: "1"},
			wantStatus: func(string) string {
				return "Skipped the CJK search extension (AGENTSVIEW_SKIP_CJK_FTS=1)."
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearSidecarEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			root, exe := newInstallRoot(t)
			target := filepath.Join(root, "lib", "agentsview", "simple")
			if tt.manual {
				require.NoError(t, os.MkdirAll(target, 0o755))
				require.NoError(t, os.WriteFile(
					filepath.Join(target, "libsimple.so"), []byte("built by hand"), 0o755,
				))
			}
			srv := serveSidecars(t, map[string]string{"v1.tar.gz": v1})

			status, err := installSidecar(t.Context(), srv.asset("v1.tar.gz", v1), exe)
			require.NoError(t, err)

			assert.Equal(t, tt.wantStatus(target), status)
			assert.Equal(t, int32(0), srv.requests.Load())
			if tt.manual {
				assert.Equal(t, map[string]string{"libsimple.so": "built by hand"},
					readTree(t, target))
			} else {
				assert.NoDirExists(t, filepath.Join(root, "lib"))
			}
		})
	}
}

func TestInstallSidecarRejectsBadDownloadsAndKeepsCurrentCopy(t *testing.T) {
	library := map[string]string{
		"windows": "simple.dll",
		"darwin":  "libsimple.dylib",
	}[runtime.GOOS]
	if library == "" {
		library = "libsimple.so"
	}
	v1 := sidecarArchive(t, sidecarFiles("v1"))
	noLibrary := sidecarArchive(t, map[string]string{"dict/jieba.dict.utf8": "dict"})
	noDict := sidecarArchive(t, map[string]string{
		"libsimple.so": "so", "libsimple.dylib": "dylib", "simple.dll": "dll",
	})
	tests := []struct {
		name    string
		asset   func(srv *sidecarServer) *sidecarAsset
		wantErr string
	}{
		{
			name: "checksum mismatch",
			asset: func(srv *sidecarServer) *sidecarAsset {
				return srv.asset("v2.tar.gz", "different bytes")
			},
			wantErr: "checksum mismatch",
		},
		{
			name:    "archive without the library",
			asset:   func(srv *sidecarServer) *sidecarAsset { return srv.asset("nolib.tar.gz", noLibrary) },
			wantErr: "nolib.tar.gz: archive has no " + library,
		},
		{
			name:    "archive without dictionaries",
			asset:   func(srv *sidecarServer) *sidecarAsset { return srv.asset("nodict.tar.gz", noDict) },
			wantErr: "nodict.tar.gz: archive has no dict directory",
		},
		{
			name:    "missing asset",
			asset:   func(srv *sidecarServer) *sidecarAsset { return srv.asset("gone.tar.gz", "gone") },
			wantErr: "404 Not Found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearSidecarEnv(t)
			root, exe := newInstallRoot(t)
			srv := serveSidecars(t, map[string]string{
				"v1.tar.gz":     v1,
				"v2.tar.gz":     sidecarArchive(t, sidecarFiles("v2")),
				"nolib.tar.gz":  noLibrary,
				"nodict.tar.gz": noDict,
			})
			_, err := installSidecar(t.Context(), srv.asset("v1.tar.gz", v1), exe)
			require.NoError(t, err)
			target := filepath.Join(root, "lib", "agentsview", "simple")
			before := readTree(t, target)

			_, err = installSidecar(t.Context(), tt.asset(srv), exe)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, before, readTree(t, target), "the working copy stays")
			assert.Equal(t, []string{"simple"}, entryNames(t, filepath.Dir(target)),
				"the failed attempt leaves nothing behind")
		})
	}
}

func TestCheckRollingUpdateReadsSidecarFromSameChecksums(t *testing.T) {
	sidecar := rollingSidecarAssetName(runtime.GOOS, runtime.GOARCH)
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	tests := []struct {
		name        string
		publish     bool
		wantSidecar bool
	}{
		{name: "snapshot with a sidecar", publish: true, wantSidecar: true},
		{name: "snapshot from before sidecars", publish: false, wantSidecar: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := newRollingRelease("new-binary")
			if tt.publish {
				release.update(func(r map[string]map[string]string) {
					r[snapshotTag][sidecar] = "sidecar-archive"
					r[snapshotTag]["SHA256SUMS"] += sha256Hex("sidecar-archive") + "  " + sidecar + "\n"
				})
			}
			root := release.serve(t)

			info, err := checkRollingUpdate(t.Context(), root, runningBuild, true, t.TempDir())
			require.NoError(t, err)
			require.NotNil(t, info)

			if tt.wantSidecar {
				assert.Equal(t, &sidecarAsset{
					URL:      root + "/" + snapshotTag + "/" + sidecar,
					Name:     sidecar,
					Checksum: sha256Hex("sidecar-archive"),
				}, info.sidecar)
			} else {
				assert.Nil(t, info.sidecar)
			}
			requests, _ := release.seen()
			assert.Equal(t, []string{
				"GET latest/BUILD_TAG",
				"GET " + snapshotTag + "/VERSION",
				"GET " + snapshotTag + "/SHA256SUMS",
				"HEAD " + snapshotTag + "/" + asset,
			}, requests, "the sidecar adds no request to the check")
		})
	}
}

func TestPerformUpdateInstallsSidecarWithBinary(t *testing.T) {
	clearSidecarEnv(t)
	withoutAria2(t)
	root, exe := newInstallRoot(t)
	fakeExecutable(t, exe, nil)
	archive := sidecarArchive(t, sidecarFiles("v2"))
	srv := serveSidecars(t, map[string]string{
		"agentsview":    "new-binary",
		"simple.tar.gz": archive,
	})
	tests := []struct {
		name        string
		sidecar     *sidecarAsset
		wantSidecar map[string]string
	}{
		{
			name:        "sidecar installs beside the new binary",
			sidecar:     srv.asset("simple.tar.gz", archive),
			wantSidecar: installedFiles(sidecarFiles("v2"), archive),
		},
		{
			name: "a failed sidecar download still updates the binary",
			sidecar: &sidecarAsset{
				URL: srv.url + "/missing.tar.gz", Name: "missing.tar.gz", Checksum: sha256Hex("x"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := filepath.Join(root, "lib", "agentsview", "simple")
			require.NoError(t, os.RemoveAll(filepath.Join(root, "lib")))
			require.NoError(t, os.WriteFile(exe, []byte("old-binary"), 0o755))

			require.NoError(t, PerformUpdate(t.Context(), &UpdateInfo{
				DownloadURL: srv.url + "/agentsview",
				AssetName:   "agentsview",
				Checksum:    sha256Hex("new-binary"),
				rawBinary:   true,
				sidecar:     tt.sidecar,
			}, nil))

			got, err := os.ReadFile(exe)
			require.NoError(t, err)
			assert.Equal(t, "new-binary", string(got))
			if tt.wantSidecar != nil {
				assert.Equal(t, tt.wantSidecar, readTree(t, target))
			} else {
				assert.NoDirExists(t, target)
			}
		})
	}
}

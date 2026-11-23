package update

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAria2Env makes the test binary act as aria2c. Its value selects
// the behavior: "succeed" or "fail".
const fakeAria2Env = "AGENTSVIEW_TEST_FAKE_ARIA2"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeAria2Env); mode != "" {
		os.Exit(fakeAria2(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeAria2 stands in for aria2c. On success it writes a line naming
// the URL it was given to the file named by --dir and --out. On failure
// it exits with aria2c's "resource not found" status.
func fakeAria2(mode string, args []string) int {
	if mode == "fail" {
		fmt.Println("errorCode=3 Resource not found")
		return 3
	}
	var dir, out, url string
	for _, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--dir="); ok {
			dir = v
		} else if v, ok := strings.CutPrefix(arg, "--out="); ok {
			out = v
		} else if !strings.HasPrefix(arg, "--") {
			url = arg
		}
	}
	if dir == "" || out == "" || url == "" {
		return 28 // aria2c's status for a bad option
	}
	if err := os.WriteFile(
		filepath.Join(dir, out), []byte("aria2c fetched "+url), 0o600,
	); err != nil {
		return 1
	}
	return 0
}

// stubAria2 makes lookups of aria2c on PATH return path and err.
func stubAria2(t *testing.T, path string, err error) {
	t.Helper()
	old := lookPath
	lookPath = func(file string) (string, error) {
		if file != aria2Command {
			return "", exec.ErrNotFound
		}
		return path, err
	}
	t.Cleanup(func() { lookPath = old })
}

// withoutAria2 hides any aria2c installed on the machine.
func withoutAria2(t *testing.T) {
	t.Helper()
	stubAria2(t, "", exec.ErrNotFound)
}

// testContent returns size reproducible bytes that differ at every
// offset, so a part written at the wrong place shows.
func testContent(size int) []byte {
	rng := rand.New(rand.NewPCG(1, 2))
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(rng.Uint32())
	}
	return data
}

// requestLog records the Range header of each request a test server
// receives, "" for a request without one.
type requestLog struct {
	mu     sync.Mutex
	ranges []string
}

func (l *requestLog) add(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ranges = append(l.ranges, r.Header.Get("Range"))
}

func (l *requestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ranges...)
}

// serveContent answers r with data, honoring a Range header as a
// release download server does.
func serveContent(w http.ResponseWriter, r *http.Request, data []byte) {
	http.ServeContent(w, r, "asset", time.Time{}, bytes.NewReader(data))
}

// isFirstPart reports whether r asks for the first part of a file.
func isFirstPart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Range"), "bytes=0-")
}

// holdLaterParts holds each request for a part after the first until n
// of them have arrived, which only parallel requests can achieve. The
// first part's request, which the downloader awaits before it starts
// the others, passes straight through. A request still held after the
// hang guard fails with 503.
func holdLaterParts(n int, next http.HandlerFunc) http.HandlerFunc {
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	return func(w http.ResponseWriter, r *http.Request) {
		if isFirstPart(r) {
			next(w, r)
			return
		}
		mu.Lock()
		arrived++
		if arrived == n {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
		case <-time.After(30 * time.Second):
			http.Error(w, "requests were not parallel", http.StatusServiceUnavailable)
			return
		}
		next(w, r)
	}
}

func testDownloader(client *http.Client) rangedDownloader {
	return rangedDownloader{
		client:       client,
		connections:  8,
		minPart:      4096,
		attempts:     3,
		stallTimeout: 30 * time.Second,
	}
}

func TestSplitRanges(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		parts   int
		minPart int64
		want    []byteRange
	}{
		{
			name: "unknown size has no ranges",
			size: 0, parts: 8, minPart: 10,
			want: nil,
		},
		{
			name: "file shorter than two parts stays whole",
			size: 19, parts: 8, minPart: 10,
			want: []byteRange{{0, 18}},
		},
		{
			name: "last part takes the remainder",
			size: 103, parts: 4, minPart: 10,
			want: []byteRange{{0, 24}, {25, 49}, {50, 74}, {75, 102}},
		},
		{
			name: "minimum part size limits the count",
			size: 35, parts: 8, minPart: 10,
			want: []byteRange{{0, 10}, {11, 21}, {22, 34}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitRanges(tt.size, tt.parts, tt.minPart))
		})
	}
}

func TestRangedDownload(t *testing.T) {
	// 100,000 bytes split by testDownloader: eight parts of 12,500
	// bytes, so the third part is bytes 25000-37499.
	const size = 100_000
	content := testContent(size)
	type report struct{ downloaded, total int64 }
	tests := []struct {
		name string
		// size is what the update check reported; 0 when unknown.
		size         int64
		stallTimeout time.Duration
		handler      func(log *requestLog) http.HandlerFunc
		wantRanges   []string // requests made, in any order
		wantSome     string   // one request that must appear
		wantChecksum string
		wantReports  []report // first and last report
		wantErr      []string
		// wantNoReports is set when the download fails before it
		// starts, so a progress display never draws.
		wantNoReports bool
	}{
		{
			name: "parts download over parallel range requests",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				return holdLaterParts(7, func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					serveContent(w, r, content)
				})
			},
			wantRanges: []string{
				"bytes=0-12499", "bytes=12500-24999",
				"bytes=25000-37499", "bytes=37500-49999",
				"bytes=50000-62499", "bytes=62500-74999",
				"bytes=75000-87499", "bytes=87500-99999",
			},
			wantReports: []report{{0, size}, {size, size}},
		},
		{
			name: "server ignoring Range sends the file in one stream",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					_, _ = w.Write(content)
				}
			},
			wantRanges:   []string{"bytes=0-12499"},
			wantChecksum: sha256Hex(string(content)),
			wantReports:  []report{{0, size}, {size, size}},
		},
		{
			name: "unknown size downloads in one request",
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					serveContent(w, r, content)
				}
			},
			wantRanges:   []string{""},
			wantChecksum: sha256Hex(string(content)),
			wantReports:  []report{{0, 0}, {size, 0}},
		},
		{
			name: "dropped part resumes after the bytes it received",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				var once sync.Once
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					drop := false
					if r.Header.Get("Range") == "bytes=25000-37499" {
						once.Do(func() { drop = true })
					}
					if !drop {
						serveContent(w, r, content)
						return
					}
					w.Header().Set("Content-Range", "bytes 25000-37499/100000")
					w.Header().Set("Content-Length", "12500")
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(content[25000:30000])
					http.NewResponseController(w).Flush()
					panic(http.ErrAbortHandler)
				}
			},
			wantSome:    "bytes=30000-37499",
			wantReports: []report{{0, size}, {size, size}},
		},
		{
			name:         "stalled part resumes after the bytes it received",
			size:         size,
			stallTimeout: time.Second,
			handler: func(log *requestLog) http.HandlerFunc {
				var once sync.Once
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					stall := false
					if r.Header.Get("Range") == "bytes=25000-37499" {
						once.Do(func() { stall = true })
					}
					if !stall {
						serveContent(w, r, content)
						return
					}
					w.Header().Set("Content-Range", "bytes 25000-37499/100000")
					w.Header().Set("Content-Length", "12500")
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(content[25000:30000])
					http.NewResponseController(w).Flush()
					<-r.Context().Done()
				}
			},
			wantSome:    "bytes=30000-37499",
			wantReports: []report{{0, size}, {size, size}},
		},
		{
			name: "part failing every attempt fails the download",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					if r.Header.Get("Range") == "bytes=25000-37499" {
						http.Error(w, "boom", http.StatusInternalServerError)
						return
					}
					serveContent(w, r, content)
				}
			},
			wantSome: "bytes=25000-37499",
			wantErr:  []string{"bytes 25000-37499: after 3 attempts", "500"},
		},
		{
			name: "first request failing with a server error is retried",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				var once sync.Once
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					fail := false
					once.Do(func() { fail = true })
					if fail {
						http.Error(w, "busy", http.StatusServiceUnavailable)
						return
					}
					serveContent(w, r, content)
				}
			},
			wantRanges: []string{
				"bytes=0-12499", "bytes=0-12499", "bytes=12500-24999",
				"bytes=25000-37499", "bytes=37500-49999",
				"bytes=50000-62499", "bytes=62500-74999",
				"bytes=75000-87499", "bytes=87500-99999",
			},
			wantReports: []report{{0, size}, {size, size}},
		},
		{
			name: "first request failing every attempt reports no progress",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					http.Error(w, "busy", http.StatusServiceUnavailable)
				}
			},
			wantRanges: []string{
				"bytes=0-12499", "bytes=0-12499", "bytes=0-12499",
			},
			wantErr:       []string{"after 3 attempts", "503"},
			wantNoReports: true,
		},
		{
			name: "range from another offset is rejected",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					w.Header().Set("Content-Range", "bytes 1-12500/100000")
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(content[1:12501])
				}
			},
			wantRanges:    []string{"bytes=0-12499"},
			wantErr:       []string{`Content-Range "bytes 1-12500/100000"`},
			wantNoReports: true,
		},
		{
			name: "range of a different file size is rejected",
			size: size + 1,
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					serveContent(w, r, content)
				}
			},
			wantRanges:    []string{"bytes=0-12499"},
			wantErr:       []string{"for requested bytes 0-12499/100001"},
			wantNoReports: true,
		},
		{
			name: "missing asset reports no progress",
			size: size,
			handler: func(log *requestLog) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					http.NotFound(w, r)
				}
			},
			wantRanges:    []string{"bytes=0-12499"},
			wantErr:       []string{"404"},
			wantNoReports: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log requestLog
			srv := httptest.NewServer(tt.handler(&log))
			t.Cleanup(srv.Close)

			d := testDownloader(newRangeClient())
			if tt.stallTimeout > 0 {
				d.stallTimeout = tt.stallTimeout
			}
			dest := filepath.Join(t.TempDir(), "asset")
			var reports []report
			checksum, err := d.download(t.Context(), srv.URL, dest, tt.size,
				func(downloaded, total int64) {
					reports = append(reports, report{downloaded, total})
				},
			)

			if tt.wantRanges != nil {
				assert.ElementsMatch(t, tt.wantRanges, log.all())
			}
			if tt.wantSome != "" {
				assert.Contains(t, log.all(), tt.wantSome)
			}
			if tt.wantErr != nil {
				require.Error(t, err)
				for _, sub := range tt.wantErr {
					assert.Contains(t, err.Error(), sub)
				}
				if tt.wantNoReports {
					assert.Empty(t, reports)
				}
				return
			}
			require.NoError(t, err)
			got, err := os.ReadFile(dest)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(content, got), "downloaded bytes differ")
			assert.Equal(t, tt.wantChecksum, checksum)
			require.GreaterOrEqual(t, len(reports), 2)
			assert.Equal(t, tt.wantReports,
				[]report{reports[0], reports[len(reports)-1]})
		})
	}
}

func TestRangedDownloadOpensOneConnectionPerPart(t *testing.T) {
	// GitHub serves release assets over HTTP/2, which would carry every
	// part over one TCP connection. Parts must use separate connections.
	// The seven parts after the first are held open together, so none of
	// them can reuse another's connection. The server offers HTTP/2 the
	// way GitHub does, so a client whose TLS handshake still offers h2
	// fails here.
	content := testContent(100_000)
	var mu sync.Mutex
	protos := map[int]int{}
	peers := map[string]bool{}
	srv := httptest.NewUnstartedServer(holdLaterParts(7,
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			protos[r.ProtoMajor]++
			if !isFirstPart(r) {
				peers[r.RemoteAddr] = true
			}
			mu.Unlock()
			serveContent(w, r, content)
		},
	))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	// Trust the test certificate without replacing the client's own
	// TLS settings, which decide the protocols it offers.
	client := newRangeClient()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots

	dest := filepath.Join(t.TempDir(), "asset")
	_, err := testDownloader(client).download(t.Context(),
		srv.URL, dest, int64(len(content)), nil,
	)
	require.NoError(t, err)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(content, got), "downloaded bytes differ")
	assert.Equal(t, map[int]int{1: 8}, protos, "requests by HTTP major version")
	assert.Len(t, peers, 7, "connections of the parts held open together")
}

func TestDownloadAsset(t *testing.T) {
	const serverContent = "served by the release server"
	tests := []struct {
		name         string
		aria2        string // "", "succeed", or "fail"
		wantContent  func(url string) string
		wantRequests bool
	}{
		{
			name:        "aria2c on PATH downloads the asset",
			aria2:       "succeed",
			wantContent: func(url string) string { return "aria2c fetched " + url },
		},
		{
			name:         "failed aria2c falls back to the built-in downloader",
			aria2:        "fail",
			wantContent:  func(string) string { return serverContent },
			wantRequests: true,
		},
		{
			name:         "missing aria2c uses the built-in downloader",
			wantContent:  func(string) string { return serverContent },
			wantRequests: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log requestLog
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					log.add(r)
					serveContent(w, r, []byte(serverContent))
				},
			))
			t.Cleanup(srv.Close)

			if tt.aria2 == "" {
				withoutAria2(t)
			} else {
				self, err := os.Executable()
				require.NoError(t, err)
				stubAria2(t, self, nil)
				t.Setenv(fakeAria2Env, tt.aria2)
			}

			url := srv.URL + "/agentsview-test"
			dest := filepath.Join(t.TempDir(), "agentsview-test")
			_, err := downloadAsset(t.Context(), url, dest,
				int64(len(serverContent)), nil,
			)
			require.NoError(t, err)

			got, err := os.ReadFile(dest)
			require.NoError(t, err)
			assert.Equal(t, tt.wantContent(url), string(got))
			assert.Equal(t, tt.wantRequests, len(log.all()) > 0,
				"release server requests")
		})
	}
}

func TestDownloadWithInstalledAria2(t *testing.T) {
	// Runs the real aria2c, when installed, to check that it accepts
	// the options updates pass and splits the download.
	aria2, err := exec.LookPath(aria2Command)
	if err != nil {
		t.Skip("aria2c is not installed")
	}
	for _, name := range []string{
		"http_proxy", "https_proxy", "all_proxy",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
	} {
		t.Setenv(name, "")
	}

	content := testContent(4 << 20)
	var log requestLog
	agents := map[string]bool{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			log.add(r)
			mu.Lock()
			agents[r.UserAgent()] = true
			mu.Unlock()
			serveContent(w, r, content)
		},
	))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "agentsview-test")
	require.NoError(t, downloadWithAria2(t.Context(), aria2, srv.URL, dest))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(content, got), "downloaded bytes differ")
	assert.Equal(t, map[string]bool{updateUserAgent: true}, agents)
	var later int
	for _, r := range log.all() {
		if strings.HasPrefix(r, "bytes=") && !strings.HasPrefix(r, "bytes=0-") {
			later++
		}
	}
	assert.Positive(t, later, "range requests past the first part: %v", log.all())
}

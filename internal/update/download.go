package update

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	// downloadConnections is how many connections an update download
	// opens, through aria2c or the built-in ranged downloader.
	downloadConnections = 8
	// minPartSize is the smallest part a download is split into. A file
	// shorter than two parts downloads over one connection.
	minPartSize = 1 << 20
	// requestAttempts bounds how often the built-in downloader tries one
	// request. A retried part resumes where the previous attempt stopped.
	requestAttempts = 5
	// retryDelay is the pause before each retry.
	retryDelay = 2 * time.Second
	// stallTimeout abandons a request that has received no bytes for
	// this long, so a hung connection is retried instead of waited on.
	stallTimeout = 60 * time.Second
	// aria2Command is the aria2 executable updates use when PATH has it.
	aria2Command = "aria2c"
)

// lookPath finds executables on PATH. Tests replace it.
var lookPath = exec.LookPath

// errStalled marks a request abandoned after its stall timeout.
var errStalled = errors.New("download stalled")

// downloadAsset downloads url to dest over downloadConnections
// connections. It runs aria2c when PATH has it, which draws its own
// progress line. When aria2c is missing or fails, it says so and uses
// the built-in ranged downloader, which reports through progressFn.
//
// It returns the SHA-256 of dest when the download computed it in
// flight, or "" when the caller must hash the file.
func downloadAsset(ctx context.Context,
	url, dest string, size int64,
	progressFn func(downloaded, total int64),
) (string, error) {
	if aria2, err := lookPath(aria2Command); err == nil {
		fmt.Printf("Downloading %s with aria2c over %d connections...\n",
			filepath.Base(dest), downloadConnections,
		)
		err := downloadWithAria2(ctx, aria2, url, dest)
		if err == nil {
			return "", nil
		}
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		fmt.Printf(
			"aria2c failed: %v\nRetrying with the built-in downloader.\n",
			err,
		)
	} else {
		fmt.Println("aria2c not found in PATH; using the built-in " +
			"ranged downloader. Install aria2 to download updates " +
			"with aria2c.")
	}

	checksum, err := newRangedDownloader().download(ctx,
		url, dest, size, progressFn,
	)
	if err == nil && progressFn != nil {
		// End the progress line.
		fmt.Println()
	}
	return checksum, err
}

// aria2Args are the aria2c arguments that download url to dir/name
// over downloadConnections connections.
func aria2Args(url, dir, name string) []string {
	conns := strconv.Itoa(downloadConnections)
	return []string{
		// Ignore aria2.conf: options such as enable-rpc or daemon would
		// keep aria2c running after the download or detach it. A proxy
		// still applies through https_proxy, which the built-in
		// downloader also honors.
		"--no-conf=true",
		"--dir=" + dir,
		"--out=" + name,
		"--split=" + conns,
		"--max-connection-per-server=" + conns,
		"--min-split-size=" + strconv.Itoa(minPartSize>>20) + "M",
		"--file-allocation=none",
		"--allow-overwrite=true",
		"--auto-file-renaming=false",
		"--user-agent=" + updateUserAgent,
		"--console-log-level=warn",
		"--summary-interval=0",
		"--download-result=hide",
		url,
	}
}

// downloadWithAria2 runs the aria2c at aria2 to download url to dest.
// aria2c writes its progress and errors to the terminal.
func downloadWithAria2(ctx context.Context, aria2, url, dest string) error {
	cmd := exec.CommandContext(ctx, aria2,
		aria2Args(url, filepath.Dir(dest), filepath.Base(dest))...,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if _, err := os.Stat(dest); err != nil {
		return fmt.Errorf("aria2c exited without the download: %w", err)
	}
	return nil
}

// rangedDownloader downloads a file in parts over parallel HTTP Range
// requests, one connection per part.
type rangedDownloader struct {
	client       *http.Client
	connections  int
	minPart      int64
	attempts     int
	retryDelay   time.Duration
	stallTimeout time.Duration
}

func newRangedDownloader() rangedDownloader {
	return rangedDownloader{
		client:       newRangeClient(),
		connections:  downloadConnections,
		minPart:      minPartSize,
		attempts:     requestAttempts,
		retryDelay:   retryDelay,
		stallTimeout: stallTimeout,
	}
}

// newRangeClient returns the client for ranged downloads. It speaks
// only HTTP/1.1, because over HTTP/2 every part would share one TCP
// connection, which defeats splitting the download.
//
// The transport is built fresh rather than cloned from
// http.DefaultTransport: a clone inherits the default transport's
// HTTP/2 setup, so its TLS handshake still offers h2, and a server
// that accepts it closes the connection on the HTTP/1.1 request that
// follows.
//
// It sets no overall or TLS handshake timeout: a slow link can take
// minutes to fetch a release and many seconds to complete a handshake.
// The stall timeout of each request bounds both instead.
func newRangeClient() *http.Client {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}},
		Protocols:             &protocols,
		MaxIdleConns:          downloadConnections,
		MaxIdleConnsPerHost:   downloadConnections,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}}
}

// byteRange is an inclusive byte range, as in an HTTP Range header.
type byteRange struct{ start, end int64 }

// splitRanges divides size bytes into at most parts contiguous ranges
// of at least minPart bytes each. It returns no ranges for an unknown
// or empty size.
func splitRanges(size int64, parts int, minPart int64) []byteRange {
	if size <= 0 {
		return nil
	}
	n := max(min(int64(parts), size/minPart), 1)
	ranges := make([]byteRange, 0, n)
	step := size / n
	for i := range n {
		end := (i+1)*step - 1
		if i == n-1 {
			end = size - 1
		}
		ranges = append(ranges, byteRange{start: i * step, end: end})
	}
	return ranges
}

// download fetches url into dest. A file of known size large enough to
// split downloads in parts over parallel Range requests, and the result
// is "" because the parts arrive out of order and are not hashed in
// flight. Otherwise, or when the server ignores Range, it downloads
// over one connection and returns the SHA-256 it computed on the way.
func (d rangedDownloader) download(ctx context.Context,
	url, dest string, size int64,
	progressFn func(downloaded, total int64),
) (string, error) {
	name := filepath.Base(dest)
	ranges := splitRanges(size, d.connections, d.minPart)
	if len(ranges) < 2 {
		fmt.Printf("Downloading %s over one connection...\n", name)
		resp, err := d.openWithRetry(ctx, url, nil)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("download failed: %s", resp.Status)
		}
		return streamToFile(resp.Body, dest, size, progressFn)
	}

	fmt.Printf("Downloading %s over %d connections...\n", name, len(ranges))
	// One failed part cancels the others, including the first request,
	// which doubles as the check that the server honors Range.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	first, err := d.openWithRetry(ctx, url, &ranges[0])
	if err != nil {
		return "", err
	}
	switch first.StatusCode {
	case http.StatusOK:
		defer first.Body.Close()
		fmt.Println("The server ignored the range request; " +
			"downloading over one connection.")
		return streamToFile(first.Body, dest, size, progressFn)
	case http.StatusPartialContent:
		if err := checkContentRange(first, ranges[0], size); err != nil {
			first.Body.Close()
			return "", err
		}
	default:
		first.Body.Close()
		return "", fmt.Errorf("download failed: %s", first.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		first.Body.Close()
		return "", err
	}
	defer out.Close()

	var mu sync.Mutex
	var downloaded int64
	report := func(n int64) {
		mu.Lock()
		defer mu.Unlock()
		downloaded += n
		if progressFn != nil {
			progressFn(downloaded, size)
		}
	}
	// Report the start, so a progress display times the transfer from
	// the first response.
	report(0)

	var wg sync.WaitGroup
	var failOnce sync.Once
	var failure error
	for i, r := range ranges {
		var resp *http.Response
		if i == 0 {
			resp = first
		}
		wg.Go(func() {
			err := d.fetchPart(ctx, url, out, r, size, resp, report)
			if err != nil {
				failOnce.Do(func() {
					failure = err
					cancel(err)
				})
			}
		})
	}
	wg.Wait()
	if failure != nil {
		return "", failure
	}
	return "", out.Close()
}

// fetchPart writes bytes r of url into out at their offsets. resp, when
// set, is an open response for r. A failed or short attempt is retried
// from the first byte not yet written, up to d.attempts requests.
func (d rangedDownloader) fetchPart(ctx context.Context,
	url string, out *os.File, r byteRange, size int64,
	resp *http.Response, report func(int64),
) error {
	offset := r.start
	for attempt := 1; ; attempt++ {
		err := func() error {
			if resp == nil {
				rest := byteRange{start: offset, end: r.end}
				var err error
				if resp, err = d.open(ctx, url, &rest); err != nil {
					return err
				}
				if err := checkContentRange(resp, rest, size); err != nil {
					resp.Body.Close()
					resp = nil
					return err
				}
			}
			defer func() { resp.Body.Close(); resp = nil }()
			n, err := io.Copy(
				&countingWriter{
					w:      io.NewOffsetWriter(out, offset),
					report: report,
				},
				io.LimitReader(resp.Body, r.end-offset+1),
			)
			offset += n
			if err == nil && offset <= r.end {
				err = io.ErrUnexpectedEOF
			}
			return err
		}()
		if err == nil {
			return nil
		}
		if err := d.waitToRetry(ctx, attempt, err); err != nil {
			return fmt.Errorf("bytes %d-%d: %w", r.start, r.end, err)
		}
	}
}

// openWithRetry opens url, limited to r when r is set, and retries
// requests that fail to connect or get a server error (5xx). It returns
// any other response for the caller to judge.
func (d rangedDownloader) openWithRetry(ctx context.Context,
	url string, r *byteRange,
) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		resp, err := d.open(ctx, url, r)
		if err == nil && resp.StatusCode < http.StatusInternalServerError {
			return resp, nil
		}
		if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("download failed: %s", resp.Status)
		}
		if err := d.waitToRetry(ctx, attempt, err); err != nil {
			return nil, err
		}
	}
}

// waitToRetry waits d.retryDelay after attempt failed with err and
// returns nil, or returns the error to give up with: err once attempt
// is the last one, or the cause of ctx ending.
func (d rangedDownloader) waitToRetry(ctx context.Context,
	attempt int, err error,
) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if attempt >= d.attempts {
		return fmt.Errorf("after %d attempts: %w", attempt, err)
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-time.After(d.retryDelay):
		return nil
	}
}

// open sends a GET for url, limited to r when r is set. The response
// body fails with errStalled once no bytes arrive for d.stallTimeout,
// counting from the request; closing the body releases the request.
func (d rangedDownloader) open(ctx context.Context,
	url string, r *byteRange,
) (*http.Response, error) {
	reqCtx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(d.stallTimeout, func() {
		cancel(fmt.Errorf("%w: no data for %s", errStalled, d.stallTimeout))
	})
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		timer.Stop()
		cancel(nil)
		return nil, err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	if r != nil {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.start, r.end))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		timer.Stop()
		cancel(nil)
		if cause := context.Cause(reqCtx); errors.Is(cause, errStalled) {
			return nil, cause
		}
		return nil, err
	}
	resp.Body = &stallBody{
		ReadCloser: resp.Body,
		ctx:        reqCtx,
		cancel:     cancel,
		timer:      timer,
		timeout:    d.stallTimeout,
	}
	return resp, nil
}

// stallBody restarts its request's stall timer whenever bytes arrive.
type stallBody struct {
	io.ReadCloser
	ctx     context.Context
	cancel  context.CancelCauseFunc
	timer   *time.Timer
	timeout time.Duration
}

func (b *stallBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		if cause := context.Cause(b.ctx); errors.Is(cause, errStalled) {
			err = cause
		}
	}
	return n, err
}

func (b *stallBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// checkContentRange confirms that resp is a 206 response starting at
// r.start, ending no later than r.end, of a file of size bytes. A
// response ending early is fine: the caller resumes after it.
func checkContentRange(resp *http.Response, r byteRange, size int64) error {
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	header := resp.Header.Get("Content-Range")
	var start, end, total int64
	_, err := fmt.Sscanf(header, "bytes %d-%d/%d", &start, &end, &total)
	if err != nil || start != r.start || end < start || end > r.end ||
		total != size {
		return fmt.Errorf(
			"server sent Content-Range %q for requested bytes %d-%d/%d",
			header, r.start, r.end, size,
		)
	}
	return nil
}

// countingWriter reports the size of each write it passes on.
type countingWriter struct {
	w      io.Writer
	report func(int64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.report(int64(n))
	}
	return n, err
}

// streamToFile writes body to dest, reporting progress against total,
// and returns the SHA-256 of what it wrote.
func streamToFile(body io.Reader, dest string, total int64,
	progressFn func(downloaded, total int64),
) (string, error) {
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// Report the start, so a progress display times the transfer from
	// the response rather than from the first 32 KB.
	if progressFn != nil {
		progressFn(0, total)
	}
	hasher := sha256.New()
	var downloaded int64
	writer := &countingWriter{
		w: io.MultiWriter(out, hasher),
		report: func(n int64) {
			downloaded += n
			if progressFn != nil {
				progressFn(downloaded, total)
			}
		},
	}
	if _, err := io.Copy(writer, body); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

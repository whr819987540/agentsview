package update

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrNotRollingBuild reports that the running binary follows the upstream
// stable releases, which publish no per-commit builds to switch between.
var ErrNotRollingBuild = errors.New(
	"switching builds needs a rolling build; this binary follows the " +
		"upstream stable releases",
)

// ErrNotPublishedBuild reports that a binary does not match the published
// build its commit names, so switch cannot keep it to switch back to.
var ErrNotPublishedBuild = errors.New("binary does not match its published build")

const (
	buildsDirName       = "builds"
	dataVersionFileName = "DATA_VERSION"
	githubAPIBase       = "https://api.github.com"
	githubRawBase       = "https://raw.githubusercontent.com"
	// dataVersionSourcePath holds `const dataVersion = N` in every build.
	dataVersionSourcePath = "internal/db/db.go"
	maxDataVersionSource  = 16 << 20
	maxReleasePageSize    = 8 << 20
	releasesPerPage       = 100
	maxReleasePages       = 10
	binaryReportTimeout   = 30 * time.Second
)

var (
	dataVersionConstPattern = regexp.MustCompile(
		`(?m)^const dataVersion = ([0-9]+)\s*$`,
	)
	commitHashPattern     = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	describeCommitPattern = regexp.MustCompile(`-g([0-9a-f]{7,40})$`)
)

// buildSource is where snapshot builds and their metadata come from.
type buildSource struct {
	repo string
	// downloadRoot serves release assets at <root>/<tag>/<asset>.
	downloadRoot string
	// apiBase serves the GitHub REST API, which lists releases.
	apiBase string
	// rawBase serves source files at <base>/<repo>/<commit>/<path>.
	rawBase string
}

func githubBuildSource(repo string) buildSource {
	return buildSource{
		repo:         repo,
		downloadRoot: rollingDownloadRoot(repo),
		apiBase:      githubAPIBase,
		rawBase:      githubRawBase,
	}
}

// BinaryReport is what `agentsview version --json` says about a binary.
// DataVersion is 0 for builds that predate the data_version field.
type BinaryReport struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	DataVersion int    `json:"data_version"`
}

// ReportBinary runs `version --json` on the agentsview binary at path.
func ReportBinary(ctx context.Context, path string) (BinaryReport, error) {
	ctx, cancel := context.WithTimeout(ctx, binaryReportTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version", "--json").Output()
	if err != nil {
		return BinaryReport{}, fmt.Errorf("%s version --json: %w", path, err)
	}
	var report BinaryReport
	if err := json.Unmarshal(out, &report); err != nil {
		return BinaryReport{}, fmt.Errorf("%s version --json: %w", path, err)
	}
	return report, nil
}

// SupportsSwitch reports whether the agentsview binary at path has the
// switch command. Older builds reject `switch --list` with a non-zero exit;
// they print the root help for `switch --help`, so that probe cannot tell.
func SupportsSwitch(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, binaryReportTimeout)
	defer cancel()
	return exec.CommandContext(ctx, path, "switch", "--list").Run() == nil
}

// BuildCommit returns the commit hash a build identifies itself by: commit
// when it is a hash, else the g<hash> suffix of a rolling version such as
// v0.44.0-426-gae921ad3. It returns "" when neither names a commit.
func BuildCommit(version, commit string) string {
	commit = strings.ToLower(strings.TrimSpace(commit))
	if commitHashPattern.MatchString(commit) {
		return commit
	}
	version = strings.ToLower(strings.TrimSpace(version))
	if !IsRollingBuildVersion(version) {
		return ""
	}
	if m := describeCommitPattern.FindStringSubmatch(version); m != nil {
		return m[1]
	}
	return ""
}

// BuildsDir returns where switch keeps builds for the agentsview installed
// at installPath: lib/agentsview/builds beside the install directory, next
// to the CJK search extension in lib/agentsview/simple.
func BuildsDir(installPath string) string {
	return filepath.Clean(filepath.Join(
		filepath.Dir(installPath), "..", "lib", "agentsview", buildsDirName,
	))
}

// InstallPathFor returns the install that switch replaces when it runs from
// exe. A kept build, at <prefix>/lib/agentsview/builds/<tag>/<asset>,
// switches <prefix>/bin/agentsview, so a kept build can switch an install
// whose own binary predates the switch command. Any other executable is
// the install, as it is for agentsview update. It does not check that the
// install exists.
func InstallPathFor(exe string) string {
	tagDir := filepath.Dir(exe)
	builds := filepath.Dir(tagDir)
	appDir := filepath.Dir(builds)
	libDir := filepath.Dir(appDir)
	if !rollingBuildTagPattern.MatchString(filepath.Base(tagDir)) ||
		filepath.Base(builds) != buildsDirName ||
		filepath.Base(appDir) != "agentsview" ||
		filepath.Base(libDir) != "lib" {
		return exe
	}
	return filepath.Join(filepath.Dir(libDir), "bin", binaryName())
}

// KeptBuild is a build in the builds directory.
type KeptBuild struct {
	Tag        string
	Dir        string
	BinaryPath string
	// Checksum is the binary's SHA-256 that the build's SHA256SUMS lists.
	Checksum string
	// DataVersion is the parser data version the build opens archives at,
	// or 0 when it is not known yet.
	DataVersion int
	sidecar     *sidecarAsset
}

// Switcher finds, keeps, and installs rolling snapshot builds.
type Switcher struct {
	src       buildSource
	buildsDir string
	// report runs a binary's version --json. Tests replace it.
	report func(ctx context.Context, path string) (BinaryReport, error)

	listed      bool
	releaseTags []string
}

// NewSwitcher returns a Switcher for the agentsview installed at
// installPath. It fails with ErrNotRollingBuild on stable builds.
func NewSwitcher(installPath string) (*Switcher, error) {
	if rollingRepo == "" {
		return nil, ErrNotRollingBuild
	}
	return newSwitcher(githubBuildSource(rollingRepo), BuildsDir(installPath)), nil
}

func newSwitcher(src buildSource, buildsDir string) *Switcher {
	return &Switcher{src: src, buildsDir: buildsDir, report: ReportBinary}
}

// BuildsDir is the directory the Switcher keeps builds in.
func (s *Switcher) BuildsDir() string {
	return s.buildsDir
}

// ReleasesURL is the page that lists every published build.
func (s *Switcher) ReleasesURL() string {
	return "https://github.com/" + s.src.repo + "/releases"
}

// Resolve returns the snapshot tag that target names: "latest", a
// build-YYYYMMDD-<commit> tag, a commit hash of at least 7 characters, or a
// rolling version such as v0.44.0-426-gae921ad3.
func (s *Switcher) Resolve(ctx context.Context, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == rollingReleaseTag {
		return fetchRollingBuildTag(ctx,
			s.src.downloadRoot+"/"+rollingReleaseTag+"/"+rollingBuildTagAsset,
		)
	}
	if rollingBuildTagPattern.MatchString(target) {
		return target, nil
	}
	commit := BuildCommit(target, target)
	if commit == "" {
		return "", fmt.Errorf(
			"%q does not name a build; use latest, build-YYYYMMDD-<commit>, "+
				"a commit hash, or a rolling version", target,
		)
	}
	tag, err := s.TagForCommit(ctx, commit)
	if err != nil {
		return "", err
	}
	if tag == "" {
		return "", fmt.Errorf(
			"no published build for commit %s; see %s", commit, s.ReleasesURL(),
		)
	}
	return tag, nil
}

// TagForCommit returns the snapshot tag published for commit, or "" when
// there is none. Kept builds answer without a network request.
func (s *Switcher) TagForCommit(ctx context.Context, commit string) (string, error) {
	commit = strings.ToLower(commit)
	if !commitHashPattern.MatchString(commit) {
		return "", nil
	}
	if tag, err := matchCommit(commit, s.keptTags()); tag != "" || err != nil {
		return tag, err
	}
	tags, err := s.publishedTags(ctx)
	if err != nil {
		return "", err
	}
	return matchCommit(commit, tags)
}

// matchCommit returns the tag whose commit agrees with commit on the
// shorter of the two hashes. Several matches are an error.
func matchCommit(commit string, tags []string) (string, error) {
	var matches []string
	for _, tag := range tags {
		sha := tagCommit(tag)
		if strings.HasPrefix(sha, commit) || strings.HasPrefix(commit, sha) {
			matches = append(matches, tag)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf(
		"commit %s matches several builds (%s); give more of the hash or the build tag",
		commit, strings.Join(matches, ", "),
	)
}

// tagCommit is the commit hash a snapshot tag ends with.
func tagCommit(tag string) string {
	return tag[strings.LastIndexByte(tag, '-')+1:]
}

// publishedTags lists the snapshot tags of the repository's releases.
func (s *Switcher) publishedTags(ctx context.Context) ([]string, error) {
	if s.listed {
		return s.releaseTags, nil
	}
	var tags []string
	for page := 1; page <= maxReleasePages; page++ {
		url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d",
			s.src.apiBase, s.src.repo, releasesPerPage, page,
		)
		body, err := fetchSmallAsset(ctx, url, maxReleasePageSize)
		if err != nil {
			return nil, fmt.Errorf("list builds: %w", err)
		}
		var releases []struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal([]byte(body), &releases); err != nil {
			return nil, fmt.Errorf("list builds: %w", err)
		}
		for _, r := range releases {
			if rollingBuildTagPattern.MatchString(r.TagName) {
				tags = append(tags, r.TagName)
			}
		}
		if len(releases) < releasesPerPage {
			break
		}
	}
	s.releaseTags, s.listed = tags, true
	return tags, nil
}

// keptTags lists the tags that have a directory in the builds directory.
func (s *Switcher) keptTags() []string {
	entries, err := os.ReadDir(s.buildsDir)
	if err != nil {
		return nil
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() && rollingBuildTagPattern.MatchString(e.Name()) {
			tags = append(tags, e.Name())
		}
	}
	return tags
}

// List returns the kept builds, newest tag first. It reads each build's
// SHA256SUMS and DATA_VERSION but does not hash the binaries.
func (s *Switcher) List() []KeptBuild {
	var builds []KeptBuild
	for _, tag := range s.keptTags() {
		if b, err := s.readKept(tag); err == nil {
			builds = append(builds, *b)
		}
	}
	slices.SortFunc(builds, func(a, b KeptBuild) int {
		return strings.Compare(b.Tag, a.Tag)
	})
	return builds
}

// KeptTagOf returns the tag of the kept build whose checksum binaryPath
// matches, or "" when none does.
func (s *Switcher) KeptTagOf(binaryPath string) string {
	sum, err := hashFile(binaryPath)
	if err != nil {
		return ""
	}
	for _, b := range s.List() {
		if strings.EqualFold(b.Checksum, sum) {
			return b.Tag
		}
	}
	return ""
}

// readKept loads tag's build from the builds directory without verifying
// its binary.
func (s *Switcher) readKept(tag string) (*KeptBuild, error) {
	dir := filepath.Join(s.buildsDir, tag)
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	sums, err := os.ReadFile(filepath.Join(dir, rollingChecksumsAsset))
	if err != nil {
		return nil, err
	}
	checksum := extractChecksum(string(sums), asset)
	if checksum == "" {
		return nil, fmt.Errorf("%s lists no %s", rollingChecksumsAsset, asset)
	}
	b := &KeptBuild{
		Tag:        tag,
		Dir:        dir,
		BinaryPath: filepath.Join(dir, asset),
		Checksum:   checksum,
		sidecar:    s.sidecarFor(tag, string(sums)),
	}
	if _, err := os.Stat(b.BinaryPath); err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(filepath.Join(dir, dataVersionFileName)); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && v > 0 {
			b.DataVersion = v
		}
	}
	return b, nil
}

// sidecarFor returns the CJK search sidecar that tag's SHA256SUMS lists for
// this platform, or nil when it lists none.
func (s *Switcher) sidecarFor(tag, sums string) *sidecarAsset {
	name := rollingSidecarAssetName(runtime.GOOS, runtime.GOARCH)
	sum := extractChecksum(sums, name)
	if sum == "" {
		return nil
	}
	return &sidecarAsset{
		URL:      s.src.downloadRoot + "/" + tag + "/" + name,
		Name:     name,
		Checksum: sum,
	}
}

// verifiedKept loads tag's build from the builds directory and checks its
// binary against its SHA256SUMS.
func (s *Switcher) verifiedKept(tag string) (*KeptBuild, error) {
	b, err := s.readKept(tag)
	if err != nil {
		return nil, err
	}
	if err := verifyChecksum(b.BinaryPath, b.Checksum, ""); err != nil {
		return nil, err
	}
	return b, nil
}

// Ensure returns tag's build with its data version. A build that is not
// kept, or whose binary no longer matches its SHA256SUMS, is downloaded,
// verified, and kept first.
func (s *Switcher) Ensure(ctx context.Context,
	tag string, progressFn func(downloaded, total int64),
) (*KeptBuild, error) {
	b, err := s.verifiedKept(tag)
	if err != nil {
		b, err = s.download(ctx, tag, progressFn)
		if err != nil {
			return nil, err
		}
	}
	if err := s.ensureDataVersion(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// download fetches tag's binary and SHA256SUMS into the builds directory.
func (s *Switcher) download(ctx context.Context,
	tag string, progressFn func(downloaded, total int64),
) (*KeptBuild, error) {
	base := s.src.downloadRoot + "/" + tag
	sums, err := fetchChecksums(ctx, base+"/"+rollingChecksumsAsset)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", tag, err)
	}
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	expected := extractChecksum(sums, asset)
	if expected == "" {
		return nil, fmt.Errorf(
			"build %s has no binary for %s/%s", tag, runtime.GOOS, runtime.GOARCH,
		)
	}
	size, err := fetchContentLength(ctx, base+"/"+asset)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", tag, err)
	}
	return s.keep(tag, sums, func(dest string) error {
		precomputed, err := downloadAsset(ctx, base+"/"+asset, dest, size, progressFn)
		if err != nil {
			return fmt.Errorf("download %s: %w", asset, err)
		}
		return verifyChecksum(dest, expected, precomputed)
	})
}

// Keep stores the binary at binaryPath as tag's build after checking it
// against tag's published SHA256SUMS. dataVersion is recorded when it is
// known (above 0). An already kept build is returned as it is.
func (s *Switcher) Keep(ctx context.Context,
	tag, binaryPath string, dataVersion int,
) (*KeptBuild, error) {
	if b, err := s.verifiedKept(tag); err == nil {
		if b.DataVersion == 0 && dataVersion > 0 {
			if err := s.writeDataVersion(b, dataVersion); err != nil {
				return nil, err
			}
		}
		return b, nil
	}
	sums, err := fetchChecksums(ctx,
		s.src.downloadRoot+"/"+tag+"/"+rollingChecksumsAsset,
	)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", tag, err)
	}
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	expected := extractChecksum(sums, asset)
	if err := verifyChecksum(binaryPath, expected, ""); err != nil || expected == "" {
		return nil, fmt.Errorf("%s is not build %s: %w", binaryPath, tag, ErrNotPublishedBuild)
	}
	b, err := s.keep(tag, sums, func(dest string) error {
		return copyFile(binaryPath, dest)
	})
	if err != nil {
		return nil, err
	}
	if dataVersion > 0 {
		if err := s.writeDataVersion(b, dataVersion); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// keep writes tag's build into the builds directory: fill stores the
// binary at the path it is given, which must match sums afterwards. The
// build is staged in a sibling directory and renamed into place, so a
// failure never leaves a partial build under the tag's name.
func (s *Switcher) keep(tag, sums string, fill func(dest string) error) (*KeptBuild, error) {
	if err := os.MkdirAll(s.buildsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", s.buildsDir, err)
	}
	staged, err := os.MkdirTemp(s.buildsDir, "."+tag+".new-*")
	if err != nil {
		return nil, fmt.Errorf("stage build %s: %w", tag, err)
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(staged)
		}
	}()
	asset := rollingAssetName(runtime.GOOS, runtime.GOARCH)
	bin := filepath.Join(staged, asset)
	if err := fill(bin); err != nil {
		return nil, err
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		return nil, fmt.Errorf("chmod %s: %w", bin, err)
	}
	if err := os.WriteFile(
		filepath.Join(staged, rollingChecksumsAsset), []byte(sums), 0o644,
	); err != nil {
		return nil, fmt.Errorf("stage build %s: %w", tag, err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return nil, fmt.Errorf("stage build %s: %w", tag, err)
	}
	if err := replaceDir(staged, filepath.Join(s.buildsDir, tag)); err != nil {
		return nil, err
	}
	keep = true
	return s.readKept(tag)
}

// ensureDataVersion fills in b's data version when it is not recorded: from
// the binary's own report, else from the build's source.
func (s *Switcher) ensureDataVersion(ctx context.Context, b *KeptBuild) error {
	if b.DataVersion > 0 {
		return nil
	}
	if report, err := s.report(ctx, b.BinaryPath); err == nil && report.DataVersion > 0 {
		return s.writeDataVersion(b, report.DataVersion)
	}
	v, err := s.sourceDataVersion(ctx, tagCommit(b.Tag))
	if err != nil {
		return fmt.Errorf("find the data version of %s: %w", b.Tag, err)
	}
	return s.writeDataVersion(b, v)
}

// sourceDataVersion reads `const dataVersion = N` from commit's source.
func (s *Switcher) sourceDataVersion(ctx context.Context, commit string) (int, error) {
	url := fmt.Sprintf("%s/%s/%s/%s",
		s.src.rawBase, s.src.repo, commit, dataVersionSourcePath,
	)
	body, err := fetchSmallAsset(ctx, url, maxDataVersionSource)
	if err != nil {
		return 0, err
	}
	m := dataVersionConstPattern.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("%s at %s declares no dataVersion", dataVersionSourcePath, commit)
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%s at %s: invalid dataVersion %q", dataVersionSourcePath, commit, m[1])
	}
	return v, nil
}

func (s *Switcher) writeDataVersion(b *KeptBuild, v int) error {
	path := filepath.Join(b.Dir, dataVersionFileName)
	if err := os.WriteFile(path, []byte(strconv.Itoa(v)+"\n"), 0o644); err != nil {
		return fmt.Errorf("record data version of %s: %w", b.Tag, err)
	}
	b.DataVersion = v
	return nil
}

// InstallKeptBuild verifies b's binary again and installs it at dstPath,
// then brings the CJK search sidecar in line with b. The sidecar is
// optional: a failure to switch it is returned as a warning status, not an
// error, as agentsview update does.
func InstallKeptBuild(ctx context.Context, b *KeptBuild, dstPath string) (string, error) {
	if err := installRawBinaryTo(b.BinaryPath, b.Checksum, dstPath, ""); err != nil {
		return "", err
	}
	if b.sidecar == nil {
		return "", nil
	}
	status, err := installSidecar(ctx, b.sidecar, dstPath)
	if err != nil {
		return fmt.Sprintf("Warning: the CJK search extension was not switched: %v", err), nil
	}
	return status, nil
}

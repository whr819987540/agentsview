package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.kenn.io/agentsview/internal/config"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/update"
)

const (
	switchPollInterval = 2 * time.Second
	// switchReadyTimeout bounds how long switch waits for a server that a
	// service manager starts. Its startup re-parse runs before it listens,
	// so the wait covers the re-parse of a large archive. The server keeps
	// going after switch stops waiting.
	switchReadyTimeout = 30 * time.Minute
)

// SwitchConfig holds the agentsview switch flags.
type SwitchConfig struct {
	Target   string
	List     bool
	Yes      bool
	NoBackup bool
}

func newSwitchCommand() *cobra.Command {
	var cfg SwitchConfig
	cmd := &cobra.Command{
		Use:   "switch <build>",
		Short: "Switch to another rolling build, downloading it when needed",
		Long: `Switch the installed agentsview to another rolling build.

<build> is latest, a build tag such as build-20261126-ae921ad3, a commit
hash of at least 7 characters, or a rolling version such as
v0.44.0-426-gae921ad3. Builds are kept in lib/agentsview/builds beside the
install directory, so switching back needs no download. The installed build
is kept too before it is replaced.

Switch stops the server, backs up the archive when the data version
changes, marks the archive for a full re-parse when the target build is
older, installs the build, starts the server again, and waits until the
re-parse finishes. A server run by a systemd service is stopped and started
through systemctl.`,
		GroupID:      groupMeta,
		SilenceUsage: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if cfg.List {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				cfg.Target = args[0]
			}
			return runSwitch(cmd.Context(), cfg, os.Stdin, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&cfg.List, "list", false, "List kept builds and exit")
	cmd.Flags().BoolVar(&cfg.Yes, "yes", false, "Switch without a confirmation prompt")
	cmd.Flags().BoolVar(&cfg.NoBackup, "no-backup", false, "Do not back up the archive before a data version change")
	return cmd
}

func runSwitch(ctx context.Context, cfg SwitchConfig, in io.Reader, out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	installPath, err := update.InstallPathFor(exe)
	if err != nil {
		return err
	}
	sw, err := update.NewSwitcher(installPath)
	if err != nil {
		return err
	}
	if cfg.List {
		printKeptBuilds(out, sw, installPath)
		return nil
	}

	appCfg, err := loadDaemonConfigForUpdate()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	fmt.Fprintf(out, "Finding build %s...\n", cfg.Target)
	targetTag, err := sw.Resolve(ctx, cfg.Target)
	if err != nil {
		return err
	}
	installed := identifyInstalledBuild(ctx, sw, exe, installPath)
	if installed.tag == targetTag {
		fmt.Fprintf(out, "%s is already installed at %s.\n", targetTag, installPath)
		return nil
	}
	progress := newDownloadProgress(out, time.Now)
	target, err := sw.Ensure(ctx, targetTag, progress.update)
	if err != nil {
		return err
	}

	archivePath := switchArchivePath(appCfg)
	archiveVersion, archiveExists, err := db.ArchiveDataVersion(ctx, archivePath)
	if err != nil {
		return err
	}
	daemon, err := detectSwitchDaemon(ctx, appCfg)
	if err != nil {
		return err
	}

	plan := newSwitchPlan(switchPlanInput{
		installPath:    installPath,
		installed:      installed,
		target:         target,
		archivePath:    archivePath,
		archiveExists:  archiveExists,
		archiveVersion: archiveVersion,
		dataDir:        appCfg.DataDir,
		noBackup:       cfg.NoBackup,
		now:            time.Now(),
	})
	plan.switchBack = switchBackCommand(installed,
		update.SupportsSwitch(ctx, target.BinaryPath), sameFile(exe, installPath), exe,
	)
	printSwitchPlan(out, plan, sw.BuildsDir(), daemon)
	if !cfg.Yes && !confirmSwitch(in, out) {
		fmt.Fprintln(out, "Switch cancelled.")
		return nil
	}
	return performSwitch(ctx, plan, daemon, defaultSwitchOps(appCfg.DataDir), out)
}

// switchArchivePath is the SQLite archive the server opens.
func switchArchivePath(cfg config.Config) string {
	if cfg.DBPath != "" {
		return cfg.DBPath
	}
	return filepath.Join(cfg.DataDir, "sessions.db")
}

// installedBuild is what switch knows about the build it replaces.
type installedBuild struct {
	// name is the build tag when the build is kept, else its version.
	name        string
	dataVersion int
	// tag is set only when the build is kept, so switch can bring it back.
	tag string
	// keptPath is the kept copy of the build's binary.
	keptPath string
	// notKept says why the build could not be kept.
	notKept string
}

// identifyInstalledBuild finds the published build installed at installPath
// and keeps it, so a later switch can return to it without a download.
func identifyInstalledBuild(ctx context.Context,
	sw *update.Switcher, exe, installPath string,
) installedBuild {
	if tag := sw.KeptTagOf(installPath); tag != "" {
		b := installedBuild{name: tag, tag: tag}
		for _, kept := range sw.List() {
			if kept.Tag == tag {
				b.dataVersion, b.keptPath = kept.DataVersion, kept.BinaryPath
			}
		}
		// Record the data version of a build kept before switch knew it.
		if b.dataVersion == 0 {
			if kept, err := sw.Ensure(ctx, tag, nil); err == nil {
				b.dataVersion = kept.DataVersion
			}
		}
		return b
	}
	report := update.BinaryReport{
		Version: version, Commit: commit, DataVersion: db.CurrentDataVersion(),
	}
	if !sameFile(exe, installPath) {
		r, err := update.ReportBinary(ctx, installPath)
		if err != nil {
			return installedBuild{name: installPath, notKept: err.Error()}
		}
		report = r
	}
	b := installedBuild{name: report.Version, dataVersion: report.DataVersion}
	tag, err := sw.TagForCommit(ctx, update.BuildCommit(report.Version, report.Commit))
	switch {
	case err != nil:
		b.notKept = err.Error()
	case tag == "":
		b.notKept = "it is not a published build"
	default:
		kept, err := sw.Keep(ctx, tag, installPath, report.DataVersion)
		if err != nil {
			b.notKept = err.Error()
			break
		}
		b.name, b.tag, b.keptPath = tag, tag, kept.BinaryPath
		if kept.DataVersion > 0 {
			b.dataVersion = kept.DataVersion
		}
	}
	return b
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// switchPlan is everything a switch will do, decided before it changes
// anything.
type switchPlan struct {
	installPath    string
	installed      installedBuild
	target         *update.KeptBuild
	archivePath    string
	archiveExists  bool
	archiveVersion int
	// markReparse lowers the archive's data version below the target's, so
	// the target, which is older, re-parses every session on its first
	// start instead of refusing the archive.
	markReparse bool
	// reparse reports that the target re-parses every session on its first
	// start: it is older or newer than the archive's data version.
	reparse bool
	// backupDir receives a copy of the archive before anything changes. It
	// is empty when the data version stays or backups are turned off.
	backupDir string
	// switchBack is the command that returns to the installed build, or
	// empty when that build is not kept.
	switchBack string
}

type switchPlanInput struct {
	installPath    string
	installed      installedBuild
	target         *update.KeptBuild
	archivePath    string
	archiveExists  bool
	archiveVersion int
	dataDir        string
	noBackup       bool
	now            time.Time
}

func newSwitchPlan(in switchPlanInput) switchPlan {
	p := switchPlan{
		installPath:    in.installPath,
		installed:      in.installed,
		target:         in.target,
		archivePath:    in.archivePath,
		archiveExists:  in.archiveExists,
		archiveVersion: in.archiveVersion,
	}
	if !in.archiveExists || in.archiveVersion == in.target.DataVersion {
		return p
	}
	p.reparse = true
	p.markReparse = in.archiveVersion > in.target.DataVersion
	if !in.noBackup {
		p.backupDir = filepath.Join(in.dataDir, "backups",
			"switch-"+in.now.Format("20060102-150405"),
		)
	}
	return p
}

func printSwitchPlan(out io.Writer, p switchPlan, buildsDir string, d switchDaemon) {
	fmt.Fprintf(out, "Installed: %s\n", describeInstalledBuild(p.installed, buildsDir))
	fmt.Fprintf(out, "Switch to: %s (data version %d)\n", p.target.Tag, p.target.DataVersion)
	fmt.Fprintf(out, "Server:    %s\n", d.describe())
	switch {
	case !p.archiveExists:
		fmt.Fprintf(out, "Archive:   none at %s yet\n", p.archivePath)
	case p.markReparse:
		fmt.Fprintf(out,
			"Archive:   %s is at data version %d, newer than the build's. It is marked %d, so %s re-parses every session when it starts.\n",
			p.archivePath, p.archiveVersion, p.target.DataVersion-1, p.target.Tag,
		)
	case p.reparse:
		fmt.Fprintf(out,
			"Archive:   %s is at data version %d; %s re-parses every session when it starts.\n",
			p.archivePath, p.archiveVersion, p.target.Tag,
		)
	default:
		fmt.Fprintf(out, "Archive:   %s stays at data version %d\n", p.archivePath, p.archiveVersion)
	}
	switch {
	case p.backupDir != "":
		fmt.Fprintf(out, "Backup:    %s\n", p.backupDir)
	case p.reparse:
		fmt.Fprintln(out, "Backup:    skipped (--no-backup)")
	}
}

func describeInstalledBuild(b installedBuild, buildsDir string) string {
	dataVersion := ""
	if b.dataVersion > 0 {
		dataVersion = fmt.Sprintf(" (data version %d)", b.dataVersion)
	}
	if b.tag != "" {
		return fmt.Sprintf("%s%s, kept in %s", b.name, dataVersion, buildsDir)
	}
	return fmt.Sprintf(
		"%s%s, not kept (%s); switch cannot bring it back", b.name, dataVersion, b.notKept,
	)
}

func confirmSwitch(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "Switch builds? [y/N] ")
	answer, _ := bufio.NewReader(in).ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func printKeptBuilds(out io.Writer, sw *update.Switcher, installPath string) {
	builds := sw.List()
	if len(builds) == 0 {
		fmt.Fprintf(out,
			"No builds are kept in %s yet. agentsview switch keeps the installed build and each build it switches to.\n",
			sw.BuildsDir(),
		)
		return
	}
	installedTag := sw.KeptTagOf(installPath)
	fmt.Fprintf(out, "Builds kept in %s:\n", sw.BuildsDir())
	for _, b := range builds {
		mark, suffix := "  ", ""
		if b.Tag == installedTag {
			mark, suffix = "* ", "  (installed)"
		}
		dataVersion := "data version unknown"
		if b.DataVersion > 0 {
			dataVersion = fmt.Sprintf("data version %d", b.DataVersion)
		}
		fmt.Fprintf(out, "%s%s  %s%s\n", mark, b.Tag, dataVersion, suffix)
	}
	fmt.Fprintf(out, "Switch with `agentsview switch <build>`. Every published build: %s\n", sw.ReleasesURL())
}

// switchOps are the steps of a switch that touch the archive and the
// install. Tests replace them.
type switchOps struct {
	lock           func() (io.Closer, error)
	backup         func(path, dir string) ([]string, error)
	prepare        func(ctx context.Context, path string, target int) (int, bool, error)
	install        func(ctx context.Context, b *update.KeptBuild, dst string) (string, error)
	archiveVersion func(ctx context.Context, path string) (int, bool, error)
}

func defaultSwitchOps(dataDir string) switchOps {
	return switchOps{
		lock: func() (io.Closer, error) {
			l, err := tryAcquireWriteOwnerLock(dataDir)
			if err != nil {
				return nil, err
			}
			return l, nil
		},
		backup:         db.BackupArchive,
		prepare:        db.PrepareArchiveForDataVersion,
		install:        update.InstallKeptBuild,
		archiveVersion: db.ArchiveDataVersion,
	}
}

// performSwitch carries out plan. It stops the server, changes the archive
// and the install under the archive's write lock, then starts the server
// and checks that the new build brought the archive to its data version.
// When a step before the install fails, the server is started again on the
// build that is still installed.
func performSwitch(ctx context.Context,
	plan switchPlan, d switchDaemon, ops switchOps, out io.Writer,
) error {
	tag := plan.target.Tag
	wasRunning := d.running()
	if wasRunning {
		fmt.Fprintln(out, "Stopping the server...")
		if err := d.stop(ctx); err != nil {
			return fmt.Errorf("stopping the server: %w", err)
		}
	}
	if err := applySwitch(ctx, plan, ops, out); err != nil {
		if !wasRunning {
			return err
		}
		if startErr := d.start(ctx, plan.installPath); startErr != nil {
			return fmt.Errorf("%w (also failed to start the server again: %w)", err, startErr)
		}
		return err
	}
	if !wasRunning {
		next := "starts it"
		if plan.reparse {
			next = "starts it and re-parses every session"
		}
		fmt.Fprintf(out, "Switched to %s. No server was running; the next `agentsview serve` %s.\n", tag, next)
		printSwitchBack(out, plan)
		return nil
	}
	if plan.reparse {
		fmt.Fprintf(out, "Starting %s, which re-parses every session first...\n", tag)
	} else {
		fmt.Fprintf(out, "Starting %s...\n", tag)
	}
	started := time.Now()
	if err := d.start(ctx, plan.installPath); err != nil {
		return fmt.Errorf("%s is installed, but the server did not start: %w%s",
			tag, err, switchBackHint(plan),
		)
	}
	if plan.archiveExists {
		if err := checkSwitchedDataVersion(ctx, plan, ops); err != nil {
			return err
		}
		if plan.reparse {
			fmt.Fprintf(out, "Re-parse finished in %s.\n", time.Since(started).Round(time.Second))
		}
	}
	fmt.Fprintf(out, "Switched to %s.\n", tag)
	printSwitchBack(out, plan)
	return nil
}

// applySwitch backs up and marks the archive and installs the target while
// it holds the archive's write lock, so no other writer runs in between.
func applySwitch(ctx context.Context, plan switchPlan, ops switchOps, out io.Writer) error {
	tag := plan.target.Tag
	lock, err := ops.lock()
	if err != nil {
		return err
	}
	defer lock.Close()

	if plan.backupDir != "" {
		if _, err := ops.backup(plan.archivePath, plan.backupDir); err != nil {
			return fmt.Errorf("backing up the archive: %w", err)
		}
		fmt.Fprintf(out, "Backed up the archive to %s.\n", plan.backupDir)
	}
	if plan.markReparse {
		previous, changed, err := ops.prepare(ctx, plan.archivePath, plan.target.DataVersion)
		if err != nil {
			return fmt.Errorf("marking the archive for re-parse: %w", err)
		}
		if changed {
			fmt.Fprintf(out, "Marked the archive (data version %d) for %s to re-parse.\n", previous, tag)
		}
	}
	status, err := ops.install(ctx, plan.target, plan.installPath)
	if err != nil {
		if plan.markReparse {
			return fmt.Errorf(
				"installing %s: %w; the archive stays marked for re-parse, so the installed build re-parses every session when it starts",
				tag, err,
			)
		}
		return fmt.Errorf("installing %s: %w", tag, err)
	}
	if status != "" {
		fmt.Fprintln(out, status)
	}
	fmt.Fprintf(out, "Installed %s at %s.\n", tag, plan.installPath)
	return nil
}

// checkSwitchedDataVersion confirms that the started build brought the
// archive to its data version. A build stamps its version only after its
// startup re-parse succeeds; one that abandons the re-parse, for example
// when it finds no session sources at all, keeps the archive marked and
// tries again on its next start.
func checkSwitchedDataVersion(ctx context.Context, plan switchPlan, ops switchOps) error {
	want := plan.target.DataVersion
	got, _, err := ops.archiveVersion(ctx, plan.archivePath)
	if err != nil {
		return fmt.Errorf("%s is running, but its archive could not be read: %w", plan.target.Tag, err)
	}
	if got != want {
		return fmt.Errorf(
			"%s is running, but the archive is at data version %d instead of %d: "+
				"its re-parse did not finish, so check the server log; "+
				"the build tries again on its next start%s",
			plan.target.Tag, got, want, switchBackHint(plan),
		)
	}
	return nil
}

// switchBackCommand is the command that returns to the installed build.
// Builds older than the switch command cannot run it once installed, so
// the command then runs a binary that has it: the kept copy of the build
// being replaced, or exe when switch itself runs from a kept build.
func switchBackCommand(installed installedBuild,
	targetSupportsSwitch, exeIsInstall bool, exe string,
) string {
	if installed.tag == "" {
		return ""
	}
	runner := "agentsview"
	if !targetSupportsSwitch {
		runner = exe
		if exeIsInstall {
			runner = installed.keptPath
		}
	}
	return runner + " switch " + installed.tag
}

func printSwitchBack(out io.Writer, plan switchPlan) {
	if plan.switchBack != "" {
		fmt.Fprintf(out, "Switch back with `%s`.\n", plan.switchBack)
	}
}

func switchBackHint(plan switchPlan) string {
	if plan.switchBack == "" {
		return ""
	}
	return fmt.Sprintf("; switch back with `%s`", plan.switchBack)
}

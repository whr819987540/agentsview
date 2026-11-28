package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.kenn.io/agentsview/internal/config"
)

// switchDaemon is the writable server switch stops before it changes the
// install and starts again afterwards.
type switchDaemon interface {
	// describe says how the server runs and what switch does with it.
	describe() string
	running() bool
	stop(ctx context.Context) error
	// start runs the server again from the binary installed at exe and
	// returns once it is ready. A server runs its startup sync, including
	// a full re-parse, before it starts listening, so a ready server has
	// finished or abandoned that re-parse.
	start(ctx context.Context, exe string) error
}

// detectSwitchDaemon finds the writable server for cfg's data directory. A
// server that is the main process of a systemd service is handled through
// systemctl; any other server is stopped and restarted the way
// agentsview update does it.
func detectSwitchDaemon(ctx context.Context, cfg config.Config) (switchDaemon, error) {
	records, _ := localWritableDaemonRecordsWithFallback(cfg.DataDir, cfg.AuthToken)
	for _, rec := range records {
		if daemonRuntimeFromRecord(rec).ReadOnly {
			continue
		}
		if unit, ok := findSystemdUnit(ctx, rec.PID, readProcCgroup, runSystemctl); ok {
			unit.ready = func() bool { return writableDaemonAnswers(cfg) }
			return unit, nil
		}
		return &backgroundSwitchDaemon{cfg: cfg}, nil
	}
	if IsDaemonStarting(cfg.DataDir) {
		return nil, errors.New("agentsview server is starting; retry the switch once it is ready")
	}
	return noSwitchDaemon{}, nil
}

// noSwitchDaemon stands for a data directory with no server running.
type noSwitchDaemon struct{}

func (noSwitchDaemon) describe() string                    { return "no server is running" }
func (noSwitchDaemon) running() bool                       { return false }
func (noSwitchDaemon) stop(context.Context) error          { return nil }
func (noSwitchDaemon) start(context.Context, string) error { return nil }

// backgroundSwitchDaemon is a server that no service manager supervises.
type backgroundSwitchDaemon struct {
	cfg     config.Config
	stopped updateDaemonStopResult
}

func (d *backgroundSwitchDaemon) describe() string {
	return "a background server is running; switch stops it and starts the new build with the same settings"
}

func (d *backgroundSwitchDaemon) running() bool { return true }

func (d *backgroundSwitchDaemon) stop(ctx context.Context) error {
	result, err := stopWritableDaemonsForUpdate(ctx, d.cfg)
	d.stopped = result
	return err
}

// start runs `serve --background` from exe, which returns once the new
// server is ready.
func (d *backgroundSwitchDaemon) start(ctx context.Context, exe string) error {
	if !d.stopped.Stopped {
		return nil
	}
	return restartDaemonWithExecutable(ctx, exe, d.cfg, d.stopped)
}

// writableDaemonAnswers reports whether a writable server for cfg's data
// directory answers the ping probe. It accepts a server of any data
// version, since switch usually starts a build other than its own.
func writableDaemonAnswers(cfg config.Config) bool {
	for _, rec := range liveDaemonRecords(cfg.DataDir) {
		if !daemonRuntimeFromRecord(rec).ReadOnly && daemonRecordPingConfirmed(rec, cfg.AuthToken) {
			return true
		}
	}
	return false
}

// systemdUnit is a systemd service whose main process is the server.
// Switch stops and starts the unit through systemctl. Signalling the
// server directly would let the unit's Restart= policy start the old build
// again while switch replaces it, and a server started in the background
// would run outside the unit.
type systemdUnit struct {
	name      string
	user      bool
	pid       int
	systemctl func(ctx context.Context, args ...string) (string, error)
	// ready reports whether the started server answers.
	ready        func() bool
	pollInterval time.Duration
	// readyTimeout bounds the wait for the started server, which may be
	// re-parsing a large archive before it listens.
	readyTimeout time.Duration
}

// runSystemctl runs systemctl with args and returns its trimmed output.
func runSystemctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func readProcCgroup(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	return string(data), err
}

// findSystemdUnit returns the systemd service whose main process is pid.
// A process that only shares a service's cgroup, such as a server started
// in the background from a shell inside one, is not that service's main
// process and is not reported.
func findSystemdUnit(ctx context.Context, pid int,
	readCgroup func(pid int) (string, error),
	systemctl func(ctx context.Context, args ...string) (string, error),
) (*systemdUnit, bool) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return nil, false
	}
	content, err := readCgroup(pid)
	if err != nil {
		return nil, false
	}
	name, user, ok := systemdUnitFromCgroup(content)
	if !ok {
		return nil, false
	}
	unit := &systemdUnit{
		name: name, user: user, pid: pid, systemctl: systemctl,
		pollInterval: switchPollInterval, readyTimeout: switchReadyTimeout,
	}
	mainPID, err := systemctl(ctx, unit.args("show", "--property=MainPID", "--value")...)
	if err != nil || mainPID != strconv.Itoa(pid) {
		return nil, false
	}
	return unit, true
}

// systemdUnitFromCgroup returns the service unit named by the contents of
// /proc/<pid>/cgroup, and whether a user manager runs it (systemctl --user)
// rather than the system manager. It reads the unified hierarchy ("0::")
// or, on cgroup v1, the name=systemd hierarchy.
func systemdUnitFromCgroup(content string) (name string, user bool, ok bool) {
	for line := range strings.SplitSeq(content, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if (parts[0] != "0" || parts[1] != "") && parts[1] != "name=systemd" {
			continue
		}
		segments := strings.Split(strings.Trim(parts[2], "/"), "/")
		last := segments[len(segments)-1]
		if !strings.HasSuffix(last, ".service") || isUserManagerUnit(last) {
			continue
		}
		for _, seg := range segments[:len(segments)-1] {
			if isUserManagerUnit(seg) {
				user = true
			}
		}
		return last, user, true
	}
	return "", false, false
}

// isUserManagerUnit reports whether unit is a user's systemd manager, such
// as user@1000.service, under which that user's services run.
func isUserManagerUnit(unit string) bool {
	return strings.HasPrefix(unit, "user@") && strings.HasSuffix(unit, ".service")
}

// args builds a systemctl command line for the unit: verb, extra, name.
func (u *systemdUnit) args(verb string, extra ...string) []string {
	var args []string
	if u.user {
		args = append(args, "--user")
	}
	args = append(args, verb)
	args = append(args, extra...)
	return append(args, u.name)
}

// command is the systemctl command a person would type for verb.
func (u *systemdUnit) command(verb string) string {
	return "systemctl " + strings.Join(u.args(verb), " ")
}

func (u *systemdUnit) describe() string {
	scope := "system"
	if u.user {
		scope = "user"
	}
	return fmt.Sprintf(
		"the server runs as systemd %s unit %s; switch stops it with `%s` and starts it with `%s`",
		scope, u.name, u.command("stop"), u.command("start"),
	)
}

func (u *systemdUnit) running() bool { return true }

func (u *systemdUnit) stop(ctx context.Context) error {
	if out, err := u.systemctl(ctx, u.args("stop")...); err != nil {
		return fmt.Errorf("%s: %w: %s", u.command("stop"), err, out)
	}
	if !waitForProcessExit(u.pid, serveStopGraceTimeout) {
		return fmt.Errorf("%s returned, but pid %d is still running", u.command("stop"), u.pid)
	}
	return nil
}

// start starts the unit, which runs whatever its ExecStart names: the
// install switch replaced. systemctl returns as soon as the process runs,
// so start then waits for the server to answer, failing early when the
// unit stops.
func (u *systemdUnit) start(ctx context.Context, _ string) error {
	if out, err := u.systemctl(ctx, u.args("start")...); err != nil {
		return fmt.Errorf("%s: %w: %s", u.command("start"), err, out)
	}
	started := time.Now()
	for {
		if u.ready() {
			return nil
		}
		if err := u.alive(ctx); err != nil {
			return err
		}
		if time.Since(started) >= u.readyTimeout {
			return fmt.Errorf(
				"systemd unit %s did not answer within %s; it may still be re-parsing, see `%s`",
				u.name, u.readyTimeout, u.journalCommand(),
			)
		}
		timer := time.NewTimer(u.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("stopped waiting for systemd unit %s, which keeps starting: %w", u.name, ctx.Err())
		case <-timer.C:
		}
	}
}

func (u *systemdUnit) journalCommand() string {
	if u.user {
		return "journalctl --user -u " + u.name
	}
	return "journalctl -u " + u.name
}

func (u *systemdUnit) alive(ctx context.Context) error {
	// is-active exits non-zero for every state but active, so read the
	// state it prints instead of its exit status.
	state, _ := u.systemctl(ctx, u.args("is-active")...)
	switch state {
	case "active", "activating", "reloading":
		return nil
	}
	return fmt.Errorf("systemd unit %s is %s; see `%s`", u.name, state, u.journalCommand())
}

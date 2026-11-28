package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/update"
)

func TestSystemdUnitFromCgroup(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantUnit string
		wantUser bool
		wantOK   bool
	}{
		{
			name:     "user service",
			content:  "0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentsview.service\n",
			wantUnit: "agentsview.service", wantUser: true, wantOK: true,
		},
		{
			name:     "system service",
			content:  "0::/system.slice/agentsview.service\n",
			wantUnit: "agentsview.service", wantOK: true,
		},
		{
			name:    "terminal scope",
			content: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.gnome.Terminal.slice/vte-spawn-1.scope\n",
		},
		{
			name:    "tmux scope",
			content: "0::/user.slice/user-1000.slice/user@1000.service/tmux-spawn-2.scope\n",
		},
		{
			name:    "the user manager itself",
			content: "0::/user.slice/user-1000.slice/user@1000.service\n",
		},
		{
			name:     "cgroup v1 systemd hierarchy",
			content:  "12:cpu,cpuacct:/\n1:name=systemd:/system.slice/agentsview.service\n",
			wantUnit: "agentsview.service", wantOK: true,
		},
		{
			name:    "no systemd hierarchy",
			content: "12:cpu,cpuacct:/system.slice/agentsview.service\n",
		},
		{
			name:    "empty",
			content: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit, user, ok := systemdUnitFromCgroup(tt.content)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantUnit, unit)
			assert.Equal(t, tt.wantUser, user)
		})
	}
}

// fakeSystemctl answers systemctl commands from a table and records them.
type fakeSystemctl struct {
	answers map[string]string
	fail    map[string]bool
	calls   []string
}

func (f *fakeSystemctl) run(_ context.Context, args ...string) (string, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.fail[line] {
		return f.answers[line], errors.New("exit status 1")
	}
	return f.answers[line], nil
}

func TestFindSystemdUnitNeedsTheMainProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd units exist only on Linux")
	}
	cgroup := func(int) (string, error) {
		return "0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentsview.service\n", nil
	}
	show := "--user show --property=MainPID --value agentsview.service"

	main := &fakeSystemctl{answers: map[string]string{show: "4242"}}
	unit, ok := findSystemdUnit(t.Context(), 4242, cgroup, main.run)
	require.True(t, ok)
	assert.Equal(t, "agentsview.service", unit.name)
	assert.True(t, unit.user)

	// A server started in the background from a shell inside the unit
	// shares its cgroup but is not its main process; stopping the unit
	// would not be how it was started.
	child := &fakeSystemctl{answers: map[string]string{show: "1000"}}
	_, ok = findSystemdUnit(t.Context(), 4242, cgroup, child.run)
	assert.False(t, ok)

	broken := &fakeSystemctl{fail: map[string]bool{show: true}}
	_, ok = findSystemdUnit(t.Context(), 4242, cgroup, broken.run)
	assert.False(t, ok)

	unreadable := func(int) (string, error) { return "", errors.New("no such process") }
	_, ok = findSystemdUnit(t.Context(), 4242, unreadable, main.run)
	assert.False(t, ok)
}

func TestSystemdUnitCommands(t *testing.T) {
	ctl := &fakeSystemctl{answers: map[string]string{}}
	user := &systemdUnit{name: "agentsview.service", user: true, pid: -1, systemctl: ctl.run, ready: readyAfter(1)}
	system := &systemdUnit{name: "agentsview.service", pid: -1, systemctl: ctl.run, ready: readyAfter(1)}

	require.NoError(t, user.start(t.Context(), "/ignored"))
	require.NoError(t, system.start(t.Context(), "/ignored"))
	assert.Equal(t, []string{
		"--user start agentsview.service",
		"start agentsview.service",
	}, ctl.calls)
	assert.Contains(t, user.describe(), "`systemctl --user stop agentsview.service`")
}

func TestSystemdUnitAlive(t *testing.T) {
	tests := []struct {
		state   string
		wantErr bool
	}{
		{"active", false},
		{"activating", false},
		{"reloading", false},
		{"failed", true},
		{"inactive", true},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			ctl := &fakeSystemctl{
				answers: map[string]string{"--user is-active agentsview.service": tt.state},
				// is-active exits non-zero for every state but active.
				fail: map[string]bool{"--user is-active agentsview.service": tt.state != "active"},
			}
			unit := &systemdUnit{name: "agentsview.service", user: true, systemctl: ctl.run}
			err := unit.alive(t.Context())
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "journalctl --user -u agentsview.service")
		})
	}
}

func TestNewSwitchPlan(t *testing.T) {
	now := time.Date(2026, 11, 27, 22, 15, 0, 0, time.UTC)
	tests := []struct {
		name          string
		archiveExists bool
		archive       int
		target        int
		noBackup      bool
		wantMark      bool
		wantReparse   bool
		wantBackup    bool
	}{
		{"older target marks and backs up", true, 130, 129, false, true, true, true},
		{"newer target re-parses by itself", true, 129, 130, false, false, true, true},
		{"same data version needs nothing", true, 130, 130, false, false, false, false},
		{"no archive needs nothing", false, 0, 129, false, false, false, false},
		{"backups can be turned off", true, 130, 129, true, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newSwitchPlan(switchPlanInput{
				target:         &update.KeptBuild{Tag: "build-20261126-ae921ad3", DataVersion: tt.target},
				archivePath:    "/data/sessions.db",
				archiveExists:  tt.archiveExists,
				archiveVersion: tt.archive,
				dataDir:        "/data",
				noBackup:       tt.noBackup,
				now:            now,
			})
			assert.Equal(t, tt.wantMark, p.markReparse)
			assert.Equal(t, tt.wantReparse, p.reparse)
			if tt.wantBackup {
				assert.Equal(t, filepath.Join("/data", "backups", "switch-20261127-221500"), p.backupDir)
			} else {
				assert.Empty(t, p.backupDir)
			}
		})
	}
}

func TestSwitchBackCommand(t *testing.T) {
	kept := installedBuild{tag: "build-20261127-b168b550", keptPath: "/lib/builds/b168b550/agentsview-linux-amd64"}
	tests := []struct {
		name         string
		installed    installedBuild
		targetSwitch bool
		exeIsInstall bool
		exe          string
		want         string
	}{
		{"target has switch", kept, true, true, "/bin/agentsview", "agentsview switch build-20261127-b168b550"},
		{"older target runs the kept copy", kept, false, true, "/bin/agentsview", "/lib/builds/b168b550/agentsview-linux-amd64 switch build-20261127-b168b550"},
		{"switch run from a kept build reuses it", kept, false, false, "/lib/builds/c0ffee00/agentsview-linux-amd64", "/lib/builds/c0ffee00/agentsview-linux-amd64 switch build-20261127-b168b550"},
		{"unkept build cannot come back", installedBuild{name: "v0.44.0-dirty"}, true, true, "/bin/agentsview", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, switchBackCommand(tt.installed, tt.targetSwitch, tt.exeIsInstall, tt.exe))
		})
	}
}

// switchRecorder records the order of daemon and archive steps.
type switchRecorder struct {
	events []string
}

func (r *switchRecorder) add(event string) { r.events = append(r.events, event) }

type fakeSwitchDaemon struct {
	rec      *switchRecorder
	isUp     bool
	stopErr  error
	startErr error
}

func (d *fakeSwitchDaemon) describe() string { return "fake server" }
func (d *fakeSwitchDaemon) running() bool    { return d.isUp }
func (d *fakeSwitchDaemon) stop(context.Context) error {
	d.rec.add("stop")
	return d.stopErr
}

func (d *fakeSwitchDaemon) start(_ context.Context, exe string) error {
	d.rec.add("start " + exe)
	return d.startErr
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// fakeSwitchOps records each archive step; reads of the archive's data
// version return archiveVersion.
func fakeSwitchOps(rec *switchRecorder, archiveVersion int) *switchOps {
	return &switchOps{
		lock: func() (io.Closer, error) {
			rec.add("lock")
			return closerFunc(func() error { rec.add("unlock"); return nil }), nil
		},
		backup: func(path, dir string) ([]string, error) {
			rec.add("backup " + dir)
			return nil, nil
		},
		prepare: func(_ context.Context, path string, target int) (int, bool, error) {
			rec.add("prepare")
			return 130, true, nil
		},
		install: func(_ context.Context, b *update.KeptBuild, dst string) (string, error) {
			rec.add("install " + b.Tag)
			return "", nil
		},
		archiveVersion: func(context.Context, string) (int, bool, error) {
			rec.add("read")
			return archiveVersion, true, nil
		},
	}
}

func downgradePlan() switchPlan {
	return switchPlan{
		installPath:    "/bin/agentsview",
		installed:      installedBuild{name: "build-20261127-b168b550", tag: "build-20261127-b168b550"},
		target:         &update.KeptBuild{Tag: "build-20261126-ae921ad3", DataVersion: 129},
		archivePath:    "/data/sessions.db",
		archiveExists:  true,
		archiveVersion: 130,
		markReparse:    true,
		reparse:        true,
		backupDir:      "/data/backups/switch-1",
		switchBack:     "agentsview switch build-20261127-b168b550",
	}
}

func TestPerformSwitchOrder(t *testing.T) {
	rec := &switchRecorder{}
	d := &fakeSwitchDaemon{rec: rec, isUp: true}
	var out bytes.Buffer

	err := performSwitch(t.Context(), downgradePlan(), d, *fakeSwitchOps(rec, 129), &out)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"stop",
		"lock",
		"backup /data/backups/switch-1",
		"prepare",
		"install build-20261126-ae921ad3",
		"unlock",
		"start /bin/agentsview",
		"read",
	}, rec.events, "the archive changes only with the server stopped and the write lock held")
	assert.Contains(t, out.String(), "Switch back with `agentsview switch build-20261127-b168b550`")
}

func TestPerformSwitchRestartsInstalledBuildOnFailure(t *testing.T) {
	tests := []struct {
		name       string
		break_     func(*switchOps)
		wantEvents []string
		wantErr    string
	}{
		{
			name: "write lock held",
			break_: func(ops *switchOps) {
				ops.lock = func() (io.Closer, error) { return nil, errors.New("write lock is held") }
			},
			wantEvents: []string{"stop", "start /bin/agentsview"},
			wantErr:    "write lock is held",
		},
		{
			name: "backup fails",
			break_: func(ops *switchOps) {
				ops.backup = func(string, string) ([]string, error) { return nil, errors.New("disk full") }
			},
			wantEvents: []string{"stop", "lock", "unlock", "start /bin/agentsview"},
			wantErr:    "backing up the archive: disk full",
		},
		{
			name: "install fails",
			break_: func(ops *switchOps) {
				ops.install = func(context.Context, *update.KeptBuild, string) (string, error) {
					return "", errors.New("permission denied")
				}
			},
			wantEvents: []string{"stop", "lock", "backup /data/backups/switch-1", "prepare", "unlock", "start /bin/agentsview"},
			wantErr:    "re-parses every session",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &switchRecorder{}
			d := &fakeSwitchDaemon{rec: rec, isUp: true}
			ops := fakeSwitchOps(rec, 129)
			tt.break_(ops)

			err := performSwitch(t.Context(), downgradePlan(), d, *ops, io.Discard)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, tt.wantEvents, rec.events)
		})
	}
}

func TestPerformSwitchStopFailureChangesNothing(t *testing.T) {
	rec := &switchRecorder{}
	d := &fakeSwitchDaemon{rec: rec, isUp: true, stopErr: errors.New("permission denied")}

	err := performSwitch(t.Context(), downgradePlan(), d, *fakeSwitchOps(rec, 129), io.Discard)
	require.Error(t, err)
	assert.Equal(t, []string{"stop"}, rec.events)
}

func TestPerformSwitchWithoutServer(t *testing.T) {
	rec := &switchRecorder{}
	d := &fakeSwitchDaemon{rec: rec}
	var out bytes.Buffer

	err := performSwitch(t.Context(), downgradePlan(), d, *fakeSwitchOps(rec, 128), &out)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"lock", "backup /data/backups/switch-1", "prepare", "install build-20261126-ae921ad3", "unlock",
	}, rec.events, "with no server running, switch must not start one")
	assert.Contains(t, out.String(), "the next `agentsview serve` starts it and re-parses every session")
}

func TestPerformSwitchStartFailureNamesTheWayBack(t *testing.T) {
	rec := &switchRecorder{}
	d := &fakeSwitchDaemon{rec: rec, isUp: true, startErr: errors.New("unit failed")}

	err := performSwitch(t.Context(), downgradePlan(), d, *fakeSwitchOps(rec, 129), io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "switch back with `agentsview switch build-20261127-b168b550`")
}

func TestPerformSwitchReportsUnfinishedReparse(t *testing.T) {
	rec := &switchRecorder{}
	d := &fakeSwitchDaemon{rec: rec, isUp: true}

	// The started build abandoned its re-parse and left the archive marked.
	err := performSwitch(t.Context(), downgradePlan(), d, *fakeSwitchOps(rec, 128), io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at data version 128 instead of 129")
	assert.Contains(t, err.Error(), "switch back with `agentsview switch build-20261127-b168b550`")
	assert.Equal(t, "read", rec.events[len(rec.events)-1], "the archive is checked once the server is ready")
}

// readyAfter returns a ready probe that answers true from its nth call on.
func readyAfter(n int) func() bool {
	calls := 0
	return func() bool {
		calls++
		return calls >= n
	}
}

func TestSystemdUnitStartWaitsForTheServer(t *testing.T) {
	isActive := "--user is-active agentsview.service"
	newUnit := func(ctl *fakeSystemctl, ready func() bool) *systemdUnit {
		return &systemdUnit{
			name: "agentsview.service", user: true, systemctl: ctl.run, ready: ready,
			pollInterval: switchPollInterval, readyTimeout: switchReadyTimeout,
		}
	}

	t.Run("returns once the server answers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctl := &fakeSystemctl{answers: map[string]string{isActive: "activating"}}
			started := time.Now()

			require.NoError(t, newUnit(ctl, readyAfter(3)).start(t.Context(), ""))
			assert.Equal(t, 2*switchPollInterval, time.Since(started))
			assert.Equal(t, []string{"--user start agentsview.service", isActive, isActive}, ctl.calls)
		})
	})
	t.Run("fails as soon as the unit stops", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctl := &fakeSystemctl{
				answers: map[string]string{isActive: "failed"},
				fail:    map[string]bool{isActive: true},
			}
			started := time.Now()

			err := newUnit(ctl, readyAfter(1000)).start(t.Context(), "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is failed")
			assert.Zero(t, time.Since(started), "a failed unit must not be waited on")
		})
	})
	t.Run("gives up after the timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctl := &fakeSystemctl{answers: map[string]string{isActive: "active"}}
			started := time.Now()

			err := newUnit(ctl, func() bool { return false }).start(t.Context(), "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "did not answer within")
			assert.Equal(t, switchReadyTimeout, time.Since(started))
		})
	})
	t.Run("a failed systemctl start is reported", func(t *testing.T) {
		ctl := &fakeSystemctl{
			answers: map[string]string{"--user start agentsview.service": "Unit agentsview.service not found."},
			fail:    map[string]bool{"--user start agentsview.service": true},
		}
		err := newUnit(ctl, readyAfter(1)).start(t.Context(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

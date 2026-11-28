package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeArchiveVersion stamps version into the archive at path, as a build
// at that data version would.
func writeArchiveVersion(t *testing.T, path string, version int) {
	t.Helper()
	conn, err := sql.Open(sqliteArchiveDriverName, makeDSN(path, false))
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version = %d", version))
	require.NoError(t, err)
}

// archiveAt creates a real archive at path with one session and stamps it
// with version.
func archiveAt(t *testing.T, path string, version int) {
	t.Helper()
	d, err := Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, d.UpsertSession(t.Context(), Session{
		ID: "s1", Project: "proj", Machine: "local", Agent: "claude", MessageCount: 1,
	}))
	require.NoError(t, d.Close())
	writeArchiveVersion(t, path, version)
}

func TestArchiveDataVersion(t *testing.T) {
	dir := t.TempDir()

	version, exists, err := ArchiveDataVersion(t.Context(), filepath.Join(dir, "missing.db"))
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Zero(t, version)

	path := filepath.Join(dir, "sessions.db")
	archiveAt(t, path, dataVersion+3)
	version, exists, err = ArchiveDataVersion(t.Context(), path)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, dataVersion+3, version)
}

func TestPrepareArchiveForDataVersion(t *testing.T) {
	tests := []struct {
		name        string
		archive     int
		target      int
		wantVersion int
		wantChanged bool
	}{
		{"newer archive is marked below the target", 130, 129, 128, true},
		{"much newer archive is marked below the target", 140, 120, 119, true},
		{"archive at the target is left alone", 129, 129, 129, false},
		{"older archive is left alone", 100, 129, 100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sessions.db")
			archiveAt(t, path, tt.archive)

			previous, changed, err := PrepareArchiveForDataVersion(t.Context(), path, tt.target)
			require.NoError(t, err)
			assert.Equal(t, tt.archive, previous)
			assert.Equal(t, tt.wantChanged, changed)

			got, _, err := ArchiveDataVersion(t.Context(), path)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, got)
		})
	}
}

func TestPrepareArchiveForDataVersionRejectsInvalidTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	archiveAt(t, path, 5)

	_, _, err := PrepareArchiveForDataVersion(t.Context(), path, 0)
	require.Error(t, err)

	got, _, err := ArchiveDataVersion(t.Context(), path)
	require.NoError(t, err)
	assert.Equal(t, 5, got, "a rejected target must not change the archive")
}

// TestPrepareArchiveLetsOlderBuildOpen checks the contract switch relies
// on against this build's own Open: an archive from a newer build is
// refused, and once prepared for this build's data version it opens as
// stale, so the build re-parses it with its sessions intact.
func TestPrepareArchiveLetsOlderBuildOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	archiveAt(t, path, dataVersion+1)

	refused, err := Open(t.Context(), path)
	require.Nil(t, refused)
	require.True(t, IsDataVersionTooNew(err), "a newer archive must be refused before preparing: %v", err)

	_, changed, err := PrepareArchiveForDataVersion(t.Context(), path, dataVersion)
	require.NoError(t, err)
	require.True(t, changed)

	d, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })
	assert.True(t, d.NeedsResync(), "a prepared archive must open as stale so the build re-parses it")
	session, err := d.GetSession(t.Context(), "s1")
	require.NoError(t, err)
	require.NotNil(t, session, "preparing must keep the archived sessions")
}

func TestBackupArchiveIncludesUncheckpointedWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")

	// Hold a connection open after the write so SQLite keeps the committed
	// page in the WAL instead of checkpointing it on close.
	conn, err := sql.Open(sqliteArchiveDriverName, makeDSN(path, false))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	_, err = conn.ExecContext(t.Context(), "PRAGMA wal_autocheckpoint = 0")
	require.NoError(t, err)
	_, err = conn.ExecContext(t.Context(), "CREATE TABLE notes (body TEXT)")
	require.NoError(t, err)
	_, err = conn.ExecContext(t.Context(), "INSERT INTO notes VALUES ('only in the wal')")
	require.NoError(t, err)
	walInfo, err := os.Stat(path + "-wal")
	require.NoError(t, err)
	require.Positive(t, walInfo.Size(), "the write must still be in the WAL for this test")

	backupDir := filepath.Join(dir, "backups", "switch-1")
	written, err := BackupArchive(path, backupDir)
	require.NoError(t, err)
	assert.Contains(t, written, filepath.Join(backupDir, "sessions.db"))
	assert.Contains(t, written, filepath.Join(backupDir, "sessions.db-wal"))

	backup, err := sql.Open(sqliteArchiveDriverName, makeDSN(filepath.Join(backupDir, "sessions.db"), false))
	require.NoError(t, err)
	t.Cleanup(func() { backup.Close() })
	var body string
	require.NoError(t, backup.QueryRowContext(t.Context(), "SELECT body FROM notes").Scan(&body))
	assert.Equal(t, "only in the wal", body)
}

func TestBackupArchiveRefusesExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")
	archiveAt(t, path, 1)
	backupDir := filepath.Join(dir, "backup")
	require.NoError(t, os.Mkdir(backupDir, 0o700))
	marker := filepath.Join(backupDir, "sessions.db")
	require.NoError(t, os.WriteFile(marker, []byte("earlier backup"), 0o600))

	_, err := BackupArchive(path, backupDir)
	require.Error(t, err)

	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "earlier backup", string(data), "an earlier backup must not be overwritten")
}

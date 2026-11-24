package main

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
)

// curateArchive stars s1 and files s2 in alpha's Docs folder.
func curateArchive(t *testing.T, dataDir string) {
	t.Helper()
	d, err := db.Open(t.Context(), sessionsDBPath(dataDir))
	require.NoError(t, err)
	defer d.Close()
	_, err = d.StarSession(t.Context(), "s1")
	require.NoError(t, err)
	folder, err := d.CreateSessionFolder(t.Context(), "alpha", "Docs")
	require.NoError(t, err)
	require.NoError(t, d.MoveSessionsToFolder(t.Context(), folder.ID, []string{"s2"}))
}

func archiveCuration(t *testing.T, dataDir string) ([]string, map[string][]string) {
	t.Helper()
	d, err := db.OpenReadOnly(t.Context(), sessionsDBPath(dataDir))
	require.NoError(t, err)
	defer d.Close()
	starred, err := d.ListStarredSessionIDs(t.Context())
	require.NoError(t, err)
	folders, err := d.ListSessionFolders(t.Context())
	require.NoError(t, err)
	members := map[string][]string{}
	for _, f := range folders {
		members[f.Project+"/"+f.Name] = f.SessionIDs
	}
	return starred, members
}

func TestCurationMovesBetweenMachinesThroughDaemons(t *testing.T) {
	oldDir := testDataDir(t)
	seedSessionArchiveRows(t, oldDir,
		sessionSeed{id: "s1", project: "alpha"},
		sessionSeed{id: "s2", project: "alpha"},
	)
	curateArchive(t, oldDir)
	registerSQLiteWritableDaemonRuntime(t, oldDir)

	exportPath := filepath.Join(t.TempDir(), "curation.json")
	out, err := executeCommand(newRootCommand(), "curation", "export", "-o", exportPath)
	require.NoError(t, err)
	assert.Equal(t, "Exported 2 curated sessions and 1 folder to "+exportPath+"\n", out)
	info, err := os.Stat(exportPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the export names projects and folders, so only its owner reads it")

	// The new machine received s2 through artifact sync, which prefixes the
	// sending machine's origin.
	newDir := testDataDir(t)
	seedSessionArchiveRows(t, newDir,
		sessionSeed{id: "s1", project: "alpha"},
		sessionSeed{id: "laptop~s2", project: "alpha"},
	)
	registerSQLiteWritableDaemonRuntime(t, newDir)

	out, err = executeCommand(newRootCommand(), "curation", "import", exportPath, "--dry-run")
	require.NoError(t, err)
	assert.Equal(t, "Matched 2 of 2 sessions (1 through a prefixed ID).\n"+
		"Stars:           added 1, already here 0, kept existing 0\n"+
		"End marks:       added 0, already here 0, kept existing 0\n"+
		"Folder members:  added 1, already here 0, kept existing 0\n"+
		"Folders created: 1\n"+
		"Dry run: nothing was written.\n", out)
	starred, members := archiveCuration(t, newDir)
	assert.Empty(t, starred)
	assert.Empty(t, members)

	out, err = executeCommand(newRootCommand(), "curation", "import", exportPath, "--json")
	require.NoError(t, err)
	var result db.CurationImportResult
	require.NoError(t, json.Unmarshal([]byte(out), &result), "stdout: %q", out)
	assert.False(t, result.DryRun)
	assert.Equal(t, db.CurationImportCounts{Added: 1}, result.Stars)
	assert.Equal(t, db.CurationImportCounts{Added: 1}, result.FolderMembers)
	starred, members = archiveCuration(t, newDir)
	assert.Equal(t, []string{"s1"}, starred)
	assert.Equal(t, map[string][]string{"alpha/Docs": {"laptop~s2"}}, members)
}

func TestCurationImportReportListsUnmatchedSessions(t *testing.T) {
	r := &db.CurationImportResult{
		Sessions: 13, Matched: 1,
		Unmatched: []string{"u01", "u02", "u03", "u04", "u05", "u06", "u07", "u08", "u09", "u10", "u11"},
		Ambiguous: []string{"a1"},
		Stars:     db.CurationImportCounts{Added: 1},
	}
	var out bytes.Buffer
	printCurationImportResult(&out, r)
	assert.Equal(t, "Matched 1 of 13 sessions.\n"+
		"Stars:           added 1, already here 0, kept existing 0\n"+
		"End marks:       added 0, already here 0, kept existing 0\n"+
		"Folder members:  added 0, already here 0, kept existing 0\n"+
		"Folders created: 0\n"+
		"Not found here (11): u01, u02, u03, u04, u05, u06, u07, u08, u09, u10, and 1 more (use --json to list all)\n"+
		"Matched more than one session (1): a1\n", out.String())
}

func TestCurationImportRejectsBadFileBeforeOpeningTheArchive(t *testing.T) {
	dataDir := testDataDir(t)
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "not JSON", content: "stars: s1", wantErr: "reading curation export"},
		{name: "another document", content: `{"format":"recall","version":1}`, wantErr: `format is "recall"`},
		{name: "newer version", content: `{"format":"agentsview-curation","version":2}`, wantErr: "version 2 is not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "curation.json")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))
			_, err := executeCommand(newRootCommand(), "curation", "import", path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assertNoLocalSessionsDB(t, dataDir)
		})
	}
}

func TestCurationImportWithoutDaemonWritesTheArchiveDirectly(t *testing.T) {
	dataDir := testDataDir(t)
	t.Setenv("AGENTSVIEW_NO_DAEMON", "1")
	seedSessionArchiveRows(t, dataDir, sessionSeed{id: "s1", project: "alpha"})
	path := filepath.Join(t.TempDir(), "curation.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"format":"agentsview-curation","version":1,`+
		`"sessions":[{"id":"s1","agent":"claude","starred":true,"folder":"Docs"}],"folders":[]}`), 0o600))

	_, err := executeCommand(newRootCommand(), "curation", "import", path)
	require.NoError(t, err)
	starred, members := archiveCuration(t, dataDir)
	assert.Equal(t, []string{"s1"}, starred)
	assert.Equal(t, map[string][]string{"alpha/Docs": {"s1"}}, members)
}

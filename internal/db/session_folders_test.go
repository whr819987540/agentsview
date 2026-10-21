package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSessionFolderName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trims surrounding space", input: "  Release work  ", want: "Release work"},
		{name: "keeps inner punctuation", input: "bugs / triage", want: "bugs / triage"},
		{name: "120 CJK runes fit", input: strings.Repeat("目", 120), want: strings.Repeat("目", 120)},
		{name: "empty", input: "", wantErr: true},
		{name: "only spaces", input: "   ", wantErr: true},
		{name: "121 runes", input: strings.Repeat("a", 121), wantErr: true},
		{name: "newline inside", input: "a\nb", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSessionFolderName(tt.input)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrSessionFolderInvalid)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSessionFolderLifecycle(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	insertSession(t, d, "a1", "alpha")
	insertSession(t, d, "a2", "alpha")
	insertSession(t, d, "a3", "alpha")
	insertSession(t, d, "b1", "beta")

	docs, err := d.CreateSessionFolder(ctx, " alpha ", "  Docs ")
	require.NoError(t, err)
	assert.Equal(t, "alpha", docs.Project)
	assert.Equal(t, "Docs", docs.Name)
	assert.Empty(t, docs.SessionIDs)

	bugs, err := d.CreateSessionFolder(ctx, "alpha", "Bugs")
	require.NoError(t, err)
	_, err = d.CreateSessionFolder(ctx, "alpha", "Docs")
	require.ErrorIs(t, err, ErrSessionFolderNameTaken)
	betaDocs, err := d.CreateSessionFolder(ctx, "beta", "Docs")
	require.NoError(t, err, "another project may reuse a folder name")
	_, err = d.CreateSessionFolder(ctx, "", "Docs")
	require.ErrorIs(t, err, ErrSessionFolderInvalid)

	// b1 belongs to beta and "missing" does not exist; both are skipped.
	require.NoError(t, d.MoveSessionsToFolder(ctx, docs.ID, []string{"a1", "a2", "b1", "missing"}))
	// Moving a2 again takes it out of Docs.
	require.NoError(t, d.MoveSessionsToFolder(ctx, bugs.ID, []string{"a2"}))
	require.NoError(t, d.MoveSessionsToFolder(ctx, betaDocs.ID, []string{"b1"}))
	require.ErrorIs(t, d.MoveSessionsToFolder(ctx, 9999, []string{"a3"}), sql.ErrNoRows)

	assert.Equal(t, map[string][]string{
		"alpha/Bugs": {"a2"},
		"alpha/Docs": {"a1"},
		"beta/Docs":  {"b1"},
	}, folderMembers(t, d))

	renamed, err := d.RenameSessionFolder(ctx, docs.ID, " Notes ")
	require.NoError(t, err)
	assert.Equal(t, "Notes", renamed.Name)
	assert.Equal(t, []string{"a1"}, renamed.SessionIDs)
	_, err = d.RenameSessionFolder(ctx, docs.ID, "Bugs")
	require.ErrorIs(t, err, ErrSessionFolderNameTaken)
	_, err = d.RenameSessionFolder(ctx, docs.ID, " ")
	require.ErrorIs(t, err, ErrSessionFolderInvalid)
	_, err = d.RenameSessionFolder(ctx, 9999, "Other")
	require.ErrorIs(t, err, sql.ErrNoRows)

	require.NoError(t, d.RemoveSessionsFromFolders(ctx, []string{"a2", "missing"}))
	assert.Equal(t, map[string][]string{
		"alpha/Bugs":  {},
		"alpha/Notes": {"a1"},
		"beta/Docs":   {"b1"},
	}, folderMembers(t, d))

	require.NoError(t, d.DeleteSessionFolder(ctx, renamed.ID))
	require.ErrorIs(t, d.DeleteSessionFolder(ctx, renamed.ID), sql.ErrNoRows)
	assert.Equal(t, map[string][]string{
		"alpha/Bugs": {},
		"beta/Docs":  {"b1"},
	}, folderMembers(t, d))
	session, err := d.GetSession(ctx, "a1")
	require.NoError(t, err)
	require.NotNil(t, session, "deleting a folder keeps its sessions")
	assert.Equal(t, "alpha", session.Project)

	// The folder name is free again once its folder is gone.
	_, err = d.CreateSessionFolder(ctx, "alpha", "Notes")
	require.NoError(t, err)
}

func TestDeleteSessionRemovesFolderMembership(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	insertSession(t, d, "a1", "alpha")
	insertSession(t, d, "a2", "alpha")
	folder, err := d.CreateSessionFolder(ctx, "alpha", "Docs")
	require.NoError(t, err)
	require.NoError(t, d.MoveSessionsToFolder(ctx, folder.ID, []string{"a1", "a2"}))

	require.NoError(t, d.DeleteSession(ctx, "a1"))

	assert.Equal(t, map[string][]string{"alpha/Docs": {"a2"}}, folderMembers(t, d))
}

func TestCopySessionMetadataFromPreservesSessionFolders(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	src, err := Open(ctx, srcPath)
	require.NoError(t, err)
	insertSession(t, src, "kept", "alpha")
	insertSession(t, src, "gone", "alpha")
	docs, err := src.CreateSessionFolder(ctx, "alpha", "Docs")
	require.NoError(t, err)
	_, err = src.CreateSessionFolder(ctx, "alpha", "Empty")
	require.NoError(t, err)
	require.NoError(t, src.MoveSessionsToFolder(ctx, docs.ID, []string{"kept", "gone"}))
	require.NoError(t, src.Close())

	// The rebuilt archive re-synced only "kept".
	dst, err := Open(ctx, filepath.Join(dir, "dst.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, dst.Close()) })
	insertSession(t, dst, "kept", "alpha")

	require.NoError(t, dst.CopySessionMetadataFrom(srcPath))

	assert.Equal(t, map[string][]string{
		"alpha/Docs":  {"kept"},
		"alpha/Empty": {},
	}, folderMembers(t, dst))
	folders, err := dst.ListSessionFolders(ctx)
	require.NoError(t, err)
	require.Len(t, folders, 2)
	assert.Equal(t, docs.ID, folders[0].ID, "folder IDs survive the rebuild")
}

func TestOpenReadOnlyListsSessionFoldersAndRejectsEdits(t *testing.T) {
	path := createClosedTestDB(t, tempDBPath(t, "sessions.db"), func(d *DB) {
		ctx := t.Context()
		insertSession(t, d, "a1", "alpha")
		folder, err := d.CreateSessionFolder(ctx, "alpha", "Docs")
		require.NoError(t, err)
		require.NoError(t, d.MoveSessionsToFolder(ctx, folder.ID, []string{"a1"}))
	})
	readonly := openReadOnlyTestDB(t, path)

	assert.Equal(t, map[string][]string{"alpha/Docs": {"a1"}}, folderMembers(t, readonly))

	ctx := t.Context()
	requireReadOnlyOp(t, "CreateSessionFolder", func() error {
		_, err := readonly.CreateSessionFolder(ctx, "alpha", "New")
		return err
	})
	requireReadOnlyOp(t, "RenameSessionFolder", func() error {
		_, err := readonly.RenameSessionFolder(ctx, 1, "New")
		return err
	})
	requireReadOnlyOp(t, "DeleteSessionFolder", func() error {
		return readonly.DeleteSessionFolder(ctx, 1)
	})
	requireReadOnlyOp(t, "MoveSessionsToFolder", func() error {
		return readonly.MoveSessionsToFolder(ctx, 1, []string{"a1"})
	})
	requireReadOnlyOp(t, "RemoveSessionsFromFolders", func() error {
		return readonly.RemoveSessionsFromFolders(ctx, nil)
	})
}

// folderMembers lists folders as "project/name" -> member session IDs.
func folderMembers(t *testing.T, d *DB) map[string][]string {
	t.Helper()
	folders, err := d.ListSessionFolders(t.Context())
	require.NoError(t, err)
	out := make(map[string][]string, len(folders))
	for _, f := range folders {
		out[f.Project+"/"+f.Name] = f.SessionIDs
	}
	return out
}

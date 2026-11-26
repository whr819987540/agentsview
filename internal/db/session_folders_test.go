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

	docs, err := d.CreateSessionFolder(ctx, " alpha ", "  Docs ", nil)
	require.NoError(t, err)
	assert.Equal(t, "alpha", docs.Project)
	assert.Equal(t, "Docs", docs.Name)
	assert.Empty(t, docs.SessionIDs)

	bugs, err := d.CreateSessionFolder(ctx, "alpha", "Bugs", nil)
	require.NoError(t, err)
	_, err = d.CreateSessionFolder(ctx, "alpha", "Docs", nil)
	require.ErrorIs(t, err, ErrSessionFolderNameTaken)
	betaDocs, err := d.CreateSessionFolder(ctx, "beta", "Docs", nil)
	require.NoError(t, err, "another project may reuse a folder name")
	_, err = d.CreateSessionFolder(ctx, "", "Docs", nil)
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
	_, err = d.CreateSessionFolder(ctx, "alpha", "Notes", nil)
	require.NoError(t, err)
}

func TestDeleteSessionRemovesFolderMembership(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	insertSession(t, d, "a1", "alpha")
	insertSession(t, d, "a2", "alpha")
	folder, err := d.CreateSessionFolder(ctx, "alpha", "Docs", nil)
	require.NoError(t, err)
	require.NoError(t, d.MoveSessionsToFolder(ctx, folder.ID, []string{"a1", "a2"}))

	require.NoError(t, d.DeleteSession(ctx, "a1"))

	assert.Equal(t, map[string][]string{"alpha/Docs": {"a2"}}, folderMembers(t, d))
}

func TestSessionFolderNesting(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	insertSession(t, d, "a1", "alpha")

	work, err := d.CreateSessionFolder(ctx, "alpha", "Work", nil)
	require.NoError(t, err)
	assert.Nil(t, work.ParentID)
	bugs, err := d.CreateSessionFolder(ctx, "alpha", "Bugs", &work.ID)
	require.NoError(t, err)
	require.NotNil(t, bugs.ParentID)
	assert.Equal(t, work.ID, *bugs.ParentID)
	triage, err := d.CreateSessionFolder(ctx, "alpha", "Triage", &bugs.ID)
	require.NoError(t, err)
	betaDocs, err := d.CreateSessionFolder(ctx, "beta", "Docs", nil)
	require.NoError(t, err)

	_, err = d.CreateSessionFolder(ctx, "alpha", "Work", &bugs.ID)
	require.ErrorIs(t, err, ErrSessionFolderNameTaken,
		"names stay unique across the project, at every level")
	_, err = d.CreateSessionFolder(ctx, "alpha", "Other", &betaDocs.ID)
	require.ErrorIs(t, err, ErrSessionFolderInvalid, "a parent from another project")
	_, err = d.CreateSessionFolder(ctx, "alpha", "Other", new(int64(9999)))
	require.ErrorIs(t, err, ErrSessionFolderInvalid, "a missing parent")

	assert.Equal(t, map[string]string{
		"alpha/Work":   "",
		"alpha/Bugs":   "Work",
		"alpha/Triage": "Bugs",
		"beta/Docs":    "",
	}, folderParents(t, d))

	// A session filed deep down stays in its folder while folders move.
	require.NoError(t, d.MoveSessionsToFolder(ctx, triage.ID, []string{"a1"}))

	invalid := []struct {
		name     string
		id       int64
		parentID int64
	}{
		{name: "into itself", id: work.ID, parentID: work.ID},
		{name: "into its child", id: work.ID, parentID: bugs.ID},
		{name: "into its grandchild", id: work.ID, parentID: triage.ID},
		{name: "into another project", id: bugs.ID, parentID: betaDocs.ID},
		{name: "into a missing folder", id: bugs.ID, parentID: 9999},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.MoveSessionFolder(ctx, tt.id, &tt.parentID)
			require.ErrorIs(t, err, ErrSessionFolderInvalid)
		})
	}
	_, err = d.MoveSessionFolder(ctx, 9999, nil)
	require.ErrorIs(t, err, sql.ErrNoRows)

	moved, err := d.MoveSessionFolder(ctx, triage.ID, &work.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ParentID)
	assert.Equal(t, work.ID, *moved.ParentID)
	assert.Equal(t, []string{"a1"}, moved.SessionIDs)
	// Bugs is no longer above Triage, so it can now go inside it.
	_, err = d.MoveSessionFolder(ctx, bugs.ID, &triage.ID)
	require.NoError(t, err)
	top, err := d.MoveSessionFolder(ctx, triage.ID, nil)
	require.NoError(t, err)
	assert.Nil(t, top.ParentID)

	assert.Equal(t, map[string]string{
		"alpha/Work":   "",
		"alpha/Triage": "",
		"alpha/Bugs":   "Triage",
		"beta/Docs":    "",
	}, folderParents(t, d))
	assert.Equal(t, []string{"a1"}, folderMembers(t, d)["alpha/Triage"])
}

func TestDeleteSessionFolderMovesItsContentsUp(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	for _, id := range []string{"a1", "a2", "a3"} {
		insertSession(t, d, id, "alpha")
	}
	work, err := d.CreateSessionFolder(ctx, "alpha", "Work", nil)
	require.NoError(t, err)
	bugs, err := d.CreateSessionFolder(ctx, "alpha", "Bugs", &work.ID)
	require.NoError(t, err)
	triage, err := d.CreateSessionFolder(ctx, "alpha", "Triage", &bugs.ID)
	require.NoError(t, err)
	_, err = d.CreateSessionFolder(ctx, "alpha", "Notes", &bugs.ID)
	require.NoError(t, err)
	require.NoError(t, d.MoveSessionsToFolder(ctx, work.ID, []string{"a1"}))
	require.NoError(t, d.MoveSessionsToFolder(ctx, bugs.ID, []string{"a2"}))
	require.NoError(t, d.MoveSessionsToFolder(ctx, triage.ID, []string{"a3"}))

	// Deleting a nested folder moves its sessions and subfolders into the
	// folder that held it.
	require.NoError(t, d.DeleteSessionFolder(ctx, bugs.ID))
	assert.Equal(t, map[string]string{
		"alpha/Work":   "",
		"alpha/Triage": "Work",
		"alpha/Notes":  "Work",
	}, folderParents(t, d))
	assert.Equal(t, map[string][]string{
		"alpha/Work":   {"a1", "a2"},
		"alpha/Triage": {"a3"},
		"alpha/Notes":  {},
	}, folderMembers(t, d))

	// Deleting a top-level folder moves them to the project's top level.
	require.NoError(t, d.DeleteSessionFolder(ctx, work.ID))
	assert.Equal(t, map[string]string{
		"alpha/Triage": "",
		"alpha/Notes":  "",
	}, folderParents(t, d))
	assert.Equal(t, map[string][]string{
		"alpha/Triage": {"a3"},
		"alpha/Notes":  {},
	}, folderMembers(t, d))
}

func TestSessionFolderParentMigrationKeepsFoldersAtTopLevel(t *testing.T) {
	path := createClosedTestDB(t, tempDBPath(t, "sessions.db"), func(d *DB) {
		insertSession(t, d, "a1", "alpha")
		folder, err := d.CreateSessionFolder(t.Context(), "alpha", "Docs", nil)
		require.NoError(t, err)
		require.NoError(t, d.MoveSessionsToFolder(t.Context(), folder.ID, []string{"a1"}))
	})
	execRawSQLite(t, path, "ALTER TABLE session_folders DROP COLUMN parent_id")

	d, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	assert.Equal(t, map[string]string{"alpha/Docs": ""}, folderParents(t, d))
	assert.Equal(t, map[string][]string{"alpha/Docs": {"a1"}}, folderMembers(t, d))
	docs := folderByName(t, d, "alpha", "Docs")
	_, err = d.CreateSessionFolder(t.Context(), "alpha", "Inner", &docs.ID)
	require.NoError(t, err, "the migrated archive takes nested folders")
}

func TestCopySessionMetadataFromPreservesSessionFolders(t *testing.T) {
	tests := []struct {
		name string
		// legacy drops parent_id from the old archive, as before nesting.
		legacy      bool
		wantParents map[string]string
	}{
		{
			name:        "nesting is kept",
			wantParents: map[string]string{"alpha/Docs": "", "alpha/Empty": "", "alpha/Inner": "Docs"},
		},
		{
			name:        "folders from before nesting sit at the top level",
			legacy:      true,
			wantParents: map[string]string{"alpha/Docs": "", "alpha/Empty": "", "alpha/Inner": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			srcPath := filepath.Join(dir, "src.db")
			src, err := Open(ctx, srcPath)
			require.NoError(t, err)
			insertSession(t, src, "kept", "alpha")
			insertSession(t, src, "gone", "alpha")
			// Inner gets the lower ID, so the copy meets the child before
			// its parent.
			inner, err := src.CreateSessionFolder(ctx, "alpha", "Inner", nil)
			require.NoError(t, err)
			docs, err := src.CreateSessionFolder(ctx, "alpha", "Docs", nil)
			require.NoError(t, err)
			_, err = src.MoveSessionFolder(ctx, inner.ID, &docs.ID)
			require.NoError(t, err)
			_, err = src.CreateSessionFolder(ctx, "alpha", "Empty", nil)
			require.NoError(t, err)
			require.NoError(t, src.MoveSessionsToFolder(ctx, docs.ID, []string{"kept", "gone"}))
			require.NoError(t, src.Close())
			if tt.legacy {
				execRawSQLite(t, srcPath, "ALTER TABLE session_folders DROP COLUMN parent_id")
			}

			// The rebuilt archive re-synced only "kept".
			dst, err := Open(ctx, filepath.Join(dir, "dst.db"))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, dst.Close()) })
			insertSession(t, dst, "kept", "alpha")

			require.NoError(t, dst.CopySessionMetadataFrom(srcPath))

			assert.Equal(t, map[string][]string{
				"alpha/Docs":  {"kept"},
				"alpha/Empty": {},
				"alpha/Inner": {},
			}, folderMembers(t, dst))
			assert.Equal(t, tt.wantParents, folderParents(t, dst))
			assert.Equal(t, docs.ID, folderByName(t, dst, "alpha", "Docs").ID,
				"folder IDs survive the rebuild")
		})
	}
}

func TestOpenReadOnlyListsSessionFoldersAndRejectsEdits(t *testing.T) {
	path := createClosedTestDB(t, tempDBPath(t, "sessions.db"), func(d *DB) {
		ctx := t.Context()
		insertSession(t, d, "a1", "alpha")
		folder, err := d.CreateSessionFolder(ctx, "alpha", "Docs", nil)
		require.NoError(t, err)
		require.NoError(t, d.MoveSessionsToFolder(ctx, folder.ID, []string{"a1"}))
	})
	readonly := openReadOnlyTestDB(t, path)

	assert.Equal(t, map[string][]string{"alpha/Docs": {"a1"}}, folderMembers(t, readonly))

	ctx := t.Context()
	requireReadOnlyOp(t, "CreateSessionFolder", func() error {
		_, err := readonly.CreateSessionFolder(ctx, "alpha", "New", nil)
		return err
	})
	requireReadOnlyOp(t, "RenameSessionFolder", func() error {
		_, err := readonly.RenameSessionFolder(ctx, 1, "New")
		return err
	})
	requireReadOnlyOp(t, "MoveSessionFolder", func() error {
		_, err := readonly.MoveSessionFolder(ctx, 1, nil)
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

// folderParents lists folders as "project/name" -> parent folder name, with
// "" for the project's top level.
func folderParents(t *testing.T, d *DB) map[string]string {
	t.Helper()
	folders, err := d.ListSessionFolders(t.Context())
	require.NoError(t, err)
	names := make(map[int64]string, len(folders))
	for _, f := range folders {
		names[f.ID] = f.Name
	}
	out := make(map[string]string, len(folders))
	for _, f := range folders {
		parent := ""
		if f.ParentID != nil {
			name, ok := names[*f.ParentID]
			require.True(t, ok, "folder %s has a missing parent %d", f.Name, *f.ParentID)
			parent = name
		}
		out[f.Project+"/"+f.Name] = parent
	}
	return out
}

func folderByName(t *testing.T, d *DB, project, name string) SessionFolder {
	t.Helper()
	folders, err := d.ListSessionFolders(t.Context())
	require.NoError(t, err)
	for _, f := range folders {
		if f.Project == project && f.Name == name {
			return f
		}
	}
	require.Failf(t, "folder not found", "%s/%s", project, name)
	return SessionFolder{}
}

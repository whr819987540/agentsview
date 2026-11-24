package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curationSourceArchive builds the archive a user curated on the old
// machine: stars, end marks, and folders, plus curation the sidebar no
// longer shows.
func curationSourceArchive(t *testing.T) *DB {
	t.Helper()
	d := testDB(t)
	ctx := t.Context()
	require.NoError(t, d.SetSyncState(ctx, "artifact_origin_id", "origin-a"))
	insertSession(t, d, "s1", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T10:00:00Z")
	})
	insertSession(t, d, "s2", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T11:00:00Z")
	})
	insertSession(t, d, "s3", "beta")
	insertSession(t, d, "hidden", "alpha")
	insertSession(t, d, "old", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T09:00:00Z")
	})

	_, err := d.StarSession(ctx, "s1")
	require.NoError(t, err)
	_, err = d.StarSession(ctx, "s3")
	require.NoError(t, err)
	_, ok, err := d.MarkSessionEnded(ctx, "s2", EndMarkRow)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = d.MarkSessionEnded(ctx, "old", EndMarkRow)
	require.NoError(t, err)
	require.True(t, ok)
	// New activity after the mark means the mark no longer counts.
	insertSession(t, d, "old", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-02T09:00:00Z")
	})

	docs, err := d.CreateSessionFolder(ctx, "alpha", "Docs")
	require.NoError(t, err)
	ops, err := d.CreateSessionFolder(ctx, "beta", "Ops")
	require.NoError(t, err)
	_, err = d.CreateSessionFolder(ctx, "alpha", "Empty")
	require.NoError(t, err)
	require.NoError(t, d.MoveSessionsToFolder(ctx, docs.ID, []string{"s1", "s2", "hidden"}))
	require.NoError(t, d.MoveSessionsToFolder(ctx, ops.ID, []string{"s3"}))
	// The session moved to another project, so the sidebar no longer shows
	// it in alpha's folder.
	insertSession(t, d, "hidden", "gamma")
	return d
}

func TestExportCuration(t *testing.T) {
	d := curationSourceArchive(t)

	doc, err := d.ExportCuration(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "agentsview-curation", doc.Format)
	assert.Equal(t, 1, doc.Version)
	assert.Equal(t, "origin-a", doc.ArtifactOrigin)
	_, err = time.Parse(time.RFC3339, doc.ExportedAt)
	require.NoError(t, err, "exported_at is RFC 3339")
	for i := range doc.Sessions {
		if doc.Sessions[i].Starred {
			_, err := time.Parse(time.RFC3339Nano, doc.Sessions[i].StarredAt)
			require.NoError(t, err, "starred_at of %s", doc.Sessions[i].ID)
			doc.Sessions[i].StarredAt = ""
		}
	}
	assert.Equal(t, []CurationExportSession{
		{ID: "s1", Agent: "claude", Project: "alpha", Starred: true, Folder: "Docs"},
		{
			ID: "s2", Agent: "claude", Project: "alpha", Folder: "Docs",
			EndMark: &CurationEndMark{Kind: EndMarkRow, ActivityAt: "2026-03-01T11:00:00Z"},
		},
		{ID: "s3", Agent: "claude", Project: "beta", Starred: true, Folder: "Ops"},
	}, doc.Sessions, "hidden memberships and outdated marks stay behind")
	assert.Equal(t, []CurationExportFolder{
		{Project: "alpha", Name: "Docs"},
		{Project: "alpha", Name: "Empty"},
		{Project: "beta", Name: "Ops"},
	}, doc.Folders)
}

func TestImportCurationRestoresExportOnAnotherArchive(t *testing.T) {
	doc, err := curationSourceArchive(t).ExportCuration(t.Context())
	require.NoError(t, err)

	target := testDB(t)
	ctx := t.Context()
	// The new machine holds the same sessions under the IDs each transfer
	// gives them: a copied provider file keeps the ID, artifact sync
	// prefixes the source origin, and remote sync prefixes the host and
	// may file the session under another project.
	insertSession(t, target, "s1", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T10:00:00Z")
	})
	insertSession(t, target, "origin-a~s2", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T11:00:00Z")
	})
	insertSession(t, target, "devbox~s3", "beta-remote")

	result, err := target.ImportCuration(ctx, doc, CurationImportOptions{})
	require.NoError(t, err)
	assert.Equal(t, CurationImportResult{
		Sessions:        3,
		Matched:         3,
		MatchedByPrefix: 2,
		Unmatched:       []string{},
		Ambiguous:       []string{},
		Stars:           CurationImportCounts{Added: 2},
		EndMarks:        CurationImportCounts{Added: 1},
		FolderMembers:   CurationImportCounts{Added: 3},
		FoldersCreated:  3,
	}, result)

	starred, err := target.ListStarredSessionIDs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"s1", "devbox~s3"}, starred)
	marks, err := target.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []SessionEndMark{
		{SessionID: "origin-a~s2", ActivityAt: "2026-03-01T11:00:00Z", Kind: EndMarkRow},
	}, marks)
	assert.Equal(t, map[string][]string{
		"alpha/Docs":      {"origin-a~s2", "s1"},
		"alpha/Empty":     {},
		"beta-remote/Ops": {"devbox~s3"},
	}, folderMembers(t, target), "folders follow each session's project here")

	again, err := target.ImportCuration(ctx, doc, CurationImportOptions{})
	require.NoError(t, err)
	assert.Equal(t, CurationImportResult{
		Sessions:        3,
		Matched:         3,
		MatchedByPrefix: 2,
		Unmatched:       []string{},
		Ambiguous:       []string{},
		Stars:           CurationImportCounts{Unchanged: 2},
		EndMarks:        CurationImportCounts{Unchanged: 1},
		FolderMembers:   CurationImportCounts{Unchanged: 3},
	}, again, "a repeated import changes nothing")
}

func TestImportCurationKeepsTheExportedActivityOfEndMarks(t *testing.T) {
	target := testDB(t)
	ctx := t.Context()
	// This copy saw activity after the mark was made on the old machine.
	insertSession(t, target, "s1", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-05T08:00:00Z")
	})
	insertSession(t, target, "s2", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T10:00:00Z")
	})
	doc := CurationExport{
		Format: CurationExportFormat, Version: CurationExportVersion,
		Sessions: []CurationExportSession{
			{ID: "s1", EndMark: &CurationEndMark{Kind: EndMarkRow, ActivityAt: "2026-03-01T10:00:00Z"}},
			{ID: "s2", EndMark: &CurationEndMark{Kind: EndMarkBranch, ActivityAt: "2026-03-01T10:00:00Z"}},
		},
	}

	result, err := target.ImportCuration(ctx, doc, CurationImportOptions{})
	require.NoError(t, err)
	assert.Equal(t, CurationImportCounts{Added: 2}, result.EndMarks)

	marks, err := target.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []SessionEndMark{
		{SessionID: "s2", ActivityAt: "2026-03-01T10:00:00Z", Kind: EndMarkBranch},
	}, marks, "a mark older than this archive's activity no longer counts")
}

func TestImportCurationKeepsCurationAlreadyHere(t *testing.T) {
	target := testDB(t)
	ctx := t.Context()
	for _, id := range []string{"k1", "k3", "k4"} {
		insertSession(t, target, id, "alpha")
	}
	insertSession(t, target, "k2", "alpha", func(s *Session) {
		s.EndedAt = new("2026-03-01T10:00:00Z")
	})
	insertSession(t, target, "k5", "gamma")

	_, err := target.StarSession(ctx, "k1")
	require.NoError(t, err)
	_, ok, err := target.MarkSessionEnded(ctx, "k2", EndMarkBranch)
	require.NoError(t, err)
	require.True(t, ok)
	mine, err := target.CreateSessionFolder(ctx, "alpha", "Mine")
	require.NoError(t, err)
	docs, err := target.CreateSessionFolder(ctx, "alpha", "Docs")
	require.NoError(t, err)
	old, err := target.CreateSessionFolder(ctx, "gamma", "Old")
	require.NoError(t, err)
	require.NoError(t, target.MoveSessionsToFolder(ctx, mine.ID, []string{"k3"}))
	require.NoError(t, target.MoveSessionsToFolder(ctx, docs.ID, []string{"k4"}))
	require.NoError(t, target.MoveSessionsToFolder(ctx, old.ID, []string{"k5"}))
	// k5 moved to alpha, so its gamma folder no longer shows it.
	insertSession(t, target, "k5", "alpha")

	doc := CurationExport{
		Format: CurationExportFormat, Version: CurationExportVersion,
		Sessions: []CurationExportSession{
			{ID: "k1", Starred: true},
			{ID: "k2", EndMark: &CurationEndMark{Kind: EndMarkRow, ActivityAt: "2026-02-01T00:00:00Z"}},
			{ID: "k3", Project: "alpha", Folder: "Docs"},
			{ID: "k4", Project: "alpha", Folder: "Docs"},
			{ID: "k5", Project: "alpha", Folder: "Docs"},
		},
	}
	result, err := target.ImportCuration(ctx, doc, CurationImportOptions{})
	require.NoError(t, err)
	assert.Equal(t, CurationImportCounts{Unchanged: 1}, result.Stars)
	assert.Equal(t, CurationImportCounts{Kept: 1}, result.EndMarks)
	assert.Equal(t, CurationImportCounts{Added: 1, Unchanged: 1, Kept: 1}, result.FolderMembers)
	assert.Zero(t, result.FoldersCreated)

	marks, err := target.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []SessionEndMark{
		{SessionID: "k2", ActivityAt: "2026-03-01T10:00:00Z", Kind: EndMarkBranch},
	}, marks)
	assert.Equal(t, map[string][]string{
		"alpha/Docs": {"k4", "k5"},
		"alpha/Mine": {"k3"},
		"gamma/Old":  {},
	}, folderMembers(t, target))
}

func TestImportCurationMatchesSessions(t *testing.T) {
	type stored struct{ id, agent string }
	tests := []struct {
		name         string
		sessions     []stored
		origin       string
		entryAgent   string
		wantStarred  []string
		wantPrefixed int
		wantUnmatch  []string
		wantAmbig    []string
	}{
		{
			name:        "same ID",
			sessions:    []stored{{"x1", "claude"}},
			entryAgent:  "claude",
			wantStarred: []string{"x1"},
		},
		{
			name:        "same ID wins over a prefixed copy",
			sessions:    []stored{{"x1", "claude"}, {"devbox~x1", "claude"}},
			entryAgent:  "claude",
			wantStarred: []string{"x1"},
		},
		{
			name:         "one prefixed copy",
			sessions:     []stored{{"devbox~x1", "claude"}},
			entryAgent:   "claude",
			wantStarred:  []string{"devbox~x1"},
			wantPrefixed: 1,
		},
		{
			name:         "exporting origin wins among prefixed copies",
			sessions:     []stored{{"devbox~x1", "claude"}, {"origin-a~x1", "claude"}},
			origin:       "origin-a",
			entryAgent:   "claude",
			wantStarred:  []string{"origin-a~x1"},
			wantPrefixed: 1,
		},
		{
			name:       "several prefixed copies without the origin",
			sessions:   []stored{{"devbox~x1", "claude"}, {"laptop~x1", "claude"}},
			origin:     "origin-a",
			entryAgent: "claude",
			wantAmbig:  []string{"x1"},
		},
		{
			name:        "other agent does not match",
			sessions:    []stored{{"x1", "codex"}},
			entryAgent:  "claude",
			wantUnmatch: []string{"x1"},
		},
		{
			name:         "other agent with the same ID falls back to a prefixed copy",
			sessions:     []stored{{"x1", "codex"}, {"devbox~x1", "claude"}},
			entryAgent:   "claude",
			wantStarred:  []string{"devbox~x1"},
			wantPrefixed: 1,
		},
		{
			name:        "export without an agent matches any agent",
			sessions:    []stored{{"x1", "codex"}},
			wantStarred: []string{"x1"},
		},
		{
			name:        "a prefix needs the tilde",
			sessions:    []stored{{"devboxx1", "claude"}},
			entryAgent:  "claude",
			wantUnmatch: []string{"x1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDB(t)
			ctx := t.Context()
			for _, s := range tt.sessions {
				insertSession(t, d, s.id, "alpha", func(sess *Session) { sess.Agent = s.agent })
			}
			doc := CurationExport{
				Format: CurationExportFormat, Version: CurationExportVersion,
				ArtifactOrigin: tt.origin,
				Sessions:       []CurationExportSession{{ID: "x1", Agent: tt.entryAgent, Starred: true}},
			}
			result, err := d.ImportCuration(ctx, doc, CurationImportOptions{})
			require.NoError(t, err)

			starred, err := d.ListStarredSessionIDs(ctx)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantStarred, starred)
			assert.Equal(t, len(tt.wantStarred), result.Matched)
			assert.Equal(t, tt.wantPrefixed, result.MatchedByPrefix)
			assert.Equal(t, append([]string{}, tt.wantUnmatch...), result.Unmatched)
			assert.Equal(t, append([]string{}, tt.wantAmbig...), result.Ambiguous)
		})
	}
}

func TestImportCurationDryRunWritesNothing(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	insertSession(t, d, "s1", "alpha")
	doc := CurationExport{
		Format: CurationExportFormat, Version: CurationExportVersion,
		Sessions: []CurationExportSession{{
			ID: "s1", Starred: true, Folder: "Docs",
			EndMark: &CurationEndMark{Kind: EndMarkRow, ActivityAt: "2026-03-01T10:00:00Z"},
		}},
	}

	result, err := d.ImportCuration(ctx, doc, CurationImportOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, CurationImportResult{
		DryRun:         true,
		Sessions:       1,
		Matched:        1,
		Unmatched:      []string{},
		Ambiguous:      []string{},
		Stars:          CurationImportCounts{Added: 1},
		EndMarks:       CurationImportCounts{Added: 1},
		FolderMembers:  CurationImportCounts{Added: 1},
		FoldersCreated: 1,
	}, result)

	starred, err := d.ListStarredSessionIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, starred)
	marks, err := d.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
	assert.Empty(t, folderMembers(t, d))
}

func TestImportCurationStarTime(t *testing.T) {
	tests := []struct {
		name      string
		starredAt string
		want      string
	}{
		{name: "keeps the exported time", starredAt: "2026-01-02T03:04:05.6Z", want: "2026-01-02T03:04:05.600Z"},
		{name: "converts to UTC", starredAt: "2026-01-02T11:04:05+08:00", want: "2026-01-02T03:04:05.000Z"},
		{name: "unreadable time uses the import time", starredAt: "yesterday"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDB(t)
			ctx := t.Context()
			insertSession(t, d, "s1", "alpha")
			before := time.Now().UTC().Truncate(time.Millisecond)
			doc := CurationExport{
				Format: CurationExportFormat, Version: CurationExportVersion,
				Sessions: []CurationExportSession{{ID: "s1", Starred: true, StarredAt: tt.starredAt}},
			}
			_, err := d.ImportCuration(ctx, doc, CurationImportOptions{})
			require.NoError(t, err)

			var createdAt string
			require.NoError(t, d.getReader().QueryRow(ctx,
				`SELECT created_at FROM starred_sessions WHERE session_id = 's1'`,
			).Scan(&createdAt))
			if tt.want != "" {
				assert.Equal(t, tt.want, createdAt)
				return
			}
			got, err := time.Parse(time.RFC3339Nano, createdAt)
			require.NoError(t, err)
			assert.False(t, got.Before(before), "star time %s predates the import", createdAt)
		})
	}
}

func TestValidateCurationExport(t *testing.T) {
	valid := func() CurationExport {
		return CurationExport{Format: CurationExportFormat, Version: CurationExportVersion}
	}
	tests := []struct {
		name    string
		edit    func(*CurationExport)
		wantErr string
	}{
		{name: "wrong format", edit: func(d *CurationExport) { d.Format = "something-else" }, wantErr: `format is "something-else"`},
		{name: "newer version", edit: func(d *CurationExport) { d.Version = 2 }, wantErr: "version 2 is not supported"},
		{name: "session without id", edit: func(d *CurationExport) {
			d.Sessions = []CurationExportSession{{ID: " "}}
		}, wantErr: "session 0 has no id"},
		{name: "unknown end mark kind", edit: func(d *CurationExport) {
			d.Sessions = []CurationExportSession{{ID: "s1", EndMark: &CurationEndMark{Kind: "all", ActivityAt: "2026-03-01T10:00:00Z"}}}
		}, wantErr: `end mark kind "all"`},
		{name: "end mark without activity", edit: func(d *CurationExport) {
			d.Sessions = []CurationExportSession{{ID: "s1", EndMark: &CurationEndMark{Kind: EndMarkRow}}}
		}, wantErr: "without activity_at"},
		{name: "session folder with a control character", edit: func(d *CurationExport) {
			d.Sessions = []CurationExportSession{{ID: "s1", Folder: "a\nb"}}
		}, wantErr: "control characters"},
		{name: "folder without project", edit: func(d *CurationExport) {
			d.Folders = []CurationExportFolder{{Name: "Docs"}}
		}, wantErr: "folder 0 has no project"},
		{name: "folder with a blank name", edit: func(d *CurationExport) {
			d.Folders = []CurationExportFolder{{Project: "alpha", Name: "  "}}
		}, wantErr: "name is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := valid()
			tt.edit(&doc)
			err := ValidateCurationExport(&doc)
			require.ErrorIs(t, err, ErrCurationImportInvalid)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	t.Run("folder names are trimmed", func(t *testing.T) {
		doc := valid()
		doc.Sessions = []CurationExportSession{{ID: "s1", Folder: " Docs "}}
		doc.Folders = []CurationExportFolder{{Project: "alpha", Name: " Empty "}}
		require.NoError(t, ValidateCurationExport(&doc))
		assert.Equal(t, "Docs", doc.Sessions[0].Folder)
		assert.Equal(t, "Empty", doc.Folders[0].Name)
	})
}

func TestCurationOnReadOnlyArchive(t *testing.T) {
	ro := openReadOnlyTestDB(t, curationSourceArchive(t).Path())
	ctx := t.Context()

	doc, err := ro.ExportCuration(ctx)
	require.NoError(t, err, "export only reads")
	ids := make([]string, 0, len(doc.Sessions))
	for _, entry := range doc.Sessions {
		ids = append(ids, entry.ID)
	}
	assert.Equal(t, []string{"s1", "s2", "s3"}, ids,
		"a read-only archive still leaves out marks that no longer count")

	_, err = ro.ImportCuration(ctx, doc, CurationImportOptions{DryRun: true})
	require.ErrorIs(t, err, ErrReadOnly)
}

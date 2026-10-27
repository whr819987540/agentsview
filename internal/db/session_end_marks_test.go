package db

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionEndMarkLifecycle(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	insertSession(t, d, "ended", "alpha", func(s *Session) {
		s.StartedAt = new("2026-10-01T09:00:00Z")
		s.EndedAt = new("2026-10-01T10:00:00Z")
	})
	insertSession(t, d, "unstarted", "alpha")

	marks, err := d.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
	assert.NotNil(t, marks, "an empty list encodes as [] for the API")

	mark, ok, err := d.MarkSessionEnded(ctx, "ended")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, SessionEndMark{SessionID: "ended", ActivityAt: "2026-10-01T10:00:00Z"}, mark,
		"the mark returns what it stored")
	_, ok, err = d.MarkSessionEnded(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, ok, "a mark needs an existing session")

	// A session with neither start nor end falls back to its creation time.
	_, ok, err = d.MarkSessionEnded(ctx, "unstarted")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, map[string]string{
		"ended":     "2026-10-01T10:00:00Z",
		"unstarted": sessionCreatedAt(t, d, "unstarted"),
	}, endMarkActivity(t, d))

	// Marking again after new activity moves the mark to it.
	insertSession(t, d, "ended", "alpha", func(s *Session) {
		s.StartedAt = new("2026-10-01T09:00:00Z")
		s.EndedAt = new("2026-10-03T12:00:00Z")
	})
	_, ok, err = d.MarkSessionEnded(ctx, "ended")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-10-03T12:00:00Z", endMarkActivity(t, d)["ended"])

	require.NoError(t, d.UnmarkSessionEnded(ctx, "ended"))
	require.NoError(t, d.UnmarkSessionEnded(ctx, "ended"), "unmarking twice succeeds")
	assert.NotContains(t, endMarkActivity(t, d), "ended")

	require.NoError(t, d.DeleteSession(ctx, "unstarted"))
	assert.NotContains(t, endMarkActivity(t, d), "unstarted",
		"deleting a session removes its mark")
}

// seedEndMarkRow stores a sidebar row: root, a continuation, a subagent of
// the continuation, an empty child and a trashed child.
func seedEndMarkRow(t *testing.T, d *DB) {
	t.Helper()
	insertSession(t, d, "root", "alpha", func(s *Session) {
		s.StartedAt = new("2026-10-01T09:00:00Z")
		s.EndedAt = new("2026-10-01T10:00:00Z")
	})
	insertSession(t, d, "cont", "alpha", func(s *Session) {
		s.ParentSessionID = new("root")
		s.RelationshipType = "continuation"
		s.StartedAt = new("2026-10-01T10:01:00Z")
		s.EndedAt = new("2026-10-01T10:30:00Z")
	})
	insertSession(t, d, "sub", "alpha", func(s *Session) {
		s.ParentSessionID = new("cont")
		s.RelationshipType = "subagent"
		s.StartedAt = new("2026-10-01T10:10:00Z")
		// Later than cont's end by half a second. Compared as text it
		// sorts before "2026-10-01T10:30:00Z".
		s.EndedAt = new("2026-10-01T10:30:00.5Z")
	})
	insertSession(t, d, "empty", "alpha", func(s *Session) {
		s.ParentSessionID = new("root")
		s.MessageCount = 0
		s.EndedAt = new("2026-10-01T12:00:00Z")
	})
	insertSession(t, d, "unrelated", "alpha", func(s *Session) {
		s.EndedAt = new("2026-10-01T13:00:00Z")
	})
}

func TestMarkSessionEndedTakesTheWholeRowsLatestActivity(t *testing.T) {
	tests := []struct {
		name   string
		target string
		trash  bool
		want   string
	}{
		{
			name:   "root mark covers its continuation and subagent",
			target: "root",
			want:   "2026-10-01T10:30:00.5Z",
		},
		{
			name:   "a mark on a session lower in the row walks up to its root",
			target: "cont",
			want:   "2026-10-01T10:30:00.5Z",
		},
		{
			name:   "trashed sessions still count",
			target: "root",
			trash:  true,
			want:   "2026-10-01T11:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDB(t)
			ctx := t.Context()
			seedEndMarkRow(t, d)
			if tt.trash {
				insertSession(t, d, "trashed", "alpha", func(s *Session) {
					s.ParentSessionID = new("root")
					s.EndedAt = new("2026-10-01T11:00:00Z")
				})
				require.NoError(t, d.SoftDeleteSession(ctx, "trashed"))
			}

			mark, ok, err := d.MarkSessionEnded(ctx, tt.target)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, tt.want, mark.ActivityAt)
			assert.Equal(t, map[string]string{tt.target: tt.want}, endMarkActivity(t, d))
		})
	}
}

func TestMarkSessionEndedSkipsEmptyTimestamps(t *testing.T) {
	d := testDB(t)
	insertSession(t, d, "legacy", "alpha", func(s *Session) {
		s.StartedAt = new("2026-10-01T09:00:00Z")
		s.EndedAt = new("")
	})

	mark, ok, err := d.MarkSessionEnded(t.Context(), "legacy")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-10-01T09:00:00Z", mark.ActivityAt,
		"an empty end time falls back to the start time")
}

func TestMarkSessionEndedSurvivesAParentCycle(t *testing.T) {
	d := testDB(t)
	insertSession(t, d, "a", "alpha", func(s *Session) {
		s.ParentSessionID = new("b")
		s.EndedAt = new("2026-10-01T10:00:00Z")
	})
	insertSession(t, d, "b", "alpha", func(s *Session) {
		s.ParentSessionID = new("a")
		s.EndedAt = new("2026-10-01T11:00:00Z")
	})

	mark, ok, err := d.MarkSessionEnded(t.Context(), "a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-10-01T11:00:00Z", mark.ActivityAt)
}

func TestListSessionEndMarksDropsOutdatedMarks(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	seedEndMarkRow(t, d)
	for _, id := range []string{"root", "unrelated"} {
		_, ok, err := d.MarkSessionEnded(ctx, id)
		require.NoError(t, err)
		require.True(t, ok)
	}

	// The continuation is resumed after the mark.
	insertSession(t, d, "cont", "alpha", func(s *Session) {
		s.ParentSessionID = new("root")
		s.RelationshipType = "continuation"
		s.StartedAt = new("2026-10-01T10:01:00Z")
		s.EndedAt = new("2026-10-01T14:00:00Z")
	})
	assert.Equal(t, map[string]string{"unrelated": "2026-10-01T13:00:00Z"},
		endMarkActivity(t, d), "new activity in the row ends the mark")
	assert.Equal(t, []string{"unrelated"}, storedEndMarkIDs(t, d),
		"listing deletes the outdated mark")

	// Trashing, then deleting, the resumed session must not bring the
	// mark back.
	require.NoError(t, d.SoftDeleteSession(ctx, "cont"))
	assert.NotContains(t, endMarkActivity(t, d), "root")
	require.NoError(t, d.DeleteSession(ctx, "cont"))
	assert.NotContains(t, endMarkActivity(t, d), "root")
}

func TestListSessionEndMarksForScope(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	for _, s := range []struct{ id, project string }{
		{"a1", "alpha"}, {"b1", "beta"}, {"g1", "gamma"},
	} {
		insertSession(t, d, s.id, s.project, func(sess *Session) {
			sess.EndedAt = new("2026-10-01T10:00:00Z")
		})
		_, ok, err := d.MarkSessionEnded(ctx, s.id)
		require.NoError(t, err)
		require.True(t, ok)
	}

	tests := []struct {
		name              string
		projects, exclude []string
		want              []string
	}{
		{name: "unscoped", want: []string{"a1", "b1", "g1"}},
		{name: "projects", projects: []string{"alpha", "gamma"}, want: []string{"a1", "g1"}},
		{name: "excluded", exclude: []string{"alpha"}, want: []string{"b1", "g1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks, err := d.ListSessionEndMarksForScope(ctx, tt.projects, tt.exclude)
			require.NoError(t, err)
			ids := make([]string, 0, len(marks))
			for _, mark := range marks {
				ids = append(ids, mark.SessionID)
			}
			assert.Equal(t, tt.want, ids)
		})
	}
}

func TestListSessionEndMarksDropsMarksWithoutATime(t *testing.T) {
	d := testDB(t)
	insertSession(t, d, "legacy", "alpha", func(s *Session) {
		s.EndedAt = new("2026-10-01T10:00:00Z")
	})
	_, err := d.getWriter().Exec(t.Context(),
		`INSERT INTO session_end_marks (session_id, activity_at) VALUES ('legacy', '')`)
	require.NoError(t, err)

	assert.Empty(t, endMarkActivity(t, d))
	assert.Empty(t, storedEndMarkIDs(t, d))
}

func TestCopySessionMetadataFromPreservesSessionEndMarks(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	src, err := Open(ctx, srcPath)
	require.NoError(t, err)
	for _, id := range []string{"kept", "gone"} {
		insertSession(t, src, id, "alpha", func(s *Session) {
			s.EndedAt = new("2026-10-01T10:00:00Z")
		})
		_, ok, err := src.MarkSessionEnded(ctx, id)
		require.NoError(t, err)
		require.True(t, ok)
	}
	require.NoError(t, src.Close())

	// The rebuilt archive re-synced only "kept".
	dst, err := Open(ctx, filepath.Join(dir, "dst.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, dst.Close()) })
	insertSession(t, dst, "kept", "alpha", func(s *Session) {
		s.EndedAt = new("2026-10-01T10:00:00Z")
	})

	require.NoError(t, dst.CopySessionMetadataFrom(srcPath))

	assert.Equal(t, map[string]string{"kept": "2026-10-01T10:00:00Z"}, endMarkActivity(t, dst))
}

func TestOpenReadOnlyListsSessionEndMarksAndRejectsEdits(t *testing.T) {
	path := createClosedTestDB(t, tempDBPath(t, "sessions.db"), func(d *DB) {
		for _, id := range []string{"a1", "a2"} {
			insertSession(t, d, id, "alpha", func(s *Session) {
				s.EndedAt = new("2026-10-01T10:00:00Z")
			})
			_, ok, err := d.MarkSessionEnded(t.Context(), id)
			require.NoError(t, err)
			require.True(t, ok)
		}
		// a2 has activity after its mark.
		insertSession(t, d, "a2", "alpha", func(s *Session) {
			s.EndedAt = new("2026-10-01T11:00:00Z")
		})
	})
	readonly := openReadOnlyTestDB(t, path)

	assert.Equal(t, map[string]string{"a1": "2026-10-01T10:00:00Z"}, endMarkActivity(t, readonly),
		"a read-only archive hides the outdated mark")
	assert.Equal(t, []string{"a1", "a2"}, storedEndMarkIDs(t, readonly),
		"a read-only archive does not delete it")

	ctx := t.Context()
	requireReadOnlyOp(t, "MarkSessionEnded", func() error {
		_, _, err := readonly.MarkSessionEnded(ctx, "a1")
		return err
	})
	requireReadOnlyOp(t, "UnmarkSessionEnded", func() error {
		return readonly.UnmarkSessionEnded(ctx, "a1")
	})
}

// endMarkActivity lists end marks as session ID -> activity time.
func endMarkActivity(t *testing.T, d *DB) map[string]string {
	t.Helper()
	marks, err := d.ListSessionEndMarks(t.Context())
	require.NoError(t, err)
	out := make(map[string]string, len(marks))
	for _, mark := range marks {
		out[mark.SessionID] = mark.ActivityAt
	}
	return out
}

// storedEndMarkIDs returns the session IDs of every stored mark, in effect
// or not.
func storedEndMarkIDs(t *testing.T, d *DB) []string {
	t.Helper()
	rows, err := d.getReader().QueryContext(t.Context(),
		`SELECT session_id FROM session_end_marks ORDER BY session_id`)
	require.NoError(t, err)
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func sessionCreatedAt(t *testing.T, d *DB, id string) string {
	t.Helper()
	var createdAt string
	require.NoError(t, d.getReader().QueryRowContext(t.Context(),
		`SELECT created_at FROM sessions WHERE id = ?`, id).Scan(&createdAt))
	return createdAt
}

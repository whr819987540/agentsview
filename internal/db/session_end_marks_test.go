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
	insertSession(t, d, "group-root", "alpha")

	marks, err := d.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
	assert.NotNil(t, marks, "an empty list encodes as [] for the API")

	ok, err := d.MarkSessionEnded(ctx, "ended", "")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = d.MarkSessionEnded(ctx, "group-root", "2026-10-02T08:00:00.5Z")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = d.MarkSessionEnded(ctx, "missing", "")
	require.NoError(t, err)
	assert.False(t, ok, "a mark needs an existing session")

	assert.Equal(t, map[string]string{
		"ended":      "2026-10-01T10:00:00Z",
		"group-root": "2026-10-02T08:00:00.5Z",
	}, endMarkActivity(t, d), "without an activity time the mark takes the session's own")

	// A session with neither start nor end falls back to its creation time.
	ok, err = d.MarkSessionEnded(ctx, "unstarted", "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.NotEmpty(t, endMarkActivity(t, d)["unstarted"])

	// Marking again moves the mark to the newer activity.
	ok, err = d.MarkSessionEnded(ctx, "ended", "2026-10-03T12:00:00Z")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "2026-10-03T12:00:00Z", endMarkActivity(t, d)["ended"])

	require.NoError(t, d.UnmarkSessionEnded(ctx, "ended"))
	require.NoError(t, d.UnmarkSessionEnded(ctx, "ended"), "unmarking twice succeeds")
	assert.NotContains(t, endMarkActivity(t, d), "ended")

	require.NoError(t, d.DeleteSession(ctx, "group-root"))
	assert.NotContains(t, endMarkActivity(t, d), "group-root",
		"deleting a session removes its mark")
}

func TestCopySessionMetadataFromPreservesSessionEndMarks(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	src, err := Open(ctx, srcPath)
	require.NoError(t, err)
	insertSession(t, src, "kept", "alpha")
	insertSession(t, src, "gone", "alpha")
	for _, id := range []string{"kept", "gone"} {
		ok, err := src.MarkSessionEnded(ctx, id, "2026-10-01T10:00:00Z")
		require.NoError(t, err)
		require.True(t, ok)
	}
	require.NoError(t, src.Close())

	// The rebuilt archive re-synced only "kept".
	dst, err := Open(ctx, filepath.Join(dir, "dst.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, dst.Close()) })
	insertSession(t, dst, "kept", "alpha")

	require.NoError(t, dst.CopySessionMetadataFrom(srcPath))

	assert.Equal(t, map[string]string{"kept": "2026-10-01T10:00:00Z"}, endMarkActivity(t, dst))
}

func TestOpenReadOnlyListsSessionEndMarksAndRejectsEdits(t *testing.T) {
	path := createClosedTestDB(t, tempDBPath(t, "sessions.db"), func(d *DB) {
		insertSession(t, d, "a1", "alpha")
		ok, err := d.MarkSessionEnded(t.Context(), "a1", "2026-10-01T10:00:00Z")
		require.NoError(t, err)
		require.True(t, ok)
	})
	readonly := openReadOnlyTestDB(t, path)

	assert.Equal(t, map[string]string{"a1": "2026-10-01T10:00:00Z"}, endMarkActivity(t, readonly))

	ctx := t.Context()
	requireReadOnlyOp(t, "MarkSessionEnded", func() error {
		_, err := readonly.MarkSessionEnded(ctx, "a1", "")
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

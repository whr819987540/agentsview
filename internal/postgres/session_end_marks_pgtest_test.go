//go:build pgtest

package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/db"
)

func TestStoreSessionEndMarks(t *testing.T) {
	pgURL := testPGURL(t)

	const schema = "agentsview_end_marks_test"
	pg, err := Open(pgURL, schema, true)
	require.NoError(t, err, "Open")
	defer pg.Close()
	defer func() {
		_, _ = pg.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	}()

	ctx := t.Context()
	_, err = pg.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	require.NoError(t, err, "drop schema")
	require.NoError(t, EnsureSchema(ctx, pg, schema), "EnsureSchema")

	// A row: root, a continuation and the continuation's subagent, whose
	// end is the row's latest activity, plus an unrelated session.
	_, err = pg.ExecContext(ctx, `
		INSERT INTO sessions
			(id, machine, project, agent, parent_session_id,
			 started_at, ended_at, message_count, user_message_count)
		VALUES
			('root', 'm', 'proj', 'claude', NULL,
			 '2026-05-01T09:00:00Z', '2026-05-01T10:00:00Z', 2, 1),
			('cont', 'm', 'proj', 'claude', 'root',
			 '2026-05-01T10:01:00Z', '2026-05-01T10:30:00Z', 2, 1),
			('sub', 'm', 'proj', 'claude', 'cont',
			 '2026-05-01T10:10:00Z', '2026-05-01T10:30:00.5Z', 2, 1),
			('other', 'm', 'proj', 'claude', NULL,
			 '2026-05-01T11:00:00Z', '2026-05-01T12:00:00Z', 2, 1)`)
	require.NoError(t, err, "insert sessions")

	store, err := NewStore(pgURL, schema, true)
	require.NoError(t, err, "NewStore")
	defer store.Close()

	assert.True(t, store.SessionEndMarksEditable(ctx))
	marks, err := store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
	assert.NotNil(t, marks, "an empty list encodes as [] for the API")

	mark, ok, err := store.MarkSessionEnded(ctx, "cont", db.EndMarkRow)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, db.SessionEndMark{SessionID: "cont", ActivityAt: "2026-05-01T10:30:00.5Z", Kind: db.EndMarkRow}, mark,
		"a mark lower in the row takes the whole row's latest activity")
	_, ok, err = store.MarkSessionEnded(ctx, "missing", db.EndMarkRow)
	require.NoError(t, err)
	assert.False(t, ok, "a mark needs an existing session")
	_, ok, err = store.MarkSessionEnded(ctx, "other", db.EndMarkRow)
	require.NoError(t, err)
	require.True(t, ok)

	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{
		{SessionID: "cont", ActivityAt: "2026-05-01T10:30:00.5Z", Kind: db.EndMarkRow},
		{SessionID: "other", ActivityAt: "2026-05-01T12:00:00Z", Kind: db.EndMarkRow},
	}, marks)

	// The root is resumed, then trashed: the mark is gone for good.
	_, err = pg.ExecContext(ctx,
		`UPDATE sessions SET ended_at = '2026-05-01T13:00:00Z' WHERE id = 'root'`)
	require.NoError(t, err)
	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{{SessionID: "other", ActivityAt: "2026-05-01T12:00:00Z", Kind: db.EndMarkRow}}, marks)
	_, err = pg.ExecContext(ctx,
		`UPDATE sessions SET deleted_at = NOW() WHERE id = 'root'`)
	require.NoError(t, err)
	var stored int
	require.NoError(t, pg.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_end_marks`).Scan(&stored))
	assert.Equal(t, 1, stored, "listing deleted the outdated mark")

	require.NoError(t, store.UnmarkSessionEnded(ctx, "other", db.EndMarkRow))
	require.NoError(t, store.UnmarkSessionEnded(ctx, "other", db.EndMarkRow), "unmarking twice succeeds")
	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
}

func TestStoreSessionEndMarksWithoutTable(t *testing.T) {
	pgURL := testPGURL(t)

	const schema = "agentsview_end_marks_missing_test"
	pg, err := Open(pgURL, schema, true)
	require.NoError(t, err, "Open")
	defer pg.Close()
	defer func() {
		_, _ = pg.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	}()

	ctx := t.Context()
	_, err = pg.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	require.NoError(t, err, "drop schema")
	require.NoError(t, EnsureSchema(ctx, pg, schema), "EnsureSchema")
	// A schema from before marks, served by a role that cannot migrate it.
	_, err = pg.ExecContext(ctx, `DROP TABLE session_end_marks`)
	require.NoError(t, err)

	store, err := NewStore(pgURL, schema, true)
	require.NoError(t, err, "NewStore")
	defer store.Close()

	assert.False(t, store.SessionEndMarksEditable(ctx))
	marks, err := store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
}

// openEndMarkTestSchema creates a fresh schema with two forks of a root:
// "fork", whose subagent is its latest activity, and "sibling", the row's
// latest.
func openEndMarkTestSchema(t *testing.T, schema string) (*sql.DB, string) {
	t.Helper()
	pgURL := testPGURL(t)
	pg, err := Open(pgURL, schema, true)
	require.NoError(t, err, "Open")
	t.Cleanup(func() {
		_, _ = pg.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = pg.Close()
	})

	ctx := t.Context()
	_, err = pg.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	require.NoError(t, err, "drop schema")
	require.NoError(t, EnsureSchema(ctx, pg, schema), "EnsureSchema")
	_, err = pg.ExecContext(ctx, `
		INSERT INTO sessions
			(id, machine, project, agent, parent_session_id, relationship_type,
			 started_at, ended_at, message_count, user_message_count)
		VALUES
			('root', 'm', 'proj', 'claude', NULL, '',
			 '2026-05-01T09:00:00Z', '2026-05-01T10:00:00Z', 2, 1),
			('fork', 'm', 'proj', 'claude', 'root', 'fork',
			 '2026-05-01T10:05:00Z', '2026-05-01T10:20:00Z', 2, 1),
			('forksub', 'm', 'proj', 'claude', 'fork', 'subagent',
			 '2026-05-01T10:10:00Z', '2026-05-01T10:40:00Z', 2, 1),
			('sibling', 'm', 'proj', 'claude', 'root', 'fork',
			 '2026-05-01T10:30:00Z', '2026-05-01T11:00:00Z', 2, 1)`)
	require.NoError(t, err, "insert sessions")
	return pg, pgURL
}

func TestStoreSessionEndMarkKinds(t *testing.T) {
	const schema = "agentsview_end_mark_kinds_test"
	pg, pgURL := openEndMarkTestSchema(t, schema)
	store, err := NewStore(pgURL, schema, true)
	require.NoError(t, err, "NewStore")
	defer store.Close()
	ctx := t.Context()

	branch, ok, err := store.MarkSessionEnded(ctx, "fork", db.EndMarkBranch)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, db.SessionEndMark{
		SessionID: "fork", ActivityAt: "2026-05-01T10:40:00Z", Kind: db.EndMarkBranch,
	}, branch, "a branch mark covers the fork and its subagent, not its sibling")
	row, ok, err := store.MarkSessionEnded(ctx, "root", db.EndMarkRow)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-05-01T11:00:00Z", row.ActivityAt)

	// The sibling is resumed: only the row mark ends.
	_, err = pg.ExecContext(ctx,
		`UPDATE sessions SET ended_at = '2026-05-01T15:00:00Z' WHERE id = 'sibling'`)
	require.NoError(t, err)
	marks, err := store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{branch}, marks)

	require.NoError(t, store.UnmarkSessionEnded(ctx, "fork", db.EndMarkRow))
	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{branch}, marks, "removing a row mark keeps the branch mark")
	require.NoError(t, store.UnmarkSessionEnded(ctx, "fork", db.EndMarkBranch))
	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Empty(t, marks)
}

func TestStoreSessionEndMarksWithoutKindColumn(t *testing.T) {
	const schema = "agentsview_end_marks_no_kind_test"
	pg, pgURL := openEndMarkTestSchema(t, schema)
	ctx := t.Context()
	// A schema from before kinds, served by a role that cannot migrate it.
	_, err := pg.ExecContext(ctx, `ALTER TABLE session_end_marks DROP COLUMN kind`)
	require.NoError(t, err)
	_, err = pg.ExecContext(ctx, `
		INSERT INTO session_end_marks (session_id, activity_at)
		VALUES ('fork', '2026-05-01T11:00:00Z')`)
	require.NoError(t, err)

	store, err := NewStore(pgURL, schema, true)
	require.NoError(t, err, "NewStore")
	defer store.Close()

	assert.False(t, store.SessionEndMarksEditable(ctx))
	marks, err := store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{
		{SessionID: "fork", ActivityAt: "2026-05-01T11:00:00Z", Kind: db.EndMarkRow},
	}, marks, "a mark from before kinds covers its row")
}

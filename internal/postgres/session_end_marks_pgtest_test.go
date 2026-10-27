//go:build pgtest

package postgres

import (
	"context"
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
		_, _ = pg.ExecContext(
			context.Background(),
			`DROP SCHEMA IF EXISTS `+schema+` CASCADE`,
		)
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

	mark, ok, err := store.MarkSessionEnded(ctx, "cont")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, db.SessionEndMark{SessionID: "cont", ActivityAt: "2026-05-01T10:30:00.5Z"}, mark,
		"a mark lower in the row takes the whole row's latest activity")
	_, ok, err = store.MarkSessionEnded(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, ok, "a mark needs an existing session")
	_, ok, err = store.MarkSessionEnded(ctx, "other")
	require.NoError(t, err)
	require.True(t, ok)

	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{
		{SessionID: "cont", ActivityAt: "2026-05-01T10:30:00.5Z"},
		{SessionID: "other", ActivityAt: "2026-05-01T12:00:00Z"},
	}, marks)

	// The root is resumed, then trashed: the mark is gone for good.
	_, err = pg.ExecContext(ctx,
		`UPDATE sessions SET ended_at = '2026-05-01T13:00:00Z' WHERE id = 'root'`)
	require.NoError(t, err)
	marks, err = store.ListSessionEndMarks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []db.SessionEndMark{{SessionID: "other", ActivityAt: "2026-05-01T12:00:00Z"}}, marks)
	_, err = pg.ExecContext(ctx,
		`UPDATE sessions SET deleted_at = NOW() WHERE id = 'root'`)
	require.NoError(t, err)
	var stored int
	require.NoError(t, pg.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_end_marks`).Scan(&stored))
	assert.Equal(t, 1, stored, "listing deleted the outdated mark")

	require.NoError(t, store.UnmarkSessionEnded(ctx, "other"))
	require.NoError(t, store.UnmarkSessionEnded(ctx, "other"), "unmarking twice succeeds")
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
		_, _ = pg.ExecContext(
			context.Background(),
			`DROP SCHEMA IF EXISTS `+schema+` CASCADE`,
		)
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

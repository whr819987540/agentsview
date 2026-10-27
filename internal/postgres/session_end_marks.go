package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.kenn.io/agentsview/internal/db"
)

// pgEndMarkLatestCTE computes, for each session that marked yields, the
// latest activity of its sidebar row, as the SQLite archive does (see
// db.ListSessionEndMarks): the topmost ancestor through parent_session_id
// and every descendant with messages, trashed ones included. A parent cycle
// has no top, so the marked session seeds its own row.
const pgEndMarkLatestCTE = `
	up(mark_id, id, parent_id) AS (
		SELECT m.mark_id, s.id, s.parent_session_id
		FROM marked m JOIN sessions s ON s.id = m.mark_id
		UNION
		SELECT u.mark_id, p.id, p.parent_session_id
		FROM up u JOIN sessions p ON p.id = u.parent_id
	),
	seeds(mark_id, id) AS (
		SELECT mark_id, id FROM up
		WHERE COALESCE(parent_id, '') = ''
		   OR NOT EXISTS (SELECT 1 FROM sessions p WHERE p.id = up.parent_id)
		UNION
		SELECT mark_id, mark_id FROM marked
	),
	family(mark_id, id) AS (
		SELECT mark_id, id FROM seeds
		UNION
		SELECT f.mark_id, s.id
		FROM family f JOIN sessions s ON s.parent_session_id = f.id
		WHERE s.message_count > 0
	),
	latest(mark_id, at) AS (
		SELECT f.mark_id, MAX(` + pgSidebarActivityExprS + `)
		FROM family f JOIN sessions s ON s.id = f.id
		GROUP BY f.mark_id
	)`

// SessionEndMarksEditable reports whether the serving role may change
// marks. Like stars, end marks are shared PG dashboard metadata that pg
// serve writes; a schema the role could not migrate has no marks table.
func (s *Store) SessionEndMarksEditable(ctx context.Context) bool {
	var editable bool
	err := s.pg.QueryRowContext(ctx, `
		SELECT COALESCE(has_table_privilege(
			to_regclass('session_end_marks'), 'INSERT, DELETE'), false)`,
	).Scan(&editable)
	return err == nil && editable
}

// ListSessionEndMarks returns the end marks still in effect, ordered by
// session ID, and deletes the ones whose row has newer activity, as the
// SQLite archive does. A schema without the marks table lists none.
func (s *Store) ListSessionEndMarks(ctx context.Context) ([]db.SessionEndMark, error) {
	if !pgHasTable(ctx, s.pg, "session_end_marks") {
		return []db.SessionEndMark{}, nil
	}
	rows, err := s.pg.QueryContext(ctx, `
		WITH RECURSIVE marked(mark_id) AS (
			SELECT session_id FROM session_end_marks
		),`+pgEndMarkLatestCTE+`
		SELECT m.session_id, m.activity_at, COALESCE(l.at > m.activity_at, false)
		FROM session_end_marks m
		LEFT JOIN latest l ON l.mark_id = m.session_id
		ORDER BY m.session_id`)
	if err != nil {
		return nil, fmt.Errorf("listing session end marks: %w", err)
	}
	defer rows.Close()
	marks := []db.SessionEndMark{}
	type outdatedMark struct {
		id string
		at time.Time
	}
	var outdated []outdatedMark
	for rows.Next() {
		var id string
		var at time.Time
		var stale bool
		if err := rows.Scan(&id, &at, &stale); err != nil {
			return nil, fmt.Errorf("scanning session end mark: %w", err)
		}
		if stale {
			outdated = append(outdated, outdatedMark{id: id, at: at})
			continue
		}
		marks = append(marks, db.SessionEndMark{SessionID: id, ActivityAt: FormatISO8601(at)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing session end marks: %w", err)
	}
	// Best effort: an outdated mark never lists, so a failed delete, such
	// as under a read-only role, only postpones the cleanup.
	for _, mark := range outdated {
		_, _ = s.pg.ExecContext(ctx, `
			DELETE FROM session_end_marks
			WHERE session_id = $1 AND activity_at = $2`,
			mark.id, mark.at)
	}
	return marks, nil
}

// MarkSessionEnded marks a session's sidebar row as ended as of the row's
// latest activity, replacing an earlier mark on the session. It returns
// false when the session does not exist.
func (s *Store) MarkSessionEnded(
	ctx context.Context, sessionID string,
) (db.SessionEndMark, bool, error) {
	var at time.Time
	err := s.pg.QueryRowContext(ctx, `
		WITH RECURSIVE marked(mark_id) AS (SELECT $1::text),`+pgEndMarkLatestCTE+`
		INSERT INTO session_end_marks (session_id, activity_at)
		SELECT mark_id, at FROM latest WHERE at IS NOT NULL
		ON CONFLICT (session_id) DO UPDATE SET
			activity_at = EXCLUDED.activity_at,
			created_at = NOW()
		RETURNING activity_at`,
		sessionID,
	).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return db.SessionEndMark{}, false, nil
	}
	if err != nil {
		return db.SessionEndMark{}, false,
			fmt.Errorf("marking session %s ended: %w", sessionID, err)
	}
	return db.SessionEndMark{SessionID: sessionID, ActivityAt: FormatISO8601(at)}, true, nil
}

// UnmarkSessionEnded removes a session's end mark. Removing a mark that does
// not exist succeeds.
func (s *Store) UnmarkSessionEnded(ctx context.Context, sessionID string) error {
	if _, err := s.pg.ExecContext(ctx,
		`DELETE FROM session_end_marks WHERE session_id = $1`, sessionID,
	); err != nil {
		return fmt.Errorf("unmarking session %s ended: %w", sessionID, err)
	}
	return nil
}

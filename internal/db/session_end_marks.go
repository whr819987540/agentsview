package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// End mark kinds say which sessions a mark covers.
const (
	// EndMarkRow covers the marked session's whole sidebar row: its
	// topmost ancestor and every descendant with messages.
	EndMarkRow = "row"
	// EndMarkBranch covers one branch row of the session tree, such as a
	// fork: the marked session and its descendants with messages. It
	// leaves the rest of the row unmarked.
	EndMarkBranch = "branch"
)

// ValidEndMarkKind reports whether kind names an end mark kind.
func ValidEndMarkKind(kind string) bool {
	return kind == EndMarkRow || kind == EndMarkBranch
}

// SessionEndMark records that the user marked a session as ended. The mark
// covers the sessions its kind names and holds their latest activity when
// it was set, so the mark stops counting once they have newer activity. A
// session holds at most one mark.
type SessionEndMark struct {
	SessionID  string `json:"session_id"`
	ActivityAt string `json:"activity_at"`
	Kind       string `json:"kind" enum:"row,branch"`
}

// endMarkLatestCTE computes, for each session that marked(mark_id, kind)
// yields, the latest activity of the sessions its mark covers. A row mark
// covers the topmost ancestor reachable through parent_session_id and every
// descendant with messages; a branch mark covers the marked session and its
// descendants with messages. Trashed sessions count, since their activity
// still happened after any mark made before it. A parent cycle has no top,
// so the marked session seeds its own row. latest(mark_id, at) holds the
// newest activity time as stored, compared by instant rather than by text.
const endMarkLatestCTE = `
	up(mark_id, id, parent_id) AS (
		SELECT m.mark_id, s.id, s.parent_session_id
		FROM marked m JOIN sessions s ON s.id = m.mark_id
		WHERE m.kind = 'row'
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
	activity(mark_id, at) AS (
		SELECT f.mark_id, ` + sidebarActivityExprSQLiteS + `
		FROM family f JOIN sessions s ON s.id = f.id
	),
	latest(mark_id, at) AS (
		-- SQLite takes a bare column from the row that holds the MAX.
		SELECT mark_id, at FROM (
			SELECT mark_id, at, MAX(julianday(at)) FROM activity GROUP BY mark_id
		)
	)`

// SessionEndMarksEditable reports whether marks can change: false on a
// read-only archive.
func (db *DB) SessionEndMarksEditable(context.Context) bool { return !db.readOnly }

// ListSessionEndMarks returns the end marks still in effect, ordered by
// session ID. A mark stops being in effect once the sessions it covers have
// activity after it, or when it holds no readable time; a writable archive
// deletes those marks, so trashing or deleting the newer session does not
// bring the mark back.
func (db *DB) ListSessionEndMarks(ctx context.Context) ([]SessionEndMark, error) {
	return db.ListSessionEndMarksForScope(ctx, nil, nil)
}

// ListSessionEndMarksForScope is ListSessionEndMarks restricted to marks on
// sessions in the given project scope (see curationScopeWhere), for mirror
// pushes. Its cost is bounded by the marks and their rows, not the archive.
func (db *DB) ListSessionEndMarksForScope(
	ctx context.Context, projects, excludeProjects []string,
) ([]SessionEndMark, error) {
	where, args := curationScopeWhere("s", projects, excludeProjects)
	rows, err := db.getReader().QueryContext(ctx, `
		WITH RECURSIVE marked(mark_id, kind) AS (
			SELECT m.session_id, m.kind FROM session_end_marks m
			JOIN sessions s ON s.id = m.session_id`+where+`
		),`+endMarkLatestCTE+`
		SELECT m.session_id, m.activity_at, m.kind,
			julianday(m.activity_at) IS NULL
				OR COALESCE(julianday(l.at) > julianday(m.activity_at), 0)
		FROM marked k
		JOIN session_end_marks m ON m.session_id = k.mark_id
		LEFT JOIN latest l ON l.mark_id = m.session_id
		ORDER BY m.session_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing session end marks: %w", err)
	}
	defer rows.Close()
	marks := []SessionEndMark{}
	var outdated []SessionEndMark
	for rows.Next() {
		var mark SessionEndMark
		var stale bool
		if err := rows.Scan(&mark.SessionID, &mark.ActivityAt, &mark.Kind, &stale); err != nil {
			return nil, fmt.Errorf("scanning session end mark: %w", err)
		}
		if stale {
			outdated = append(outdated, mark)
		} else {
			marks = append(marks, mark)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing session end marks: %w", err)
	}
	if len(outdated) > 0 && !db.readOnly {
		// Best effort: an outdated mark never lists, so a failed delete
		// only postpones the cleanup to the next list.
		_ = db.deleteOutdatedSessionEndMarks(ctx, outdated)
	}
	return marks, nil
}

// deleteOutdatedSessionEndMarks deletes marks that were outdated when
// listed, unless they were set again since.
func (db *DB) deleteOutdatedSessionEndMarks(
	ctx context.Context, marks []SessionEndMark,
) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.getWriter().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleting outdated session end marks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, mark := range marks {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM session_end_marks
			WHERE session_id = ? AND activity_at = ?`,
			mark.SessionID, mark.ActivityAt,
		); err != nil {
			return fmt.Errorf("deleting outdated session end mark %s: %w",
				mark.SessionID, err)
		}
	}
	return tx.Commit()
}

// MarkSessionEnded marks the sessions a mark of the given kind covers as
// ended as of their latest activity, replacing an earlier mark of either
// kind on the session. The server reads that activity from every covered
// session, so a client that loaded only some of them still marks all of
// them. It returns false when the session does not exist.
func (db *DB) MarkSessionEnded(
	ctx context.Context, sessionID, kind string,
) (SessionEndMark, bool, error) {
	if !ValidEndMarkKind(kind) {
		return SessionEndMark{}, false, fmt.Errorf("unknown end mark kind %q", kind)
	}
	if err := db.requireWritable(); err != nil {
		return SessionEndMark{}, false, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	mark := SessionEndMark{SessionID: sessionID, Kind: kind}
	err := db.getWriter().QueryRow(ctx, `
		WITH RECURSIVE marked(mark_id, kind) AS (SELECT ?, ?),`+endMarkLatestCTE+`
		INSERT INTO session_end_marks (session_id, activity_at, kind)
		SELECT l.mark_id, l.at, m.kind
		FROM latest l JOIN marked m ON m.mark_id = l.mark_id
		WHERE l.at IS NOT NULL
		ON CONFLICT(session_id) DO UPDATE SET
			activity_at = excluded.activity_at,
			kind = excluded.kind,
			created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		RETURNING activity_at`,
		sessionID, kind,
	).Scan(&mark.ActivityAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionEndMark{}, false, nil
	}
	if err != nil {
		return SessionEndMark{}, false,
			fmt.Errorf("marking session %s ended: %w", sessionID, err)
	}
	return mark, true, nil
}

// UnmarkSessionEnded removes a session's end mark when it has the given
// kind, and keeps a mark of the other kind. Removing a mark that does not
// exist succeeds.
func (db *DB) UnmarkSessionEnded(ctx context.Context, sessionID, kind string) error {
	if !ValidEndMarkKind(kind) {
		return fmt.Errorf("unknown end mark kind %q", kind)
	}
	if err := db.requireWritable(); err != nil {
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if _, err := db.getWriter().Exec(ctx,
		`DELETE FROM session_end_marks WHERE session_id = ? AND kind = ?`,
		sessionID, kind,
	); err != nil {
		return fmt.Errorf("unmarking session %s ended: %w", sessionID, err)
	}
	return nil
}

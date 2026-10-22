package db

import (
	"context"
	"fmt"
)

// SessionEndMark records that the user marked a session as ended. The mark
// holds the session's last activity the user saw when marking, so the
// sidebar can treat it as cleared once the session has newer activity.
type SessionEndMark struct {
	SessionID  string `json:"session_id"`
	ActivityAt string `json:"activity_at"`
	CreatedAt  string `json:"created_at"`
}

// ListSessionEndMarks returns every end mark, ordered by session ID.
func (db *DB) ListSessionEndMarks(ctx context.Context) ([]SessionEndMark, error) {
	rows, err := db.getReader().QueryContext(ctx, `
		SELECT session_id, activity_at, created_at
		FROM session_end_marks
		ORDER BY session_id`)
	if err != nil {
		return nil, fmt.Errorf("listing session end marks: %w", err)
	}
	defer rows.Close()
	marks := []SessionEndMark{}
	for rows.Next() {
		var mark SessionEndMark
		if err := rows.Scan(
			&mark.SessionID, &mark.ActivityAt, &mark.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning session end mark: %w", err)
		}
		marks = append(marks, mark)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing session end marks: %w", err)
	}
	return marks, nil
}

// MarkSessionEnded marks a session as ended as of activityAt, replacing an
// earlier mark. An empty activityAt uses the session's own last activity.
// It returns false when the session does not exist.
func (db *DB) MarkSessionEnded(
	ctx context.Context, sessionID, activityAt string,
) (bool, error) {
	if err := db.requireWritable(); err != nil {
		return false, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	res, err := db.getWriter().Exec(ctx, `
		INSERT INTO session_end_marks (session_id, activity_at)
		SELECT id, COALESCE(NULLIF(?, ''), ended_at, started_at, created_at)
		FROM sessions WHERE id = ?
		ON CONFLICT(session_id) DO UPDATE SET
			activity_at = excluded.activity_at,
			created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		activityAt, sessionID,
	)
	if err != nil {
		return false, fmt.Errorf("marking session %s ended: %w", sessionID, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnmarkSessionEnded removes a session's end mark. Removing a mark that does
// not exist succeeds.
func (db *DB) UnmarkSessionEnded(ctx context.Context, sessionID string) error {
	if err := db.requireWritable(); err != nil {
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if _, err := db.getWriter().Exec(ctx,
		`DELETE FROM session_end_marks WHERE session_id = ?`, sessionID,
	); err != nil {
		return fmt.Errorf("unmarking session %s ended: %w", sessionID, err)
	}
	return nil
}

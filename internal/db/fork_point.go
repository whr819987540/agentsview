package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetSessionForkPoint returns the source UUID of the last parent message
// a session inherits: where a fork branched off its parent, or where a
// continuation took over. It returns "" when the session does not exist,
// has no parent transcript, or its parser has not reported the point.
func (db *DB) GetSessionForkPoint(
	ctx context.Context, id string,
) (string, error) {
	var raw sql.NullString
	err := db.getReader().QueryRowContext(
		ctx,
		"SELECT fork_point_uuid FROM sessions WHERE id = ? AND deleted_at IS NULL",
		id,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting fork point of %s: %w", id, err)
	}
	return raw.String, nil
}

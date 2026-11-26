package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// BranchPlacement locates a place in a session's transcript where a branch
// leaves it: a fork or continuation that takes over after a message, or
// inputs a rewind abandoned there.
type BranchPlacement struct {
	// AfterOrdinal is the ordinal of the last message before the branch,
	// or -1 when the branch leaves before the first message.
	AfterOrdinal int `json:"after_ordinal"`
	// AfterTurn counts the user prompts at or before AfterOrdinal.
	AfterTurn int `json:"after_turn"`
	// TurnsAfter counts the user prompts after AfterOrdinal.
	TurnsAfter int `json:"turns_after"`
}

// userPromptPredicate selects the messages counted as user turns, the same
// ones user_message_count counts.
const userPromptPredicate = "role = 'user' AND is_system = 0"

// LocateBranch places a branch that leaves sessionID. A non-empty
// forkPointUUID places it after the message with that source UUID. When the
// UUID is empty or names no message of the session, a non-empty before
// places it after the last message timestamped earlier. ok is false when
// neither locates a message.
func (db *DB) LocateBranch(
	ctx context.Context, sessionID, forkPointUUID, before string,
) (BranchPlacement, bool, error) {
	reader := db.getReader()
	ordinal := sql.NullInt64{}
	if forkPointUUID != "" {
		err := reader.QueryRowContext(ctx,
			`SELECT MAX(ordinal) FROM messages
			 WHERE session_id = ? AND source_uuid = ?`,
			sessionID, forkPointUUID,
		).Scan(&ordinal)
		if err != nil {
			return BranchPlacement{}, false, fmt.Errorf(
				"locating fork point in %s: %w", sessionID, err,
			)
		}
	}
	if !ordinal.Valid && before != "" {
		err := reader.QueryRowContext(ctx,
			`SELECT MAX(ordinal) FROM messages
			 WHERE session_id = ? AND timestamp != ''
			   AND julianday(timestamp) < julianday(?)`,
			sessionID, before,
		).Scan(&ordinal)
		if err != nil {
			return BranchPlacement{}, false, fmt.Errorf(
				"locating branch time in %s: %w", sessionID, err,
			)
		}
	}
	if !ordinal.Valid {
		return BranchPlacement{}, false, nil
	}
	placement, err := db.placeAfter(ctx, sessionID, int(ordinal.Int64))
	return placement, err == nil, err
}

// LocateRewind places inputs a rewind abandoned after the message with
// source UUID afterUUID: just before the user prompt that replaced them, or
// after the last message when no prompt followed. An empty afterUUID places
// them before the first prompt. ok is false when afterUUID names no message
// of the session.
func (db *DB) LocateRewind(
	ctx context.Context, sessionID, afterUUID string,
) (BranchPlacement, bool, error) {
	reader := db.getReader()
	anchor := -1
	if afterUUID != "" {
		var found sql.NullInt64
		err := reader.QueryRowContext(ctx,
			`SELECT MAX(ordinal) FROM messages
			 WHERE session_id = ? AND source_uuid = ?`,
			sessionID, afterUUID,
		).Scan(&found)
		if err != nil {
			return BranchPlacement{}, false, fmt.Errorf(
				"locating rewind in %s: %w", sessionID, err,
			)
		}
		if !found.Valid {
			return BranchPlacement{}, false, nil
		}
		anchor = int(found.Int64)
	}
	// The abandoned inputs were siblings of the next prompt after the
	// anchor, so they leave the transcript right before it.
	var last sql.NullInt64
	err := reader.QueryRowContext(ctx,
		`SELECT COALESCE(
			(SELECT MAX(ordinal) FROM messages
			 WHERE session_id = ?1 AND ordinal < (
				SELECT MIN(ordinal) FROM messages
				WHERE session_id = ?1 AND ordinal > ?2 AND `+userPromptPredicate+`)),
			(SELECT MAX(ordinal) FROM messages WHERE session_id = ?1
			 AND NOT EXISTS (
				SELECT 1 FROM messages
				WHERE session_id = ?1 AND ordinal > ?2 AND `+userPromptPredicate+`)))`,
		sessionID, anchor,
	).Scan(&last)
	if err != nil {
		return BranchPlacement{}, false, fmt.Errorf(
			"locating rewind in %s: %w", sessionID, err,
		)
	}
	after := -1
	if last.Valid {
		after = int(last.Int64)
	}
	placement, err := db.placeAfter(ctx, sessionID, after)
	return placement, err == nil, err
}

// placeAfter counts the user turns on either side of an ordinal.
func (db *DB) placeAfter(
	ctx context.Context, sessionID string, after int,
) (BranchPlacement, error) {
	var before, later sql.NullInt64
	err := db.getReader().QueryRowContext(ctx,
		`SELECT
			SUM(CASE WHEN ordinal <= ?2 THEN 1 ELSE 0 END),
			SUM(CASE WHEN ordinal > ?2 THEN 1 ELSE 0 END)
		 FROM messages
		 WHERE session_id = ?1 AND `+userPromptPredicate,
		sessionID, after,
	).Scan(&before, &later)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return BranchPlacement{}, fmt.Errorf(
			"counting turns in %s: %w", sessionID, err,
		)
	}
	return BranchPlacement{
		AfterOrdinal: after,
		AfterTurn:    int(before.Int64),
		TurnsAfter:   int(later.Int64),
	}, nil
}

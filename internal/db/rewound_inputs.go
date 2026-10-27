package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// RewoundInput is a user input on a transcript branch that a rewind
// abandoned, such as Claude Code's Esc Esc. The branch's messages are not
// archived; the input tree draws its inputs as a side branch.
type RewoundInput struct {
	// SourceUUID identifies the input's transcript entry.
	SourceUUID string `json:"source_uuid"`
	// AfterUUID is the source_uuid of the input it followed: a message
	// on the session's path, or another rewound input of the session. It
	// is empty when no input of the transcript came before it.
	AfterUUID string `json:"after_uuid,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Content   string `json:"content"`
}

// rewoundInputsColumn encodes a session's rewound inputs for the
// rewound_inputs column. Nil maps to NULL, which the upsert treats as
// "keep the stored list".
func rewoundInputsColumn(inputs []RewoundInput) any {
	if inputs == nil {
		return nil
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		// The struct holds only strings, which always encode.
		return nil
	}
	return string(encoded)
}

// GetSessionRewoundInputs returns the rewound inputs stored for a session,
// in transcript order. It returns nil when the session does not exist or
// its parser has not reported any.
func (db *DB) GetSessionRewoundInputs(
	ctx context.Context, id string,
) ([]RewoundInput, error) {
	var raw sql.NullString
	err := db.getReader().QueryRowContext(
		ctx,
		"SELECT rewound_inputs FROM sessions WHERE id = ? AND deleted_at IS NULL",
		id,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !raw.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting rewound inputs of %s: %w", id, err)
	}
	var inputs []RewoundInput
	if err := json.Unmarshal([]byte(raw.String), &inputs); err != nil {
		return nil, fmt.Errorf("decoding rewound inputs of %s: %w", id, err)
	}
	return inputs, nil
}

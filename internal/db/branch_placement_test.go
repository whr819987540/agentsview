package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPlacementSession stores a session of three turns. The first two
// prompts are answered after a tool call: Claude stores the tool output as a
// user row with no text (ordinal 2), other agents as a tool_result row
// (ordinal 6). Ordinal 8 is a system row. None of these is a turn.
func seedPlacementSession(t *testing.T, d *DB) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, d.UpsertSession(ctx, Session{
		ID: "s", Project: "proj", Machine: "local", Agent: "claude",
		MessageCount: 11, UserMessageCount: 3,
	}))
	msg := func(ordinal int, role string, system bool, uuid, ts string) Message {
		return Message{
			SessionID: "s", Ordinal: ordinal, Role: role, IsSystem: system,
			Content: uuid, ContentLength: len(uuid),
			SourceUUID: uuid, Timestamp: ts,
		}
	}
	claudeToolOutput := msg(2, "user", false, "t1", "2026-07-01T10:02:00Z")
	claudeToolOutput.Content, claudeToolOutput.ContentLength = "", 0
	markedToolOutput := msg(6, "user", false, "t2", "2026-07-01T10:12:00Z")
	markedToolOutput.SourceSubtype = "tool_result"
	require.NoError(t, d.InsertMessages(ctx, []Message{
		msg(0, "user", false, "u1", "2026-07-01T10:00:00Z"),
		msg(1, "assistant", false, "c1", "2026-07-01T10:01:00Z"),
		claudeToolOutput,
		msg(3, "assistant", false, "a1", "2026-07-01T10:03:00Z"),
		msg(4, "user", false, "u2", "2026-07-01T10:10:00Z"),
		msg(5, "assistant", false, "c2", "2026-07-01T10:11:00Z"),
		markedToolOutput,
		msg(7, "assistant", false, "a2", "2026-07-01T10:13:00Z"),
		msg(8, "user", true, "n2", "2026-07-01T10:14:00Z"),
		msg(9, "user", false, "u3", "2026-07-01T10:20:00Z"),
		msg(10, "assistant", false, "a3", "2026-07-01T10:21:00Z"),
	}))
}

func TestLocateBranch(t *testing.T) {
	d := testDB(t)
	seedPlacementSession(t, d)

	tests := []struct {
		name      string
		forkPoint string
		before    string
		want      BranchPlacement
		wantOK    bool
	}{
		{
			name:      "fork point places it even when later messages predate the child",
			forkPoint: "c1",
			before:    "2026-07-01T11:00:00Z",
			want:      BranchPlacement{AfterOrdinal: 1, AfterTurn: 1, TurnsAfter: 2},
			wantOK:    true,
		},
		{
			name:      "unknown fork point falls back to the child's start",
			forkPoint: "elsewhere",
			before:    "2026-07-01T10:15:00Z",
			want:      BranchPlacement{AfterOrdinal: 8, AfterTurn: 2, TurnsAfter: 1},
			wantOK:    true,
		},
		{
			name:   "child start after the last message places it at the end",
			before: "2026-07-01T12:00:00Z",
			want:   BranchPlacement{AfterOrdinal: 10, AfterTurn: 3, TurnsAfter: 0},
			wantOK: true,
		},
		{
			name:   "child older than every message cannot be placed",
			before: "2026-07-01T09:00:00Z",
		},
		{
			name: "nothing to place by",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := d.LocateBranch(t.Context(), "s", tt.forkPoint, tt.before)
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLocateRewind(t *testing.T) {
	d := testDB(t)
	seedPlacementSession(t, d)

	tests := []struct {
		name   string
		after  string
		want   BranchPlacement
		wantOK bool
	}{
		{
			name:   "inputs abandoned after a turn leave before the prompt that replaced them",
			after:  "u2",
			want:   BranchPlacement{AfterOrdinal: 8, AfterTurn: 2, TurnsAfter: 1},
			wantOK: true,
		},
		{
			name:   "tool output after the anchor does not replace the abandoned inputs",
			after:  "u1",
			want:   BranchPlacement{AfterOrdinal: 3, AfterTurn: 1, TurnsAfter: 2},
			wantOK: true,
		},
		{
			name:   "inputs abandoned after the last turn leave after the last message",
			after:  "u3",
			want:   BranchPlacement{AfterOrdinal: 10, AfterTurn: 3, TurnsAfter: 0},
			wantOK: true,
		},
		{
			name:   "inputs abandoned before any input leave before the first prompt",
			after:  "",
			want:   BranchPlacement{AfterOrdinal: -1, AfterTurn: 0, TurnsAfter: 3},
			wantOK: true,
		},
		{
			name:  "an anchor outside the session cannot be placed",
			after: "missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := d.LocateRewind(t.Context(), "s", tt.after)
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

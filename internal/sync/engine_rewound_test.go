package sync_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func rewoundUUIDs(t *testing.T, database *db.DB, id string) []string {
	t.Helper()
	inputs, err := database.GetSessionRewoundInputs(t.Context(), id)
	require.NoError(t, err)
	out := make([]string, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, input.AfterUUID+">"+input.SourceUUID+":"+input.Content)
	}
	return out
}

// A rewind appended after the first sync stores the abandoned input, and a
// later append to the live branch keeps it.
func TestSyncStoresRewoundInputs(t *testing.T) {
	env := setupTestEnv(t)

	base := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "start", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "ok", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "first try", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "tried", "d", "c").
		String()
	path := env.writeClaudeSession(t, "proj", "rewind-inputs.jsonl", base)
	env.engine.SyncAll(t.Context(), nil)
	assert.Empty(t, rewoundUUIDs(t, env.db, "rewind-inputs"))

	rewound := base + testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "second try", "e", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "tried again", "f", "e").
		String()
	require.NoError(t, os.WriteFile(path, []byte(rewound), 0o644))
	env.engine.SyncAll(t.Context(), nil)

	msgs := fetchMessages(t, env.db, "rewind-inputs")
	require.Len(t, msgs, 4, "the transcript follows the live branch")
	assert.Equal(t, "second try", msgs[2].Content)
	assert.Equal(t, []string{"a>c:first try"}, rewoundUUIDs(t, env.db, "rewind-inputs"))

	continued := rewound + testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:02:00Z", "keep going", "g", "f").
		AddClaudeAssistantWithUUID("2024-01-01T10:02:01Z", "going", "h", "g").
		String()
	require.NoError(t, os.WriteFile(path, []byte(continued), 0o644))
	env.engine.SyncAll(t.Context(), nil)

	assert.Len(t, fetchMessages(t, env.db, "rewind-inputs"), 6)
	assert.Equal(t, []string{"a>c:first try"}, rewoundUUIDs(t, env.db, "rewind-inputs"),
		"an append to the live branch keeps the rewound inputs")
}

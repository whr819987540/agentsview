package db

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/config"
)

func rewoundSession(id string, inputs []RewoundInput) Session {
	return Session{
		ID: id, Project: "proj", Machine: "local", Agent: "claude",
		MessageCount: 2, UserMessageCount: 1,
		RewoundInputs: inputs,
	}
}

func TestSessionRewoundInputsUpsert(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	first := []RewoundInput{
		{
			SourceUUID: "c", AfterUUID: "a",
			Timestamp: "2024-01-01T10:00:02Z", Content: "first try",
		},
		{
			SourceUUID: "e", AfterUUID: "c",
			Timestamp: "2024-01-01T10:00:04Z", Content: "then this\x00",
		},
	}

	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s1", first)))
	got, err := d.GetSessionRewoundInputs(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []RewoundInput{
		first[0],
		{
			SourceUUID: "e", AfterUUID: "c",
			Timestamp: "2024-01-01T10:00:04Z", Content: "then this",
		},
	}, got, "stored inputs are sanitized like message text")

	// An incremental or non-parse writer reports nothing and keeps them.
	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s1", nil)))
	got, err = d.GetSessionRewoundInputs(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// A parse that finds none clears them.
	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s1", []RewoundInput{})))
	got, err = d.GetSessionRewoundInputs(ctx, "s1")
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	// Rows written without a report, and missing sessions, have none.
	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s2", nil)))
	got, err = d.GetSessionRewoundInputs(ctx, "s2")
	require.NoError(t, err)
	assert.Nil(t, got)
	got, err = d.GetSessionRewoundInputs(ctx, "missing")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionRewoundInputsUsageArchiveStoresNone(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	inputs := []RewoundInput{{SourceUUID: "c", AfterUUID: "a", Content: "secret plan"}}
	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s1", inputs)))

	d.SetArchiveContent(config.ArchiveContentUsage)
	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s1", nil)))
	got, err := d.GetSessionRewoundInputs(ctx, "s1")
	require.NoError(t, err)
	assert.Empty(t, got, "a usage archive drops prompt text kept from before")

	require.NoError(t, d.UpsertSession(ctx, rewoundSession("s2", inputs)))
	got, err = d.GetSessionRewoundInputs(ctx, "s2")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCopyOrphanedDataKeepsRewoundInputs(t *testing.T) {
	inputs := []RewoundInput{{
		SourceUUID: "c", AfterUUID: "a",
		Timestamp: "2024-01-01T10:00:02Z", Content: "first try",
	}}
	seed := func(t *testing.T, path string) {
		t.Helper()
		source := testDBAtPath(t, path, "source")
		require.NoError(t, source.UpsertSession(t.Context(), rewoundSession("gone", inputs)))
		require.NoError(t, source.Close())
	}
	tests := []struct {
		policy config.ArchiveContent
		want   []RewoundInput
	}{
		{config.ArchiveContentFull, inputs},
		{config.ArchiveContentTranscripts, inputs},
		{config.ArchiveContentUsage, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.policy), func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "source.db")
			seed(t, sourcePath)
			destination := testDBAtPath(t, filepath.Join(dir, "dest.db"), "dest")
			t.Cleanup(func() { require.NoError(t, destination.Close()) })
			destination.SetArchiveContent(tt.policy)

			copied, err := destination.CopyOrphanedDataFrom(sourcePath)
			require.NoError(t, err)
			require.Equal(t, 1, copied)

			got, err := destination.GetSessionRewoundInputs(t.Context(), "gone")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func forkPointSession(id string, forkPoint *string) Session {
	parent := "parent"
	return Session{
		ID: id, Project: "proj", Machine: "local", Agent: "claude",
		MessageCount: 2, UserMessageCount: 1,
		ParentSessionID: &parent, RelationshipType: "fork",
		ForkPointUUID: forkPoint,
	}
}

func TestSessionForkPointUpsert(t *testing.T) {
	d := testDB(t)
	ctx := t.Context()
	point := func(s string) *string { return &s }

	require.NoError(t, d.UpsertSession(ctx, forkPointSession("s1", point("a2"))))
	got, err := d.GetSessionForkPoint(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "a2", got)

	// An incremental or non-parse writer reports nothing and keeps it.
	require.NoError(t, d.UpsertSession(ctx, forkPointSession("s1", nil)))
	got, err = d.GetSessionForkPoint(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "a2", got)

	// A parse that finds a different point replaces it.
	require.NoError(t, d.UpsertSession(ctx, forkPointSession("s1", point("a1"))))
	got, err = d.GetSessionForkPoint(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "a1", got)

	// A parse that finds none clears it.
	require.NoError(t, d.UpsertSession(ctx, forkPointSession("s1", point(""))))
	got, err = d.GetSessionForkPoint(ctx, "s1")
	require.NoError(t, err)
	assert.Empty(t, got)

	// Stored text is sanitized like other parser fields.
	require.NoError(t, d.UpsertSession(ctx, forkPointSession("s2", point("a3\x00"))))
	got, err = d.GetSessionForkPoint(ctx, "s2")
	require.NoError(t, err)
	assert.Equal(t, "a3", got)

	// Missing sessions have none.
	got, err = d.GetSessionForkPoint(ctx, "missing")
	require.NoError(t, err)
	assert.Empty(t, got)
}

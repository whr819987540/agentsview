package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/dbtest"
	"go.kenn.io/agentsview/internal/service"
)

type branchRow struct {
	kind    string
	after   int
	turn    int
	later   int
	session string
	inputs  []string
}

func branchRows(points []service.BranchPoint) []branchRow {
	rows := make([]branchRow, 0, len(points))
	for _, p := range points {
		row := branchRow{
			kind: p.Kind, after: p.AfterOrdinal, turn: p.AfterTurn,
			later: p.TurnsAfter, inputs: p.Inputs,
		}
		if p.Session != nil {
			row.session = p.Session.ID
		}
		rows = append(rows, row)
	}
	return rows
}

// seedBranchFamily stores a three-turn session with a rewind after its
// second prompt, a /fork after its last turn, and a handoff that took over
// after its first turn although it started after every parent message.
func seedBranchFamily(t *testing.T, d *db.DB) {
	t.Helper()
	dbtest.SeedSession(t, d, "base", "p1", func(s *db.Session) {
		s.StartedAt = new("2026-07-01T10:00:00Z")
		s.RewoundInputs = []db.RewoundInput{
			{SourceUUID: "r1", AfterUUID: "u2", Content: "first try"},
			{SourceUUID: "r2", AfterUUID: "r1", Content: "second try"},
		}
	})
	dbtest.SeedSession(t, d, "forked", "p1", func(s *db.Session) {
		s.ParentSessionID = new("base")
		s.RelationshipType = "fork"
		s.StartedAt = new("2026-07-01T11:00:00Z")
		s.ForkPointUUID = new("a3")
	})
	dbtest.SeedSession(t, d, "moved", "p1", func(s *db.Session) {
		s.ParentSessionID = new("base")
		s.RelationshipType = "continuation"
		s.StartedAt = new("2026-07-01T12:00:00Z")
		s.ForkPointUUID = new("a1")
	})
	dbtest.SeedSession(t, d, "helper", "p1", func(s *db.Session) {
		s.ParentSessionID = new("base")
		s.RelationshipType = "subagent"
		s.StartedAt = new("2026-07-01T10:05:00Z")
	})
	dbtest.SeedMessages(t, d,
		uuidMsg("base", 0, "user", "plan", "2026-07-01T10:00:00Z", "u1"),
		uuidMsg("base", 1, "assistant", "ok", "2026-07-01T10:01:00Z", "a1"),
		uuidMsg("base", 2, "user", "build", "2026-07-01T10:10:00Z", "u2"),
		uuidMsg("base", 3, "assistant", "ok", "2026-07-01T10:11:00Z", "a2"),
		uuidMsg("base", 4, "user", "build it properly", "2026-07-01T10:20:00Z", "u3"),
		uuidMsg("base", 5, "assistant", "ok", "2026-07-01T10:21:00Z", "a3"),
		uuidMsg("forked", 0, "user", "try another way", "2026-07-01T11:00:00Z", "f1"),
		uuidMsg("moved", 0, "user", "instead", "2026-07-01T12:00:00Z", "m1"),
	)
}

func newBranchPointReader(t *testing.T) (service.BranchPointReader, *directTestEnv) {
	t.Helper()
	svc, env := newDirectTestSvc(t)
	reader, ok := svc.(service.BranchPointReader)
	require.True(t, ok, "direct backend should place branch points")
	return reader, env
}

func TestDirectBackend_BranchPointsOfParent(t *testing.T) {
	t.Parallel()
	reader, env := newBranchPointReader(t)
	seedBranchFamily(t, env.db)

	points, err := reader.BranchPoints(t.Context(), "base")
	require.NoError(t, err)
	require.NotNil(t, points)
	assert.Nil(t, points.Leaves, "the root leaves no parent")
	assert.Equal(t, []branchRow{
		{kind: "continuation", after: 1, turn: 1, later: 2, session: "moved"},
		{kind: "rewind", after: 3, turn: 2, later: 1, inputs: []string{"first try", "second try"}},
		{kind: "fork", after: 5, turn: 3, later: 0, session: "forked"},
	}, branchRows(points.Points))
}

func TestDirectBackend_BranchPointsPlaceChildInParent(t *testing.T) {
	t.Parallel()
	reader, env := newBranchPointReader(t)
	seedBranchFamily(t, env.db)

	points, err := reader.BranchPoints(t.Context(), "moved")
	require.NoError(t, err)
	require.NotNil(t, points)
	require.NotNil(t, points.Leaves)
	assert.Equal(t, "base", points.Leaves.Parent.ID)
	assert.Equal(t, "continuation", points.Leaves.RelationshipType)
	assert.Equal(t, 1, points.Leaves.AfterOrdinal)
	assert.Equal(t, 1, points.Leaves.AfterTurn)
	assert.Equal(t, 2, points.Leaves.TurnsAfter,
		"the continuation does not carry the parent's later turns")
	assert.Empty(t, points.Points)

	missing, err := reader.BranchPoints(t.Context(), "missing")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

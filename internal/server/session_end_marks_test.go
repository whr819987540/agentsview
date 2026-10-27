package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.kenn.io/agentsview/internal/config"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/dbtest"
	"go.kenn.io/agentsview/internal/server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionEndMarkBody struct {
	SessionID  string `json:"session_id"`
	ActivityAt string `json:"activity_at"`
}

type sessionEndMarksBody struct {
	Marks    []sessionEndMarkBody `json:"marks"`
	Editable bool                 `json:"editable"`
}

func TestSessionEndMarkHandlers(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)
	te.seedSession(t, "a2", "alpha", 2)

	w := te.get(t, "/api/v1/session-end-marks")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list := decode[sessionEndMarksBody](t, w)
	assert.NotNil(t, list.Marks)
	assert.Empty(t, list.Marks)
	assert.True(t, list.Editable)

	for _, id := range []string{"a1", "a2"} {
		w = te.put(t, "/api/v1/sessions/"+id+"/ended", "")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, sessionEndMarkBody{SessionID: id, ActivityAt: tsSeedEnd},
			decode[sessionEndMarkBody](t, w), "the response holds the stored mark")
	}
	w = te.put(t, "/api/v1/sessions/missing/ended", "")
	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "session not found")

	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNoContent, w.Code, "unmarking twice succeeds: %s", w.Body.String())

	assert.Equal(t, []sessionEndMarkBody{{SessionID: "a2", ActivityAt: tsSeedEnd}},
		te.listEndMarks(t).Marks)
}

func TestSessionEndMarkCoversTheWholeRow(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "root", "alpha", 2)
	te.seedSession(t, "cont", "alpha", 2, func(s *db.Session) {
		s.ParentSessionID = new("root")
		s.RelationshipType = "continuation"
		s.EndedAt = new("2025-01-15T11:30:00Z")
	})

	// A client that loaded only the root still marks the whole row.
	w := te.put(t, "/api/v1/sessions/root/ended", "")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, []sessionEndMarkBody{{SessionID: "root", ActivityAt: "2025-01-15T11:30:00Z"}},
		te.listEndMarks(t).Marks, "the mark holds the continuation's later activity")

	// The continuation is resumed after the mark, then trashed.
	te.seedSession(t, "cont", "alpha", 2, func(s *db.Session) {
		s.ParentSessionID = new("root")
		s.RelationshipType = "continuation"
		s.EndedAt = new("2025-01-15T12:00:00Z")
	})
	assert.Empty(t, te.listEndMarks(t).Marks, "new activity in the row ends the mark")
	w = te.del(t, "/api/v1/sessions/cont")
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, te.listEndMarks(t).Marks, "trashing the resumed session keeps the mark ended")
}

func (te *testEnv) listEndMarks(t *testing.T) sessionEndMarksBody {
	t.Helper()
	w := te.get(t, "/api/v1/session-end-marks")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	return decode[sessionEndMarksBody](t, w)
}

func TestSessionEndMarkWriteReturns503WhileWriterClosed(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)
	require.NoError(t, te.db.CloseWriter())
	defer func() { assert.NoError(t, te.db.ReopenWriter()) }()

	w := te.put(t, "/api/v1/sessions/a1/ended", "")
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
}

func TestSessionEndMarksWithoutStoreSupportAreEmptyAndReadOnly(t *testing.T) {
	// Like hosted PostgreSQL, this store keeps no end marks.
	te := setupPGMode(t)
	te.seedSession(t, "a1", "alpha", 2)

	list := te.listEndMarks(t)
	assert.Empty(t, list.Marks)
	assert.NotNil(t, list.Marks)
	assert.False(t, list.Editable)

	w := te.put(t, "/api/v1/sessions/a1/ended", "")
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "not available in remote mode")
	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
}

// mirrorEndMarkStore serves pushed end marks read-only, as the DuckDB
// mirror and ClickHouse do.
type mirrorEndMarkStore struct {
	readOnlyTestStore
	marks []db.SessionEndMark
}

func (m mirrorEndMarkStore) ListSessionEndMarks(context.Context) ([]db.SessionEndMark, error) {
	return m.marks, nil
}

func (mirrorEndMarkStore) MarkSessionEnded(context.Context, string) (db.SessionEndMark, bool, error) {
	return db.SessionEndMark{}, false, db.ErrReadOnly
}

func (mirrorEndMarkStore) UnmarkSessionEnded(context.Context, string) error {
	return db.ErrReadOnly
}

func (mirrorEndMarkStore) SessionEndMarksEditable(context.Context) bool { return false }

func TestSessionEndMarksOnMirrorAreListedAndReadOnly(t *testing.T) {
	database := dbtest.OpenTestDB(t)
	cfg := config.Config{Host: "127.0.0.1", DataDir: t.TempDir(), WriteTimeout: 30 * time.Second}
	marks := []db.SessionEndMark{{SessionID: "a1", ActivityAt: tsSeedEnd}}
	srv := server.New(cfg, mirrorEndMarkStore{Store: database, marks: marks}, nil)
	te := &testEnv{srv: srv, handler: wrapTestHandler(cfg, srv.Handler()), db: database}

	list := te.listEndMarks(t)
	assert.Equal(t, []sessionEndMarkBody{{SessionID: "a1", ActivityAt: tsSeedEnd}}, list.Marks)
	assert.False(t, list.Editable)

	w := te.put(t, "/api/v1/sessions/a1/ended", "")
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
}

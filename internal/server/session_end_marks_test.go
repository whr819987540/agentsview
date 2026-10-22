package server_test

import (
	"net/http"
	"testing"

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

	w = te.put(t, "/api/v1/sessions/a1/ended", `{"activity_at":"2025-01-15T12:00:00.250Z"}`)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	w = te.put(t, "/api/v1/sessions/a2/ended", `{}`)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	w = te.put(t, "/api/v1/sessions/missing/ended", `{}`)
	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "session not found")

	w = te.get(t, "/api/v1/session-end-marks")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list = decode[sessionEndMarksBody](t, w)
	assert.Equal(t, []sessionEndMarkBody{
		{SessionID: "a1", ActivityAt: "2025-01-15T12:00:00.250Z"},
		{SessionID: "a2", ActivityAt: tsSeedEnd},
	}, list.Marks, "an empty body marks the session as of its own last activity")

	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNoContent, w.Code, "unmarking twice succeeds: %s", w.Body.String())

	w = te.get(t, "/api/v1/session-end-marks")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list = decode[sessionEndMarksBody](t, w)
	assert.Equal(t, []sessionEndMarkBody{{SessionID: "a2", ActivityAt: tsSeedEnd}}, list.Marks)
}

func TestSessionEndMarkWriteReturns503WhileWriterClosed(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)
	require.NoError(t, te.db.CloseWriter())
	defer func() { assert.NoError(t, te.db.ReopenWriter()) }()

	w := te.put(t, "/api/v1/sessions/a1/ended", `{}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
}

func TestSessionEndMarksOnReplicaAreEmptyAndReadOnly(t *testing.T) {
	te := setupPGMode(t)
	te.seedSession(t, "a1", "alpha", 2)

	w := te.get(t, "/api/v1/session-end-marks")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list := decode[sessionEndMarksBody](t, w)
	assert.Empty(t, list.Marks)
	assert.False(t, list.Editable)

	w = te.put(t, "/api/v1/sessions/a1/ended", `{}`)
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "not available in remote mode")
	w = te.del(t, "/api/v1/sessions/a1/ended")
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
}

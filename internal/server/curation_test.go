package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
)

func TestCurationExportHandler(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)
	te.seedSession(t, "a2", "alpha", 2)
	ctx := t.Context()
	_, err := te.db.StarSession(ctx, "a1")
	require.NoError(t, err)
	folder, err := te.db.CreateSessionFolder(ctx, "alpha", "Docs")
	require.NoError(t, err)
	require.NoError(t, te.db.MoveSessionsToFolder(ctx, folder.ID, []string{"a2"}))

	w := te.get(t, "/api/v1/curation/export")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	doc := decode[db.CurationExport](t, w)
	assert.Equal(t, "agentsview-curation", doc.Format)
	require.Len(t, doc.Sessions, 2)
	assert.Equal(t, "a1", doc.Sessions[0].ID)
	assert.True(t, doc.Sessions[0].Starred)
	assert.Equal(t, "a2", doc.Sessions[1].ID)
	assert.Equal(t, "Docs", doc.Sessions[1].Folder)
	assert.Equal(t, []db.CurationExportFolder{{Project: "alpha", Name: "Docs"}}, doc.Folders)
}

func TestCurationImportHandler(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)
	body := `{"format":"agentsview-curation","version":1,` +
		`"sessions":[{"id":"a1","starred":true},{"id":"gone","starred":true}],` +
		`"folders":[]}`

	w := te.post(t, "/api/v1/curation/import?dry_run=true", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	preview := decode[db.CurationImportResult](t, w)
	assert.True(t, preview.DryRun)
	assert.Equal(t, db.CurationImportCounts{Added: 1}, preview.Stars)
	assert.Equal(t, []string{"gone"}, preview.Unmatched)
	starred, err := te.db.ListStarredSessionIDs(t.Context())
	require.NoError(t, err)
	assert.Empty(t, starred, "a dry run writes nothing")

	w = te.post(t, "/api/v1/curation/import", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	result := decode[db.CurationImportResult](t, w)
	assert.False(t, result.DryRun)
	assert.Equal(t, db.CurationImportCounts{Added: 1}, result.Stars)
	starred, err = te.db.ListStarredSessionIDs(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"a1"}, starred)
}

func TestCurationImportRejectsUnsupportedVersion(t *testing.T) {
	te := setup(t)
	w := te.post(t, "/api/v1/curation/import",
		`{"format":"agentsview-curation","version":2,"sessions":[],"folders":[]}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w,
		"invalid curation export: version 2 is not supported; this build reads version 1")
}

func TestCurationImportReturns503WhileWriterClosed(t *testing.T) {
	te := setup(t)
	require.NoError(t, te.db.CloseWriter())
	defer func() { assert.NoError(t, te.db.ReopenWriter()) }()

	w := te.post(t, "/api/v1/curation/import",
		`{"format":"agentsview-curation","version":1,"sessions":[],"folders":[]}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
}

func TestCurationRoutesOnReplica(t *testing.T) {
	te := setupPGMode(t)

	w := te.get(t, "/api/v1/curation/export")
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "not available in remote mode")

	w = te.post(t, "/api/v1/curation/import",
		`{"format":"agentsview-curation","version":1,"sessions":[],"folders":[]}`)
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "not available in remote mode")
}

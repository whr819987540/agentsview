package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionFolderBody struct {
	ID         int64    `json:"id"`
	Project    string   `json:"project"`
	Name       string   `json:"name"`
	ParentID   *int64   `json:"parent_id"`
	SessionIDs []string `json:"session_ids"`
}

type sessionFoldersBody struct {
	Folders  []sessionFolderBody `json:"folders"`
	Editable bool                `json:"editable"`
}

func TestSessionFolderHandlers(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)
	te.seedSession(t, "a2", "alpha", 2)
	te.seedSession(t, "b1", "beta", 2)

	w := te.get(t, "/api/v1/session-folders")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list := decode[sessionFoldersBody](t, w)
	assert.Empty(t, list.Folders)
	assert.True(t, list.Editable)

	w = te.post(t, "/api/v1/session-folders", `{"project":"alpha","name":"  Docs "}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	docs := decode[sessionFolderBody](t, w)
	assert.Equal(t, "alpha", docs.Project)
	assert.Equal(t, "Docs", docs.Name)

	w = te.post(t, "/api/v1/session-folders", `{"project":"alpha","name":"Docs"}`)
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	w = te.post(t, "/api/v1/session-folders", `{"project":"alpha","name":"   "}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())

	docsPath := fmt.Sprintf("/api/v1/session-folders/%d", docs.ID)
	w = te.put(t, docsPath+"/sessions", `{"session_ids":["a1","a2","b1"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list = decode[sessionFoldersBody](t, w)
	require.Len(t, list.Folders, 1)
	assert.Equal(t, []string{"a1", "a2"}, list.Folders[0].SessionIDs,
		"the beta session stays out of an alpha folder")

	w = te.put(t, "/api/v1/session-folders/9999/sessions", `{"session_ids":["a1"]}`)
	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "session folder not found")

	w = te.patch(t, docsPath, `{"name":"Notes"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	renamed := decode[sessionFolderBody](t, w)
	assert.Equal(t, "Notes", renamed.Name)
	assert.Equal(t, []string{"a1", "a2"}, renamed.SessionIDs)

	w = te.post(t, "/api/v1/session-folders/remove-sessions", `{"session_ids":["a2"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list = decode[sessionFoldersBody](t, w)
	require.Len(t, list.Folders, 1)
	assert.Equal(t, []string{"a1"}, list.Folders[0].SessionIDs)

	w = te.del(t, docsPath)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	w = te.del(t, docsPath)
	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())

	w = te.get(t, "/api/v1/sessions/a1")
	require.Equal(t, http.StatusOK, w.Code, "deleting a folder keeps its sessions: %s", w.Body.String())
}

func TestSessionFolderNestingHandlers(t *testing.T) {
	te := setup(t)
	te.seedSession(t, "a1", "alpha", 2)

	w := te.post(t, "/api/v1/session-folders", `{"project":"alpha","name":"Work"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	work := decode[sessionFolderBody](t, w)
	assert.Nil(t, work.ParentID)
	w = te.post(t, "/api/v1/session-folders", `{"project":"beta","name":"Ops"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	ops := decode[sessionFolderBody](t, w)

	w = te.post(t, "/api/v1/session-folders",
		fmt.Sprintf(`{"project":"alpha","name":"Bugs","parent_id":%d}`, work.ID))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	bugs := decode[sessionFolderBody](t, w)
	require.NotNil(t, bugs.ParentID)
	assert.Equal(t, work.ID, *bugs.ParentID)
	w = te.post(t, "/api/v1/session-folders",
		fmt.Sprintf(`{"project":"alpha","name":"Other","parent_id":%d}`, ops.ID))
	require.Equal(t, http.StatusBadRequest, w.Code, "a parent from another project: %s", w.Body.String())

	workParent := fmt.Sprintf("/api/v1/session-folders/%d/parent", work.ID)
	bugsParent := fmt.Sprintf("/api/v1/session-folders/%d/parent", bugs.ID)
	w = te.put(t, workParent, fmt.Sprintf(`{"parent_id":%d}`, bugs.ID))
	require.Equal(t, http.StatusBadRequest, w.Code, "a folder cannot go into its subfolder: %s", w.Body.String())
	w = te.put(t, "/api/v1/session-folders/9999/parent", `{}`)
	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())

	w = te.put(t, bugsParent, `{}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Nil(t, decode[sessionFolderBody](t, w).ParentID, "no parent_id moves it to the top level")
	w = te.put(t, workParent, fmt.Sprintf(`{"parent_id":%d}`, bugs.ID))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	moved := decode[sessionFolderBody](t, w)
	require.NotNil(t, moved.ParentID)
	assert.Equal(t, bugs.ID, *moved.ParentID)

	// Deleting Bugs moves Work back to the top level.
	w = te.del(t, fmt.Sprintf("/api/v1/session-folders/%d", bugs.ID))
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
	w = te.get(t, "/api/v1/session-folders")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list := decode[sessionFoldersBody](t, w)
	require.Len(t, list.Folders, 2)
	for _, f := range list.Folders {
		assert.Nil(t, f.ParentID, "folder %s", f.Name)
	}
}

func TestSessionFolderWriteReturns503WhileWriterClosed(t *testing.T) {
	te := setup(t)
	require.NoError(t, te.db.CloseWriter())
	defer func() { assert.NoError(t, te.db.ReopenWriter()) }()

	w := te.post(t, "/api/v1/session-folders", `{"project":"alpha","name":"Docs"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
}

func TestSessionFoldersOnReplicaAreEmptyAndReadOnly(t *testing.T) {
	te := setupPGMode(t)
	te.seedSession(t, "a1", "alpha", 2)

	w := te.get(t, "/api/v1/session-folders")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	list := decode[sessionFoldersBody](t, w)
	assert.Empty(t, list.Folders)
	assert.False(t, list.Editable)

	w = te.post(t, "/api/v1/session-folders", `{"project":"alpha","name":"Docs"}`)
	require.Equal(t, http.StatusNotImplemented, w.Code, "body: %s", w.Body.String())
	assertErrorResponse(t, w, "not available in remote mode")
}

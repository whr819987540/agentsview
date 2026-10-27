package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"go.kenn.io/agentsview/internal/db"

	"github.com/danielgtaylor/huma/v2"
)

// Session folders group a project's sessions in the sidebar. They live in
// the local SQLite archive only; replicas list no folders and refuse edits.
func (s *Server) registerSessionFolderRoutes() {
	group := huma.NewGroup(s.api, "/api/v1/session-folders")
	configureRouteGroup(group, "SessionFolders")

	s.get(group, "", "List session folders", s.humaListSessionFolders)
	s.post(group, "", "Create session folder", s.humaCreateSessionFolder)
	s.patch(group, "/{id}", "Rename session folder", s.humaRenameSessionFolder)
	s.deleteRoute(group, "/{id}", "Delete session folder", s.humaDeleteSessionFolder)
	s.put(group, "/{id}/sessions", "Move sessions into a folder", s.humaMoveSessionsToFolder)
	s.post(group, "/remove-sessions",
		"Return sessions to their project's top level", s.humaRemoveSessionsFromFolders)
}

type sessionFoldersResponse struct {
	Folders []db.SessionFolder `json:"folders"`
	// Editable is false when the server reads a replica or a read-only
	// archive, so the sidebar hides folder editing.
	Editable bool `json:"editable"`
}

type sessionFolderCreateInput struct {
	Body struct {
		Project string `json:"project" required:"true" doc:"Project the folder belongs to"`
		Name    string `json:"name" required:"true" doc:"Folder name, unique within the project"`
	}
}

type sessionFolderRenameInput struct {
	ID   int64 `path:"id" required:"true" doc:"Folder ID"`
	Body struct {
		Name string `json:"name" required:"true" doc:"New folder name, unique within the project"`
	}
}

type sessionFolderPathInput struct {
	ID int64 `path:"id" required:"true" doc:"Folder ID"`
}

type sessionFolderMoveInput struct {
	ID   int64 `path:"id" required:"true" doc:"Folder ID"`
	Body struct {
		SessionIDs []string `json:"session_ids" required:"true" maxItems:"10000" doc:"Sessions to move; sessions of other projects are skipped"`
	}
}

type sessionFolderRemoveInput struct {
	Body struct {
		SessionIDs []string `json:"session_ids" required:"true" maxItems:"10000" doc:"Sessions to take out of their folders"`
	}
}

// writableLocalDB returns the local archive for edits that only SQLite
// stores, such as folders.
func (s *Server) writableLocalDB() (*db.DB, error) {
	localDB, ok := s.db.(*db.DB)
	if !ok || localDB == nil || localDB.ReadOnly() {
		return nil, apiError(http.StatusNotImplemented, "not available in remote mode")
	}
	return localDB, nil
}

func sessionFolderError(action string, err error) error {
	if handled := handleHumaReadOnly(err); handled != nil {
		return handled
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return apiError(http.StatusNotFound, "session folder not found")
	case errors.Is(err, db.ErrSessionFolderNameTaken):
		return apiError(http.StatusConflict, err.Error())
	case errors.Is(err, db.ErrSessionFolderInvalid):
		return apiError(http.StatusBadRequest, err.Error())
	default:
		return internalError(action, err)
	}
}

func (s *Server) sessionFolderList(
	ctx context.Context,
) (*jsonOutput[sessionFoldersResponse], error) {
	localDB, ok := s.db.(*db.DB)
	if !ok || localDB == nil {
		return &jsonOutput[sessionFoldersResponse]{
			Body: sessionFoldersResponse{Folders: []db.SessionFolder{}},
		}, nil
	}
	folders, err := localDB.ListSessionFolders(ctx)
	if err != nil {
		return nil, internalError("list session folders", err)
	}
	return &jsonOutput[sessionFoldersResponse]{
		Body: sessionFoldersResponse{
			Folders:  folders,
			Editable: !localDB.ReadOnly(),
		},
	}, nil
}

func (s *Server) humaListSessionFolders(
	ctx context.Context,
	_ *emptyInput,
) (*jsonOutput[sessionFoldersResponse], error) {
	return s.sessionFolderList(ctx)
}

func (s *Server) humaCreateSessionFolder(
	ctx context.Context,
	in *sessionFolderCreateInput,
) (*createdOutput[db.SessionFolder], error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	folder, err := localDB.CreateSessionFolder(ctx, in.Body.Project, in.Body.Name)
	if err != nil {
		return nil, sessionFolderError("create session folder", err)
	}
	return &createdOutput[db.SessionFolder]{Status: http.StatusCreated, Body: folder}, nil
}

func (s *Server) humaRenameSessionFolder(
	ctx context.Context,
	in *sessionFolderRenameInput,
) (*jsonOutput[db.SessionFolder], error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	folder, err := localDB.RenameSessionFolder(ctx, in.ID, in.Body.Name)
	if err != nil {
		return nil, sessionFolderError("rename session folder", err)
	}
	return &jsonOutput[db.SessionFolder]{Body: folder}, nil
}

func (s *Server) humaDeleteSessionFolder(
	ctx context.Context,
	in *sessionFolderPathInput,
) (*noContentOutput, error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	if err := localDB.DeleteSessionFolder(ctx, in.ID); err != nil {
		return nil, sessionFolderError("delete session folder", err)
	}
	return &noContentOutput{Status: http.StatusNoContent}, nil
}

func (s *Server) humaMoveSessionsToFolder(
	ctx context.Context,
	in *sessionFolderMoveInput,
) (*jsonOutput[sessionFoldersResponse], error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	if err := localDB.MoveSessionsToFolder(ctx, in.ID, in.Body.SessionIDs); err != nil {
		return nil, sessionFolderError("move sessions to folder", err)
	}
	return s.sessionFolderList(ctx)
}

func (s *Server) humaRemoveSessionsFromFolders(
	ctx context.Context,
	in *sessionFolderRemoveInput,
) (*jsonOutput[sessionFoldersResponse], error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	if err := localDB.RemoveSessionsFromFolders(ctx, in.Body.SessionIDs); err != nil {
		return nil, sessionFolderError("remove sessions from folders", err)
	}
	return s.sessionFolderList(ctx)
}

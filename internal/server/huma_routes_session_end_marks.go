package server

import (
	"context"
	"net/http"

	"go.kenn.io/agentsview/internal/db"

	"github.com/danielgtaylor/huma/v2"
)

// Session end marks record which sidebar rows the user marked as ended.
func (s *Server) registerSessionEndMarkRoutes() {
	group := huma.NewGroup(s.api, "/api/v1")
	configureRouteGroup(group, "SessionEndMarks")

	s.get(group, "/session-end-marks", "List session end marks", s.humaListSessionEndMarks)
	s.put(group, "/sessions/{id}/ended", "Mark session ended", s.humaMarkSessionEnded)
	s.deleteRoute(group, "/sessions/{id}/ended", "Unmark session ended", s.humaUnmarkSessionEnded)
}

type sessionEndMarksResponse struct {
	Marks []db.SessionEndMark `json:"marks"`
	// Editable is false when marks cannot change here, such as on a
	// read-only archive or a mirror, so the UI hides the mark actions.
	Editable bool `json:"editable"`
}

// endMarkStore returns the store's end marks, or false when the backend
// keeps none, such as hosted PostgreSQL.
func (s *Server) endMarkStore() (db.SessionEndMarkStore, bool) {
	store, ok := s.db.(db.SessionEndMarkStore)
	return store, ok
}

func (s *Server) humaListSessionEndMarks(
	ctx context.Context,
	_ *emptyInput,
) (*jsonOutput[sessionEndMarksResponse], error) {
	store, ok := s.endMarkStore()
	if !ok {
		return &jsonOutput[sessionEndMarksResponse]{
			Body: sessionEndMarksResponse{Marks: []db.SessionEndMark{}},
		}, nil
	}
	marks, err := store.ListSessionEndMarks(ctx)
	if err != nil {
		return nil, internalError("list session end marks", err)
	}
	if marks == nil {
		marks = []db.SessionEndMark{}
	}
	return &jsonOutput[sessionEndMarksResponse]{
		Body: sessionEndMarksResponse{
			Marks:    marks,
			Editable: store.SessionEndMarksEditable(ctx),
		},
	}, nil
}

func (s *Server) humaMarkSessionEnded(
	ctx context.Context,
	in *idPathInput,
) (*jsonOutput[db.SessionEndMark], error) {
	store, ok := s.endMarkStore()
	if !ok {
		return nil, handleHumaReadOnly(db.ErrReadOnly)
	}
	mark, ok, err := store.MarkSessionEnded(ctx, in.ID)
	if err != nil {
		if handled := handleHumaReadOnly(err); handled != nil {
			return nil, handled
		}
		return nil, internalError("mark session ended", err)
	}
	if !ok {
		return nil, apiError(http.StatusNotFound, "session not found")
	}
	return &jsonOutput[db.SessionEndMark]{Body: mark}, nil
}

func (s *Server) humaUnmarkSessionEnded(
	ctx context.Context,
	in *idPathInput,
) (*noContentOutput, error) {
	store, ok := s.endMarkStore()
	if !ok {
		return nil, handleHumaReadOnly(db.ErrReadOnly)
	}
	if err := store.UnmarkSessionEnded(ctx, in.ID); err != nil {
		if handled := handleHumaReadOnly(err); handled != nil {
			return nil, handled
		}
		return nil, internalError("unmark session ended", err)
	}
	return &noContentOutput{Status: http.StatusNoContent}, nil
}

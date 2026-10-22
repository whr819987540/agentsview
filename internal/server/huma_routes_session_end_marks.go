package server

import (
	"context"
	"net/http"

	"go.kenn.io/agentsview/internal/db"

	"github.com/danielgtaylor/huma/v2"
)

// Session end marks record which sessions the user marked as ended. Like
// folders they live in the local SQLite archive only; replicas list no marks
// and refuse edits.
func (s *Server) registerSessionEndMarkRoutes() {
	group := huma.NewGroup(s.api, "/api/v1")
	configureRouteGroup(group, "SessionEndMarks")

	s.get(group, "/session-end-marks", "List session end marks", s.humaListSessionEndMarks)
	s.put(group, "/sessions/{id}/ended", "Mark session ended", s.humaMarkSessionEnded)
	s.deleteRoute(group, "/sessions/{id}/ended", "Unmark session ended", s.humaUnmarkSessionEnded)
}

type sessionEndMarksResponse struct {
	Marks []db.SessionEndMark `json:"marks"`
	// Editable is false when the server reads a replica or a read-only
	// archive, so the UI hides the mark actions.
	Editable bool `json:"editable"`
}

type sessionEndMarkInput struct {
	ID   string `path:"id" required:"true" doc:"Session ID"`
	Body struct {
		ActivityAt string `json:"activity_at,omitempty" maxLength:"64" doc:"The session's last activity the user saw; the mark counts until the session has newer activity. Empty uses the session's own last activity"`
	}
}

func (s *Server) humaListSessionEndMarks(
	ctx context.Context,
	_ *emptyInput,
) (*jsonOutput[sessionEndMarksResponse], error) {
	localDB, ok := s.db.(*db.DB)
	if !ok || localDB == nil {
		return &jsonOutput[sessionEndMarksResponse]{
			Body: sessionEndMarksResponse{Marks: []db.SessionEndMark{}},
		}, nil
	}
	marks, err := localDB.ListSessionEndMarks(ctx)
	if err != nil {
		return nil, internalError("list session end marks", err)
	}
	return &jsonOutput[sessionEndMarksResponse]{
		Body: sessionEndMarksResponse{
			Marks:    marks,
			Editable: !localDB.ReadOnly(),
		},
	}, nil
}

func (s *Server) humaMarkSessionEnded(
	ctx context.Context,
	in *sessionEndMarkInput,
) (*noContentOutput, error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	ok, err := localDB.MarkSessionEnded(ctx, in.ID, in.Body.ActivityAt)
	if err != nil {
		if handled := handleHumaReadOnly(err); handled != nil {
			return nil, handled
		}
		return nil, internalError("mark session ended", err)
	}
	if !ok {
		return nil, apiError(http.StatusNotFound, "session not found")
	}
	return &noContentOutput{Status: http.StatusNoContent}, nil
}

func (s *Server) humaUnmarkSessionEnded(
	ctx context.Context,
	in *idPathInput,
) (*noContentOutput, error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	if err := localDB.UnmarkSessionEnded(ctx, in.ID); err != nil {
		if handled := handleHumaReadOnly(err); handled != nil {
			return nil, handled
		}
		return nil, internalError("unmark session ended", err)
	}
	return &noContentOutput{Status: http.StatusNoContent}, nil
}

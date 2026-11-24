package server

import (
	"context"
	"errors"
	"net/http"

	"go.kenn.io/agentsview/internal/db"

	"github.com/danielgtaylor/huma/v2"
)

// curationImportMaxBodyBytes bounds an uploaded curation export. One
// session entry takes a few hundred bytes, so this fits well over 100,000
// curated sessions.
const curationImportMaxBodyBytes = 64 << 20

// Curation transfer moves stars, end marks, and folders between local
// archives, such as from an old machine to a new one. Both routes need the
// local SQLite archive; replicas refuse them.
func (s *Server) registerCurationRoutes() {
	group := huma.NewGroup(s.api, "/api/v1/curation")
	configureRouteGroup(group, "Curation")

	s.get(group, "/export", "Export stars, end marks, and folders", s.humaExportCuration)
	registerRoute(group, http.MethodPost, "/import",
		"Import stars, end marks, and folders", s.humaImportCuration,
		s.humaTimeout(), maxBodyBytes(curationImportMaxBodyBytes))
}

type curationImportInput struct {
	DryRun bool `query:"dry_run" doc:"Report what the import would change without writing"`
	Body   db.CurationExport
}

func (s *Server) humaExportCuration(
	ctx context.Context, _ *emptyInput,
) (*jsonOutput[db.CurationExport], error) {
	localDB, ok := s.db.(*db.DB)
	if !ok || localDB == nil {
		return nil, apiError(http.StatusNotImplemented, "not available in remote mode")
	}
	doc, err := localDB.ExportCuration(ctx)
	if err != nil {
		return nil, internalError("export curation", err)
	}
	return &jsonOutput[db.CurationExport]{Body: doc}, nil
}

func (s *Server) humaImportCuration(
	ctx context.Context, in *curationImportInput,
) (*jsonOutput[db.CurationImportResult], error) {
	localDB, err := s.writableLocalDB()
	if err != nil {
		return nil, err
	}
	result, err := localDB.ImportCuration(ctx, in.Body,
		db.CurationImportOptions{DryRun: in.DryRun})
	if err != nil {
		if handled := handleHumaReadOnly(err); handled != nil {
			return nil, handled
		}
		if errors.Is(err, db.ErrCurationImportInvalid) {
			return nil, apiError(http.StatusBadRequest, err.Error())
		}
		return nil, internalError("import curation", err)
	}
	return &jsonOutput[db.CurationImportResult]{Body: result}, nil
}

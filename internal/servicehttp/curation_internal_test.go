package servicehttp

import (
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
)

func TestImportCurationSendsDocumentAndDryRun(t *testing.T) {
	tests := []struct {
		name       string
		dryRun     bool
		wantDryRun string
	}{
		{name: "import", wantDryRun: ""},
		{name: "dry run", dryRun: true, wantDryRun: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/api/v1/curation/import", r.URL.Path)
				assert.Equal(t, tt.wantDryRun, r.URL.Query().Get("dry_run"))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				var got db.CurationExport
				assert.NoError(t, json.Unmarshal(body, &got))
				assert.Equal(t, []db.CurationExportSession{{ID: "s1", Agent: "claude", Starred: true}}, got.Sessions)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"dry_run":true,"sessions":1,"matched":1,` +
					`"matched_by_prefix":0,"unmatched":[],"ambiguous":[],` +
					`"stars":{"added":1,"unchanged":0,"kept":0},` +
					`"end_marks":{"added":0,"unchanged":0,"kept":0},` +
					`"folder_members":{"added":0,"unchanged":0,"kept":0},"folders_created":0}`))
			}))
			t.Cleanup(srv.Close)

			result, err := NewHTTPBackend(srv.URL, "", false, "").ImportCuration(t.Context(),
				db.CurationExport{
					Format: db.CurationExportFormat, Version: db.CurationExportVersion,
					Sessions: []db.CurationExportSession{{ID: "s1", Agent: "claude", Starred: true}},
					Folders:  []db.CurationExportFolder{},
				},
				db.CurationImportOptions{DryRun: tt.dryRun},
			)
			require.NoError(t, err)
			assert.Equal(t, db.CurationImportCounts{Added: 1}, result.Stars)
			assert.Equal(t, 1, result.Matched)
		})
	}
}

func TestImportCurationOnReadOnlyDaemonSendsNothing(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(srv.Close)

	_, err := NewHTTPBackend(srv.URL, "", true, "").ImportCuration(t.Context(),
		db.CurationExport{Format: db.CurationExportFormat, Version: db.CurationExportVersion},
		db.CurationImportOptions{},
	)
	require.ErrorIs(t, err, db.ErrReadOnly)
	assert.Zero(t, requests.Load())
}

func TestCurationResponseErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantReadOnly bool
		wantContains string
	}{
		{name: "daemon without the routes", status: http.StatusNotFound, wantContains: "does not support curation transfer"},
		{name: "daemon serving a replica", status: http.StatusNotImplemented, wantReadOnly: true, wantContains: "does not serve the local archive"},
		{name: "invalid document", status: http.StatusBadRequest, wantContains: "version 2 is not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"error":"invalid curation export: version 2 is not supported"}`))
			}))
			t.Cleanup(srv.Close)
			backend := NewHTTPBackend(srv.URL, "", false, "")

			_, exportErr := backend.ExportCuration(t.Context())
			_, importErr := backend.ImportCuration(t.Context(),
				db.CurationExport{Format: db.CurationExportFormat, Version: db.CurationExportVersion},
				db.CurationImportOptions{},
			)
			for _, err := range []error{exportErr, importErr} {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantContains)
				assert.Equal(t, tt.wantReadOnly, errors.Is(err, db.ErrReadOnly))
			}
		})
	}
}

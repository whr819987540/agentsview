package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Curation transfer moves a user's curation, meaning stars, end marks, and
// session folders, from one archive to another, such as when sessions move
// to a new machine. The export names sessions by their IDs in the exporting
// archive; the import finds each one in its own archive and adds what is
// missing, keeping any curation the importing archive already holds.

const (
	// CurationExportFormat identifies a curation export document.
	CurationExportFormat = "agentsview-curation"
	// CurationExportVersion is the document version this build writes and
	// reads.
	CurationExportVersion = 1
)

// ErrCurationImportInvalid identifies a curation document the import
// rejects before writing anything.
var ErrCurationImportInvalid = errors.New("invalid curation export")

// CurationExport is the document `curation export` writes and `curation
// import` reads.
type CurationExport struct {
	Format     string `json:"format" doc:"Always agentsview-curation"`
	Version    int    `json:"version" doc:"Document version; this build reads version 1"`
	ExportedAt string `json:"exported_at,omitempty" doc:"When the export was made (RFC 3339)"`
	// ArtifactOrigin is the exporting archive's artifact origin. Artifact
	// sync gives sessions it imports the ID <origin>~<id>, so the importing
	// archive prefers that copy when several prefixed copies exist.
	ArtifactOrigin string                  `json:"artifact_origin,omitempty" doc:"Artifact origin of the exporting archive, when it has one"`
	Sessions       []CurationExportSession `json:"sessions" doc:"Sessions with a star, an end mark in effect, or a folder"`
	Folders        []CurationExportFolder  `json:"folders" doc:"Every folder, so empty folders come along too"`
}

// CurationExportSession holds one session's curation.
type CurationExportSession struct {
	ID        string           `json:"id" doc:"Session ID in the exporting archive"`
	Agent     string           `json:"agent,omitempty" doc:"Session agent; the import only matches sessions of the same agent"`
	Project   string           `json:"project,omitempty" doc:"Session project in the exporting archive"`
	Starred   bool             `json:"starred,omitempty" doc:"Whether the session is starred"`
	StarredAt string           `json:"starred_at,omitempty" doc:"When the session was starred (RFC 3339)"`
	EndMark   *CurationEndMark `json:"end_mark,omitempty" doc:"The session's end mark, when one is in effect"`
	Folder    string           `json:"folder,omitempty" doc:"Name of the session's folder within its project"`
}

// CurationEndMark is an exported end mark. ActivityAt keeps the exporting
// archive's value, so the mark stops counting once the importing archive
// holds newer activity for the sessions it covers.
type CurationEndMark struct {
	Kind       string `json:"kind" enum:"row,branch" doc:"Which sessions the mark covers"`
	ActivityAt string `json:"activity_at" doc:"Latest activity of the covered sessions when the mark was set"`
}

// CurationExportFolder names one folder.
type CurationExportFolder struct {
	Project string `json:"project" doc:"Project the folder belongs to"`
	Name    string `json:"name" doc:"Folder name"`
}

// CurationImportOptions controls ImportCuration.
type CurationImportOptions struct {
	// DryRun reports what the import would change without writing.
	DryRun bool
}

// CurationImportCounts reports how one kind of curation was applied.
type CurationImportCounts struct {
	Added     int `json:"added" doc:"Applied to a session that had none"`
	Unchanged int `json:"unchanged" doc:"This archive already held the same value"`
	Kept      int `json:"kept" doc:"This archive held a different value, which the import kept"`
}

// CurationImportResult reports what ImportCuration matched and changed.
type CurationImportResult struct {
	DryRun          bool                 `json:"dry_run"`
	Sessions        int                  `json:"sessions" doc:"Session entries in the export"`
	Matched         int                  `json:"matched" doc:"Entries matched to a session in this archive"`
	MatchedByPrefix int                  `json:"matched_by_prefix" doc:"Matched entries whose session here carries a machine or origin prefix"`
	Unmatched       []string             `json:"unmatched" doc:"Exported session IDs with no session in this archive"`
	Ambiguous       []string             `json:"ambiguous" doc:"Exported session IDs that match more than one session here"`
	Stars           CurationImportCounts `json:"stars"`
	EndMarks        CurationImportCounts `json:"end_marks"`
	FolderMembers   CurationImportCounts `json:"folder_members"`
	FoldersCreated  int                  `json:"folders_created" doc:"Folders the import created"`
}

// errCurationDryRun rolls back a dry-run import after it has counted the
// changes.
var errCurationDryRun = errors.New("curation import dry run")

// ExportCuration returns the archive's stars, end marks still in effect, and
// session folders. A folder membership whose session has since moved to
// another project is left out, since the sidebar no longer shows it.
func (db *DB) ExportCuration(ctx context.Context) (CurationExport, error) {
	doc := CurationExport{
		Format:     CurationExportFormat,
		Version:    CurationExportVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Sessions:   []CurationExportSession{},
		Folders:    []CurationExportFolder{},
	}
	origin, err := db.GetSyncState(ctx, "artifact_origin_id")
	if err != nil {
		return CurationExport{}, fmt.Errorf("reading artifact origin: %w", err)
	}
	doc.ArtifactOrigin = origin

	marks, err := db.ListSessionEndMarks(ctx)
	if err != nil {
		return CurationExport{}, err
	}
	markBySession := make(map[string]SessionEndMark, len(marks))
	for _, mark := range marks {
		markBySession[mark.SessionID] = mark
	}

	r := db.getReader()
	rows, err := r.QueryContext(ctx, `
		WITH curated(id) AS (
			SELECT session_id FROM starred_sessions
			UNION SELECT session_id FROM session_end_marks
			UNION SELECT session_id FROM session_folder_members
		)
		SELECT s.id, s.agent, s.project,
			ss.session_id IS NOT NULL, COALESCE(ss.created_at, ''),
			COALESCE(f.name, '')
		FROM curated c
		JOIN sessions s ON s.id = c.id
		LEFT JOIN starred_sessions ss ON ss.session_id = s.id
		LEFT JOIN session_folder_members fm ON fm.session_id = s.id
		LEFT JOIN session_folders f
			ON f.id = fm.folder_id AND f.project = s.project
		ORDER BY s.id`)
	if err != nil {
		return CurationExport{}, fmt.Errorf("listing curated sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry CurationExportSession
		if err := rows.Scan(
			&entry.ID, &entry.Agent, &entry.Project,
			&entry.Starred, &entry.StarredAt, &entry.Folder,
		); err != nil {
			return CurationExport{}, fmt.Errorf("scanning curated session: %w", err)
		}
		if mark, ok := markBySession[entry.ID]; ok {
			entry.EndMark = &CurationEndMark{Kind: mark.Kind, ActivityAt: mark.ActivityAt}
		}
		if !entry.Starred && entry.EndMark == nil && entry.Folder == "" {
			// Only an outdated end mark or a hidden folder membership.
			continue
		}
		doc.Sessions = append(doc.Sessions, entry)
	}
	if err := rows.Err(); err != nil {
		return CurationExport{}, fmt.Errorf("listing curated sessions: %w", err)
	}

	folders, err := listSessionFolderRows(ctx, r)
	if err != nil {
		return CurationExport{}, err
	}
	for _, f := range folders {
		doc.Folders = append(doc.Folders, CurationExportFolder{
			Project: f.Project, Name: f.Name,
		})
	}
	return doc, nil
}

// ValidateCurationExport checks a curation document before an import uses
// it. Folder names come back normalized.
func ValidateCurationExport(doc *CurationExport) error {
	if doc.Format != CurationExportFormat {
		return fmt.Errorf("%w: format is %q, want %q",
			ErrCurationImportInvalid, doc.Format, CurationExportFormat)
	}
	if doc.Version != CurationExportVersion {
		return fmt.Errorf("%w: version %d is not supported; this build reads version %d",
			ErrCurationImportInvalid, doc.Version, CurationExportVersion)
	}
	for i := range doc.Sessions {
		entry := &doc.Sessions[i]
		if strings.TrimSpace(entry.ID) == "" {
			return fmt.Errorf("%w: session %d has no id", ErrCurationImportInvalid, i)
		}
		if entry.EndMark != nil {
			if !ValidEndMarkKind(entry.EndMark.Kind) {
				return fmt.Errorf("%w: session %s has end mark kind %q",
					ErrCurationImportInvalid, entry.ID, entry.EndMark.Kind)
			}
			if strings.TrimSpace(entry.EndMark.ActivityAt) == "" {
				return fmt.Errorf("%w: session %s has an end mark without activity_at",
					ErrCurationImportInvalid, entry.ID)
			}
		}
		if entry.Folder != "" {
			name, err := NormalizeSessionFolderName(entry.Folder)
			if err != nil {
				return fmt.Errorf("%w: session %s: %w", ErrCurationImportInvalid, entry.ID, err)
			}
			entry.Folder = name
		}
	}
	for i := range doc.Folders {
		folder := &doc.Folders[i]
		if strings.TrimSpace(folder.Project) == "" {
			return fmt.Errorf("%w: folder %d has no project", ErrCurationImportInvalid, i)
		}
		name, err := NormalizeSessionFolderName(folder.Name)
		if err != nil {
			return fmt.Errorf("%w: folder %d: %w", ErrCurationImportInvalid, i, err)
		}
		folder.Name = name
	}
	return nil
}

// ImportCuration applies an exported curation to the sessions of this
// archive. Each exported session matches the session with the same ID, or
// failing that the one session whose ID ends in "~" plus that ID, which is
// how artifact sync and remote sync name the copies they import. A match
// must have the same agent. The import adds what a matched session lacks and
// keeps every star, end mark, and folder this archive already holds. A
// folder is recreated in the project the matched session has here. An empty
// exported folder is recreated only when this archive has sessions in its
// project. The import commits as one transaction; a dry run rolls it back.
func (db *DB) ImportCuration(
	ctx context.Context, doc CurationExport, opts CurationImportOptions,
) (CurationImportResult, error) {
	if err := db.requireWritable(); err != nil {
		return CurationImportResult{}, err
	}
	if err := ValidateCurationExport(&doc); err != nil {
		return CurationImportResult{}, err
	}
	result := CurationImportResult{
		DryRun:    opts.DryRun,
		Sessions:  len(doc.Sessions),
		Unmatched: []string{},
		Ambiguous: []string{},
	}
	err := db.Update(ctx, func(tx *sql.Tx) error {
		imp := curationImporter{
			ctx: ctx, tx: tx, result: &result,
			folders: map[curationFolderKey]int64{},
		}
		matcher, err := loadCurationMatcher(ctx, tx, doc.ArtifactOrigin)
		if err != nil {
			return err
		}
		withMembers := map[curationFolderKey]bool{}
		for _, entry := range doc.Sessions {
			if entry.Folder != "" {
				withMembers[curationFolderKey{entry.Project, entry.Folder}] = true
			}
			target, outcome := matcher.match(entry.ID, entry.Agent)
			switch outcome {
			case curationUnmatched:
				result.Unmatched = append(result.Unmatched, entry.ID)
				continue
			case curationAmbiguous:
				result.Ambiguous = append(result.Ambiguous, entry.ID)
				continue
			case curationPrefixed:
				result.MatchedByPrefix++
			case curationExact:
			}
			result.Matched++
			if err := imp.apply(entry, target); err != nil {
				return err
			}
		}
		for _, folder := range doc.Folders {
			key := curationFolderKey{folder.Project, folder.Name}
			if withMembers[key] || !matcher.projects[folder.Project] {
				continue
			}
			if _, err := imp.folderID(key); err != nil {
				return err
			}
		}
		if opts.DryRun {
			return errCurationDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errCurationDryRun) {
		return CurationImportResult{}, err
	}
	return result, nil
}

// curationTarget is a session of the importing archive.
type curationTarget struct {
	id, agent, project string
}

type curationMatchOutcome int

const (
	curationUnmatched curationMatchOutcome = iota
	curationExact
	curationPrefixed
	curationAmbiguous
)

// curationMatcher finds the importing archive's copy of an exported session.
type curationMatcher struct {
	byID map[string]curationTarget
	// bySuffix maps the text after each "~" in a session ID to the sessions
	// whose ID ends that way.
	bySuffix map[string][]curationTarget
	projects map[string]bool
	origin   string
}

func loadCurationMatcher(
	ctx context.Context, tx *sql.Tx, origin string,
) (*curationMatcher, error) {
	m := &curationMatcher{
		byID:     map[string]curationTarget{},
		bySuffix: map[string][]curationTarget{},
		projects: map[string]bool{},
		origin:   origin,
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, agent, project FROM sessions`)
	if err != nil {
		return nil, fmt.Errorf("loading sessions for curation import: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t curationTarget
		if err := rows.Scan(&t.id, &t.agent, &t.project); err != nil {
			return nil, fmt.Errorf("scanning session for curation import: %w", err)
		}
		m.byID[t.id] = t
		m.projects[t.project] = true
		for i := range len(t.id) {
			if t.id[i] == '~' && i+1 < len(t.id) {
				suffix := t.id[i+1:]
				m.bySuffix[suffix] = append(m.bySuffix[suffix], t)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading sessions for curation import: %w", err)
	}
	return m, nil
}

func (m *curationMatcher) match(id, agent string) (curationTarget, curationMatchOutcome) {
	sameAgent := func(t curationTarget) bool { return agent == "" || t.agent == agent }
	if t, ok := m.byID[id]; ok && sameAgent(t) {
		return t, curationExact
	}
	var candidates []curationTarget
	for _, t := range m.bySuffix[id] {
		if sameAgent(t) {
			candidates = append(candidates, t)
		}
	}
	if m.origin != "" {
		for _, t := range candidates {
			if t.id == m.origin+"~"+id {
				return t, curationPrefixed
			}
		}
	}
	switch len(candidates) {
	case 0:
		return curationTarget{}, curationUnmatched
	case 1:
		return candidates[0], curationPrefixed
	default:
		return curationTarget{}, curationAmbiguous
	}
}

type curationFolderKey struct {
	project, name string
}

// curationImporter applies matched entries inside the import transaction.
type curationImporter struct {
	ctx     context.Context
	tx      *sql.Tx
	result  *CurationImportResult
	folders map[curationFolderKey]int64
}

func (imp *curationImporter) apply(
	entry CurationExportSession, target curationTarget,
) error {
	if entry.Starred {
		if err := imp.star(target.id, entry.StarredAt); err != nil {
			return err
		}
	}
	if entry.EndMark != nil {
		if err := imp.endMark(target.id, *entry.EndMark); err != nil {
			return err
		}
	}
	if entry.Folder != "" && target.project != "" {
		if err := imp.folderMember(target, entry.Folder); err != nil {
			return err
		}
	}
	return nil
}

// star stars a session, keeping the export's star time when it parses so
// the starred list keeps its order.
func (imp *curationImporter) star(sessionID, starredAt string) error {
	createdAt := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339Nano, starredAt); err == nil {
		createdAt = t.UTC()
	}
	res, err := imp.tx.ExecContext(imp.ctx, `
		INSERT OR IGNORE INTO starred_sessions (session_id, created_at)
		VALUES (?, ?)`,
		sessionID, createdAt.Format("2006-01-02T15:04:05.000Z"),
	)
	if err != nil {
		return fmt.Errorf("starring session %s: %w", sessionID, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		imp.result.Stars.Added++
	} else {
		imp.result.Stars.Unchanged++
	}
	return nil
}

func (imp *curationImporter) endMark(sessionID string, mark CurationEndMark) error {
	var kind, activityAt string
	err := imp.tx.QueryRowContext(imp.ctx,
		`SELECT kind, activity_at FROM session_end_marks WHERE session_id = ?`,
		sessionID,
	).Scan(&kind, &activityAt)
	switch {
	case err == nil:
		if kind == mark.Kind && activityAt == mark.ActivityAt {
			imp.result.EndMarks.Unchanged++
		} else {
			imp.result.EndMarks.Kept++
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("reading end mark of session %s: %w", sessionID, err)
	}
	if _, err := imp.tx.ExecContext(imp.ctx, `
		INSERT INTO session_end_marks (session_id, activity_at, kind)
		VALUES (?, ?, ?)`,
		sessionID, mark.ActivityAt, mark.Kind,
	); err != nil {
		return fmt.Errorf("marking session %s ended: %w", sessionID, err)
	}
	imp.result.EndMarks.Added++
	return nil
}

// folderMember puts a session into the named folder of its project here.
// A session already in a folder of its project stays there. A membership in
// another project's folder does not show in the sidebar, so the import
// replaces it.
func (imp *curationImporter) folderMember(target curationTarget, name string) error {
	var project, current string
	err := imp.tx.QueryRowContext(imp.ctx, `
		SELECT f.project, f.name
		FROM session_folder_members m
		JOIN session_folders f ON f.id = m.folder_id
		WHERE m.session_id = ?`, target.id,
	).Scan(&project, &current)
	switch {
	case err == nil && project == target.project:
		if current == name {
			imp.result.FolderMembers.Unchanged++
		} else {
			imp.result.FolderMembers.Kept++
		}
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("reading folder of session %s: %w", target.id, err)
	}
	folderID, err := imp.folderID(curationFolderKey{target.project, name})
	if err != nil {
		return err
	}
	if _, err := imp.tx.ExecContext(imp.ctx, `
		INSERT INTO session_folder_members (session_id, folder_id)
		VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			folder_id = excluded.folder_id,
			created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		target.id, folderID,
	); err != nil {
		return fmt.Errorf("moving session %s to folder %q: %w", target.id, name, err)
	}
	imp.result.FolderMembers.Added++
	return nil
}

// folderID returns the folder with the given project and name, creating it
// when this archive has none.
func (imp *curationImporter) folderID(key curationFolderKey) (int64, error) {
	if id, ok := imp.folders[key]; ok {
		return id, nil
	}
	var id int64
	err := imp.tx.QueryRowContext(imp.ctx,
		`SELECT id FROM session_folders WHERE project = ? AND name = ?`,
		key.project, key.name,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, insertErr := imp.tx.ExecContext(imp.ctx,
			`INSERT INTO session_folders (project, name) VALUES (?, ?)`,
			key.project, key.name,
		)
		if insertErr != nil {
			return 0, fmt.Errorf("creating folder %q in %s: %w", key.name, key.project, insertErr)
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, fmt.Errorf("reading created folder id: %w", err)
		}
		imp.result.FoldersCreated++
	} else if err != nil {
		return 0, fmt.Errorf("finding folder %q in %s: %w", key.name, key.project, err)
	}
	imp.folders[key] = id
	return id, nil
}

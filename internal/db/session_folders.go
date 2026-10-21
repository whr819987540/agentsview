package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSessionFolderNameRunes bounds a folder name so it fits a sidebar row.
const MaxSessionFolderNameRunes = 120

var (
	// ErrSessionFolderInvalid identifies invalid folder input.
	ErrSessionFolderInvalid = errors.New("invalid session folder")
	// ErrSessionFolderNameTaken reports that the project already has a
	// folder with the requested name.
	ErrSessionFolderNameTaken = errors.New("a folder with this name already exists in the project")
)

// SessionFolder is a user-made sidebar folder within one project. Folders
// change only how the sidebar lists sessions; sessions keep their own
// project and storage.
type SessionFolder struct {
	ID         int64    `json:"id"`
	Project    string   `json:"project"`
	Name       string   `json:"name"`
	SessionIDs []string `json:"session_ids"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

// NormalizeSessionFolderName trims a folder name and rejects empty, overlong,
// or control-character names.
func NormalizeSessionFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", ErrSessionFolderInvalid)
	}
	if utf8.RuneCountInString(name) > MaxSessionFolderNameRunes {
		return "", fmt.Errorf(
			"%w: name is longer than %d characters",
			ErrSessionFolderInvalid, MaxSessionFolderNameRunes,
		)
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("%w: name contains control characters", ErrSessionFolderInvalid)
	}
	return name, nil
}

// ListSessionFolders returns every folder with its member session IDs,
// ordered by project and name.
func (db *DB) ListSessionFolders(ctx context.Context) ([]SessionFolder, error) {
	r := db.getReader()
	folders, err := listSessionFolderRows(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return []SessionFolder{}, nil
	}
	byID := make(map[int64]int, len(folders))
	for i, f := range folders {
		byID[f.ID] = i
	}

	members, err := r.QueryContext(ctx, `
		SELECT folder_id, session_id
		FROM session_folder_members
		ORDER BY folder_id, session_id`)
	if err != nil {
		return nil, fmt.Errorf("listing session folder members: %w", err)
	}
	defer members.Close()
	for members.Next() {
		var folderID int64
		var sessionID string
		if err := members.Scan(&folderID, &sessionID); err != nil {
			return nil, fmt.Errorf("scanning session folder member: %w", err)
		}
		if i, ok := byID[folderID]; ok {
			folders[i].SessionIDs = append(folders[i].SessionIDs, sessionID)
		}
	}
	if err := members.Err(); err != nil {
		return nil, fmt.Errorf("listing session folder members: %w", err)
	}
	return folders, nil
}

func listSessionFolderRows(
	ctx context.Context, r *readerHandle,
) ([]SessionFolder, error) {
	rows, err := r.QueryContext(ctx, `
		SELECT id, project, name, created_at, updated_at
		FROM session_folders
		ORDER BY project, name, id`)
	if err != nil {
		return nil, fmt.Errorf("listing session folders: %w", err)
	}
	defer rows.Close()
	var folders []SessionFolder
	for rows.Next() {
		f := SessionFolder{SessionIDs: []string{}}
		if err := rows.Scan(
			&f.ID, &f.Project, &f.Name, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning session folder: %w", err)
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing session folders: %w", err)
	}
	return folders, nil
}

// CreateSessionFolder adds an empty folder to a project. It returns
// ErrSessionFolderNameTaken when the project already has that name.
func (db *DB) CreateSessionFolder(
	ctx context.Context, project, name string,
) (SessionFolder, error) {
	if err := db.requireWritable(); err != nil {
		return SessionFolder{}, err
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return SessionFolder{}, fmt.Errorf("%w: project is required", ErrSessionFolderInvalid)
	}
	name, err := NormalizeSessionFolderName(name)
	if err != nil {
		return SessionFolder{}, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	w := db.getWriter()
	res, err := w.Exec(ctx,
		`INSERT INTO session_folders (project, name) VALUES (?, ?)`,
		project, name,
	)
	if err != nil {
		if isSQLiteUniqueConstraint(err) {
			return SessionFolder{}, ErrSessionFolderNameTaken
		}
		return SessionFolder{}, fmt.Errorf("creating session folder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SessionFolder{}, fmt.Errorf("reading session folder id: %w", err)
	}
	return loadSessionFolder(ctx, w, id)
}

// RenameSessionFolder changes a folder's name. It returns sql.ErrNoRows when
// the folder does not exist and ErrSessionFolderNameTaken when another
// folder in the project already has the name.
func (db *DB) RenameSessionFolder(
	ctx context.Context, id int64, name string,
) (SessionFolder, error) {
	if err := db.requireWritable(); err != nil {
		return SessionFolder{}, err
	}
	name, err := NormalizeSessionFolderName(name)
	if err != nil {
		return SessionFolder{}, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	w := db.getWriter()
	res, err := w.Exec(ctx, `
		UPDATE session_folders
		SET name = ?,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`, name, id,
	)
	if err != nil {
		if isSQLiteUniqueConstraint(err) {
			return SessionFolder{}, ErrSessionFolderNameTaken
		}
		return SessionFolder{}, fmt.Errorf("renaming session folder %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SessionFolder{}, sql.ErrNoRows
	}
	return loadSessionFolder(ctx, w, id)
}

// DeleteSessionFolder removes a folder. Its sessions return to the project's
// top level; the sessions themselves are untouched. It returns sql.ErrNoRows
// when the folder does not exist.
func (db *DB) DeleteSessionFolder(ctx context.Context, id int64) error {
	if err := db.requireWritable(); err != nil {
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.getWriter().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning session folder delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete members explicitly so the result does not depend on the
	// connection's foreign_keys setting.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM session_folder_members WHERE folder_id = ?`, id,
	); err != nil {
		return fmt.Errorf("clearing session folder %d: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM session_folders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting session folder %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// MoveSessionsToFolder puts sessions into a folder, taking them out of any
// other folder. Sessions that do not exist or belong to another project are
// skipped. It returns sql.ErrNoRows when the folder does not exist.
func (db *DB) MoveSessionsToFolder(
	ctx context.Context, folderID int64, sessionIDs []string,
) error {
	if err := db.requireWritable(); err != nil {
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.getWriter().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning session folder move: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var project string
	if err := tx.QueryRowContext(ctx,
		`SELECT project FROM session_folders WHERE id = ?`, folderID,
	).Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return fmt.Errorf("loading session folder %d: %w", folderID, err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO session_folder_members (session_id, folder_id)
		SELECT id, ? FROM sessions WHERE id = ? AND project = ?
		ON CONFLICT(session_id) DO UPDATE SET
			folder_id = excluded.folder_id,
			created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`)
	if err != nil {
		return fmt.Errorf("preparing session folder move: %w", err)
	}
	defer stmt.Close()
	for _, sessionID := range sessionIDs {
		if _, err := stmt.ExecContext(ctx, folderID, sessionID, project); err != nil {
			return fmt.Errorf("moving session %s to folder %d: %w", sessionID, folderID, err)
		}
	}
	return tx.Commit()
}

// RemoveSessionsFromFolders returns sessions to their project's top level.
func (db *DB) RemoveSessionsFromFolders(
	ctx context.Context, sessionIDs []string,
) error {
	if err := db.requireWritable(); err != nil {
		return err
	}
	if len(sessionIDs) == 0 {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.getWriter().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning session folder removal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`DELETE FROM session_folder_members WHERE session_id = ?`)
	if err != nil {
		return fmt.Errorf("preparing session folder removal: %w", err)
	}
	defer stmt.Close()
	for _, sessionID := range sessionIDs {
		if _, err := stmt.ExecContext(ctx, sessionID); err != nil {
			return fmt.Errorf("removing session %s from its folder: %w", sessionID, err)
		}
	}
	return tx.Commit()
}

func loadSessionFolder(
	ctx context.Context, w *writerHandle, id int64,
) (SessionFolder, error) {
	var f SessionFolder
	if err := w.QueryRow(ctx, `
		SELECT id, project, name, created_at, updated_at
		FROM session_folders WHERE id = ?`, id,
	).Scan(&f.ID, &f.Project, &f.Name, &f.CreatedAt, &f.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionFolder{}, err
		}
		return SessionFolder{}, fmt.Errorf("loading session folder %d: %w", id, err)
	}
	rows, err := w.Query(ctx, `
		SELECT session_id FROM session_folder_members
		WHERE folder_id = ? ORDER BY session_id`, id,
	)
	if err != nil {
		return SessionFolder{}, fmt.Errorf("loading session folder %d members: %w", id, err)
	}
	defer rows.Close()
	f.SessionIDs = []string{}
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return SessionFolder{}, fmt.Errorf("scanning session folder %d member: %w", id, err)
		}
		f.SessionIDs = append(f.SessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		return SessionFolder{}, fmt.Errorf("loading session folder %d members: %w", id, err)
	}
	return f, nil
}

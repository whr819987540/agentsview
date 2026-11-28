package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// archiveCompanionSuffixes name the files SQLite keeps beside an archive in
// WAL mode. A copy of the archive needs them to include committed pages
// that have not been checkpointed into the main file yet.
var archiveCompanionSuffixes = []string{"-wal", "-shm"}

// ArchiveDataVersion reads the parser data version recorded in the archive
// at path without changing it. exists is false when there is no archive.
func ArchiveDataVersion(ctx context.Context, path string) (version int, exists bool, err error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("checking archive: %w", err)
	}
	conn, err := sql.Open(sqliteArchiveDriverName, makeDSN(path, true))
	if err != nil {
		return 0, true, fmt.Errorf("opening archive: %w", err)
	}
	defer conn.Close()
	version, err = readUserVersion(ctx, conn)
	return version, true, err
}

// PrepareArchiveForDataVersion lets a build whose parser data version is
// target open the archive at path. Such a build refuses an archive written
// at a newer data version, so that archive is marked one version below
// target: the build then treats it as stale and runs its non-destructive
// full resync on its next start, which re-parses every session with its
// own parser and keeps sessions whose source files are gone. An archive at
// or below target is left alone; a build already re-parses older archives
// by itself. It returns the version the archive had and whether it changed.
//
// The caller must hold the archive's write lock with no daemon running, so
// no writer can stamp a version in between.
func PrepareArchiveForDataVersion(ctx context.Context,
	path string, target int,
) (previous int, changed bool, err error) {
	if target < 1 {
		return 0, false, fmt.Errorf("invalid target data version %d", target)
	}
	conn, err := sql.Open(sqliteArchiveDriverName, makeDSN(path, false))
	if err != nil {
		return 0, false, fmt.Errorf("opening archive: %w", err)
	}
	defer conn.Close()
	c, err := conn.Conn(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("opening archive: %w", err)
	}
	defer c.Close()

	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, false, fmt.Errorf("locking archive: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&previous); err != nil {
		return 0, false, fmt.Errorf("reading archive data version: %w", err)
	}
	if previous <= target {
		return previous, false, nil
	}
	if _, err := c.ExecContext(ctx,
		fmt.Sprintf("PRAGMA user_version = %d", target-1),
	); err != nil {
		return previous, false, fmt.Errorf("marking archive for re-parse: %w", err)
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return previous, false, fmt.Errorf("marking archive for re-parse: %w", err)
	}
	committed = true
	return previous, true, nil
}

// BackupArchive copies the archive at path, with its WAL companion files,
// into dir, which must not exist yet. It returns the files it wrote. The
// caller must hold the archive's write lock with no daemon running, so the
// copied files are consistent with each other.
func BackupArchive(path, dir string) ([]string, error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, fmt.Errorf("creating backup parent: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating backup directory: %w", err)
	}
	sources := []string{path}
	for _, suffix := range archiveCompanionSuffixes {
		if _, err := os.Stat(path + suffix); err == nil {
			sources = append(sources, path+suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("checking %s: %w", path+suffix, err)
		}
	}
	written := make([]string, 0, len(sources))
	for _, src := range sources {
		dst := filepath.Join(dir, filepath.Base(src))
		if err := copyArchiveFile(src, dst); err != nil {
			return written, err
		}
		written = append(written, dst)
	}
	return written, nil
}

func copyArchiveFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backing up %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("backing up %s: %w", src, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("backing up %s: %w", src, err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return fmt.Errorf("backing up %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("backing up %s: %w", src, err)
	}
	return nil
}

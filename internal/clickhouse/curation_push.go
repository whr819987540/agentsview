package clickhouse

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"time"

	"go.kenn.io/agentsview/internal/db"
)

// curationTables hold per-session curation rows that only the curation
// refresh writes; deleting a session clears them too.
var curationTables = []string{"starred_sessions", "session_end_marks"}

// curationSnapshot is the local in-scope star, pin and end mark state.
type curationSnapshot struct {
	starred       []string
	pinsBySession map[string][]db.PinnedMessage
	endMarks      []db.SessionEndMark
}

func (s *Sync) loadCurationSnapshot(ctx context.Context) (curationSnapshot, error) {
	starred, err := s.local.ListStarredSessionIDsForScope(ctx, s.projects, s.excludeProjects)
	if err != nil {
		return curationSnapshot{}, fmt.Errorf("loading starred sessions: %w", err)
	}
	pinnedSessions, err := s.local.ListPinnedSessionIDsForScope(ctx, s.projects, s.excludeProjects)
	if err != nil {
		return curationSnapshot{}, fmt.Errorf("loading pinned session ids: %w", err)
	}
	pinsBySession, err := s.local.PinnedMessagesBySession(ctx, pinnedSessions)
	if err != nil {
		return curationSnapshot{}, fmt.Errorf("loading pinned messages: %w", err)
	}
	// Only marks still in effect: the archive drops outdated ones here.
	endMarks, err := s.local.ListSessionEndMarksForScope(ctx, s.projects, s.excludeProjects)
	if err != nil {
		return curationSnapshot{}, fmt.Errorf("loading session end marks: %w", err)
	}
	return curationSnapshot{starred: starred, pinsBySession: pinsBySession, endMarks: endMarks}, nil
}

// fingerprint hashes starred ids and pin id/note state so a note-only edit
// still refreshes. It matches the DuckDB mirror's payload shape.
func (snap curationSnapshot) fingerprint() (string, error) {
	pinned := make([]db.PinCurationEntry, 0, len(snap.pinsBySession))
	for _, pins := range snap.pinsBySession {
		for _, p := range pins {
			entry := db.PinCurationEntry{ID: p.ID, MessageID: p.MessageID, CreatedAt: p.CreatedAt}
			if p.Note != nil {
				entry.Note = *p.Note
				entry.HasNote = true
			}
			pinned = append(pinned, entry)
		}
	}
	slices.SortFunc(pinned, func(a, b db.PinCurationEntry) int {
		if c := cmp.Compare(a.MessageID, b.MessageID); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	// EndMarks is left out when empty, so a mirror without marks keeps
	// the fingerprint it had before marks were mirrored.
	payload := struct {
		Starred  []string
		Pinned   []db.PinCurationEntry
		EndMarks []db.SessionEndMark `json:",omitempty"`
	}{Starred: db.SortedCopy(snap.starred), Pinned: pinned, EndMarks: snap.endMarks}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encoding curation fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// refreshCurationIfChanged rewrites starred_sessions, pinned_messages and
// session_end_marks for this archive when the local in-scope curation state differs from
// what the last refresh wrote. The stored fingerprint covers only the rows
// written for sessions that were mirror-resident, so a star or pin for a
// session that has not landed yet keeps the state mismatched and the next
// push retries until it converges.
func (s *Sync) refreshCurationIfChanged(ctx context.Context, version uint64) error {
	snap, err := s.loadCurationSnapshot(ctx)
	if err != nil {
		return err
	}
	fingerprint, err := snap.fingerprint()
	if err != nil {
		return err
	}
	key := s.archiveKey(curationFingerprintKeyBase)
	stored, err := readMetadata(ctx, s.conn, key)
	if err != nil {
		return err
	}
	if stored[key] == fingerprint {
		return nil
	}
	written, err := s.replaceCuration(ctx, snap, version)
	if err != nil {
		return err
	}
	return writeMetadataVersion(ctx, s.conn, map[string]string{key: written}, version)
}

func (s *Sync) replaceCuration(
	ctx context.Context, snap curationSnapshot, version uint64,
) (string, error) {
	pinnedSessions := make([]string, 0, len(snap.pinsBySession))
	for id := range snap.pinsBySession {
		pinnedSessions = append(pinnedSessions, id)
	}
	candidates := append(append([]string(nil), snap.starred...), pinnedSessions...)
	for _, mark := range snap.endMarks {
		candidates = append(candidates, mark.SessionID)
	}
	resident, err := s.residentSessionIDs(ctx, candidates)
	if err != nil {
		return "", err
	}
	written := curationSnapshot{pinsBySession: map[string][]db.PinnedMessage{}}
	var starRows, pinRows, endMarkRows [][]any
	now := time.Now().UTC()
	for _, id := range db.SortedCopy(snap.starred) {
		if !resident[id] {
			continue
		}
		written.starred = append(written.starred, id)
		starRows = append(starRows, []any{id, &now, version})
	}
	for _, id := range db.SortedCopy(pinnedSessions) {
		if !resident[id] {
			continue
		}
		written.pinsBySession[id] = snap.pinsBySession[id]
		for _, pin := range snap.pinsBySession[id] {
			pinRows = append(pinRows, pinnedMessageRow(pin, version))
		}
	}
	for _, mark := range snap.endMarks {
		if !resident[mark.SessionID] {
			continue
		}
		written.endMarks = append(written.endMarks, mark)
		endMarkRows = append(endMarkRows, []any{mark.SessionID, mark.ActivityAt, version})
	}
	fingerprint, err := written.fingerprint()
	if err != nil {
		return "", err
	}
	if err := insertRows(ctx, s.conn, "starred_sessions", starRows); err != nil {
		return "", err
	}
	if err := insertRows(ctx, s.conn, "pinned_messages", pinRows); err != nil {
		return "", err
	}
	if err := insertRows(ctx, s.conn, "session_end_marks", endMarkRows); err != nil {
		return "", err
	}
	for _, table := range append([]string{"pinned_messages"}, curationTables...) {
		if _, err := s.conn.ExecContext(ctx,
			"DELETE FROM "+table+" WHERE push_version < ? AND session_id IN "+
				"(SELECT id FROM sessions WHERE source_archive_id = ?)",
			version, s.archiveID); err != nil {
			return "", fmt.Errorf("deleting older clickhouse %s rows: %w", table, err)
		}
	}
	return fingerprint, nil
}

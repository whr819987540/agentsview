package parser

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// codexReplayBoundary anchors the instant a forked Codex rollout was
// created. A fork replays the parent's history at the top of its own
// file, so telling replayed turns from the fork's own turns needs a
// boundary. The main parse gets one by reading the parent transcript
// and matching turn ids (see codexForkGate); a caller holding only the
// fork's path cannot do that, so this boundary derives the instant from
// the fork's own metadata and compares it against each turn's embedded
// UUIDv7 timestamp. It fails closed: any turn id that carries no
// timestamp makes the boundary unusable rather than guessed.
type codexReplayBoundary struct {
	createdMs int64
}

// codexReplayBoundaryFromMeta anchors the boundary from a forked
// session_meta: the fork's UUIDv7 id, its payload timestamp, or the
// JSONL envelope timestamp, in that order. ok is false for a
// non-forked meta or when no anchor is available.
func codexReplayBoundaryFromMeta(
	payload gjson.Result, envelopeTS time.Time,
) (codexReplayBoundary, bool) {
	if strings.TrimSpace(payload.Get("forked_from_id").Str) == "" {
		return codexReplayBoundary{}, false
	}
	ms := uuidV7Millis(payload.Get("id").Str)
	if ms == 0 {
		if t := parseTimestamp(payload.Get("timestamp").Str); !t.IsZero() {
			ms = t.UnixMilli()
		}
	}
	if ms == 0 && !envelopeTS.IsZero() {
		ms = envelopeTS.UnixMilli()
	}
	if ms == 0 {
		return codexReplayBoundary{}, false
	}
	return codexReplayBoundary{createdMs: ms}, true
}

// replayedTurnContext reports whether a turn_context record belongs to
// the replayed parent prefix. ok is false when the turn id carries no
// UUIDv7 timestamp, which leaves the boundary undecidable.
func (b codexReplayBoundary) replayedTurnContext(
	payload gjson.Result,
) (replayed bool, ok bool) {
	tid := payload.Get("turn_id").Str
	if tid == "" {
		return true, true // pre-turn_id parent history
	}
	ms := uuidV7Millis(tid)
	if ms == 0 {
		return false, false
	}
	return ms < b.createdMs, true
}

// uuidV7Millis extracts the millisecond timestamp embedded in a
// UUIDv7, returning 0 for anything that is not a v7 UUID.
func uuidV7Millis(id string) int64 {
	hex := strings.ReplaceAll(id, "-", "")
	if len(hex) != 32 || hex[12] != '7' {
		return 0
	}
	ms, err := strconv.ParseInt(hex[:12], 16, 64)
	if err != nil {
		return 0
	}
	return ms
}

// CodexForkReplayMessages returns the parent-history messages replayed
// at the top of a forked Codex rollout, as the fork itself recorded
// them. Callers use it to trim the inherited context shown above a
// fork boundary to exactly the prefix the fork replayed.
//
// The bool is false when the file is not a forked Codex rollout or when
// the replay/genuine boundary cannot be identified safely; callers then
// fall back to showing the parent's full stored history.
func CodexForkReplayMessages(
	path string,
) ([]ParsedMessage, bool, error) {
	ctx := context.Background()
	sink := NewCodexCollectingSink(0)
	// A nil parent-turn resolver keeps the builder's own fork gate
	// disarmed: this scan feeds it only replayed records and wants
	// them collected, not suppressed.
	b := newCodexSessionBuilder(ctx, false, nil, sink)

	// The replayed prefix carries the parent's rollbacks with it, so
	// the same pre-scan the full parse uses decides which replayed
	// records the user had already rewound past.
	rolledBackLines, _, err := codexRolledBackLines(path)
	if err != nil {
		return nil, false, err
	}

	var (
		boundary    codexReplayBoundary
		sawForkMeta bool
		usable      = true
		done        bool
		ordinal     int
	)

	_, err = readJSONLFrom(path, 0, func(line string) {
		current := ordinal
		ordinal++
		if done || !usable {
			return
		}
		if _, rolledBack := rolledBackLines[current]; rolledBack {
			return
		}
		lineType := gjson.Get(line, "type").Str
		payload := gjson.Get(line, "payload")
		ts := parseTimestamp(gjson.Get(line, "timestamp").Str)

		if lineType == codexTypeSessionMeta {
			if sawForkMeta {
				// The copied parent meta inside the replay prefix.
				return
			}
			anchored, ok := codexReplayBoundaryFromMeta(payload, ts)
			if !ok {
				usable = false
				return
			}
			boundary = anchored
			sawForkMeta = true
			return
		}

		if !sawForkMeta {
			// Content before the fork's own meta: not a fork file.
			done = true
			return
		}

		switch lineType {
		case codexTypeTurnContext:
			replayed, ok := boundary.replayedTurnContext(payload)
			if !ok {
				usable = false
				return
			}
			if !replayed {
				done = true
				return
			}
			b.model = payload.Get("model").Str
		case codexTypeResponseItem:
			b.handleResponseItem(ctx, payload, ts)
		}
	})
	if err != nil {
		return nil, false, err
	}
	if !sawForkMeta || !usable {
		return nil, false, nil
	}
	sink.Finalize()
	return sink.Messages(), true, nil
}

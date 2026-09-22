package parser

import (
	"github.com/tidwall/gjson"
)

// codexThreadRolledBackEvent is the event Codex writes when the user
// rewinds a thread: it drops the last num_turns user turns, and
// everything those turns produced, from the visible thread. The records
// stay in the rollout file, so a decode that replays the file verbatim
// would resurrect turns the user removed.
const codexThreadRolledBackEvent = "thread_rolled_back"

// codexRolledBackLines pre-scans a rollout and reports which records
// thread_rolled_back events removed from the visible thread. Ordinals
// are counted over valid JSON lines only, which is how every decoder
// walks the file, so the returned set can be applied by line position
// during the decode.
//
// The scan exists because the decoder's sink is append-only: it cannot
// un-emit a message once a later line turns out to have cancelled it.
// Resolving rollbacks up front keeps the streaming decode single-pass
// and makes the suppression identical for the collecting sink.
//
// The second return value reports whether the file contains any
// rollback at all, which callers use to refuse a resume cursor: a
// rolled-back file must be re-read from the start, never appended to.
func codexRolledBackLines(path string) (map[int]struct{}, bool, error) {
	var (
		ordinal   int
		userTurns []int // line ordinal of each still-live user turn
		rolled    = make(map[int]struct{})
		sawEvent  bool
	)

	_, err := readJSONLFrom(path, 0, func(line string) {
		current := ordinal
		ordinal++
		if _, dead := rolled[current]; dead {
			return
		}

		payload := gjson.Get(line, "payload")
		switch gjson.Get(line, "type").Str {
		case codexTypeResponseItem:
			if payload.Get("role").Str == "user" {
				userTurns = append(userTurns, current)
			}
		case codexTypeEventMsg:
			if payload.Get("type").Str != codexThreadRolledBackEvent {
				break
			}
			sawEvent = true
			n := int(payload.Get("num_turns").Int())
			if n <= 0 {
				// The event itself is never part of the thread.
				rolled[current] = struct{}{}
				return
			}
			if n > len(userTurns) {
				n = len(userTurns)
			}
			// Drop the last n user turns: everything from the
			// n-th most recent turn's first record through this
			// event leaves the visible thread.
			from := current
			if n > 0 {
				from = userTurns[len(userTurns)-n]
				userTurns = userTurns[:len(userTurns)-n]
			}
			for i := from; i <= current; i++ {
				rolled[i] = struct{}{}
			}
			return
		}
	})
	if err != nil {
		return nil, false, err
	}
	if !sawEvent {
		return nil, false, nil
	}
	return rolled, true, nil
}

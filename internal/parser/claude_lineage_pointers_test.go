package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures below mirror Claude Code 2.1.332 transcripts. /fork spawns a
// bg session that replays the forking session's chain and opens with a
// <fork-source> task notification, while the forking session records the
// /fork command, its stdout naming the child's id prefix, and a meta notice.
// A background handoff writes a continued-in record into the session it
// leaves. The original writer interleaves its own uuid-less records (API
// request logs, last-prompt, titles) with its chain entries; a replay copy
// never does.

const (
	ptrOrigID   = "a1a1a1a1-0000-4000-8000-000000000001"
	ptrFork1ID  = "f1f1f1f1-0000-4000-8000-000000000002"
	ptrContID   = "b2b2b2b2-0000-4000-8000-000000000003"
	ptrFork2ID  = "f3f3f3f3-0000-4000-8000-000000000004"
	ptrSessName = "Problem review"
)

func ptrUser(uuid, parent, ts, sid, kind, content string) string {
	return lineageUserLine(uuid, parent, ts, sid, kind, content)
}

func ptrAssistant(uuid, parent, ts, sid, kind, msgID, text string) string {
	return lineageAssistantLine(uuid, parent, ts, sid, kind, msgID, text, 5)
}

func ptrKind(kind string) string {
	if kind == "" {
		return ""
	}
	return fmt.Sprintf(`"sessionKind":%q,`, kind)
}

// ptrUUIDLess is an operational record the session writes for itself, such as
// last-prompt or an API request log. It carries no uuid.
func ptrUUIDLess(recordType, ts, sid string) string {
	return fmt.Sprintf(`{"type":%q,"timestamp":%q,"sessionId":%q}`, recordType, ts, sid)
}

// ptrForkCommand returns the three records /fork appends to the forking
// session: the command, its stdout naming the child, and a meta notice.
func ptrForkCommand(cmdUUID, stdoutUUID, metaUUID, parent, ts, sid, kind, childID string) []string {
	return []string{
		fmt.Sprintf(
			`{"parentUuid":%q,"type":"system","subtype":"local_command","content":"<command-name>/fork</command-name>\n            <command-message>fork</command-message>\n            <command-args></command-args>","level":"info","timestamp":%q,"uuid":%q,"isMeta":false,%s"sessionId":%q}`,
			parent, ts, cmdUUID, ptrKind(kind), sid,
		),
		fmt.Sprintf(
			`{"parentUuid":%q,"type":"system","subtype":"local_command","content":"<local-command-stdout>session waiting for a prompt · %s · %s</local-command-stdout>","level":"info","timestamp":%q,"uuid":%q,"isMeta":false,"commandRun":{"command":"fork","args":""},%s"sessionId":%q}`,
			cmdUUID, ptrSessName, childID[:8], ts, stdoutUUID, ptrKind(kind), sid,
		),
		fmt.Sprintf(
			`{"parentUuid":%q,"type":"user","message":{"role":"user","content":"The fork runs as its own separate session."},"isMeta":true,"uuid":%q,"timestamp":%q,%s"sessionId":%q}`,
			stdoutUUID, metaUUID, ts, ptrKind(kind), sid,
		),
	}
}

// ptrForkSource returns the queue record and the task notification a /fork
// child opens with.
func ptrForkSource(uuid, parent, ts, sid string) []string {
	body := "<task-notification>\n<fork-source>\nThis session began as a fork (copy) of another session that is still running.\n</fork-source>\n</task-notification>"
	return []string{
		fmt.Sprintf(
			`{"type":"queue-operation","operation":"enqueue","timestamp":%q,"sessionId":%q,"content":%q}`,
			ts, sid, body,
		),
		fmt.Sprintf(
			`{"parentUuid":%q,"type":"user","message":{"role":"user","content":%q},"uuid":%q,"timestamp":%q,"origin":{"kind":"task-notification"},"promptSource":"system","sessionKind":"bg","sessionId":%q}`,
			parent, body, uuid, ts, sid,
		),
	}
}

func ptrContinuedIn(ts, sid, childID string) string {
	return fmt.Sprintf(
		`{"type":"continued-in","timestamp":%q,"sessionId":%q,"continuedInSessionId":%q}`,
		ts, sid, childID,
	)
}

// ptrTurnEnd is the turn_duration record written after each reply. The next
// prompt parents to it, and so does a prompt sent after a rewind to that turn.
func ptrTurnEnd(uuid, parent, ts, sid, kind string) string {
	return fmt.Sprintf(
		`{"parentUuid":%q,"type":"system","subtype":"turn_duration","durationMs":1000,"timestamp":%q,"uuid":%q,"isMeta":false,%s"sessionId":%q}`,
		parent, ts, uuid, ptrKind(kind), sid,
	)
}

// ptrAttachment is an attachment record that carries a uuid, such as the
// session_cron_carry record a handoff writes before its first prompt.
func ptrAttachment(uuid, parent, ts, sid, kind, attachmentType string) string {
	return fmt.Sprintf(
		`{"parentUuid":%q,"type":"attachment","attachment":{"type":%q},"timestamp":%q,"uuid":%q,%s"sessionId":%q}`,
		parent, attachmentType, ts, uuid, ptrKind(kind), sid,
	)
}

func joinLines(groups ...[]string) string {
	var all []string
	for _, g := range groups {
		all = append(all, g...)
	}
	return strings.Join(all, "\n") + "\n"
}

// ptrFamily writes the session family from a real report:
//
//	orig    interactive, two turns, then /fork and a handoff
//	fork1   /fork of orig after its second turn, never used
//	cont    the handoff of orig, rewound to the first turn, one new turn,
//	        then /fork again
//	fork2   /fork of cont after its new turn, one turn of its own
//
// cont shares only the first turn with orig but every line up to its /fork
// with fork2, so content overlap alone points cont at its own child.
func ptrFamily(t *testing.T) map[string]string {
	t.Helper()
	return ptrFamilyWith(t, ptrFamilyOpts{
		continuedIn: true, forkCommand: true, interleave: true,
	})
}

// ptrFamilyOpts selects which lineage signals the family's transcripts
// carry, so a test can leave a single one.
type ptrFamilyOpts struct {
	// continuedIn writes orig's continued-in record naming cont.
	continuedIn bool
	// forkCommand writes the /fork command records into cont.
	forkCommand bool
	// interleave writes cont's own uuid-less records between its turns.
	interleave bool
	// trailingTurn adds a turn cont wrote after fork2 left it.
	trailingTurn bool
}

func ptrFamilyWith(t *testing.T, opts ptrFamilyOpts) map[string]string {
	t.Helper()
	const (
		o  = ptrOrigID
		f1 = ptrFork1ID
		c  = ptrContID
		f2 = ptrFork2ID
	)
	orig := joinLines(
		[]string{
			ptrUser("u1", "", "2026-11-25T01:50:00Z", o, "", "first question"),
			ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", o, "", "msg_1", "first answer"),
			ptrUUIDLess("last-prompt", "", o),
			ptrUser("u2", "a1", "2026-11-25T02:00:00Z", o, "", "second question"),
			ptrUUIDLess("api-request", "2026-11-25T02:00:01Z", o),
			ptrAssistant("a2", "u2", "2026-11-25T02:50:00Z", o, "", "msg_2", "second answer"),
		},
		ptrForkCommand("oc1", "oc2", "oc3", "a2", "2026-11-25T02:53:12Z", o, "", f1),
	)
	if opts.continuedIn {
		orig += ptrContinuedIn("2026-11-25T02:53:20Z", o, c) + "\n"
	}
	fork1 := joinLines(
		[]string{
			ptrUser("u1", "", "2026-11-25T01:50:00Z", f1, "bg", "first question"),
			ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", f1, "bg", "msg_1", "first answer"),
			ptrUser("u2", "a1", "2026-11-25T02:00:00Z", f1, "bg", "second question"),
			ptrAssistant("a2", "u2", "2026-11-25T02:50:00Z", f1, "bg", "msg_2", "second answer"),
		},
		ptrForkSource("fs1", "a2", "2026-11-25T02:53:13Z", f1),
	)
	contHead := []string{
		ptrUser("u1", "", "2026-11-25T01:50:00Z", c, "bg", "first question"),
		ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", c, "bg", "msg_1", "first answer"),
	}
	contOwn := []string{
		ptrUser("u3", "a1", "2026-11-25T03:04:00Z", c, "bg", "third question"),
		ptrAssistant("a3", "u3", "2026-11-25T03:34:00Z", c, "bg", "msg_3", "third answer"),
	}
	var ownRecords [][]string
	if opts.interleave {
		ownRecords = [][]string{
			{ptrUUIDLess("last-prompt", "", c)},
			{ptrUUIDLess("api-request", "2026-11-25T03:04:01Z", c)},
		}
	} else {
		ownRecords = [][]string{nil, nil}
	}
	cont := joinLines(
		contHead, ownRecords[0], contOwn[:1], ownRecords[1], contOwn[1:],
	)
	if opts.forkCommand {
		cont += joinLines(ptrForkCommand(
			"cc1", "cc2", "cc3", "a3", "2026-11-25T03:35:02Z", c, "bg", f2,
		))
	}
	if opts.trailingTurn {
		cont += joinLines([]string{
			ptrUser("u5", "a3", "2026-11-25T04:00:00Z", c, "bg", "fifth question"),
			ptrAssistant("a5", "u5", "2026-11-25T04:01:00Z", c, "bg", "msg_5", "fifth answer"),
		})
	}
	fork2 := joinLines(
		[]string{
			ptrUser("u1", "", "2026-11-25T01:50:00Z", f2, "bg", "first question"),
			ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", f2, "bg", "msg_1", "first answer"),
			ptrUser("u3", "a1", "2026-11-25T03:04:00Z", f2, "bg", "third question"),
			ptrAssistant("a3", "u3", "2026-11-25T03:34:00Z", f2, "bg", "msg_3", "third answer"),
		},
		ptrForkSource("fs2", "a3", "2026-11-25T03:35:05Z", f2),
		[]string{
			ptrUser("u4", "fs2", "2026-11-25T03:36:00Z", f2, "bg", "fourth question"),
			ptrAssistant("a4", "u4", "2026-11-25T03:40:00Z", f2, "bg", "msg_4", "fourth answer"),
		},
	)

	dir := t.TempDir()
	paths := map[string]string{}
	for id, content := range map[string]string{o: orig, f1: fork1, c: cont, f2: fork2} {
		path := filepath.Join(dir, id+".jsonl")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		paths[id] = path
	}
	return paths
}

// userContents returns the prompts among msgs, skipping system rows and
// the records that only carry tool results.
func userContents(msgs []ParsedMessage) []string {
	var out []string
	for _, m := range msgs {
		if m.Role != RoleUser || m.IsSystem {
			continue
		}
		if m.Content == "" && len(m.ToolResults) > 0 {
			continue
		}
		out = append(out, m.Content)
	}
	return out
}

func TestClaudeForkFamilyLinksEachSessionToItsSource(t *testing.T) {
	t.Parallel()
	paths := ptrFamily(t)

	tests := []struct {
		name         string
		id           string
		wantParent   string
		wantRel      RelationshipType
		wantForkAt   string
		wantPrompts  []string
		wantMessages int
	}{
		{
			name:         "handoff rewound to an earlier turn keeps its own turn",
			id:           ptrContID,
			wantParent:   ptrOrigID,
			wantRel:      RelContinuation,
			wantForkAt:   "a1",
			wantPrompts:  []string{"third question"},
			wantMessages: 2,
		},
		{
			name:         "fork of the handoff",
			id:           ptrFork2ID,
			wantParent:   ptrContID,
			wantRel:      RelFork,
			wantForkAt:   "a3",
			wantPrompts:  []string{"fourth question"},
			wantMessages: 3,
		},
		{
			name:         "unused fork keeps only its fork notice",
			id:           ptrFork1ID,
			wantParent:   ptrOrigID,
			wantRel:      RelFork,
			wantForkAt:   "a2",
			wantPrompts:  nil,
			wantMessages: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results, excluded, err := claudeParseFile(
				paths[tt.id], "my_app", "local",
				claudeParseOptions{siblingLineage: true},
			)
			require.NoError(t, err)
			assert.Empty(t, excluded)
			require.Len(t, results, 1)
			sess := results[0].Session
			assert.Equal(t, tt.wantParent, sess.ParentSessionID)
			assert.Equal(t, tt.wantRel, sess.RelationshipType)
			require.NotNil(t, sess.ForkPointUUID)
			assert.Equal(t, tt.wantForkAt, *sess.ForkPointUUID)
			assert.Equal(t, tt.wantPrompts, userContents(results[0].Messages))
			assert.Len(t, results[0].Messages, tt.wantMessages)
		})
	}
}

func TestClaudeForkFamilyOriginalHasNoParent(t *testing.T) {
	t.Parallel()
	paths := ptrFamily(t)
	results, _, err := claudeParseFile(
		paths[ptrOrigID], "my_app", "local",
		claudeParseOptions{siblingLineage: true},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	sess := results[0].Session
	assert.Empty(t, sess.ParentSessionID)
	assert.Equal(t, RelNone, sess.RelationshipType)
	require.NotNil(t, sess.ForkPointUUID)
	assert.Empty(t, *sess.ForkPointUUID)
	assert.Equal(t,
		[]string{"first question", "second question"},
		userContents(results[0].Messages),
	)
}

// Each lineage signal alone keeps a handoff that was later forked from
// being trimmed against its own fork. Without them the fork, which
// replays nearly all of the handoff, wins the overlap comparison.
func TestClaudeHandoffResolvesFromEachSignalAlone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		opts        ptrFamilyOpts
		wantPrompts []string
	}{
		{
			name:        "continued-in record in the parent",
			opts:        ptrFamilyOpts{continuedIn: true, trailingTurn: true},
			wantPrompts: []string{"third question", "fifth question"},
		},
		{
			name:        "fork command naming the child",
			opts:        ptrFamilyOpts{forkCommand: true},
			wantPrompts: []string{"third question"},
		},
		{
			name:        "own uuid-less records between turns",
			opts:        ptrFamilyOpts{interleave: true, trailingTurn: true},
			wantPrompts: []string{"third question", "fifth question"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			paths := ptrFamilyWith(t, tt.opts)

			results, excluded, err := claudeParseFile(
				paths[ptrContID], "my_app", "local",
				claudeParseOptions{siblingLineage: true},
			)
			require.NoError(t, err)
			assert.Empty(t, excluded)
			require.Len(t, results, 1)
			assert.Equal(t, ptrOrigID, results[0].Session.ParentSessionID)
			assert.Equal(t, tt.wantPrompts, userContents(results[0].Messages))

			forkResults, _, err := claudeParseFile(
				paths[ptrFork2ID], "my_app", "local",
				claudeParseOptions{siblingLineage: true},
			)
			require.NoError(t, err)
			require.Len(t, forkResults, 1)
			assert.Equal(t, ptrContID, forkResults[0].Session.ParentSessionID)
			assert.Equal(t, RelFork, forkResults[0].Session.RelationshipType)
		})
	}
}

const (
	rwOrigID     = "c1c1c1c1-0000-4000-8000-000000000011"
	rwForkID     = "c2c2c2c2-0000-4000-8000-000000000012"
	rwForkForkID = "c3c3c3c3-0000-4000-8000-000000000013"
)

// rwHistory is the three turns orig wrote, as orig wrote them or as a fork
// copied them.
func rwHistory(sid, kind string) []string {
	return []string{
		ptrUser("u1", "", "2026-11-25T01:50:00Z", sid, kind, "first question"),
		ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", sid, kind, "msg_1", "first answer"),
		ptrTurnEnd("s1", "a1", "2026-11-25T01:51:01Z", sid, kind),
		ptrUser("u2", "s1", "2026-11-25T02:00:00Z", sid, kind, "second question"),
		ptrAssistant("a2", "u2", "2026-11-25T02:10:00Z", sid, kind, "msg_2", "second answer"),
		ptrTurnEnd("s2", "a2", "2026-11-25T02:10:01Z", sid, kind),
		ptrUser("u3", "s2", "2026-11-25T02:20:00Z", sid, kind, "third question"),
		ptrAssistant("a3", "u3", "2026-11-25T02:30:00Z", sid, kind, "msg_3", "third answer"),
		ptrTurnEnd("s3", "a3", "2026-11-25T02:30:01Z", sid, kind),
	}
}

// rwForkTurn is the turn fork wrote before it was forked again, as fork wrote
// it or as forkfork copied it.
func rwForkTurn(sid string) []string {
	return []string{
		ptrUser("u4", "fs1", "2026-11-26T02:24:00Z", sid, "bg", "abandoned question"),
		ptrAssistant("a4", "u4", "2026-11-26T02:27:00Z", sid, "bg", "msg_4", "abandoned answer"),
		ptrTurnEnd("s4", "a4", "2026-11-26T02:27:01Z", sid, "bg"),
	}
}

// ptrRewoundForkFamily writes the family from a real report:
//
//	orig      interactive, three turns, then /fork
//	fork      /fork of orig after its third turn: one turn of its own, then
//	          /fork again, then a rewind to the end of orig's second turn and
//	          two new turns
//	forkfork  /fork of fork before the rewind, never used
//
// The rewind lands in the history fork copied from orig, so two branches of
// fork's own hang off that copy: the one its fork notice starts and the one
// the rewind starts.
func ptrRewoundForkFamily(t *testing.T) map[string]string {
	t.Helper()
	const (
		o  = rwOrigID
		f  = rwForkID
		ff = rwForkForkID
	)
	orig := joinLines(
		rwHistory(o, ""),
		ptrForkCommand("oc1", "oc2", "oc3", "s3", "2026-11-25T02:31:00Z", o, "", f),
	)
	// A fork's queued notice lands before the copied history, and the
	// notice itself right after it.
	notice := ptrForkSource("fs1", "s3", "2026-11-25T02:31:01Z", f)
	fork := joinLines(
		notice[:1],
		rwHistory(f, "bg"),
		notice[1:],
		[]string{ptrUUIDLess("last-prompt", "", f)},
		rwForkTurn(f),
		ptrForkCommand("fc1", "fc2", "fc3", "s4", "2026-11-26T02:28:28Z", f, "bg", ff),
		[]string{
			ptrUUIDLess("file-history-snapshot", "", f),
			ptrUser("u5", "s2", "2026-11-26T02:28:40Z", f, "bg", "rewound question"),
			ptrAssistant("a5", "u5", "2026-11-26T02:32:00Z", f, "bg", "msg_5", "rewound answer"),
			ptrTurnEnd("s5", "a5", "2026-11-26T02:32:01Z", f, "bg"),
			ptrUser("u6", "s5", "2026-11-26T02:40:00Z", f, "bg", "follow-up question"),
			ptrAssistant("a6", "u6", "2026-11-26T02:41:00Z", f, "bg", "msg_6", "follow-up answer"),
		},
	)
	ffNotice := ptrForkSource("fs2", "s4", "2026-11-26T02:28:29Z", ff)
	forkFork := joinLines(
		ffNotice[:1],
		rwHistory(ff, "bg"),
		ptrForkSource("fs1", "s3", "2026-11-25T02:31:01Z", ff)[1:],
		rwForkTurn(ff),
		ffNotice[1:],
		[]string{ptrUUIDLess("last-prompt", "", ff)},
	)

	dir := t.TempDir()
	paths := map[string]string{}
	for id, content := range map[string]string{o: orig, f: fork, ff: forkFork} {
		path := filepath.Join(dir, id+".jsonl")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		paths[id] = path
	}
	return paths
}

func TestClaudeForkRewoundIntoCopiedHistory(t *testing.T) {
	t.Parallel()
	paths := ptrRewoundForkFamily(t)

	tests := []struct {
		name         string
		id           string
		wantParent   string
		wantForkAt   string
		wantPrompts  []string
		wantMessages int
		wantRewound  []ParsedRewoundInput
	}{
		{
			// The rewind kept orig's first two turns, so fork inherits up
			// to the end of the second. The turn it abandoned is its own
			// rewound input; orig's third turn stays orig's.
			name:         "fork rewound into the copied history",
			id:           rwForkID,
			wantParent:   rwOrigID,
			wantForkAt:   "a2",
			wantPrompts:  []string{"rewound question", "follow-up question"},
			wantMessages: 4,
			wantRewound: []ParsedRewoundInput{{
				SourceUUID: "u4",
				Timestamp:  time.Date(2026, 11, 26, 2, 24, 0, 0, time.UTC),
				Content:    "abandoned question",
			}},
		},
		{
			name:         "fork of the fork made before the rewind",
			id:           rwForkForkID,
			wantParent:   rwForkID,
			wantForkAt:   "a4",
			wantPrompts:  nil,
			wantMessages: 1,
			wantRewound:  []ParsedRewoundInput{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results, excluded, err := claudeParseFile(
				paths[tt.id], "my_app", "local",
				claudeParseOptions{siblingLineage: true},
			)
			require.NoError(t, err)
			assert.Empty(t, excluded)
			require.Len(t, results, 1)
			sess := results[0].Session
			assert.Equal(t, tt.wantParent, sess.ParentSessionID)
			assert.Equal(t, RelFork, sess.RelationshipType)
			require.NotNil(t, sess.ForkPointUUID)
			assert.Equal(t, tt.wantForkAt, *sess.ForkPointUUID)
			assert.Equal(t, tt.wantPrompts, userContents(results[0].Messages))
			assert.Len(t, results[0].Messages, tt.wantMessages)
			assert.Equal(t, tt.wantRewound, sess.RewoundInputs)
		})
	}
}

func TestClaudeForkRewoundIntoCopiedHistoryLeavesOriginalAlone(t *testing.T) {
	t.Parallel()
	paths := ptrRewoundForkFamily(t)
	results, _, err := claudeParseFile(
		paths[rwOrigID], "my_app", "local",
		claudeParseOptions{siblingLineage: true},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Empty(t, results[0].Session.ParentSessionID)
	assert.Equal(t,
		[]string{"first question", "second question", "third question"},
		userContents(results[0].Messages),
	)
}

// A handoff writes a session_cron_carry attachment before its first prompt,
// so its own records reach the copied history only through that attachment.
// The session must still be read as a branching conversation: here a rewind
// replaced a prompt, and one reply ran two tool calls in parallel.
func TestClaudeHandoffOpeningWithAttachmentKeepsBranches(t *testing.T) {
	t.Parallel()
	const (
		o = ptrOrigID
		c = ptrContID
	)
	orig := joinLines([]string{
		ptrUser("u1", "", "2026-11-25T01:50:00Z", o, "", "first question"),
		ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", o, "", "msg_1", "first answer"),
		ptrTurnEnd("s1", "a1", "2026-11-25T01:51:01Z", o, ""),
		ptrUUIDLess("last-prompt", "", o),
		ptrContinuedIn("2026-11-25T02:53:20Z", o, c),
	})
	cont := joinLines([]string{
		ptrUser("u1", "", "2026-11-25T01:50:00Z", c, "bg", "first question"),
		ptrAssistant("a1", "u1", "2026-11-25T01:51:00Z", c, "bg", "msg_1", "first answer"),
		ptrTurnEnd("s1", "a1", "2026-11-25T01:51:01Z", c, "bg"),
		ptrUUIDLess("last-prompt", "", c),
		ptrAttachment("cc1", "s1", "2026-11-25T03:02:48Z", c, "bg", "session_cron_carry"),
		ptrUser("u2", "cc1", "2026-11-25T03:04:00Z", c, "bg", "second question"),
		claudeToolUseChunkLine("2026-11-25T03:04:10Z", "t1", "u2", "msg_2", "tu1"),
		claudeToolUseChunkLine("2026-11-25T03:04:11Z", "t2", "t1", "msg_2", "tu2"),
		claudeToolResultUserLine("2026-11-25T03:04:12Z", "r2", "t2", "tu2"),
		claudeToolResultUserLine("2026-11-25T03:04:13Z", "r1", "t1", "tu1"),
		ptrAssistant("a2", "r1", "2026-11-25T03:05:00Z", c, "bg", "msg_3", "second answer"),
		ptrTurnEnd("s2", "a2", "2026-11-25T03:05:01Z", c, "bg"),
		ptrUser("u3", "s2", "2026-11-25T03:10:00Z", c, "bg", "abandoned question"),
		ptrAssistant("a3", "u3", "2026-11-25T03:11:00Z", c, "bg", "msg_4", "abandoned answer"),
		ptrUser("u4", "s2", "2026-11-25T03:20:00Z", c, "bg", "replacement question"),
		ptrAssistant("a4", "u4", "2026-11-25T03:21:00Z", c, "bg", "msg_5", "replacement answer"),
	})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, o+".jsonl"), []byte(orig), 0o644))
	contPath := filepath.Join(dir, c+".jsonl")
	require.NoError(t, os.WriteFile(contPath, []byte(cont), 0o644))

	results, excluded, err := claudeParseFile(
		contPath, "my_app", "local",
		claudeParseOptions{siblingLineage: true},
	)
	require.NoError(t, err)
	assert.Empty(t, excluded)
	require.Len(t, results, 1)
	sess := results[0].Session
	assert.Equal(t, o, sess.ParentSessionID)
	assert.Equal(t, RelContinuation, sess.RelationshipType)
	require.NotNil(t, sess.ForkPointUUID)
	assert.Equal(t, "a1", *sess.ForkPointUUID)
	assert.Equal(t,
		[]string{"second question", "replacement question"},
		userContents(results[0].Messages),
	)
	assert.Equal(t, []ParsedRewoundInput{{
		SourceUUID: "u3",
		AfterUUID:  "u2",
		Timestamp:  time.Date(2026, 11, 25, 3, 10, 0, 0, time.UTC),
		Content:    "abandoned question",
	}}, sess.RewoundInputs)
	var resultIDs []string
	for _, m := range results[0].Messages {
		for _, r := range m.ToolResults {
			resultIDs = append(resultIDs, r.ToolUseID)
		}
	}
	assert.ElementsMatch(t, []string{"tu1", "tu2"}, resultIDs)
}

// A fork that abandons a long branch when it rewinds into its copied history
// keeps that branch as a session of its own. The branch left the copy at
// the end of orig's third turn, so it hangs off orig there, not off the
// fork's main branch, which shares only orig's first two turns with it.
func TestClaudeForkRewoundIntoCopiedHistoryKeepsLongBranch(t *testing.T) {
	t.Parallel()
	const (
		o = rwOrigID
		f = rwForkID
	)
	orig := joinLines(
		rwHistory(o, ""),
		ptrForkCommand("oc1", "oc2", "oc3", "s3", "2026-11-25T02:31:00Z", o, "", f),
	)
	notice := ptrForkSource("fs1", "s3", "2026-11-25T02:31:01Z", f)
	var ownTurns []string
	parent := "fs1"
	for i := range 4 {
		n := i + 4
		minute := fmt.Sprintf("2026-11-26T02:%02d:00Z", 10*i)
		ownTurns = append(ownTurns,
			ptrUser(fmt.Sprintf("u%d", n), parent, minute, f, "bg", fmt.Sprintf("branch question %d", n)),
			ptrAssistant(fmt.Sprintf("a%d", n), fmt.Sprintf("u%d", n), minute, f, "bg", fmt.Sprintf("msg_%d", n), "branch answer"),
		)
		parent = fmt.Sprintf("a%d", n)
	}
	fork := joinLines(
		notice[:1],
		rwHistory(f, "bg"),
		notice[1:],
		[]string{ptrUUIDLess("last-prompt", "", f)},
		ownTurns,
		[]string{
			ptrUser("u9", "s2", "2026-11-26T03:00:00Z", f, "bg", "rewound question"),
			ptrAssistant("a9", "u9", "2026-11-26T03:01:00Z", f, "bg", "msg_9", "rewound answer"),
		},
	)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, o+".jsonl"), []byte(orig), 0o644))
	forkPath := filepath.Join(dir, f+".jsonl")
	require.NoError(t, os.WriteFile(forkPath, []byte(fork), 0o644))

	results, excluded, err := claudeParseFile(
		forkPath, "my_app", "local",
		claudeParseOptions{siblingLineage: true},
	)
	require.NoError(t, err)
	assert.Empty(t, excluded)
	require.Len(t, results, 2)

	main := results[0].Session
	assert.Equal(t, f, main.ID)
	assert.Equal(t, o, main.ParentSessionID)
	assert.Equal(t, RelFork, main.RelationshipType)
	require.NotNil(t, main.ForkPointUUID)
	assert.Equal(t, "a2", *main.ForkPointUUID)
	assert.Equal(t, []string{"rewound question"}, userContents(results[0].Messages))
	assert.Empty(t, main.RewoundInputs)

	branch := results[1].Session
	assert.Equal(t, f+"-u3", branch.ID)
	assert.Equal(t, o, branch.ParentSessionID)
	assert.Equal(t, RelFork, branch.RelationshipType)
	require.NotNil(t, branch.ForkPointUUID)
	assert.Equal(t, "a3", *branch.ForkPointUUID)
	assert.Equal(t,
		[]string{"branch question 4", "branch question 5", "branch question 6", "branch question 7"},
		userContents(results[1].Messages),
	)
}

package sync_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	familyOrig = "a1a1a1a1-0000-4000-8000-000000000001"
	familyCont = "b2b2b2b2-0000-4000-8000-000000000003"
	familyFork = "f3f3f3f3-0000-4000-8000-000000000004"
)

func familyUser(uuid, parent, ts, sid, kind, text string) string {
	parentField := "null"
	if parent != "" {
		parentField = fmt.Sprintf("%q", parent)
	}
	kindField := ""
	if kind != "" {
		kindField = fmt.Sprintf(`"sessionKind":%q,`, kind)
	}
	return fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":%s,"timestamp":%q,"sessionId":%q,%s"message":{"content":%q}}`,
		uuid, parentField, ts, sid, kindField, text,
	)
}

func familyAssistant(uuid, parent, ts, sid, kind, msgID, text string) string {
	kindField := ""
	if kind != "" {
		kindField = fmt.Sprintf(`"sessionKind":%q,`, kind)
	}
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":%q,%s"message":{"id":%q,"content":[{"type":"text","text":%q}]}}`,
		uuid, parent, ts, sid, kindField, msgID, text,
	)
}

func familyLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// A background handoff that later runs /fork must stay in the archive. Its
// child replays every line the handoff wrote before the fork, so the child's
// content covers almost all of the handoff. Lineage used to read that as the
// handoff being a replay of its own child, trimmed it to nothing, and
// deleted the stored session.
func TestSyncKeepsHandoffWhenItsForkAppears(t *testing.T) {
	env := setupTestEnv(t)
	ctx := t.Context()

	orig := familyLines(
		familyUser("u1", "", "2026-11-25T01:50:00Z", familyOrig, "", "first question"),
		familyAssistant("a1", "u1", "2026-11-25T01:51:00Z", familyOrig, "", "msg_1", "first answer"),
		familyUser("u2", "a1", "2026-11-25T02:00:00Z", familyOrig, "", "second question"),
		`{"type":"api-request","timestamp":"2026-11-25T02:00:01Z","sessionId":"`+familyOrig+`"}`,
		familyAssistant("a2", "u2", "2026-11-25T02:50:00Z", familyOrig, "", "msg_2", "second answer"),
		`{"type":"continued-in","timestamp":"2026-11-25T02:53:20Z","sessionId":"`+familyOrig+`","continuedInSessionId":"`+familyCont+`"}`,
	)
	cont := familyLines(
		familyUser("u1", "", "2026-11-25T01:50:00Z", familyCont, "bg", "first question"),
		familyAssistant("a1", "u1", "2026-11-25T01:51:00Z", familyCont, "bg", "msg_1", "first answer"),
		`{"type":"last-prompt","sessionId":"`+familyCont+`"}`,
		familyUser("u3", "a1", "2026-11-25T03:04:00Z", familyCont, "bg", "third question"),
		`{"type":"api-request","timestamp":"2026-11-25T03:04:01Z","sessionId":"`+familyCont+`"}`,
		familyAssistant("a3", "u3", "2026-11-25T03:34:00Z", familyCont, "bg", "msg_3", "third answer"),
	)
	env.writeClaudeSession(t, "project", familyOrig+".jsonl", orig)
	contPath := env.writeClaudeSession(t, "project", familyCont+".jsonl", cont)

	first := env.engine.SyncAll(ctx, nil)
	require.Zero(t, first.Failed)
	stored, err := env.db.GetSession(ctx, familyCont)
	require.NoError(t, err)
	require.NotNil(t, stored, "the handoff is archived before its fork exists")
	require.NotNil(t, stored.ParentSessionID)
	assert.Equal(t, familyOrig, *stored.ParentSessionID)
	assert.Equal(t, "continuation", stored.RelationshipType)

	// /fork appends its command records to the handoff and spawns a child
	// that replays the handoff's chain.
	forkRecords := familyLines(
		`{"parentUuid":"a3","type":"system","subtype":"local_command","content":"<command-name>/fork</command-name>","timestamp":"2026-11-25T03:35:02Z","uuid":"cc1","sessionKind":"bg","sessionId":"`+familyCont+`"}`,
		`{"parentUuid":"cc1","type":"system","subtype":"local_command","content":"<local-command-stdout>session waiting for a prompt · review · f3f3f3f3</local-command-stdout>","commandRun":{"command":"fork","args":""},"timestamp":"2026-11-25T03:35:02Z","uuid":"cc2","sessionKind":"bg","sessionId":"`+familyCont+`"}`,
		`{"parentUuid":"cc2","type":"user","isMeta":true,"message":{"content":"The fork runs as its own separate session."},"timestamp":"2026-11-25T03:35:02Z","uuid":"cc3","sessionKind":"bg","sessionId":"`+familyCont+`"}`,
	)
	f, err := os.OpenFile(contPath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(forkRecords)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	notice := "<task-notification>\n<fork-source>\nThis session began as a fork.\n</fork-source>\n</task-notification>"
	fork := familyLines(
		familyUser("u1", "", "2026-11-25T01:50:00Z", familyFork, "bg", "first question"),
		familyAssistant("a1", "u1", "2026-11-25T01:51:00Z", familyFork, "bg", "msg_1", "first answer"),
		familyUser("u3", "a1", "2026-11-25T03:04:00Z", familyFork, "bg", "third question"),
		familyAssistant("a3", "u3", "2026-11-25T03:34:00Z", familyFork, "bg", "msg_3", "third answer"),
		fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-25T03:35:05Z","sessionId":%q,"content":%q}`, familyFork, notice),
		fmt.Sprintf(`{"parentUuid":"a3","type":"user","message":{"content":%q},"uuid":"fs","timestamp":"2026-11-25T03:35:05Z","origin":{"kind":"task-notification"},"sessionKind":"bg","sessionId":%q}`, notice, familyFork),
		familyUser("u4", "fs", "2026-11-25T03:36:00Z", familyFork, "bg", "fourth question"),
		familyAssistant("a4", "u4", "2026-11-25T03:40:00Z", familyFork, "bg", "msg_4", "fourth answer"),
	)
	env.writeClaudeSession(t, "project", familyFork+".jsonl", fork)

	second := env.engine.SyncAll(ctx, nil)
	require.Zero(t, second.Failed)
	// A full resync re-derives every session from its source, the path that
	// deleted the handoff.
	env.engine.ResyncAll(ctx, nil)

	stored, err = env.db.GetSession(ctx, familyCont)
	require.NoError(t, err)
	require.NotNil(t, stored, "the handoff must survive its fork")
	require.NotNil(t, stored.ParentSessionID)
	assert.Equal(t, familyOrig, *stored.ParentSessionID)
	assert.Equal(t, "continuation", stored.RelationshipType)
	msgs, err := env.db.GetAllMessages(ctx, familyCont)
	require.NoError(t, err)
	var contents []string
	for _, m := range msgs {
		contents = append(contents, m.Content)
	}
	assert.Contains(t, contents, "third question")
	point, err := env.db.GetSessionForkPoint(ctx, familyCont)
	require.NoError(t, err)
	assert.Equal(t, "a1", point)

	child, err := env.db.GetSession(ctx, familyFork)
	require.NoError(t, err)
	require.NotNil(t, child)
	require.NotNil(t, child.ParentSessionID)
	assert.Equal(t, familyCont, *child.ParentSessionID)
	assert.Equal(t, "fork", child.RelationshipType)
	point, err = env.db.GetSessionForkPoint(ctx, familyFork)
	require.NoError(t, err)
	assert.Equal(t, "a3", point)
}

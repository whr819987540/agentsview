// ABOUTME: Tests for DAG fork detection in Claude JSONL session files.
// ABOUTME: Validates rewind live-branch selection, abandoned-branch preservation, and backward compat.
package parser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func parseTestContent(t *testing.T, name, content string, expectedLen int) []ParseResult {
	t.Helper()
	path := createTestFile(t, name, content)
	results, err := parseClaudeSession(path, "proj", "local")
	require.NoError(t, err, "ParseClaudeSession")
	require.Len(t, results, expectedLen)
	return results
}

func formatTime(ts time.Time) string {
	return ts.Format(time.RFC3339)
}

func TestForkDetection_LinearSession(t *testing.T) {
	// Linear chain: a -> b -> c -> d, all with uuid/parentUuid.
	// Should return 1 result with 4 messages.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi there", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "next question", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "answer", "d", "c").
		String()

	results := parseTestContent(t, "linear.jsonl", content, 1)

	assertSessionMeta(t, &results[0].Session, "linear", "proj", AgentClaude)
	assertMessageCount(t, len(results[0].Messages), 4)
}

func TestForkDetection_LargeGapFork(t *testing.T) {
	// A rewind (esc+esc) past a substantial branch: the user ran
	// 4 user turns (c,e,g,k), rewound back to b, and continued
	// with i->j. The live conversation — what Claude Code itself
	// displays — is a,b,i,j; the abandoned branch c..l has 4 user
	// turns (> forkThreshold) and is preserved as a fork session.
	//
	//   a(user) -> b(asst) -> c(user) -> d(asst) -> e(user) -> f(asst)
	//                         -> g(user) -> h(asst) -> k(user) -> l(asst)
	//                      -> i(user) -> j(asst)   [live branch after rewind]
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "q1", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "a1", "d", "c").
		AddClaudeUserWithUUID("2024-01-01T10:00:04Z", "q2", "e", "d").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:05Z", "a2", "f", "e").
		AddClaudeUserWithUUID("2024-01-01T10:00:06Z", "q3", "g", "f").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "a3", "h", "g").
		AddClaudeUserWithUUID("2024-01-01T10:00:08Z", "q4", "k", "h").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:09Z", "a4", "l", "k").
		// Live branch appended after the rewind
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "fork q1", "i", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "fork a1", "j", "i").
		String()

	results := parseTestContent(t, "fork.jsonl", content, 2)

	// Main session: the live branch (a,b,i,j).
	main := results[0]
	assertSessionMeta(t, &main.Session, "fork", "proj", AgentClaude)
	assertMessageCount(t, len(main.Messages), 4)
	assertMessage(t, main.Messages[2], RoleUser, "fork q1")
	assertMessage(t, main.Messages[3], RoleAssistant, "fork a1")
	assert.Empty(t, main.Session.ParentSessionID, "main ParentSessionID")

	// Fork session: the abandoned branch (c..l).
	fork := results[1]
	assert.Equal(t, "fork-c", fork.Session.ID, "fork session ID")
	assertMessageCount(t, len(fork.Messages), 8)
	assert.Equal(t, "fork", fork.Session.ParentSessionID, "fork ParentSessionID")
	assert.Equal(t, RelFork, fork.Session.RelationshipType, "fork RelationshipType")
	assert.Equal(t, "q1", fork.Session.FirstMessage, "fork FirstMessage")
}

func TestForkDetection_SmallGapRetry(t *testing.T) {
	// A quick rewind: the user asked c, rewound to b, and asked e
	// instead. The abandoned branch c,d has only 1 user turn
	// (<= forkThreshold), so it is dropped as retry noise and the
	// session shows just the live path: a,b,e,f.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "first try", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "first answer", "d", "c").
		// Retry from b (later in file)
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "retry", "e", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "retry answer", "f", "e").
		String()

	results := parseTestContent(t, "retry.jsonl", content, 1)

	// Latest branch wins: a, b, e, f
	assertMessageCount(t, len(results[0].Messages), 4)
	assertMessage(t, results[0].Messages[0], RoleUser, "hello")
	assertMessage(t, results[0].Messages[1], RoleAssistant, "hi")
	assertMessage(t, results[0].Messages[2], RoleUser, "retry")
	assertMessage(t, results[0].Messages[3], RoleAssistant, "retry answer")
}

func TestForkDetection_NoUUIDs(t *testing.T) {
	// Entries without uuid fields — should work as before, 1 result.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUser("2024-01-01T10:00:00Z", "hello").
		AddClaudeAssistant("2024-01-01T10:00:01Z", "hi").
		AddClaudeUser("2024-01-01T10:00:02Z", "bye").
		AddClaudeAssistant("2024-01-01T10:00:03Z", "goodbye").
		String()

	results := parseTestContent(t, "nouuid.jsonl", content, 1)

	assertMessageCount(t, len(results[0].Messages), 4)
	assertMessage(t, results[0].Messages[0], RoleUser, "hello")
}

func TestForkDetection_MixedUUIDs(t *testing.T) {
	// Some entries have uuid, some don't — fall back to linear.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUser("2024-01-01T10:00:00Z", "no uuid").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "has uuid", "x", "").
		AddClaudeUser("2024-01-01T10:00:02Z", "no uuid again").
		String()

	results := parseTestContent(t, "mixed.jsonl", content, 1)

	assertMessageCount(t, len(results[0].Messages), 3)
}

func TestForkDetection_NestedFork(t *testing.T) {
	// Two successive rewinds. The user ran c..l (5 user turns),
	// rewound to b and ran m..v (5 user turns), then rewound again
	// to n and continued with w,x. The live conversation is
	// a,b,m,n,w,x; both abandoned branches are substantial
	// (> forkThreshold user turns) and preserved as forks.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "start", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "ok", "b", "a").
		// Main branch from b
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "main1", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "m-ok1", "d", "c").
		AddClaudeUserWithUUID("2024-01-01T10:00:04Z", "main2", "e", "d").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:05Z", "m-ok2", "f", "e").
		AddClaudeUserWithUUID("2024-01-01T10:00:06Z", "main3", "g", "f").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "m-ok3", "h", "g").
		AddClaudeUserWithUUID("2024-01-01T10:00:08Z", "main4", "k", "h").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:09Z", "m-ok4", "l", "k").
		// Fork branch from b
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "fork1", "m", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "f-ok1", "n", "m").
		AddClaudeUserWithUUID("2024-01-01T10:01:02Z", "fork2", "o", "n").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:03Z", "f-ok2", "p", "o").
		AddClaudeUserWithUUID("2024-01-01T10:01:04Z", "fork3", "q", "p").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:05Z", "f-ok3", "r", "q").
		AddClaudeUserWithUUID("2024-01-01T10:01:06Z", "fork4", "s", "r").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:07Z", "f-ok4", "tt", "s").
		AddClaudeUserWithUUID("2024-01-01T10:01:08Z", "fork5", "u", "tt").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:09Z", "f-ok5", "v", "u").
		// Nested fork from n (within the fork branch)
		AddClaudeUserWithUUID("2024-01-01T10:02:00Z", "nested", "w", "n").
		AddClaudeAssistantWithUUID("2024-01-01T10:02:01Z", "n-ok", "x", "w").
		String()

	// Expect 3 results: the live path plus the two abandoned
	// branches, discovered in walk order along the live path.
	results := parseTestContent(t, "nested-fork.jsonl", content, 3)

	// Main: the live path a,b,m,n,w,x = 6 messages.
	main := results[0]
	assertMessageCount(t, len(main.Messages), 6)
	assertMessage(t, main.Messages[4], RoleUser, "nested")
	assertMessage(t, main.Messages[5], RoleAssistant, "n-ok")

	// First rewind's abandoned branch: c..l = 8 messages.
	fork := results[1]
	assert.Equal(t, "nested-fork-c", fork.Session.ID, "fork ID")
	assertMessageCount(t, len(fork.Messages), 8)
	assert.Equal(t, RelFork, fork.Session.RelationshipType, "fork RelationshipType")
	// Both fork points sit on the live path, so both abandoned
	// branches parent to the root session.
	assert.Equal(t, "nested-fork", fork.Session.ParentSessionID, "fork ParentSessionID")

	// Second rewind's abandoned branch: o..v = 8 messages.
	nested := results[2]
	assert.Equal(t, "nested-fork-o", nested.Session.ID, "nested ID")
	assertMessageCount(t, len(nested.Messages), 8)
	assert.Equal(t, RelFork, nested.Session.RelationshipType, "nested RelationshipType")
	assert.Equal(t, "nested-fork", nested.Session.ParentSessionID, "nested ParentSessionID")
}

func TestForkDetection_AbandonedBranchKeepsItsOwnLivePath(t *testing.T) {
	// Rewind inside a branch that is later abandoned wholesale.
	// The user ran c..h, rewound to d and continued with m..r
	// (the branch's own live tail), then rewound all the way back
	// to b and continued with y,z. The preserved fork session must
	// show the abandoned branch as it looked when it was left:
	// c,d,m..r — not the branch's own abandoned tail e..h (2 user
	// turns <= forkThreshold, dropped as noise).
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "start", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "ok", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "q1", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "a1", "d", "c").
		AddClaudeUserWithUUID("2024-01-01T10:00:04Z", "old1", "e", "d").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:05Z", "old-a1", "f", "e").
		AddClaudeUserWithUUID("2024-01-01T10:00:06Z", "old2", "g", "f").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "old-a2", "h", "g").
		// First rewind: back to d, continue inside the branch.
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "q2", "m", "d").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "a2", "n", "m").
		AddClaudeUserWithUUID("2024-01-01T10:01:02Z", "q3", "o", "n").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:03Z", "a3", "p", "o").
		AddClaudeUserWithUUID("2024-01-01T10:01:04Z", "q4", "q", "p").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:05Z", "a4", "r", "q").
		// Second rewind: all the way back to b.
		AddClaudeUserWithUUID("2024-01-01T10:02:00Z", "fresh", "y", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:02:01Z", "fresh-a", "z", "y").
		String()

	results := parseTestContent(t, "abandoned-live.jsonl", content, 2)

	// Main: the live path a,b,y,z.
	main := results[0]
	assertMessageCount(t, len(main.Messages), 4)
	assertMessage(t, main.Messages[2], RoleUser, "fresh")

	// The abandoned branch keeps its own live path: c,d,m..r.
	// Its internally-abandoned tail e..h is dropped.
	fork := results[1]
	assert.Equal(t, "abandoned-live-c", fork.Session.ID, "fork ID")
	assertMessageCount(t, len(fork.Messages), 8)
	assertMessage(t, fork.Messages[0], RoleUser, "q1")
	assertMessage(t, fork.Messages[2], RoleUser, "q2")
	assertMessage(t, fork.Messages[7], RoleAssistant, "a4")
	assert.Equal(t, "abandoned-live", fork.Session.ParentSessionID, "fork ParentSessionID")
}

func TestForkDetection_MultipleRoots(t *testing.T) {
	// Two entries with empty parentUuid = two roots.
	// A well-formed DAG has exactly one root, so multiple roots
	// should fall back to linear parsing, returning all messages.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "root one", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "reply one", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "root two", "c", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "reply two", "d", "c").
		String()

	results := parseTestContent(t, "multi-root.jsonl", content, 1)

	// All 4 messages must be present.
	assertMessageCount(t, len(results[0].Messages), 4)
	assertMessage(t, results[0].Messages[0], RoleUser, "root one")
	assertMessage(t, results[0].Messages[1], RoleAssistant, "reply one")
	assertMessage(t, results[0].Messages[2], RoleUser, "root two")
	assertMessage(t, results[0].Messages[3], RoleAssistant, "reply two")
}

func TestForkDetection_DisconnectedParent(t *testing.T) {
	// Entry "c" has parentUuid "nonexistent" which doesn't match
	// any entry's uuid. This means the DAG is disconnected, so
	// we should fall back to linear parsing.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "orphan", "c", "nonexistent").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "orphan reply", "d", "c").
		String()

	results := parseTestContent(t, "disconnected.jsonl", content, 1)

	// All 4 messages must be present.
	assertMessageCount(t, len(results[0].Messages), 4)
	assertMessage(t, results[0].Messages[0], RoleUser, "hello")
	assertMessage(t, results[0].Messages[1], RoleAssistant, "hi")
	assertMessage(t, results[0].Messages[2], RoleUser, "orphan")
	assertMessage(t, results[0].Messages[3], RoleAssistant, "orphan reply")
}

func TestSessionBoundsIncludeNonMessageEvents(t *testing.T) {
	// A trailing queue-operation event has a later timestamp
	// than any user/assistant message. Session endedAt should
	// still reflect that later timestamp.
	queueLine := `{"type":"queue-operation","operation":"enqueue",` +
		`"timestamp":"2024-01-01T11:00:00Z","content":"{}"}`

	content := testjsonl.NewSessionBuilder().
		AddClaudeUser("2024-01-01T10:00:00Z", "hello").
		AddClaudeAssistant("2024-01-01T10:00:01Z", "hi").
		AddRaw(queueLine).
		String()

	results := parseTestContent(t, "queue-ts.jsonl", content, 1)

	sess := results[0].Session
	assert.Equal(t, "2024-01-01T11:00:00Z", formatTime(sess.EndedAt), "EndedAt")
}

func TestSessionBoundsStartedAtFromLeadingEvent(t *testing.T) {
	// A leading non-message event has an earlier timestamp
	// than the first user message. StartedAt should reflect it.
	earlyLine := `{"type":"queue-operation","operation":"enqueue",` +
		`"timestamp":"2024-01-01T09:00:00Z","content":"{}"}`

	content := testjsonl.NewSessionBuilder().
		AddRaw(earlyLine).
		AddClaudeUser("2024-01-01T10:00:00Z", "hello").
		AddClaudeAssistant("2024-01-01T10:00:01Z", "hi").
		String()

	results := parseTestContent(t, "early-queue.jsonl", content, 1)

	sess := results[0].Session
	assert.Equal(t, "2024-01-01T09:00:00Z", formatTime(sess.StartedAt), "StartedAt")
}

func TestForkDetection_NestedForkCountsFullSubtree(t *testing.T) {
	// Regression test: countUserTurns must count the entire
	// subtree of an abandoned branch, not just one child path.
	// A first-child-only traversal would see 1 user turn here
	// (c -> d -> e dead-ends) and drop the branch as retry
	// noise instead of preserving it as a fork session.
	//
	// DAG:  root(a) -> b (fork)
	//   Abandoned child: c(user) -> d(asst, fork)
	//                   d -> e(asst, dead-end)
	//                   d -> f(user) -> g(asst) -> h(user) ->
	//                        i(asst) -> j(user) -> k(asst)
	//   Live child: z(user, appended after the rewind)
	//
	// First-child-only count for c: c(user,1) -> d(asst) ->
	//   e(asst, no children) = 1 user turn <= 3 -> dropped!
	// Full-subtree count for c: c,f,h,j = 4 > 3 -> preserved.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "start", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "ok", "b", "a").
		// First child branch from b: large subtree
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "main1", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "m-ok1", "d", "c").
		// Nested fork at d: first child is a dead-end
		AddClaudeAssistantWithUUID("2024-01-01T10:00:04Z", "dead-end", "e", "d").
		// Second child of d's fork continues the real conversation
		AddClaudeUserWithUUID("2024-01-01T10:00:05Z", "main2", "f", "d").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:06Z", "m-ok2", "g", "f").
		AddClaudeUserWithUUID("2024-01-01T10:00:07Z", "main3", "h", "g").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:08Z", "m-ok3", "i", "h").
		AddClaudeUserWithUUID("2024-01-01T10:00:09Z", "main4", "j", "i").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:10Z", "m-ok4", "k", "j").
		// Second child of b's fork: trivial retry
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "retry", "z", "b").
		String()

	// The abandoned subtree has 4 user turns (c,f,h,j) > 3, so it
	// must be preserved as a fork session. We expect 2 results:
	// the live path (a,b,z = 3 msgs) and the abandoned branch
	// (c,d,f,g,h,i,j,k = 8 msgs — its dead-end "e" is dropped).
	results := parseTestContent(t, "nested-fork-subtree.jsonl", content, 2)

	main := results[0]
	assertMessageCount(t, len(main.Messages), 3)
	assertMessage(t, main.Messages[2], RoleUser, "retry")

	fork := results[1]
	assert.Equal(t, "nested-fork-subtree-c", fork.Session.ID, "fork ID")
	assertMessageCount(t, len(fork.Messages), 8)
	assertMessage(t, fork.Messages[0], RoleUser, "main1")
	assert.Equal(t, RelFork, fork.Session.RelationshipType, "fork RelationshipType")
}

// claudeAttachmentLine builds a modern-format attachment record that
// carries uuid/parentUuid and so participates in the session DAG.
func claudeAttachmentLine(ts, uuid, parentUuid string) string {
	return `{"type":"attachment","timestamp":"` + ts +
		`","uuid":"` + uuid + `","parentUuid":"` + parentUuid +
		`","attachment":{"type":"task_reminder"}}`
}

// claudeSystemLine builds a modern-format system record that carries
// uuid/parentUuid and so participates in the session DAG.
func claudeSystemLine(ts, uuid, parentUuid string) string {
	return `{"type":"system","timestamp":"` + ts +
		`","uuid":"` + uuid + `","parentUuid":"` + parentUuid +
		`","content":"hook ran","subtype":"informational"}`
}

func TestForkDetection_RewindThroughNonMessageEntries(t *testing.T) {
	// Claude Code 2.x threads the uuid/parentUuid chain through
	// attachment and system records: assistant replies parent to
	// the last attachment, not to the user message. A rewind fork
	// must still be detected by resolving parent references
	// through those non-message records instead of falling back
	// to linear parsing (which would show both branches).
	//
	//   a(user) -> att1 -> att2 -> b(asst) -> sys1
	//     sys1 -> c(user) -> att3 -> d(asst)   [abandoned by rewind]
	//     sys1 -> e(user) -> att4 -> f(asst)   [live branch]
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddRaw(claudeAttachmentLine("2024-01-01T10:00:01Z", "att1", "a")).
		AddRaw(claudeAttachmentLine("2024-01-01T10:00:02Z", "att2", "att1")).
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "hi", "b", "att2").
		AddRaw(claudeSystemLine("2024-01-01T10:00:04Z", "sys1", "b")).
		AddClaudeUserWithUUID("2024-01-01T10:00:05Z", "first try", "c", "sys1").
		AddRaw(claudeAttachmentLine("2024-01-01T10:00:06Z", "att3", "c")).
		AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "first answer", "d", "att3").
		// Rewind: new user input re-parents to sys1.
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "retry", "e", "sys1").
		AddRaw(claudeAttachmentLine("2024-01-01T10:01:01Z", "att4", "e")).
		AddClaudeAssistantWithUUID("2024-01-01T10:01:02Z", "retry answer", "f", "att4").
		String()

	results := parseTestContent(t, "modern-rewind.jsonl", content, 1)

	// Only the live branch is shown: a,b,e,f. The abandoned
	// branch c,d (1 user turn) is dropped.
	assertMessageCount(t, len(results[0].Messages), 4)
	assertMessage(t, results[0].Messages[0], RoleUser, "hello")
	assertMessage(t, results[0].Messages[1], RoleAssistant, "hi")
	assertMessage(t, results[0].Messages[2], RoleUser, "retry")
	assertMessage(t, results[0].Messages[3], RoleAssistant, "retry answer")
}

func TestForkDetection_RewindWithChunkedAssistantRuns(t *testing.T) {
	// Streamed assistant responses are written as several JSONL
	// entries sharing one message.id, each parenting the previous
	// chunk. Chunk merging absorbs all but one entry, so DAG
	// resolution must follow parent references through absorbed
	// chunk uuids (and the merged entry must adopt the run's
	// incoming parent) or every chunked session degrades to
	// linear parsing and a rewind shows both branches.
	chunk := func(ts, uuid, parentUuid, mid, text string) string {
		return `{"type":"assistant","timestamp":"` + ts +
			`","uuid":"` + uuid + `","parentUuid":"` + parentUuid +
			`","message":{"id":"` + mid +
			`","content":[{"type":"text","text":"` + text + `"}]}}`
	}
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddRaw(chunk("2024-01-01T10:00:01Z", "b1", "a", "msg_1", "part one")).
		AddRaw(chunk("2024-01-01T10:00:02Z", "b2", "b1", "msg_1", "part two")).
		AddClaudeUserWithUUID("2024-01-01T10:00:03Z", "first try", "c", "b2").
		AddRaw(chunk("2024-01-01T10:00:04Z", "d1", "c", "msg_2", "old one")).
		AddRaw(chunk("2024-01-01T10:00:05Z", "d2", "d1", "msg_2", "old two")).
		// Rewind: new user input re-parents to the middle of the
		// first assistant run (an absorbed chunk uuid).
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "retry", "e", "b1").
		AddRaw(chunk("2024-01-01T10:01:01Z", "f1", "e", "msg_3", "new answer")).
		String()

	results := parseTestContent(t, "chunked-rewind.jsonl", content, 1)

	// Live branch only: a, merged(b1,b2), e, f1. The abandoned
	// branch c,d (1 user turn) is dropped.
	msgs := results[0].Messages
	assertMessageCount(t, len(msgs), 4)
	assertMessage(t, msgs[0], RoleUser, "hello")
	assertMessage(t, msgs[1], RoleAssistant, "part one")
	assertMessage(t, msgs[1], RoleAssistant, "part two")
	assertMessage(t, msgs[2], RoleUser, "retry")
	assertMessage(t, msgs[3], RoleAssistant, "new answer")
}

// claudeToolResultUserLine builds a user record whose content is a
// single tool_result block — the carrier shape Claude Code writes
// for tool outputs. type=user on disk, but not a conversation turn.
func claudeToolResultUserLine(ts, uuid, parentUuid, toolUseID string) string {
	return `{"type":"user","timestamp":"` + ts +
		`","uuid":"` + uuid + `","parentUuid":"` + parentUuid +
		`","message":{"content":[{"type":"tool_result","tool_use_id":"` +
		toolUseID + `","content":"ok"}]}}`
}

func TestForkDetection_ToolResultsDoNotInflateAbandonedBranch(t *testing.T) {
	// A typo-fix rewind over a tool-heavy turn (regression for a
	// real session): the abandoned branch has just 2 real user
	// prompts plus an interrupt notice, but 4 additional type=user
	// records (tool_result carriers). Raw user-entry counting saw
	// 7 > forkThreshold and preserved the branch as a fork
	// session; real-turn counting must drop it as retry noise.
	toolUse := func(ts, uuid, parentUuid, id string) string {
		return `{"type":"assistant","timestamp":"` + ts +
			`","uuid":"` + uuid + `","parentUuid":"` + parentUuid +
			`","message":{"content":[{"type":"tool_use","id":"` + id +
			`","name":"WebSearch","input":{}}]}}`
	}
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
		// Abandoned branch: typo'd prompt with tool activity.
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "tell me about the situwation", "c", "b").
		AddRaw(toolUse("2024-01-01T10:00:03Z", "d", "c", "tu1")).
		AddRaw(claudeToolResultUserLine("2024-01-01T10:00:04Z", "e", "d", "tu1")).
		AddRaw(toolUse("2024-01-01T10:00:05Z", "f", "e", "tu2")).
		AddRaw(claudeToolResultUserLine("2024-01-01T10:00:06Z", "g", "f", "tu2")).
		AddClaudeUserWithUUID("2024-01-01T10:00:07Z", "[Request interrupted by user for tool use]", "h", "g").
		AddClaudeUserWithUUID("2024-01-01T10:00:08Z", "the situation", "i", "h").
		AddRaw(toolUse("2024-01-01T10:00:09Z", "j", "i", "tu3")).
		AddRaw(claudeToolResultUserLine("2024-01-01T10:00:10Z", "k", "j", "tu3")).
		AddRaw(toolUse("2024-01-01T10:00:11Z", "l", "k", "tu4")).
		AddRaw(claudeToolResultUserLine("2024-01-01T10:00:12Z", "m", "l", "tu4")).
		// Rewind: corrected prompt re-parents to b.
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "tell me about the situation", "y", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "sure", "z", "y").
		String()

	results := parseTestContent(t, "toolresult-rewind.jsonl", content, 1)

	// Only the live branch remains: a,b,y,z.
	msgs := results[0].Messages
	assertMessageCount(t, len(msgs), 4)
	assertMessage(t, msgs[2], RoleUser, "tell me about the situation")
	assertMessage(t, msgs[3], RoleAssistant, "sure")
}

func TestSessionBoundsDAGMainWidenedNotFork(t *testing.T) {
	// DAG session with a trailing queue-operation after all
	// messages. Main session's EndedAt should be widened;
	// fork session should use only its own message bounds.
	queueLine := `{"type":"queue-operation","operation":"enqueue",` +
		`"timestamp":"2024-01-01T12:00:00Z","content":"{}"}`

	// Abandoned branch from b: c..l (5 user turns, preserved).
	// Live branch after the rewind: i->j.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "q1", "c", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "a1", "d", "c").
		AddClaudeUserWithUUID("2024-01-01T10:00:04Z", "q2", "e", "d").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:05Z", "a2", "f", "e").
		AddClaudeUserWithUUID("2024-01-01T10:00:06Z", "q3", "g", "f").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "a3", "h", "g").
		AddClaudeUserWithUUID("2024-01-01T10:00:08Z", "q4", "k", "h").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:09Z", "a4", "l", "k").
		// Fork from b
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "fork", "i", "b").
		AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "fork-a", "j", "i").
		AddRaw(queueLine).
		String()

	results := parseTestContent(t, "dag-queue.jsonl", content, 2)

	// Main session (live branch a,b,i,j) EndedAt should be widened
	// to the queue timestamp.
	assert.Equal(t, "2024-01-01T12:00:00Z", formatTime(results[0].Session.EndedAt), "main EndedAt")

	// Fork session (abandoned branch c..l) EndedAt should NOT be
	// widened.
	assert.Equal(t, "2024-01-01T10:00:09Z", formatTime(results[1].Session.EndedAt), "fork EndedAt")
}

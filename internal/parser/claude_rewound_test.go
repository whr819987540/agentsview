package parser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/testjsonl"
)

// rewoundView is a ParsedRewoundInput with its timestamp as text, so test
// tables can spell it out.
type rewoundView struct {
	UUID, After, Time, Content string
}

func viewRewound(inputs []ParsedRewoundInput) []rewoundView {
	out := make([]rewoundView, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, rewoundView{
			UUID:    input.SourceUUID,
			After:   input.AfterUUID,
			Time:    input.Timestamp.UTC().Format(time.RFC3339),
			Content: input.Content,
		})
	}
	return out
}

func claudeToolResultLine(timestamp, uuid, parentUUID, toolUseID string) string {
	return `{"type":"user","timestamp":"` + timestamp + `","uuid":"` + uuid +
		`","parentUuid":"` + parentUUID + `","message":{"role":"user",` +
		`"content":[{"type":"tool_result","tool_use_id":"` + toolUseID +
		`","content":"ok"}]}}`
}

func claudeToolUseLine(timestamp, uuid, parentUUID, toolUseID string) string {
	return `{"type":"assistant","timestamp":"` + timestamp + `","uuid":"` + uuid +
		`","parentUuid":"` + parentUUID + `","message":{"role":"assistant",` +
		`"content":[{"type":"tool_use","id":"` + toolUseID +
		`","name":"Bash","input":{"command":"ls"}}]}}`
}

func TestClaudeRewoundInputs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		// want lists the rewound inputs of each result, in result order.
		want [][]rewoundView
	}{
		{
			name: "linear transcript has none",
			content: testjsonl.NewSessionBuilder().
				AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
				String(),
			want: [][]rewoundView{{}},
		},
		{
			// The user asked c, rewound to b, and asked e instead.
			name: "rewind follows the input before the fork point",
			content: testjsonl.NewSessionBuilder().
				AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
				AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "first try", "c", "b").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "first answer", "d", "c").
				AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "retry", "e", "b").
				AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "retry answer", "f", "e").
				String(),
			want: [][]rewoundView{{
				{"c", "a", "2024-01-01T10:00:02Z", "first try"},
			}},
		},
		{
			// The abandoned branch ran a tool before its second input,
			// and the user rewound inside it before rewinding past it.
			// Tool results are not inputs, so e follows c, and the
			// nested rewind g follows c as well.
			name: "inputs inside an abandoned branch follow each other",
			content: testjsonl.NewSessionBuilder().
				AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
				AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "plan it", "c", "b").
				AddRaw(claudeToolUseLine("2024-01-01T10:00:03Z", "d", "c", "toolu_1")).
				AddRaw(claudeToolResultLine("2024-01-01T10:00:04Z", "d2", "d", "toolu_1")).
				AddClaudeAssistantWithUUID("2024-01-01T10:00:05Z", "planned", "d3", "d2").
				AddClaudeUserWithUUID("2024-01-01T10:00:06Z", "now build it", "e", "d3").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "built", "f", "e").
				AddClaudeUserWithUUID("2024-01-01T10:00:08Z", "only test it", "g", "d3").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:09Z", "tested", "h", "g").
				AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "start over", "i", "b").
				AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "ok", "j", "i").
				String(),
			want: [][]rewoundView{{
				{"c", "a", "2024-01-01T10:00:02Z", "plan it"},
				{"e", "c", "2024-01-01T10:00:06Z", "now build it"},
				{"g", "c", "2024-01-01T10:00:08Z", "only test it"},
			}},
		},
		{
			// Retrying an assistant reply forks at the reply; the
			// abandoned reply's follow-up still follows a.
			name: "fork point after an assistant reply",
			content: testjsonl.NewSessionBuilder().
				AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
				AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "go on", "c", "b").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "more", "d", "c").
				AddClaudeAssistantWithUUID("2024-01-01T10:01:00Z", "hi again", "e", "a").
				String(),
			want: [][]rewoundView{{
				{"c", "a", "2024-01-01T10:00:02Z", "go on"},
			}},
		},
		{
			// A long abandoned branch becomes a fork session, and a short
			// rewind inside it belongs to that fork, not the main session.
			name: "short rewind inside a fork session",
			content: testjsonl.NewSessionBuilder().
				AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
				AddClaudeUserWithUUID("2024-01-01T10:00:02Z", "q1", "c", "b").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:03Z", "a1", "d", "c").
				AddClaudeUserWithUUID("2024-01-01T10:00:04Z", "q2", "e", "d").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:05Z", "a2", "f", "e").
				AddClaudeUserWithUUID("2024-01-01T10:00:06Z", "q3 draft", "g", "f").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:07Z", "a3 draft", "h", "g").
				AddClaudeUserWithUUID("2024-01-01T10:00:08Z", "q3", "k", "f").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:09Z", "a3", "l", "k").
				AddClaudeUserWithUUID("2024-01-01T10:00:10Z", "q4", "m", "l").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:11Z", "a4", "n", "m").
				AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "other", "i", "b").
				AddClaudeAssistantWithUUID("2024-01-01T10:01:01Z", "sure", "j", "i").
				String(),
			want: [][]rewoundView{
				{},
				{{"g", "e", "2024-01-01T10:00:06Z", "q3 draft"}},
			},
		},
		{
			// A branch that holds only assistant retries has no inputs.
			name: "retried reply without inputs",
			content: testjsonl.NewSessionBuilder().
				AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
				AddClaudeAssistantWithUUID("2024-01-01T10:00:02Z", "hello there", "c", "a").
				String(),
			want: [][]rewoundView{{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := parseTestContent(t, "rewound.jsonl", tt.content, len(tt.want))
			for i, want := range tt.want {
				got := results[i].Session.RewoundInputs
				require.NotNil(t, got, "result %d reports its rewound inputs", i)
				assert.Equal(t, want, viewRewound(got), "result %d", i)
			}
		})
	}
}

func TestClaudeRewoundInputsLinearFallback(t *testing.T) {
	// Two roots send the parser down the linear path, which shows every
	// entry and so reports no rewound inputs.
	content := testjsonl.NewSessionBuilder().
		AddClaudeUserWithUUID("2024-01-01T10:00:00Z", "hello", "a", "").
		AddClaudeAssistantWithUUID("2024-01-01T10:00:01Z", "hi", "b", "a").
		AddClaudeUserWithUUID("2024-01-01T10:01:00Z", "again", "c", "").
		String()

	results := parseTestContent(t, "linear-fallback.jsonl", content, 1)

	require.NotNil(t, results[0].Session.ClaudeLinearParse)
	assert.True(t, *results[0].Session.ClaudeLinearParse)
	assert.NotNil(t, results[0].Session.RewoundInputs)
	assert.Empty(t, results[0].Session.RewoundInputs)
}

package db

import "go.kenn.io/agentsview/internal/parser"

// IsClaudeHandoff reports whether s is the transcript Claude Code started
// when it moved a session to the background. Claude Code keeps the same
// session going in the new transcript and lists only that one, so the
// session tree shows the transcript it left as part of s, not as a session
// of its own.
func IsClaudeHandoff(s Session) bool {
	return s.Agent == string(parser.AgentClaude) &&
		s.RelationshipType == string(parser.RelContinuation) &&
		s.SessionKind == parser.SessionKindBackground
}

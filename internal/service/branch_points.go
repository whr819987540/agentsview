package service

import (
	"cmp"
	"context"
	"slices"

	"go.kenn.io/agentsview/internal/db"
)

// Branch point kinds.
const (
	BranchKindFork         = "fork"
	BranchKindContinuation = "continuation"
	BranchKindRewind       = "rewind"
)

// BranchPointReader is implemented by services that can place where
// conversations branch off a session's transcript.
type BranchPointReader interface {
	// BranchPoints returns the branches that leave the session and where
	// the session leaves its own parent, or nil when the session does not
	// exist.
	BranchPoints(ctx context.Context, id string) (*BranchPoints, error)
	// SessionLeave returns where a fork or continuation leaves its parent,
	// or nil when the session has no such parent or the place is unknown.
	SessionLeave(ctx context.Context, session *db.Session) (*BranchLeave, error)
}

// BranchPoints lists the places in a session's transcript where the
// conversation went another way.
type BranchPoints struct {
	SessionID string `json:"session_id"`
	// Leaves places where this session leaves its parent. It is absent
	// when the session is not a fork or continuation, or when the archive
	// cannot place it.
	Leaves *BranchLeave `json:"leaves,omitempty"`
	// Points lists the branches that leave this session, ordered by the
	// message they follow.
	Points []BranchPoint `json:"points"`
}

// BranchLeave places where a fork or continuation leaves its parent. The
// placement counts the parent's messages and turns.
type BranchLeave struct {
	Parent           db.Session `json:"parent"`
	RelationshipType string     `json:"relationship_type"`
	// AfterOrdinal is the parent message the session follows on from.
	AfterOrdinal int `json:"after_ordinal"`
	// AfterTurn counts the parent's user prompts the session inherits.
	AfterTurn int `json:"after_turn"`
	// TurnsAfter counts the parent's user prompts after the branch. For a
	// continuation they are turns it does not carry on.
	TurnsAfter int `json:"turns_after"`
	// Handoff marks a continuation Claude Code started when it moved the
	// session to the background. The parent is then the same session's
	// earlier transcript, and turns after the branch were rewound.
	Handoff bool `json:"handoff,omitempty"`
}

// BranchPoint is a place where the conversation left a session's transcript:
// a fork or continuation that took over after a message, or inputs that a
// rewind abandoned there.
type BranchPoint struct {
	Kind string `json:"kind" enum:"fork,continuation,rewind"`
	// AfterOrdinal is the last message of this session before the branch,
	// or -1 when the branch comes before the first message.
	AfterOrdinal int `json:"after_ordinal"`
	// AfterTurn counts this session's user prompts before the branch.
	AfterTurn int `json:"after_turn"`
	// TurnsAfter counts this session's user prompts after the branch.
	TurnsAfter int `json:"turns_after"`
	// Session is the fork or continuation; absent for a rewind.
	Session *db.Session `json:"session,omitempty"`
	// Inputs lists, in transcript order, the user inputs a rewind
	// abandoned; absent for forks and continuations.
	Inputs []string `json:"inputs,omitempty"`
}

func isBranchRelationship(relationship string) bool {
	return relationship == BranchKindFork ||
		relationship == BranchKindContinuation
}

// BranchPoints places branches through the local archive, which alone
// records fork points and rewound inputs. Other stores report none.
func (b *directBackend) BranchPoints(
	ctx context.Context, id string,
) (*BranchPoints, error) {
	session, err := b.db.GetSession(ctx, id)
	if err != nil || session == nil {
		return nil, err
	}
	out := &BranchPoints{SessionID: id, Points: []BranchPoint{}}
	if b.local == nil {
		return out, nil
	}
	if out.Leaves, err = b.SessionLeave(ctx, session); err != nil {
		return nil, err
	}

	children, err := b.db.GetChildSessions(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range children {
		child := &children[i]
		if !isBranchRelationship(child.RelationshipType) {
			continue
		}
		placement, ok, err := b.locateChild(ctx, id, child)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out.Points = append(out.Points, BranchPoint{
			Kind:         child.RelationshipType,
			AfterOrdinal: placement.AfterOrdinal,
			AfterTurn:    placement.AfterTurn,
			TurnsAfter:   placement.TurnsAfter,
			Session:      child,
		})
	}

	rewinds, err := b.rewindPoints(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Points = append(out.Points, rewinds...)
	slices.SortStableFunc(out.Points, func(a, c BranchPoint) int {
		return cmp.Or(
			cmp.Compare(a.AfterOrdinal, c.AfterOrdinal),
			cmp.Compare(branchKindOrder(a.Kind), branchKindOrder(c.Kind)),
			compareStartedAt(branchStart(a), branchStart(c)),
		)
	})
	return out, nil
}

// branchKindOrder lists rewinds first at a shared place: they happened in
// this transcript, before anything that left it.
func branchKindOrder(kind string) int {
	switch kind {
	case BranchKindRewind:
		return 0
	case BranchKindContinuation:
		return 1
	default:
		return 2
	}
}

func branchStart(p BranchPoint) *string {
	if p.Session == nil {
		return nil
	}
	return p.Session.StartedAt
}

func (b *directBackend) SessionLeave(
	ctx context.Context, session *db.Session,
) (*BranchLeave, error) {
	if b.local == nil || session == nil ||
		!isBranchRelationship(session.RelationshipType) ||
		session.ParentSessionID == nil || *session.ParentSessionID == "" {
		return nil, nil
	}
	parent, err := b.db.GetSession(ctx, *session.ParentSessionID)
	if err != nil || parent == nil {
		return nil, err
	}
	placement, ok, err := b.locateChild(ctx, parent.ID, session)
	if err != nil || !ok {
		return nil, err
	}
	return &BranchLeave{
		Parent:           *parent,
		RelationshipType: session.RelationshipType,
		AfterOrdinal:     placement.AfterOrdinal,
		AfterTurn:        placement.AfterTurn,
		TurnsAfter:       placement.TurnsAfter,
		Handoff:          db.IsClaudeHandoff(*session),
	}, nil
}

// locateChild places a child in its parent's transcript: at its recorded
// fork point, or else after the parent's last message before the child
// started.
func (b *directBackend) locateChild(
	ctx context.Context, parentID string, child *db.Session,
) (db.BranchPlacement, bool, error) {
	point, err := b.local.GetSessionForkPoint(ctx, child.ID)
	if err != nil {
		return db.BranchPlacement{}, false, err
	}
	before := ""
	if child.StartedAt != nil {
		before = *child.StartedAt
	}
	return b.local.LocateBranch(ctx, parentID, point, before)
}

// rewindPoints groups a session's rewound inputs by the path message they
// branched from. An input that followed another rewound input joins that
// input's group.
func (b *directBackend) rewindPoints(
	ctx context.Context, id string,
) ([]BranchPoint, error) {
	inputs, err := b.local.GetSessionRewoundInputs(ctx, id)
	if err != nil || len(inputs) == 0 {
		return nil, err
	}
	rewound := make(map[string]string, len(inputs))
	for _, input := range inputs {
		rewound[input.SourceUUID] = input.AfterUUID
	}
	anchorOf := func(after string) string {
		for range inputs {
			next, ok := rewound[after]
			if !ok {
				return after
			}
			after = next
		}
		return after
	}
	var anchors []string
	grouped := map[string][]string{}
	for _, input := range inputs {
		anchor := anchorOf(input.AfterUUID)
		if _, seen := grouped[anchor]; !seen {
			anchors = append(anchors, anchor)
		}
		grouped[anchor] = append(grouped[anchor], input.Content)
	}
	points := make([]BranchPoint, 0, len(anchors))
	for _, anchor := range anchors {
		placement, ok, err := b.local.LocateRewind(ctx, id, anchor)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		points = append(points, BranchPoint{
			Kind:         BranchKindRewind,
			AfterOrdinal: placement.AfterOrdinal,
			AfterTurn:    placement.AfterTurn,
			TurnsAfter:   placement.TurnsAfter,
			Inputs:       grouped[anchor],
		})
	}
	return points, nil
}

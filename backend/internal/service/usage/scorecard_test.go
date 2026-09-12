package usage

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scoring"
)

// stubScorecardStore is a hand-built fake for scorecardStore. Only the fields
// a test sets are non-zero; every unset method returns the store's zero
// value, which is what an absent fact looks like.
type stubScorecardStore struct {
	session domain.SessionRecord
	usage   domain.SessionUsageSummary
	effort  domain.SessionEffort
	calls   []domain.SessionToolCall
	pr      domain.PRFacts
	hasPR   bool
	hasConv bool
	turns   []domain.ConversationTurn
}

func (s stubScorecardStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.session, true, nil
}

func (s stubScorecardStore) GetSessionUsageSummary(context.Context, domain.SessionID) (domain.SessionUsageSummary, error) {
	return s.usage, nil
}

func (s stubScorecardStore) GetSessionEffort(context.Context, domain.SessionID) (domain.SessionEffort, bool, error) {
	return s.effort, true, nil
}

func (s stubScorecardStore) ListSessionToolCalls(context.Context, domain.SessionID) ([]domain.SessionToolCall, error) {
	return s.calls, nil
}

func (s stubScorecardStore) GetDisplayPRFactsForSession(context.Context, domain.SessionID) (domain.PRFacts, bool, error) {
	return s.pr, s.hasPR, nil
}

func (s stubScorecardStore) HasConversation(context.Context, domain.SessionID) (bool, error) {
	return s.hasConv, nil
}

func (s stubScorecardStore) ConversationTurns(context.Context, domain.SessionID) ([]domain.ConversationTurn, error) {
	return s.turns, nil
}

func ptr(v int64) *int64 { return &v }

func TestScorecardServiceRefusesUnsupportedHarness(t *testing.T) {
	service := NewScorecardService(stubScorecardStore{
		session: domain.SessionRecord{Harness: domain.HarnessDroid, ID: "sess-1"},
	})

	card, err := service.Get(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(card.Factors) != 0 {
		t.Fatalf("Factors = %+v, want none for an unmeasurable harness", card.Factors)
	}
}

func TestScorecardServicePassesDeliveryFactsThrough(t *testing.T) {
	service := NewScorecardService(stubScorecardStore{
		effort:  domain.SessionEffort{FilesChanged: 4, ToolCalls: 20},
		session: domain.SessionRecord{Harness: domain.HarnessOpenCode, ID: "sess-1"},
		usage: domain.SessionUsageSummary{Totals: domain.UsageMetricTotals{
			CachedInputTokens: ptr(400_000), InputTokens: ptr(500_000), OutputTokens: ptr(25_000),
		}},
	})

	card, err := service.Get(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if card.RubricVersion != scoring.RubricVersion {
		t.Fatalf("RubricVersion = %q, want %q", card.RubricVersion, scoring.RubricVersion)
	}
	var tokenFactor scoring.FactorScore
	for _, factor := range card.Factors {
		if factor.Factor == scoring.FactorTokenEfficiency {
			tokenFactor = factor
		}
	}
	if !tokenFactor.Present {
		t.Fatalf("token efficiency absent (%s), want present", tokenFactor.AbsentReason)
	}
}

func TestScorecardServiceReworkCountsExcludeUnreportedPaths(t *testing.T) {
	service := NewScorecardService(stubScorecardStore{
		session: domain.SessionRecord{Harness: domain.HarnessOpenCode, ID: "sess-1"},
		calls: []domain.SessionToolCall{
			{ToolName: "edit", InputSummary: "a.go"},
			{ToolName: "edit", InputSummary: "a.go"},
			{ToolName: "edit", InputSummary: "a.go"},
			{ToolName: "edit", InputSummary: "b.go"},
			{ToolName: "edit", InputSummary: ""},
			{ToolName: "read", InputSummary: "c.go"},
		},
	})

	card, err := service.Get(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var delivery scoring.FactorScore
	for _, factor := range card.Factors {
		if factor.Factor == scoring.FactorDeliveryEfficiency {
			delivery = factor
		}
	}
	if !delivery.Present {
		t.Fatalf("delivery efficiency absent (%s), want present", delivery.AbsentReason)
	}
	if got := delivery.Evidence["files_edited"]; got != 2 {
		t.Fatalf("files_edited = %v, want 2 (unreported path excluded)", got)
	}
	if got := delivery.Evidence["reworked_files"]; got != 1 {
		t.Fatalf("reworked_files = %v, want 1", got)
	}
}

func TestScorecardServiceCountsRejectedApprovals(t *testing.T) {
	service := NewScorecardService(stubScorecardStore{
		session: domain.SessionRecord{Harness: domain.HarnessOpenCode, ID: "sess-1"},
		calls: []domain.SessionToolCall{
			{ToolName: "bash", Outcome: domain.ToolOutcomeCompleted},
			{ToolName: "bash", Outcome: domain.ToolOutcomeDenied},
			{ToolName: "bash", Outcome: domain.ToolOutcomeDenied},
		},
		hasConv: true,
	})

	card, err := service.Get(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	steering := factorByFactorName(card, scoring.FactorSteeringLoad)
	if !steering.Present {
		t.Fatalf("steering load absent (%s), want present", steering.AbsentReason)
	}
	if got := steering.Evidence["rejected_approvals"]; got != 2 {
		t.Fatalf("rejected_approvals = %v, want 2", got)
	}
}

func TestScorecardServiceCountsTurnsAndInterrupts(t *testing.T) {
	service := NewScorecardService(stubScorecardStore{
		session: domain.SessionRecord{Harness: domain.HarnessOpenCode, ID: "sess-1"},
		hasConv: true,
		turns: []domain.ConversationTurn{
			{ID: "t1", State: domain.TurnStateCompleted},
			{ID: "t2", State: domain.TurnStateInterrupted},
			{ID: "t3", State: domain.TurnStateCompleted},
		},
	})

	card, err := service.Get(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	steering := factorByFactorName(card, scoring.FactorSteeringLoad)
	if !steering.Present {
		t.Fatalf("steering load absent (%s), want present", steering.AbsentReason)
	}
	if got := steering.Evidence["user_turns"]; got != 3 {
		t.Fatalf("user_turns = %v, want 3", got)
	}
	if got := steering.Evidence["interrupts"]; got != 1 {
		t.Fatalf("interrupts = %v, want 1", got)
	}
}

func factorByFactorName(card scoring.Scorecard, name scoring.Factor) scoring.FactorScore {
	for _, factor := range card.Factors {
		if factor.Factor == name {
			return factor
		}
	}
	return scoring.FactorScore{}
}

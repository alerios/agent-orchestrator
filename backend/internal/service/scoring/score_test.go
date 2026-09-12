package scoring_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scoring"
)

func ptr(v int64) *int64 { return &v }

func baseFacts() scoring.Facts {
	return scoring.Facts{
		CachedInputTokens:       ptr(400_000),
		CI:                      domain.CIPassing,
		FilesEdited:             10,
		HasCertifiedUsageSource: true,
		HasConversation:         true,
		HasPR:                   true,
		InputTokens:             ptr(500_000),
		OnBranch:                true,
		OutputTokens:            ptr(25_000),
		PRMerged:                true,
		Review:                  domain.ReviewApproved,
		ReworkedFiles:           1,
		UserTurns:               1,
	}
}

func factorByName(t *testing.T, card scoring.Scorecard, name scoring.Factor) scoring.FactorScore {
	t.Helper()
	for _, factor := range card.Factors {
		if factor.Factor == name {
			return factor
		}
	}
	t.Fatalf("factor %q missing from scorecard", name)
	return scoring.FactorScore{}
}

func TestScoreRefusesEverythingWithoutACertifiedSource(t *testing.T) {
	facts := baseFacts()
	facts.HasCertifiedUsageSource = false

	card := scoring.Score(facts)

	if len(card.Factors) != 0 {
		t.Fatalf("Factors = %+v, want none for an unmeasurable harness", card.Factors)
	}
	if card.Overall != nil {
		t.Fatalf("Overall = %v, want nil", card.Overall)
	}
	if card.RubricVersion != scoring.RubricVersion {
		t.Fatalf("RubricVersion = %q, want %q even when empty", card.RubricVersion, scoring.RubricVersion)
	}
}

func TestTokenEfficiencyRewardsCacheHits(t *testing.T) {
	facts := baseFacts()
	// 400k of 500k input from cache = 0.8 hit rate, the ceiling.
	card := scoring.Score(facts)
	got := factorByName(t, card, scoring.FactorTokenEfficiency)

	if !got.Present {
		t.Fatalf("Present = false (%s), want true", got.AbsentReason)
	}
	if got.Score != 100 {
		t.Fatalf("Score = %d, want 100 at the cache-hit ceiling", got.Score)
	}
	if got.Evidence["cache_hit_rate"] != 0.8 {
		t.Fatalf("evidence cache_hit_rate = %v, want 0.8", got.Evidence["cache_hit_rate"])
	}
}

func TestTokenEfficiencyAbsentWithoutTokenCounters(t *testing.T) {
	facts := baseFacts()
	facts.InputTokens = nil

	got := factorByName(t, scoring.Score(facts), scoring.FactorTokenEfficiency)

	if got.Present {
		t.Fatal("Present = true without input tokens, want false")
	}
	if got.Score != 0 || got.AbsentReason == "" {
		t.Fatalf("got = %+v, want score 0 with a stated reason", got)
	}
}

func TestTokenEfficiencyZeroInputIsAbsentNotPerfect(t *testing.T) {
	facts := baseFacts()
	facts.InputTokens = ptr(0)
	facts.CachedInputTokens = ptr(0)

	got := factorByName(t, scoring.Score(facts), scoring.FactorTokenEfficiency)

	if got.Present {
		t.Fatal("Present = true with zero input tokens, want false — there is nothing to rate")
	}
}

func TestExploratoryOverheadPenalizesReadingBeforeEditing(t *testing.T) {
	facts := baseFacts()
	facts.Calls = []domain.SessionToolCall{
		{ToolName: "read"}, {ToolName: "read"}, {ToolName: "grep"},
		{ToolName: "read"}, {ToolName: "read"}, {ToolName: "read"},
		{ToolName: "read"}, {ToolName: "edit"},
	}

	got := factorByName(t, scoring.Score(facts), scoring.FactorExploratoryOverhead)

	if !got.Present {
		t.Fatalf("Present = false (%s), want true", got.AbsentReason)
	}
	// 7 of 8 calls precede the first edit: 0.875, past the bad threshold.
	if got.Score != 0 {
		t.Fatalf("Score = %d, want 0 for 87.5%% pre-edit exploration", got.Score)
	}
}

func TestExploratoryOverheadAbsentWithoutAnyEdit(t *testing.T) {
	facts := baseFacts()
	facts.Calls = []domain.SessionToolCall{{ToolName: "read"}, {ToolName: "read"}}

	got := factorByName(t, scoring.Score(facts), scoring.FactorExploratoryOverhead)

	if got.Present {
		t.Fatal("Present = true for a session that never edited, want false — a research session has no overhead to measure")
	}
}

func TestSteeringLoadPerfectForOneShot(t *testing.T) {
	facts := baseFacts()
	facts.UserTurns = 1

	got := factorByName(t, scoring.Score(facts), scoring.FactorSteeringLoad)

	if got.Score != 100 {
		t.Fatalf("Score = %d, want 100 for a one-shot session", got.Score)
	}
}

func TestSteeringLoadCountsEveryInterventionKind(t *testing.T) {
	facts := baseFacts()
	facts.UserTurns = 4 // 3 beyond the initial prompt
	facts.Interrupts = 3
	facts.RejectedApprovals = 2
	facts.FollowUpsAfterDone = 2 // 10 events total: the floor

	got := factorByName(t, scoring.Score(facts), scoring.FactorSteeringLoad)

	if got.Score != 0 {
		t.Fatalf("Score = %d, want 0 at the steering floor", got.Score)
	}
	if got.Evidence["steering_events"] != 10 {
		t.Fatalf("evidence steering_events = %v, want 10", got.Evidence["steering_events"])
	}
}

func TestSteeringLoadAbsentWithoutConversation(t *testing.T) {
	facts := baseFacts()
	facts.HasConversation = false

	got := factorByName(t, scoring.Score(facts), scoring.FactorSteeringLoad)

	if got.Present {
		t.Fatal("Present = true without conversation facts, want false — a terminal session's turns are unobservable")
	}
}

func TestDeliveryEfficiencyPenalizesReworkAndCIRecovery(t *testing.T) {
	facts := baseFacts()
	facts.ReworkedFiles = 6
	facts.FilesEdited = 10 // 0.6 rework share: the bad threshold
	facts.CIRecoveries = 2 // 30 further points

	got := factorByName(t, scoring.Score(facts), scoring.FactorDeliveryEfficiency)

	if !got.Present {
		t.Fatalf("Present = false (%s), want true", got.AbsentReason)
	}
	if got.Score != 0 {
		t.Fatalf("Score = %d, want 0 with maximum rework plus CI recoveries", got.Score)
	}
}

func TestDeliveryEfficiencyAbsentWithoutEdits(t *testing.T) {
	facts := baseFacts()
	facts.FilesEdited = 0

	got := factorByName(t, scoring.Score(facts), scoring.FactorDeliveryEfficiency)

	if got.Present {
		t.Fatal("Present = true with no edits, want false")
	}
}

func TestGovernanceIsStructural(t *testing.T) {
	facts := baseFacts() // on a branch, has a PR, CI passing, approved
	got := factorByName(t, scoring.Score(facts), scoring.FactorGovernance)

	if got.Score != 100 {
		t.Fatalf("Score = %d, want 100 for branch + PR + CI green + approved", got.Score)
	}
}

func TestGovernanceIgnoresTestsRunHeuristic(t *testing.T) {
	withTests := baseFacts()
	withTests.Effort.TestsRun = 12
	withoutTests := baseFacts()
	withoutTests.Effort.TestsRun = 0

	a := factorByName(t, scoring.Score(withTests), scoring.FactorGovernance)
	b := factorByName(t, scoring.Score(withoutTests), scoring.FactorGovernance)

	if a.Score != b.Score {
		t.Fatalf("governance changed with tests_run (%d vs %d) — a custom test command must never be scored down", a.Score, b.Score)
	}
}

func TestGovernancePartialCredit(t *testing.T) {
	facts := baseFacts()
	facts.CI = domain.CIFailing
	facts.Review = domain.ReviewNone

	got := factorByName(t, scoring.Score(facts), scoring.FactorGovernance)

	if got.Score != 50 {
		t.Fatalf("Score = %d, want 50 for 2 of 4 governance facts", got.Score)
	}
}

func TestOverallIsNilWithTooFewFactors(t *testing.T) {
	facts := baseFacts()
	facts.InputTokens = nil       // drops token efficiency
	facts.HasConversation = false // drops steering
	facts.FilesEdited = 0         // drops delivery and exploration
	facts.Calls = nil

	card := scoring.Score(facts)

	present := 0
	for _, factor := range card.Factors {
		if factor.Present {
			present++
		}
	}
	if present >= 3 {
		t.Fatalf("present factors = %d, want fewer than 3 for this fixture", present)
	}
	if card.Overall != nil {
		t.Fatalf("Overall = %v, want nil with fewer than 3 present factors", card.Overall)
	}
}

func TestOverallIsWeighted(t *testing.T) {
	card := scoring.Score(baseFacts())
	if card.Overall == nil {
		t.Fatal("Overall = nil, want a value for a fully-evidenced session")
	}
	if *card.Overall < 0 || *card.Overall > 100 {
		t.Fatalf("Overall = %d, want 0..100", *card.Overall)
	}
}

func TestEveryFactorIsAlwaysListed(t *testing.T) {
	facts := baseFacts()
	facts.InputTokens = nil

	card := scoring.Score(facts)

	if len(card.Factors) != 5 {
		t.Fatalf("len(Factors) = %d, want all 5 listed (absent ones included, so the UI can say what is missing)", len(card.Factors))
	}
}

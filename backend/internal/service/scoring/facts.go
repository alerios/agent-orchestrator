// Package scoring computes efficiency scores from structured session facts.
//
// Every function here is pure: no I/O, no clock, no model calls. Scores are
// derived on read and never stored, so tuning the rubric takes effect
// everywhere at once.
//
// The package's central rule is that it refuses to fabricate. A factor whose
// evidence is missing returns Present=false with a reason, never a zero score
// and never a "low" rating. The rule lives here rather than in the UI so no
// caller can bypass it.
package scoring

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Facts is everything the scorer reads. Pointer fields are nil when AO could
// not measure them; that nil is what produces an absent factor.
type Facts struct {
	// HasCertifiedUsageSource is false for a harness AO cannot measure at all.
	// When false, Score returns an empty Scorecard.
	HasCertifiedUsageSource bool

	// Token counters, from the usage summary.
	InputTokens       *int64
	CachedInputTokens *int64
	OutputTokens      *int64

	// Effort and tool facts, from the Phase 3 rollup.
	Effort  domain.SessionEffort
	ToolMix []domain.ToolMixEntry
	Calls   []domain.SessionToolCall

	// Conversation facts, for steering load.
	UserTurns          int64
	Interrupts         int64
	RejectedApprovals  int64
	FollowUpsAfterDone int64
	HasConversation    bool

	// Delivery facts AO owns outright.
	HasPR            bool
	PRMerged         bool
	CI               domain.CIState
	Review           domain.ReviewDecision
	ReviewRoundTrips int64
	CIRecoveries     int64
	OnBranch         bool
	SessionStart     time.Time
	FirstPRAt        *time.Time
	// GovernanceAbsentReason, when non-empty, marks the whole governance
	// factor absent rather than letting an unresolved fact (e.g. a branch
	// state that is genuinely unknown, as opposed to a scratch project's
	// legitimate "no branch") silently score as a deliberate false.
	GovernanceAbsentReason string

	// Rework: edit targets (grouped by the tool call's InputSummary — a
	// free-form title, not necessarily a file path) hit three or more times.
	RepeatedEditTargets int64
	// EditTargets is the denominator for rework; zero means no edits observed.
	EditTargets int64
}

// Evidence is the raw numbers that produced a score, keyed by a stable name.
// It is always rendered next to the score: a number the user cannot audit is
// worse than no number.
type Evidence map[string]float64

// Factor identifies one rubric dimension.
type Factor string

// The five deterministic factors. The rubric's four prose-reading factors are
// out of scope by design — see the spec's non-goals.
const (
	FactorTokenEfficiency     Factor = "token_efficiency"
	FactorExploratoryOverhead Factor = "exploratory_overhead"
	FactorSteeringLoad        Factor = "steering_load"
	FactorDeliveryEfficiency  Factor = "delivery_efficiency"
	FactorGovernance          Factor = "governance"
)

// FactorScore is one factor's result. When Present is false, Score is
// meaningless and AbsentReason says what was missing.
type FactorScore struct {
	AbsentReason string
	Evidence     Evidence
	Factor       Factor
	Present      bool
	Score        int
}

// Scorecard is the full result. Overall is nil unless enough factors are
// present to make a weighted average honest.
type Scorecard struct {
	Factors       []FactorScore
	Overall       *int
	RubricVersion string
}

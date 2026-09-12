package scoring

// RubricVersion identifies this threshold set. Bump it whenever a threshold or
// weight below changes, so a user comparing two readings can tell whether the
// rubric moved underneath them. Scores are not stored, so there is nothing to
// migrate — only to disclose.
const RubricVersion = "1.0.0"

// minFactorsForOverall is how many factors must be present before a weighted
// overall score is honest. Below this, Overall stays nil.
const minFactorsForOverall = 3

// factorWeights weight the overall average. Delivery and governance weigh
// most: they measure whether the work actually landed, which is the outcome AO
// uniquely knows.
var factorWeights = map[Factor]float64{
	FactorDeliveryEfficiency:  2.0,
	FactorGovernance:          1.5,
	FactorSteeringLoad:        1.5,
	FactorTokenEfficiency:     1.0,
	FactorExploratoryOverhead: 1.0,
}

// Token efficiency: cache hit rate is the dominant lever on cost. A session
// reading most of its input from cache is being driven well.
const (
	cacheHitRateFloor = 0.10 // at or below this, score 0
	cacheHitRateCeil  = 0.80 // at or above this, score 100
)

// Exploratory overhead: the share of tool calls spent reading and searching
// BEFORE the first edit. Some exploration is necessary; endless exploration is
// the signature of an underspecified task.
const (
	exploreShareGood = 0.20 // at or below this, score 100
	exploreShareBad  = 0.70 // at or above this, score 0
)

// Steering load: how many times a person had to intervene. Zero
// interventions after the initial prompt is a perfect score.
const (
	steeringEventsPerfect = 0  // score 100
	steeringEventsFloor   = 10 // at or above this, score 0
)

// Delivery efficiency: rework share plus CI recovery cycles. A session that
// edits the same files repeatedly and fights CI is burning budget.
const (
	reworkShareGood   = 0.10
	reworkShareBad    = 0.60
	ciRecoveryPenalty = 15 // points per CI recovery cycle, capped at 60
	ciRecoveryCap     = 60
)

// Governance is structural: five facts AO owns. Each contributes equally.
// Tests-run is deliberately NOT one of them — it is a command-name heuristic,
// so a project with a custom test command must never be scored down for it.
// CI passing is the evidence for "was this verified".
const governanceFactCount = 4

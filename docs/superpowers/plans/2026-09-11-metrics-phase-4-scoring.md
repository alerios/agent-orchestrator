# Metrics Phase 4: Efficiency Scoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Score each session on five efficiency factors computed entirely from structured facts, with the raw numbers shown beside every score, and absent factors rendered as absent rather than zero.

**Architecture:** A new package of pure functions over fact structs — no I/O, no persistence, no model calls. Scores are computed on read from the usage totals, the effort rollup, the tool mix and AO's own delivery facts (PR, CI, review, merge). Thresholds live in one versioned table inside the package.

**Tech Stack:** Go (pure functions, table-driven tests), TypeScript/React, Vitest, openapi-typescript.

**Spec:** `docs/superpowers/specs/2026-09-11-session-metrics-and-efficiency-scoring-design.md`

**Depends on:** Phase 1 (Metrics tab), Phase 3 (`domain.SessionEffort`, `domain.ToolMixEntry`, `domain.SessionToolCall`).

## Global Constraints

- **No LLM.** Nothing in this phase calls a model. If a factor cannot be computed from structured facts, it is not in scope.
- **No persistence.** Scores are computed on read. Do not add a table, a cache, or a stored score column.
- **Refusal to fabricate, enforced in the scoring package — not the UI.** A factor with insufficient evidence returns *absent*, never 0 and never "low". A session with no certified usage source gets no scores at all. No future caller may bypass this, which is why it lives below the API boundary.
- **Every score ships with its raw inputs.** A bare number is unfalsifiable. The response carries the evidence that produced each score.
- Scores are `0..100` integers. Higher is better for every factor, so the display needs no per-factor polarity rule.
- The rubric version is reported with every response so two screenshots taken weeks apart can be compared honestly.
- Pure functions only in `service/scoring`: no `context.Context`, no store, no clock. Pass time in.
- **Read `DESIGN.md` before any visual decision**, starting with its "clone agent-orchestrator verbatim" banner — that banner governs the current look and supersedes the older design-reference framing. Build new UI from the existing `@aoagents/product-ui` primitives and `components/ui/*` shadcn components where one fits; do not introduce new visual patterns without explicit approval.
- When demoing a frontend change, run `ao preview [url]` from inside the session so it renders in the inspector rail's Browser tab, and say "check the Browser tab" in your reply — the panel badges as unseen rather than stealing focus.
- No hardcoded English in `frontend/src/renderer`; all copy via i18n keys in all eight locale files.
- Run `npm run lint` before every backend commit; `npm run api` after changing response types.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/service/scoring/rubric.go` | Create: rubric version, thresholds, weights — the one place tuning happens |
| `backend/internal/service/scoring/facts.go` | Create: the input fact struct and the score/evidence output types |
| `backend/internal/service/scoring/score.go` | Create: the five factor functions and the top-level `Score` |
| `backend/internal/service/scoring/score_test.go` | Create: table-driven tests over fixture sessions |
| `backend/internal/service/usage/scorecard.go` | Create: assembles facts from stores and calls `scoring.Score` |
| `backend/internal/httpd/controllers/usage.go` | Modify: add the scorecard to the effort response |
| `frontend/src/renderer/components/SessionInspectorMetrics.tsx` | Modify: the Efficiency block |
| `frontend/src/renderer/i18n/*.json` | Modify: new keys, all 8 locales |

`scoring` is a leaf package with no dependency on storage or HTTP. That is what makes the refusal-to-fabricate rule testable in isolation.

---

### Task 1: The rubric and fact types

**Files:**
- Create: `backend/internal/service/scoring/rubric.go`
- Create: `backend/internal/service/scoring/facts.go`
- Test: `backend/internal/service/scoring/rubric_test.go`

**Interfaces:**
- Consumes: `domain.SessionEffort`, `domain.ToolMixEntry`, `domain.SessionToolCall` (Phase 3 Task 1); `domain.CIState`, `domain.ReviewDecision` (existing, `backend/pkg/contract/status.go`).
- Produces:
  - `const RubricVersion = "1.0.0"`
  - `type Factor string` with `FactorTokenEfficiency`, `FactorExploratoryOverhead`, `FactorSteeringLoad`, `FactorDeliveryEfficiency`, `FactorGovernance`
  - `type Facts struct { ... }` (full definition below)
  - `type Evidence map[string]float64`
  - `type FactorScore struct { Factor Factor; Score int; Present bool; AbsentReason string; Evidence Evidence }`
  - `type Scorecard struct { RubricVersion string; Factors []FactorScore; Overall *int }`

Tasks 2-4 consume all of these.

- [ ] **Step 1: Write the fact and output types**

Create `backend/internal/service/scoring/facts.go`:

```go
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
	Effort   domain.SessionEffort
	ToolMix  []domain.ToolMixEntry
	Calls    []domain.SessionToolCall

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

	// Rework: files edited three or more times.
	ReworkedFiles int64
	// FilesEdited is the denominator for rework; zero means no edits observed.
	FilesEdited int64
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
```

- [ ] **Step 2: Write the rubric**

Create `backend/internal/service/scoring/rubric.go`:

```go
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

// Governance is structural: four facts AO owns. Each contributes equally.
// Tests-run is deliberately NOT one of them — it is a command-name heuristic,
// so a project with a custom test command must never be scored down for it.
// CI passing is the evidence for "was this verified".
const governanceFactCount = 4
```

- [ ] **Step 3: Write the failing test**

Create `backend/internal/service/scoring/rubric_test.go`:

```go
func TestFactorWeightsCoverEveryFactor(t *testing.T) {
	factors := []scoring.Factor{
		scoring.FactorTokenEfficiency,
		scoring.FactorExploratoryOverhead,
		scoring.FactorSteeringLoad,
		scoring.FactorDeliveryEfficiency,
		scoring.FactorGovernance,
	}
	for _, factor := range factors {
		if scoring.WeightForTest(factor) <= 0 {
			t.Errorf("weight for %q = 0, want a positive weight", factor)
		}
	}
}

func TestRubricVersionIsSet(t *testing.T) {
	if scoring.RubricVersion == "" {
		t.Fatal("RubricVersion is empty")
	}
}
```

Add `backend/internal/service/scoring/export_test.go`:

```go
package scoring

// WeightForTest exposes the unexported weight table to the package's
// external test file.
func WeightForTest(f Factor) float64 { return factorWeights[f] }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./internal/service/scoring/ -v`

Expected: PASS. (These are guard tests over the tables, so they pass as soon as the tables exist — their value is failing later if a factor is added without a weight.)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/scoring
git commit -m "feat(scoring): add rubric thresholds and fact types"
```

---

### Task 2: The five factor functions

**Files:**
- Create: `backend/internal/service/scoring/score.go`
- Create: `backend/internal/service/scoring/score_test.go`

**Interfaces:**
- Consumes: everything from Task 1.
- Produces: `func Score(facts Facts) Scorecard`.

Task 3 calls `Score`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/service/scoring/score_test.go`:

```go
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
	facts.UserTurns = 4          // 3 beyond the initial prompt
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
	facts.InputTokens = nil      // drops token efficiency
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
```

Check the real constant names for review decisions before running — `grep -n 'Review[A-Z]' backend/pkg/contract/status.go` — and substitute the actual `ReviewApproved` / `ReviewNone` spellings.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/service/scoring/ -v`

Expected: FAIL — `scoring.Score` undefined.

- [ ] **Step 3: Implement the scorer**

Create `backend/internal/service/scoring/score.go`:

```go
package scoring

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Score computes the scorecard for one session.
//
// Every factor is always listed so the UI can explain what is missing. A
// factor with insufficient evidence carries Present=false and a reason: the
// package never substitutes zero for unknown.
func Score(facts Facts) Scorecard {
	card := Scorecard{RubricVersion: RubricVersion}
	if !facts.HasCertifiedUsageSource {
		// An unmeasurable harness gets no scores at all, not low scores.
		return card
	}
	card.Factors = []FactorScore{
		scoreTokenEfficiency(facts),
		scoreExploratoryOverhead(facts),
		scoreSteeringLoad(facts),
		scoreDeliveryEfficiency(facts),
		scoreGovernance(facts),
	}
	card.Overall = weightedOverall(card.Factors)
	return card
}

func absent(factor Factor, reason string) FactorScore {
	return FactorScore{AbsentReason: reason, Factor: factor}
}

func present(factor Factor, score int, evidence Evidence) FactorScore {
	return FactorScore{Evidence: evidence, Factor: factor, Present: true, Score: clampScore(score)}
}

func scoreTokenEfficiency(facts Facts) FactorScore {
	if facts.InputTokens == nil || facts.CachedInputTokens == nil {
		return absent(FactorTokenEfficiency, "token counters not reported")
	}
	total := *facts.InputTokens
	if total <= 0 {
		// No input means nothing to rate. Calling that 100 would flatter an
		// empty session.
		return absent(FactorTokenEfficiency, "no input tokens observed")
	}
	rate := float64(*facts.CachedInputTokens) / float64(total)
	evidence := Evidence{
		"cache_hit_rate":      rate,
		"cached_input_tokens": float64(*facts.CachedInputTokens),
		"input_tokens":        float64(total),
	}
	if facts.OutputTokens != nil {
		evidence["output_tokens"] = float64(*facts.OutputTokens)
	}
	return present(FactorTokenEfficiency, scaleScore(rate, cacheHitRateFloor, cacheHitRateCeil), evidence)
}

func scoreExploratoryOverhead(facts Facts) FactorScore {
	if len(facts.Calls) == 0 {
		return absent(FactorExploratoryOverhead, "no tool calls recorded")
	}
	firstEdit := -1
	for index, call := range facts.Calls {
		if isEditTool(call.ToolName) {
			firstEdit = index
			break
		}
	}
	if firstEdit < 0 {
		// A research session produced no edit, so there is no "overhead before
		// real work" to measure. Absent, not bad.
		return absent(FactorExploratoryOverhead, "session made no edits")
	}
	var explored float64
	for _, call := range facts.Calls[:firstEdit] {
		if isReadTool(call.ToolName) {
			explored++
		}
	}
	share := explored / float64(len(facts.Calls))
	return present(
		FactorExploratoryOverhead,
		scaleScore(share, exploreShareBad, exploreShareGood),
		Evidence{
			"explore_share":    share,
			"pre_edit_reads":   explored,
			"total_tool_calls": float64(len(facts.Calls)),
		},
	)
}

func scoreSteeringLoad(facts Facts) FactorScore {
	if !facts.HasConversation {
		return absent(FactorSteeringLoad, "conversation turns not observable for this session")
	}
	extraTurns := facts.UserTurns - 1
	if extraTurns < 0 {
		extraTurns = 0
	}
	events := float64(extraTurns + facts.Interrupts + facts.RejectedApprovals + facts.FollowUpsAfterDone)
	return present(
		FactorSteeringLoad,
		scaleScore(events, float64(steeringEventsFloor), float64(steeringEventsPerfect)),
		Evidence{
			"follow_ups_after_done": float64(facts.FollowUpsAfterDone),
			"interrupts":            float64(facts.Interrupts),
			"rejected_approvals":    float64(facts.RejectedApprovals),
			"steering_events":       events,
			"user_turns":            float64(facts.UserTurns),
		},
	)
}

func scoreDeliveryEfficiency(facts Facts) FactorScore {
	if facts.FilesEdited <= 0 {
		return absent(FactorDeliveryEfficiency, "no file edits observed")
	}
	reworkShare := float64(facts.ReworkedFiles) / float64(facts.FilesEdited)
	score := scaleScore(reworkShare, reworkShareBad, reworkShareGood)

	penalty := int(facts.CIRecoveries) * ciRecoveryPenalty
	if penalty > ciRecoveryCap {
		penalty = ciRecoveryCap
	}
	score -= penalty

	evidence := Evidence{
		"ci_recoveries":      float64(facts.CIRecoveries),
		"files_edited":       float64(facts.FilesEdited),
		"rework_share":       reworkShare,
		"reworked_files":     float64(facts.ReworkedFiles),
		"review_round_trips": float64(facts.ReviewRoundTrips),
	}
	if facts.FirstPRAt != nil && !facts.SessionStart.IsZero() {
		evidence["seconds_to_first_pr"] = facts.FirstPRAt.Sub(facts.SessionStart).Seconds()
	}
	return present(FactorDeliveryEfficiency, score, evidence)
}

// scoreGovernance is structural, not judged: four facts AO owns outright.
// Tests-run is excluded on purpose — it is a command-name heuristic, and a
// project with a custom test command must never lose points for it.
func scoreGovernance(facts Facts) FactorScore {
	satisfied := 0
	if facts.OnBranch {
		satisfied++
	}
	if facts.HasPR {
		satisfied++
	}
	if facts.CI == domain.CIPassing {
		satisfied++
	}
	if facts.Review == domain.ReviewApproved {
		satisfied++
	}
	return present(
		FactorGovernance,
		satisfied*100/governanceFactCount,
		Evidence{
			"ci_passing":        boolEvidence(facts.CI == domain.CIPassing),
			"has_pr":            boolEvidence(facts.HasPR),
			"on_branch":         boolEvidence(facts.OnBranch),
			"review_approved":   boolEvidence(facts.Review == domain.ReviewApproved),
			"satisfied_facts":   float64(satisfied),
		},
	)
}

func weightedOverall(factors []FactorScore) *int {
	var (
		weighted float64
		weights  float64
		count    int
	)
	for _, factor := range factors {
		if !factor.Present {
			continue
		}
		weight := factorWeights[factor.Factor]
		weighted += float64(factor.Score) * weight
		weights += weight
		count++
	}
	if count < minFactorsForOverall || weights == 0 {
		return nil
	}
	overall := clampScore(int(weighted/weights + 0.5))
	return &overall
}

// scaleScore maps value linearly onto 0..100 between the score-0 bound and the
// score-100 bound. The bounds may be given in either order, so a metric where
// lower is better (a share of wasted calls) and one where higher is better (a
// cache hit rate) both use this one function.
func scaleScore(value, zeroAt, hundredAt float64) int {
	if zeroAt == hundredAt {
		return 0
	}
	ratio := (value - zeroAt) / (hundredAt - zeroAt)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return int(ratio*100 + 0.5)
}

func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func boolEvidence(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func isEditTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit", "write", "multiedit", "patch":
		return true
	default:
		return false
	}
}

func isReadTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "grep", "glob", "search":
		return true
	default:
		return false
	}
}
```

`isEditTool`/`isReadTool` duplicate the maps in `observe/usage/effort.go`. Rather than importing across package boundaries in the wrong direction (scoring must stay a leaf), move the shared classification into `domain/toolcall.go` and have both packages call it. Add exactly this to `backend/internal/domain/toolcall.go`:

```go
// ToolKind is the coarse classification of a tool by what it does. Both the
// effort derivation and the scorer need the same buckets, so the mapping lives
// here rather than being duplicated per package.
type ToolKind string

// Tool kinds. ToolKindOther covers everything unclassified.
const (
	ToolKindRead    ToolKind = "read"
	ToolKindEdit    ToolKind = "edit"
	ToolKindCommand ToolKind = "command"
	ToolKindOther   ToolKind = "other"
)

// ClassifyTool buckets a provider's tool name. Matching is case-insensitive
// and trimmed because providers disagree on casing ("Read" vs "read").
func ClassifyTool(name string) ToolKind {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "grep", "glob", "search":
		return ToolKindRead
	case "edit", "write", "multiedit", "patch":
		return ToolKindEdit
	case "bash", "shell", "run", "terminal":
		return ToolKindCommand
	default:
		return ToolKindOther
	}
}
```

Then replace `scoring`'s `isEditTool(name)` with `domain.ClassifyTool(name) == domain.ToolKindEdit` and `isReadTool(name)` with `domain.ClassifyTool(name) == domain.ToolKindRead`, deleting both local helpers. In Phase 3's `observe/usage/effort.go`, delete `readToolNames`, `editToolNames` and `commandToolNames` and switch on `domain.ClassifyTool(call.ToolName)` instead. Phase 3's `effort_test.go` must stay green unchanged — it asserts behaviour, not the mechanism.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/service/scoring/ -v`

Expected: PASS, all sixteen tests.

- [ ] **Step 5: Confirm the shared classification refactor did not break Phase 3**

Run: `cd backend && go test ./internal/observe/usage/ -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/scoring backend/internal/domain/toolcall.go backend/internal/observe/usage/effort.go
git commit -m "feat(scoring): compute five deterministic efficiency factors"
```

---

### Task 3: Assemble facts and expose the scorecard

**Files:**
- Create: `backend/internal/service/usage/scorecard.go`
- Modify: `backend/internal/httpd/controllers/usage.go`
- Test: `backend/internal/service/usage/scorecard_test.go`

**Interfaces:**
- Consumes: `scoring.Score`, `scoring.Facts` (Task 2); the effort/tool-mix stores (Phase 3 Task 4); the session read model for PR/CI/review facts.
- Produces: the effort endpoint response gains
  `scorecard: { rubricVersion: string, overall: number | null, factors: [{ factor, score, present, absentReason, evidence }] }`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/service/usage/scorecard_test.go`:

```go
func TestScorecardServiceRefusesUnsupportedHarness(t *testing.T) {
	service := usage.NewScorecardService(stubScorecardStore{
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
	service := usage.NewScorecardService(stubScorecardStore{
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
```

Build `stubScorecardStore` against whatever store interface you define in Step 2.

- [ ] **Step 2: Implement the assembly service**

Create `backend/internal/service/usage/scorecard.go`. It reads the session record, the usage summary, the effort rollup, the tool calls and the PR facts, maps them onto `scoring.Facts`, and calls `scoring.Score`. Two rules to hold:

```go
	facts := scoring.Facts{
		// SupportedHarness is the single source of truth for measurability, so
		// scoring and ingestion can never disagree about a harness.
		HasCertifiedUsageSource: SupportedHarness(session.Harness),
		...
	}
```

and, for rework, derive `ReworkedFiles`/`FilesEdited` by counting edit-tool calls grouped by their `InputSummary` path — files with three or more edit calls are reworked. Where `InputSummary` is empty (a source that did not report the path), exclude the call from both numerator and denominator rather than guessing, and if that leaves `FilesEdited` at zero the delivery factor correctly reports absent.

Resolve `HasConversation` from whether AO holds structured conversation activities for the session — Chat-mode sessions do, terminal sessions may not. Do not infer it from `UserTurns > 0`, which would silently score a terminal session as a perfect one-shot.

- [ ] **Step 3: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/service/usage/ -run Scorecard -v`

Expected: PASS.

- [ ] **Step 4: Add the scorecard to the effort response**

In `controllers/usage.go`, extend `SessionEffortResponse`:

```go
// ScorecardResponse is the efficiency scorecard. Every factor is listed even
// when absent, so the UI can state what could not be measured.
type ScorecardResponse struct {
	Factors       []FactorScoreResponse `json:"factors"`
	Overall       *int                  `json:"overall"`
	RubricVersion string                `json:"rubricVersion"`
}

// FactorScoreResponse is one factor. Score is meaningless when present is
// false; absentReason then says what was missing.
type FactorScoreResponse struct {
	AbsentReason string             `json:"absentReason"`
	Evidence     map[string]float64 `json:"evidence"`
	Factor       string             `json:"factor"`
	Present      bool               `json:"present"`
	Score        int                `json:"score"`
}
```

Add `Scorecard ScorecardResponse \`json:"scorecard"\`` to `SessionEffortResponse` and populate it in `getSessionEffort`.

- [ ] **Step 5: Regenerate the contract**

Run: `npm run api`

Expected: `openapi.yaml` and `schema.ts` both gain the scorecard types.

- [ ] **Step 6: Run the full backend suite and linter**

Run: `npm run lint`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend frontend/src/api/schema.ts
git commit -m "feat(api): expose the efficiency scorecard on the effort endpoint"
```

---

### Task 4: Render the Efficiency block

**Files:**
- Modify: `frontend/src/renderer/components/SessionInspectorMetrics.tsx`
- Modify: `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx`
- Modify: `frontend/src/renderer/i18n/*.json`

**Interfaces:**
- Consumes: the `scorecard` field on the effort response (Task 3).
- Produces: no exported API.

- [ ] **Step 1: Add the i18n keys**

```bash
python3 - <<'PY'
import json, pathlib

keys = {
    "inspector.metrics.scores.title": "Efficiency",
    "inspector.metrics.scores.overall": "Overall",
    "inspector.metrics.scores.overallUnavailable": "Too few measurable factors for an overall score.",
    "inspector.metrics.scores.rubric": "Rubric {{version}}",
    "inspector.metrics.scores.unavailable": "Not measurable: {{reason}}",
    "inspector.metrics.scores.noScores": "AO cannot score this session: it has no measurable usage source.",
    "inspector.metrics.scores.factor.token_efficiency": "Token efficiency",
    "inspector.metrics.scores.factor.exploratory_overhead": "Exploratory overhead",
    "inspector.metrics.scores.factor.steering_load": "Human steering",
    "inspector.metrics.scores.factor.delivery_efficiency": "Delivery efficiency",
    "inspector.metrics.scores.factor.governance": "Governance",
}

for path in sorted(pathlib.Path("frontend/src/renderer/i18n").glob("*.json")):
    data = json.loads(path.read_text(encoding="utf-8"))
    for key, value in keys.items():
        data.setdefault(key, value)
    path.write_text(
        json.dumps({k: data[k] for k in sorted(data)}, ensure_ascii=False, indent="\t") + "\n",
        encoding="utf-8",
    )
    print("updated", path)
PY
```

Verify: `grep -l '"inspector.metrics.scores.title"' frontend/src/renderer/i18n/*.json | wc -l` → `8`

- [ ] **Step 2: Write the failing test**

Add to `SessionInspectorMetrics.test.tsx`:

```tsx
it("shows each score with the raw numbers that produced it", () => {
  mockEffort = {
    data: {
      effort: baseEffort,
      toolMix: [],
      scorecard: {
        factors: [
          {
            absentReason: "",
            evidence: { cache_hit_rate: 0.8, input_tokens: 500000 },
            factor: "token_efficiency",
            present: true,
            score: 100,
          },
        ],
        overall: 84,
        rubricVersion: "1.0.0",
      },
    },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText("100")).toBeInTheDocument();
  // The evidence must be on screen: an unauditable score is worse than none.
  expect(screen.getByTestId("score-evidence-token_efficiency")).toHaveTextContent("0.8");
  expect(screen.getByText(/Rubric 1\.0\.0/)).toBeInTheDocument();
});

it("states why an absent factor is absent instead of scoring it zero", () => {
  mockEffort = {
    data: {
      effort: baseEffort,
      toolMix: [],
      scorecard: {
        factors: [
          {
            absentReason: "token counters not reported",
            evidence: {},
            factor: "token_efficiency",
            present: false,
            score: 0,
          },
        ],
        overall: null,
        rubricVersion: "1.0.0",
      },
    },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/token counters not reported/i)).toBeInTheDocument();
  expect(screen.queryByTestId("score-value-token_efficiency")).not.toBeInTheDocument();
  expect(screen.getByText(/Too few measurable factors/i)).toBeInTheDocument();
});

it("says it cannot score a session with no measurable source", () => {
  mockEffort = {
    data: {
      effort: baseEffort,
      toolMix: [],
      scorecard: { factors: [], overall: null, rubricVersion: "1.0.0" },
    },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/cannot score this session/i)).toBeInTheDocument();
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: FAIL — no Efficiency block.

- [ ] **Step 4: Render the block**

Add to `SessionInspectorMetrics.tsx`, placed after Tool mix and before Coverage:

```tsx
function ScoresBlock({ scorecard }: { scorecard: SessionEffort["scorecard"] }) {
	const { t } = useTranslation();
	if (scorecard.factors.length === 0) {
		return (
			<Section title={t("inspector.metrics.scores.title")}>
				<p className={inspectorEmptyClass}>{t("inspector.metrics.scores.noScores")}</p>
			</Section>
		);
	}
	return (
		<Section title={t("inspector.metrics.scores.title")}>
			<div className="mb-1.5 flex items-baseline justify-between gap-2">
				<span className="text-2xs text-settings-muted">{t("inspector.metrics.scores.overall")}</span>
				<span className="font-semibold">
					{scorecard.overall === null ? "—" : scorecard.overall}
				</span>
			</div>
			{scorecard.overall === null ? (
				<p className={inspectorEmptyClass}>{t("inspector.metrics.scores.overallUnavailable")}</p>
			) : null}
			<ul className="flex flex-col gap-1.5">
				{scorecard.factors.map((factor) => (
					<li className="flex flex-col gap-0.5" key={factor.factor}>
						<div className="flex items-baseline justify-between gap-2">
							<span className="truncate">
								{t(`inspector.metrics.scores.factor.${factor.factor}` as MessageKey)}
							</span>
							{factor.present ? (
								<span className="shrink-0 font-semibold" data-testid={`score-value-${factor.factor}`}>
									{factor.score}
								</span>
							) : null}
						</div>
						{factor.present ? (
							// The evidence is not optional detail: it is what makes the
							// score auditable.
							<span
								className="text-2xs text-settings-muted"
								data-testid={`score-evidence-${factor.factor}`}
							>
								{Object.entries(factor.evidence)
									.map(([name, value]) => `${name} ${formatEvidenceValue(value)}`)
									.join(" · ")}
							</span>
						) : (
							<span className={inspectorEmptyClass}>
								{t("inspector.metrics.scores.unavailable", { reason: factor.absentReason })}
							</span>
						)}
					</li>
				))}
			</ul>
			<p className={`mt-1.5 ${inspectorEmptyClass}`}>
				{t("inspector.metrics.scores.rubric", { version: scorecard.rubricVersion })}
			</p>
		</Section>
	);
}

function formatEvidenceValue(value: number): string {
	if (Number.isInteger(value)) return String(value);
	return value.toFixed(2);
}
```

`MessageKey` is the existing i18n key type in this codebase — check how `SessionInspector.tsx` imports it and follow suit. If a dynamic key breaks the typed-key check, add a `const factorLabelKeys: Record<string, MessageKey>` map instead of template interpolation.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: PASS, all tests.

- [ ] **Step 6: Verify typecheck and the copy rule**

Run: `npm run frontend:typecheck && npm --prefix frontend test -- renderer-coverage`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/renderer
git commit -m "feat(inspector): render efficiency scores with their evidence"
```

---

### Task 5: Verify the scores are defensible

**Files:** none — manual gate. This task exists because a plausible-looking wrong score is the main risk of this phase.

- [ ] **Step 1: Score a clean session**

Open the Metrics tab for a session that went straight to a merged PR with little back-and-forth.

Expected: high delivery efficiency, high governance, low steering load count. Check each evidence figure against the session's real history — the PR, the CI run, the conversation.

- [ ] **Step 2: Score a heavily-steered session**

Open one where you intervened repeatedly.

Expected: steering load is visibly lower, and `steering_events` in the evidence matches roughly how many times you actually intervened. If it does not, the fact assembly in Task 3 is wrong — fix it rather than adjusting thresholds.

- [ ] **Step 3: Score an unsupported harness**

Expected: "AO cannot score this session". No factors, no zeros, no overall.

- [ ] **Step 4: Score a local-model opencode session**

Expected: scores present (tokens are measured) with cost `$0.00`. Token efficiency must not be penalised for a cache hit rate of zero if the local provider does no caching — if it is, add an absent-reason branch for providers that report no cache counters at all, since punishing a provider for lacking a feature is fabrication of a different kind.

- [ ] **Step 5: Record findings and commit fixes**

```bash
git add -A
git commit -m "fix(scoring): address scorecard verification findings"
```

---

## Phase Completion

```bash
npm run lint
npm run frontend:typecheck
npm --prefix frontend test
```

All three must pass. Phase 5 (the orchestrator rail) reuses `scoring.Score` unchanged and adds only project-scoped aggregation.

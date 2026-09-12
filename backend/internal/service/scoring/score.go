package scoring

import (
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
		if domain.ClassifyTool(call.ToolName) == domain.ToolKindEdit {
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
		if domain.ClassifyTool(call.ToolName) == domain.ToolKindRead {
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
	if facts.EditTargets <= 0 {
		return absent(FactorDeliveryEfficiency, "no file edits observed")
	}
	reworkShare := float64(facts.RepeatedEditTargets) / float64(facts.EditTargets)
	score := scaleScore(reworkShare, reworkShareBad, reworkShareGood)

	penalty := int(facts.CIRecoveries) * ciRecoveryPenalty
	if penalty > ciRecoveryCap {
		penalty = ciRecoveryCap
	}
	score -= penalty

	evidence := Evidence{
		"ci_recoveries":         float64(facts.CIRecoveries),
		"edit_targets":          float64(facts.EditTargets),
		"repeated_edit_targets": float64(facts.RepeatedEditTargets),
		"rework_share":          reworkShare,
		"review_round_trips":    float64(facts.ReviewRoundTrips),
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
	if facts.GovernanceAbsentReason != "" {
		return absent(FactorGovernance, facts.GovernanceAbsentReason)
	}
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
			"ci_passing":      boolEvidence(facts.CI == domain.CIPassing),
			"has_pr":          boolEvidence(facts.HasPR),
			"on_branch":       boolEvidence(facts.OnBranch),
			"review_approved": boolEvidence(facts.Review == domain.ReviewApproved),
			"satisfied_facts": float64(satisfied),
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

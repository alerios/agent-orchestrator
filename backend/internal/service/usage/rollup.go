package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// OrchestratorEfficiency is the derived answer to "is this orchestration
// shape paying off". Every pointer field is nil when the evidence does not
// support it: a missing ratio is more useful than a fabricated one.
type OrchestratorEfficiency struct {
	CostPerMergedPRNanos *int64
	IsLowerBound         bool
	OrchestratorShare    *float64
	WorkerShare          *float64
}

// DeriveOrchestratorEfficiency computes the spend split and cost per merged
// PR for one project.
//
// IsLowerBound is true whenever the underlying spend is known to be
// understated: any session without a certified usage source
// (ProjectUsageRollup.UnmeasuredSessions, which spans orchestrator and worker
// sessions alike, so it taints both totals), or either side's estimate
// reporting only partial cost coverage.
//
// A zero combined total is treated as "no basis for a share" rather than a
// 0/100 split, but a zero cost per merged PR is a real answer: local-model
// projects genuinely cost nothing. MergedPRs is only ever a divisor here; with
// no merged PRs there is nothing to divide by and CostPerMergedPRNanos stays
// nil, which is distinct from a real zero.
func DeriveOrchestratorEfficiency(rollup domain.ProjectUsageRollup) OrchestratorEfficiency {
	out := OrchestratorEfficiency{IsLowerBound: rollup.UnmeasuredSessions > 0}

	orchestrator, hasOrchestrator := costNanos(rollup.OrchestratorTotals.EstimatedCost)
	workers, hasWorkers := costNanos(rollup.WorkerTotals.EstimatedCost)
	if isPartialCost(rollup.OrchestratorTotals.EstimatedCost) || isPartialCost(rollup.WorkerTotals.EstimatedCost) {
		out.IsLowerBound = true
	}
	if !hasOrchestrator && !hasWorkers {
		// Neither side is measured: there is no total, so neither a share nor
		// a cost per merged PR can be stated.
		return out
	}

	total := orchestrator + workers
	if total > 0 {
		orchestratorShare := float64(orchestrator) / float64(total)
		workerShare := float64(workers) / float64(total)
		out.OrchestratorShare = &orchestratorShare
		out.WorkerShare = &workerShare
	}

	if rollup.MergedPRs > 0 {
		perPR := total / rollup.MergedPRs
		out.CostPerMergedPRNanos = &perPR
	}
	return out
}

// costNanos reports a scope's nano-USD total, and whether it is known at all.
// A nil estimate is unknown spend, never zero spend.
func costNanos(cost *domain.EstimatedCost) (int64, bool) {
	if cost == nil {
		return 0, false
	}
	return cost.TotalNanos, true
}

// isPartialCost reports whether an estimate covers only part of its scope,
// which makes any figure derived from it a lower bound.
func isPartialCost(cost *domain.EstimatedCost) bool {
	return cost != nil && cost.Coverage == domain.EstimatedCostCoveragePartial
}

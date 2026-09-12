package usage

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// costOf builds a fully measured estimate: domain.EstimatedCost carries its
// total in TotalNanos (not Nanos), and complete coverage is what makes the
// total a real figure rather than a stated lower bound.
func costOf(nanos int64) *domain.EstimatedCost {
	return &domain.EstimatedCost{
		TotalNanos: nanos,
		Coverage:   domain.EstimatedCostCoverageComplete,
	}
}

func TestDeriveOrchestratorEfficiencySplitsShare(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          2,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(3_000_000_000)},
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(9_000_000_000)},
	})

	if got.OrchestratorShare == nil || *got.OrchestratorShare != 0.25 {
		t.Fatalf("OrchestratorShare = %v, want 0.25", got.OrchestratorShare)
	}
	if got.WorkerShare == nil || *got.WorkerShare != 0.75 {
		t.Fatalf("WorkerShare = %v, want 0.75", got.WorkerShare)
	}
	if got.CostPerMergedPRNanos == nil || *got.CostPerMergedPRNanos != 6_000_000_000 {
		t.Fatalf("CostPerMergedPRNanos = %v, want 6000000000 (12bn / 2)", got.CostPerMergedPRNanos)
	}
	if got.IsLowerBound {
		t.Fatal("IsLowerBound = true with fully measured totals, want false")
	}
}

func TestDeriveOrchestratorEfficiencyNoMergedPRs(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          0,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(3_000_000_000)},
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(9_000_000_000)},
	})

	if got.CostPerMergedPRNanos != nil {
		t.Fatalf("CostPerMergedPRNanos = %v, want nil — dividing by zero merged PRs is not zero cost", got.CostPerMergedPRNanos)
	}
	if got.OrchestratorShare == nil || *got.OrchestratorShare != 0.25 {
		t.Fatalf("OrchestratorShare = %v, want 0.25 — the split is independent of merged PRs", got.OrchestratorShare)
	}
}

func TestDeriveOrchestratorEfficiencyUnknownCostsStayUnknown(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{MergedPRs: 3})

	if got.OrchestratorShare != nil || got.WorkerShare != nil {
		t.Fatalf("shares = (%v, %v), want both nil with no cost data", got.OrchestratorShare, got.WorkerShare)
	}
	if got.CostPerMergedPRNanos != nil {
		t.Fatal("CostPerMergedPRNanos is set with no cost data, want nil")
	}
}

func TestDeriveOrchestratorEfficiencyOneSidedCostIsAWholeShare(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:    1,
		WorkerTotals: domain.UsageMetricTotals{EstimatedCost: costOf(4_000_000_000)},
	})

	if got.OrchestratorShare == nil || *got.OrchestratorShare != 0 {
		t.Fatalf("OrchestratorShare = %v, want 0 with an unknown-but-unspent orchestrator side", got.OrchestratorShare)
	}
	if got.WorkerShare == nil || *got.WorkerShare != 1 {
		t.Fatalf("WorkerShare = %v, want 1", got.WorkerShare)
	}
	if got.CostPerMergedPRNanos == nil || *got.CostPerMergedPRNanos != 4_000_000_000 {
		t.Fatalf("CostPerMergedPRNanos = %v, want 4000000000", got.CostPerMergedPRNanos)
	}
}

func TestDeriveOrchestratorEfficiencyFlagsLowerBound(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          1,
		UnmeasuredSessions: 2,
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(1_000_000_000)},
	})

	if !got.IsLowerBound {
		t.Fatal("IsLowerBound = false with 2 unmeasured sessions, want true")
	}
}

func TestDeriveOrchestratorEfficiencyFlagsPartialCoverageAsLowerBound(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          1,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(1_000_000_000)},
		WorkerTotals: domain.UsageMetricTotals{EstimatedCost: &domain.EstimatedCost{
			TotalNanos: 3_000_000_000,
			Coverage:   domain.EstimatedCostCoveragePartial,
		}},
	})

	if !got.IsLowerBound {
		t.Fatal("IsLowerBound = false with a partially covered worker estimate, want true")
	}
}

func TestDeriveOrchestratorEfficiencyZeroTotalIsNotAShare(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          1,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(0)},
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(0)},
	})

	if got.OrchestratorShare != nil {
		t.Fatalf("OrchestratorShare = %v, want nil — a 0/0 split has no meaning", got.OrchestratorShare)
	}
	if got.WorkerShare != nil {
		t.Fatalf("WorkerShare = %v, want nil — a 0/0 split has no meaning", got.WorkerShare)
	}
	// Cost per merged PR over a genuinely free project IS zero, and that is a
	// real answer for local models.
	if got.CostPerMergedPRNanos == nil || *got.CostPerMergedPRNanos != 0 {
		t.Fatalf("CostPerMergedPRNanos = %v, want 0 for a measured, genuinely free project", got.CostPerMergedPRNanos)
	}
}

func TestDeriveOrchestratorEfficiencyNegativeMergedPRsYieldNoCost(t *testing.T) {
	got := DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:    -1,
		WorkerTotals: domain.UsageMetricTotals{EstimatedCost: costOf(1_000_000_000)},
	})

	if got.CostPerMergedPRNanos != nil {
		t.Fatalf("CostPerMergedPRNanos = %v, want nil for a nonsensical negative divisor", got.CostPerMergedPRNanos)
	}
}

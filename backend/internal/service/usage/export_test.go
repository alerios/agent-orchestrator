package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func SourceKindForHarnessForTest(h domain.AgentHarness) (domain.UsageSourceKind, bool) {
	return sourceKindForHarness(h)
}

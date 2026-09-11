package usage

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSupportedHarnessIncludesOpenCode(t *testing.T) {
	if !SupportedHarness(domain.HarnessOpenCode) {
		t.Fatal("SupportedHarness(opencode) = false, want true")
	}
}

func TestSourceKindForOpenCode(t *testing.T) {
	kind, ok := SourceKindForHarnessForTest(domain.HarnessOpenCode)
	if !ok || kind != domain.UsageSourceOpenCodeDB {
		t.Fatalf("sourceKindForHarness(opencode) = (%q, %v), want (opencode_db, true)", kind, ok)
	}
}

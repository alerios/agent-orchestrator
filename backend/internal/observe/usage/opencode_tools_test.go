package usage

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDecodeOpenCodeToolCallWithTiming(t *testing.T) {
	part := openCodePart{Seq: 1, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_a","tool":"bash",
		"state":{"status":"completed","title":"npm test",
		"time":{"start":1773336859125,"end":1773336863340}}}`)}

	got, ok := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.ToolName != "bash" || got.ProviderCallID != "call_a" {
		t.Fatalf("tool = (%q, %q), want (bash, call_a)", got.ToolName, got.ProviderCallID)
	}
	if got.Outcome != domain.ToolOutcomeCompleted {
		t.Fatalf("Outcome = %q, want completed", got.Outcome)
	}
	if got.DurationMS == nil || *got.DurationMS != 4215 {
		t.Fatalf("DurationMS = %v, want 4215", got.DurationMS)
	}
	if got.SourceKind != domain.UsageSourceOpenCodeDB {
		t.Fatalf("SourceKind = %q, want opencode_db", got.SourceKind)
	}
}

func TestDecodeOpenCodeToolCallWithoutTimingStaysUnknown(t *testing.T) {
	part := openCodePart{Seq: 2, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_b","tool":"read",
		"state":{"status":"completed","title":"read file"}}`)}

	got, ok := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.DurationMS != nil {
		t.Fatalf("DurationMS = %v, want nil — absent timing must not become zero", got.DurationMS)
	}
	if got.StartedAt != nil || got.EndedAt != nil {
		t.Fatal("StartedAt/EndedAt set, want nil for an untimed call")
	}
}

func TestDecodeOpenCodeToolCallErrorStatusIsFailed(t *testing.T) {
	part := openCodePart{Seq: 3, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_c","tool":"bash",
		"state":{"status":"error","title":"exit 1",
		"time":{"start":1773336870000,"end":1773336871000}}}`)}

	got, _ := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if got.Outcome != domain.ToolOutcomeFailed {
		t.Fatalf("Outcome = %q, want failed", got.Outcome)
	}
}

func TestDecodeOpenCodeToolCallNeverStoresOutput(t *testing.T) {
	part := openCodePart{Seq: 4, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_d","tool":"read",
		"state":{"status":"completed","title":"t","output":"SECRET-TOKEN-VALUE"}}`)}

	got, _ := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if strings.Contains(got.InputSummary, "SECRET-TOKEN-VALUE") {
		t.Fatal("tool output leaked into InputSummary")
	}
}

func TestDecodeOpenCodeToolCallIgnoresNonToolParts(t *testing.T) {
	part := openCodePart{Seq: 5, Type: "step-finish", Data: []byte(`{"type":"step-finish"}`)}
	if _, ok := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0)); ok {
		t.Fatal("ok = true for a non-tool part, want false")
	}
}

func TestDecodeOpenCodeToolCallDetectsMCPTools(t *testing.T) {
	part := openCodePart{Seq: 6, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_e","tool":"mcp__playwright__browser_click",
		"state":{"status":"completed","title":"click"}}`)}

	got, _ := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if !got.IsMCP || got.MCPServer != "playwright" {
		t.Fatalf("MCP = (%v, %q), want (true, playwright)", got.IsMCP, got.MCPServer)
	}
}

func TestOpenCodeCompactionCount(t *testing.T) {
	parts := []openCodePart{
		{Seq: 1, Type: "tool"},
		{Seq: 2, Type: "compaction"},
		{Seq: 3, Type: "compaction"},
	}
	if got := openCodeCompactionCount(parts); got != 2 {
		t.Fatalf("openCodeCompactionCount = %d, want 2", got)
	}
}

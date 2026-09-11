package usage

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func timedCall(tool string, startMS, endMS int64) domain.SessionToolCall {
	started := time.UnixMilli(startMS).UTC()
	ended := time.UnixMilli(endMS).UTC()
	duration := endMS - startMS
	return domain.SessionToolCall{
		DurationMS: &duration, EndedAt: &ended, Outcome: domain.ToolOutcomeCompleted,
		StartedAt: &started, ToolName: tool,
	}
}

func TestDeriveEffortUnionsOverlappingCallSpans(t *testing.T) {
	// Two overlapping 4s calls cover 6s of wall clock, not 8s.
	calls := []domain.SessionToolCall{
		timedCall("bash", 1_000, 5_000),
		timedCall("bash", 3_000, 7_000),
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(10_000).UTC(), 0)

	if !got.TimingAvailable {
		t.Fatal("TimingAvailable = false, want true")
	}
	if got.ActiveMS == nil || *got.ActiveMS != 6_000 {
		t.Fatalf("ActiveMS = %v, want 6000 (union, not sum)", got.ActiveMS)
	}
	if got.DurationMS == nil || *got.DurationMS != 10_000 {
		t.Fatalf("DurationMS = %v, want 10000", got.DurationMS)
	}
	if got.IdleMS == nil || *got.IdleMS != 4_000 {
		t.Fatalf("IdleMS = %v, want 4000", got.IdleMS)
	}
}

func TestDeriveEffortLeavesTimingUnavailableWithNoTimedCalls(t *testing.T) {
	calls := []domain.SessionToolCall{
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "Read"},
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "Edit"},
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(10_000).UTC(), 0)

	if got.TimingAvailable {
		t.Fatal("TimingAvailable = true, want false")
	}
	if got.ActiveMS != nil || got.IdleMS != nil {
		t.Fatalf("Active/Idle = (%v, %v), want both nil — no timing is not 'fully idle'", got.ActiveMS, got.IdleMS)
	}
	if got.ToolCalls != 2 {
		t.Fatalf("ToolCalls = %d, want 2 — counts work without timing", got.ToolCalls)
	}
}

func TestDeriveEffortCountsTestRuns(t *testing.T) {
	calls := []domain.SessionToolCall{
		{InputSummary: "npm test", Outcome: domain.ToolOutcomeCompleted, ToolName: "bash"},
		{InputSummary: "go test ./...", Outcome: domain.ToolOutcomeCompleted, ToolName: "bash"},
		{InputSummary: "ls -la", Outcome: domain.ToolOutcomeCompleted, ToolName: "bash"},
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(1_000).UTC(), 0)

	if got.CommandsRun != 3 {
		t.Fatalf("CommandsRun = %d, want 3", got.CommandsRun)
	}
	if got.TestsRun != 2 {
		t.Fatalf("TestsRun = %d, want 2", got.TestsRun)
	}
}

func TestDeriveEffortCountsReadsAndEdits(t *testing.T) {
	calls := []domain.SessionToolCall{
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "read"},
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "Read"},
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "edit"},
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(1_000).UTC(), 0)

	if got.FilesRead != 2 {
		t.Fatalf("FilesRead = %d, want 2 (tool names are case-insensitive)", got.FilesRead)
	}
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
}

func TestDeriveEffortCarriesCompactions(t *testing.T) {
	got := deriveEffort(nil, time.UnixMilli(0).UTC(), time.UnixMilli(1_000).UTC(), 3)
	if got.Compactions != 3 {
		t.Fatalf("Compactions = %d, want 3", got.Compactions)
	}
}

func TestIsTestCommand(t *testing.T) {
	for _, command := range []string{"npm test", "go test ./...", "pytest -q", "npx vitest run"} {
		if !isTestCommand(command) {
			t.Errorf("isTestCommand(%q) = false, want true", command)
		}
	}
	for _, command := range []string{"npm run build", "go build ./...", "ls"} {
		if isTestCommand(command) {
			t.Errorf("isTestCommand(%q) = true, want false", command)
		}
	}
}

func TestDeriveEffortSplitsSpansSeparatedByALongGap(t *testing.T) {
	// 121s apart: beyond activeGapThreshold, so the gap is idle, not active.
	calls := []domain.SessionToolCall{
		timedCall("bash", 0, 1_000),
		timedCall("bash", 122_000, 123_000),
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(123_000).UTC(), 0)

	if got.ActiveMS == nil || *got.ActiveMS != 2_000 {
		t.Fatalf("ActiveMS = %v, want 2000 — a gap over the threshold is idle", got.ActiveMS)
	}
}

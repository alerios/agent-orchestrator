package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUpsertAndListSessionToolCalls(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	duration := int64(4215)
	started := time.UnixMilli(1_773_336_859_125).UTC()
	ended := time.UnixMilli(1_773_336_863_340).UTC()
	calls := []domain.SessionToolCall{
		{
			DurationMS: &duration, EndedAt: &ended, InputSummary: "npm test",
			ObservedAt: ended, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "call_a", SessionID: "sess-1",
			SourceKind: domain.UsageSourceOpenCodeDB, StartedAt: &started, ToolName: "bash",
		},
		{
			InputSummary: "src/main.go", ObservedAt: ended,
			Outcome: domain.ToolOutcomeCompleted, ProviderCallID: "call_b",
			SessionID: "sess-1", SourceKind: domain.UsageSourceClaudeMain, ToolName: "Read",
		},
	}

	if err := store.UpsertSessionToolCalls(ctx, calls); err != nil {
		t.Fatalf("UpsertSessionToolCalls: %v", err)
	}
	// Idempotent: re-applying the same batch must not duplicate rows.
	if err := store.UpsertSessionToolCalls(ctx, calls); err != nil {
		t.Fatalf("second UpsertSessionToolCalls: %v", err)
	}

	got, err := store.ListSessionToolCalls(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListSessionToolCalls: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(calls) = %d, want 2 after a repeated batch", len(got))
	}
	var untimed int
	for _, call := range got {
		if call.DurationMS == nil {
			untimed++
		}
	}
	if untimed != 1 {
		t.Fatalf("untimed calls = %d, want 1 — NULL timing must round-trip as nil", untimed)
	}
}

func TestSessionToolMixOrdersByTimeThenCount(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	observed := time.UnixMilli(1_773_336_863_340).UTC()

	slow := int64(9_000)
	fast := int64(100)
	if err := store.UpsertSessionToolCalls(ctx, []domain.SessionToolCall{
		{DurationMS: &slow, ObservedAt: observed, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "a", SessionID: "sess-1", SourceKind: domain.UsageSourceOpenCodeDB, ToolName: "bash"},
		{DurationMS: &fast, ObservedAt: observed, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "b", SessionID: "sess-1", SourceKind: domain.UsageSourceOpenCodeDB, ToolName: "read"},
		{DurationMS: &fast, ObservedAt: observed, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "c", SessionID: "sess-1", SourceKind: domain.UsageSourceOpenCodeDB, ToolName: "read"},
	}); err != nil {
		t.Fatalf("UpsertSessionToolCalls: %v", err)
	}

	mix, err := store.SessionToolMix(ctx, "sess-1")
	if err != nil {
		t.Fatalf("SessionToolMix: %v", err)
	}
	if len(mix) != 2 || mix[0].ToolName != "bash" {
		t.Fatalf("mix = %+v, want bash first (9000ms beats 2 calls totalling 200ms)", mix)
	}
	if mix[1].Calls != 2 {
		t.Fatalf("read calls = %d, want 2", mix[1].Calls)
	}
}

func TestSessionEffortRoundTripsUnavailableTiming(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	duration := int64(10_000)

	effort := domain.SessionEffort{
		DurationMS: &duration, ToolCalls: 5, TimingAvailable: false,
	}
	if err := store.UpsertSessionEffort(ctx, "sess-1", effort, time.UnixMilli(1).UTC()); err != nil {
		t.Fatalf("UpsertSessionEffort: %v", err)
	}

	got, ok, err := store.GetSessionEffort(ctx, "sess-1")
	if err != nil || !ok {
		t.Fatalf("GetSessionEffort = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.ActiveMS != nil || got.IdleMS != nil {
		t.Fatalf("Active/Idle = (%v, %v), want both nil", got.ActiveMS, got.IdleMS)
	}
	if got.TimingAvailable {
		t.Fatal("TimingAvailable = true, want false")
	}
}

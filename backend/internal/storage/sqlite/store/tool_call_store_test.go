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

// TestUpsertSessionToolCallCoalescePreservesGoodData proves that re-applying
// the same tool call identity (session_id, source_kind, provider_call_id)
// with less information — e.g. a stale in-flight snapshot with null timing —
// never clobbers previously-recorded good data. The upsert SQL's
// COALESCE(excluded.X, session_tool_calls.X) on ended_at/duration_ms/exit_code
// exists exactly for this; this test is the regression guard for it.
func TestUpsertSessionToolCallCoalescePreservesGoodData(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	duration := int64(4215)
	started := time.UnixMilli(1_773_336_859_125).UTC()
	ended := time.UnixMilli(1_773_336_863_340).UTC()
	exitCode := int64(0)

	complete := domain.SessionToolCall{
		DurationMS: &duration, EndedAt: &ended, ExitCode: &exitCode,
		InputSummary: "npm test", ObservedAt: ended, Outcome: domain.ToolOutcomeCompleted,
		ProviderCallID: "call_a", SessionID: "sess-1",
		SourceKind: domain.UsageSourceOpenCodeDB, StartedAt: &started, ToolName: "bash",
	}
	if err := store.UpsertSessionToolCalls(ctx, []domain.SessionToolCall{complete}); err != nil {
		t.Fatalf("initial UpsertSessionToolCalls: %v", err)
	}

	// A stale re-read of the same call, still in flight: same identity, but
	// ended_at/duration_ms/exit_code are unknown (nil) this time.
	staleReRead := domain.SessionToolCall{
		DurationMS: nil, EndedAt: nil, ExitCode: nil,
		InputSummary: "npm test", ObservedAt: ended, Outcome: domain.ToolOutcomeCompleted,
		ProviderCallID: "call_a", SessionID: "sess-1",
		SourceKind: domain.UsageSourceOpenCodeDB, StartedAt: &started, ToolName: "bash",
	}
	if err := store.UpsertSessionToolCalls(ctx, []domain.SessionToolCall{staleReRead}); err != nil {
		t.Fatalf("stale re-read UpsertSessionToolCalls: %v", err)
	}

	got, err := store.ListSessionToolCalls(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListSessionToolCalls: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(got))
	}
	call := got[0]
	if call.DurationMS == nil || *call.DurationMS != duration {
		t.Fatalf("DurationMS = %v, want %d preserved from the original completed call", call.DurationMS, duration)
	}
	if call.EndedAt == nil || !call.EndedAt.Equal(ended) {
		t.Fatalf("EndedAt = %v, want %v preserved from the original completed call", call.EndedAt, ended)
	}
	if call.ExitCode == nil || *call.ExitCode != exitCode {
		t.Fatalf("ExitCode = %v, want %d preserved from the original completed call", call.ExitCode, exitCode)
	}
}

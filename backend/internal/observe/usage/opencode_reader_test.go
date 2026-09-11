package usage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReadOpenCodeEmitsSessionTokenVector(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{
		ID: "ses_paid", ModelID: "claude-sonnet-4-6", ProviderID: "anthropic",
		TokensInput: 184_000, TokensOutput: 23_000, TokensCacheRead: 412_000,
		TokensCacheWrite: 9_000, Cost: 2.84,
	}
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	got := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)

	if got.err != nil {
		t.Fatalf("err = %v, want nil", got.err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(got.Events))
	}
	event := got.Events[0]
	if event.ModelID != "claude-sonnet-4-6" {
		t.Fatalf("ModelID = %q, want claude-sonnet-4-6", event.ModelID)
	}
	if event.ProviderID != domain.UsageProviderOpenCode {
		t.Fatalf("ProviderID = %q, want opencode", event.ProviderID)
	}
	if event.MeasurementKind != domain.UsageMeasurementNativeReported {
		t.Fatalf("MeasurementKind = %q, want native_reported", event.MeasurementKind)
	}
	if event.Tokens.CachedInputTokens == nil || *event.Tokens.CachedInputTokens != 412_000 {
		t.Fatalf("CachedInputTokens = %v, want 412000", event.Tokens.CachedInputTokens)
	}
	// Cache WRITES belong to uncached input, per UsageTokenMetrics' contract.
	if event.Tokens.UncachedInputTokens == nil || *event.Tokens.UncachedInputTokens != 193_000 {
		t.Fatalf("UncachedInputTokens = %v, want 193000 (184000 input + 9000 cache write)", event.Tokens.UncachedInputTokens)
	}
	if event.SourceEventKey == "" {
		t.Fatal("SourceEventKey = \"\", want non-empty")
	}
	if event.CreatedAt != now {
		t.Fatalf("CreatedAt = %v, want %v", event.CreatedAt, now)
	}
}

func TestReadOpenCodeLocalModelZeroCostIsKnown(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{
		ID: "ses_local", ModelID: "gemma4:e2b", ProviderID: "ai-local-models-cpu",
		TokensInput: 1_130, TokensOutput: 778, TokensCacheRead: 19_990, Cost: 0,
	}
	state := &openCodeParserStateV1{NativeSessionID: "ses_local"}
	got := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)

	if len(got.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(got.Events))
	}
	// A known-zero token count is a pointer to zero, never nil.
	if got.Events[0].Tokens.OutputTokens == nil {
		t.Fatal("OutputTokens = nil, want a pointer to 778")
	}
	if *got.Events[0].Tokens.OutputTokens != 778 {
		t.Fatalf("OutputTokens = %d, want 778", *got.Events[0].Tokens.OutputTokens)
	}
}

func TestReadOpenCodeAdvancesCursorOverParts(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 10, TokensOutput: 2}
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	parts := []openCodePart{
		{Data: []byte(`{"type":"compaction","auto":true,"overflow":true}`), Seq: 5, Type: "compaction"},
	}

	got := readOpenCode(openCodeSourceContextForTest(), session, parts, 5, now, state)

	if got.err != nil {
		t.Fatalf("err = %v, want nil", got.err)
	}
	if state.LastSeq != 5 {
		t.Fatalf("state.LastSeq = %d, want 5", state.LastSeq)
	}
	var decoded openCodeParserStateV1
	if err := json.Unmarshal([]byte(got.Cursor.ParserStateJSON), &decoded); err != nil {
		t.Fatalf("decode cursor state: %v", err)
	}
	if decoded.LastSeq != 5 {
		t.Fatalf("persisted LastSeq = %d, want 5", decoded.LastSeq)
	}
	if got.Cursor.ByteOffset != 0 {
		t.Fatalf("ByteOffset = %d, want 0 — a database source has no byte offset", got.Cursor.ByteOffset)
	}
}

func TestReadOpenCodeIsIdempotentOnRepeatedTotals(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 10, TokensOutput: 2}
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}

	first := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)
	second := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)

	if len(first.Events) != 1 {
		t.Fatalf("first pass len(Events) = %d, want 1", len(first.Events))
	}
	if len(second.Events) != 0 {
		t.Fatalf("second pass len(Events) = %d, want 0 — unchanged totals must not re-emit", len(second.Events))
	}
}

func TestReadOpenCodeEmitsDeltaWhenTotalsGrow(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	base := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 10, TokensOutput: 2}
	first := readOpenCode(openCodeSourceContextForTest(), base, nil, 0, now, state)

	grown := base
	grown.TokensInput = 30
	grown.TokensOutput = 5
	got := readOpenCode(openCodeSourceContextForTest(), grown, nil, 0, now, state)

	if len(got.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(got.Events))
	}
	if got.Events[0].Tokens.InputTokens == nil || *got.Events[0].Tokens.InputTokens != 20 {
		t.Fatalf("InputTokens = %v, want the delta 20", got.Events[0].Tokens.InputTokens)
	}
	// Repeated identical cumulative totals must never collide with a
	// different delta's key.
	if got.Events[0].SourceEventKey == first.Events[0].SourceEventKey {
		t.Fatal("SourceEventKey did not vary between two different deltas")
	}
}

func TestReadOpenCodeRejectsNonMonotonicTotals(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	base := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 100, TokensOutput: 10}
	readOpenCode(openCodeSourceContextForTest(), base, nil, 0, now, state)

	shrunk := base
	shrunk.TokensInput = 5
	got := readOpenCode(openCodeSourceContextForTest(), shrunk, nil, 0, now, state)

	if len(got.Events) != 0 {
		t.Fatalf("len(Events) = %d, want 0 for shrinking totals", len(got.Events))
	}
	if got.Cursor.LastErrorCode != domain.UsageErrorNonMonotonicCumulativeUsage {
		t.Fatalf("LastErrorCode = %q, want non_monotonic_cumulative_usage", got.Cursor.LastErrorCode)
	}
}

func openCodeSourceContextForTest() domain.UsageSourceContext {
	return domain.UsageSourceContext{
		BindingState: domain.UsageBindingActive,
		NativeRootID: "ses_paid",
		SessionID:    domain.SessionID("sess-1"),
		Source: domain.UsageSourceRecord{
			ID:   1,
			Kind: domain.UsageSourceOpenCodeDB,
		},
	}
}

package usage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// openCodeParserStateV1 is the durable cursor for a database-backed source.
// opencode reports CUMULATIVE per-session totals, so the previously emitted
// totals must be remembered to emit deltas rather than re-counting.
type openCodeParserStateV1 struct {
	NativeSessionID      string `json:"native_session_id"`
	LastSeq              int64  `json:"last_seq"`
	EmittedSessionTotals bool   `json:"emitted_session_totals"`
	EmittedInput         int64  `json:"emitted_input"`
	EmittedOutput        int64  `json:"emitted_output"`
	EmittedCacheRead     int64  `json:"emitted_cache_read"`
	EmittedCacheWrite    int64  `json:"emitted_cache_write"`
	EmittedReasoning     int64  `json:"emitted_reasoning"`
}

// readOpenCode turns one poll of opencode's database into usage events and a
// durable cursor. Parts are scanned only to advance the sequence cursor; token
// accounting comes from the session row, which opencode keeps authoritative.
func readOpenCode(
	source domain.UsageSourceContext,
	session openCodeSession,
	parts []openCodePart,
	nextSeq int64,
	now time.Time,
	state *openCodeParserStateV1,
) parseResult {
	result := parseResult{Cursor: cursorFromSource(source.Source, 0, now)}

	for _, part := range parts {
		if part.Seq > state.LastSeq {
			state.LastSeq = part.Seq
		}
	}
	if nextSeq > state.LastSeq {
		state.LastSeq = nextSeq
	}

	if event, ok := openCodeDeltaEvent(source, session, now, state, &result); ok {
		result.Events = append(result.Events, event)
	}

	encoded, err := json.Marshal(state)
	if err != nil {
		result.err = err
		return result
	}
	result.Cursor.ParserStateJSON = string(encoded)
	return result
}

func openCodeDeltaEvent(
	source domain.UsageSourceContext,
	session openCodeSession,
	now time.Time,
	state *openCodeParserStateV1,
	result *parseResult,
) (domain.ModelUsageEvent, bool) {
	// opencode's counters only ever grow within a session. A decrease means the
	// row was replaced (a reused id, a reset) and the delta cannot be trusted.
	if state.EmittedSessionTotals && (session.TokensInput < state.EmittedInput ||
		session.TokensOutput < state.EmittedOutput ||
		session.TokensCacheRead < state.EmittedCacheRead ||
		session.TokensCacheWrite < state.EmittedCacheWrite ||
		session.TokensReasoning < state.EmittedReasoning) {
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
		return domain.ModelUsageEvent{}, false
	}

	deltaInput := session.TokensInput - state.EmittedInput
	deltaOutput := session.TokensOutput - state.EmittedOutput
	deltaCacheRead := session.TokensCacheRead - state.EmittedCacheRead
	deltaCacheWrite := session.TokensCacheWrite - state.EmittedCacheWrite
	deltaReasoning := session.TokensReasoning - state.EmittedReasoning
	if state.EmittedSessionTotals &&
		deltaInput == 0 && deltaOutput == 0 && deltaCacheRead == 0 &&
		deltaCacheWrite == 0 && deltaReasoning == 0 {
		return domain.ModelUsageEvent{}, false
	}

	// Cache writes are part of uncached input, per UsageTokenMetrics' contract:
	// their provider-specific split stays in the bounded provider usage object.
	uncached := deltaInput + deltaCacheWrite
	totalInput := uncached + deltaCacheRead
	output := deltaOutput + deltaReasoning

	state.EmittedInput = session.TokensInput
	state.EmittedOutput = session.TokensOutput
	state.EmittedCacheRead = session.TokensCacheRead
	state.EmittedCacheWrite = session.TokensCacheWrite
	state.EmittedReasoning = session.TokensReasoning
	state.EmittedSessionTotals = true

	providerUsageRaw, err := json.Marshal(map[string]any{
		"cost":               session.Cost,
		"tokens_cache_read":  session.TokensCacheRead,
		"tokens_cache_write": session.TokensCacheWrite,
		"tokens_input":       session.TokensInput,
		"tokens_output":      session.TokensOutput,
		"tokens_reasoning":   session.TokensReasoning,
	})
	providerUsage := ""
	if err == nil {
		providerUsage = boundedProviderUsage(providerUsageRaw)
	}

	// opencode emits at most one delta event per poll for a session, rather
	// than one event per native record like Claude/Codex. The key must vary
	// with the exact cumulative counters that produced this delta so that two
	// different deltas never collide, while re-emitting an identical delta
	// after a crash-and-resume reproduces the identical key (safe idempotent
	// re-insertion, left to the store layer's own SourceEventKey handling).
	sourceEventKey := stableSourceEventKey(
		"opencode",
		source.NativeRootID,
		string(source.Source.Kind),
		session.ID,
		fmt.Sprintf("%d-%d-%d-%d-%d",
			session.TokensInput, session.TokensOutput, session.TokensCacheRead,
			session.TokensCacheWrite, session.TokensReasoning),
	)

	return domain.ModelUsageEvent{
		ProviderID:            domain.UsageProviderOpenCode,
		BillingProviderID:     session.ProviderID,
		BillingProviderSource: domain.ObservedBillingProviderSource(session.ProviderID),
		ModelID:               session.ModelID,
		MeasurementKind:       domain.UsageMeasurementNativeReported,
		Tokens: domain.UsageTokenMetrics{
			InputTokens:         int64Ptr(totalInput),
			CachedInputTokens:   int64Ptr(deltaCacheRead),
			UncachedInputTokens: int64Ptr(uncached),
			OutputTokens:        int64Ptr(output),
		},
		ProviderUsageJSON: providerUsage,
		CreatedAt:         now,
		SourceEventKey:    sourceEventKey,
	}, true
}

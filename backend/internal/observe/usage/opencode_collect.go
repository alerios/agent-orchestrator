package usage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const openCodePartBatch = 500

// openCodeCollectorStore is the subset of the usage store a database-backed
// source needs. It is deliberately the same contract the file-chunk Ingestor
// uses, so transaction boundaries and crash semantics are identical.
type openCodeCollectorStore interface {
	GetUsageSourceForIngestion(context.Context, int64) (domain.UsageSourceContext, bool, error)
	ApplyUsageChunk(context.Context, int64, int64, time.Time, domain.SourceCursorState, []domain.ModelUsageEvent) error
	MarkUsageSourceFailure(context.Context, int64, int64, string, time.Time, time.Time) (bool, error)
	UpsertSessionToolCalls(context.Context, []domain.SessionToolCall) error
	ListSessionToolCalls(context.Context, domain.SessionID) ([]domain.SessionToolCall, error)
	UpsertSessionEffort(context.Context, domain.SessionID, domain.SessionEffort, time.Time) error
}

// OpenCodeCollector ingests one opencode-backed usage source per call.
type OpenCodeCollector struct {
	store         openCodeCollectorStore
	dbPath        string
	workspacePath func(domain.SessionID) (string, bool)
	now           func() time.Time
}

// NewOpenCodeCollector builds a collector reading the opencode database at
// dbPath. workspacePath resolves an AO session to its worktree path, which is
// how an opencode session is identified (opencode stores it in
// session.directory).
func NewOpenCodeCollector(
	store openCodeCollectorStore,
	dbPath string,
	workspacePath func(domain.SessionID) (string, bool),
) *OpenCodeCollector {
	return &OpenCodeCollector{
		dbPath:        dbPath,
		now:           func() time.Time { return time.Now().UTC() },
		store:         store,
		workspacePath: workspacePath,
	}
}

// Collect polls opencode for one source and commits events plus cursor in a
// single transaction. Every failure is recorded against the source and
// returns nil: a telemetry problem must never fail a session.
func (c *OpenCodeCollector) Collect(ctx context.Context, sourceID int64) error {
	now := c.now()
	source, ok, err := c.store.GetUsageSourceForIngestion(ctx, sourceID)
	if err != nil || !ok {
		return err
	}

	dir, ok := c.workspacePath(source.SessionID)
	if !ok || dir == "" {
		return c.fail(ctx, source, domain.UsageErrorSourceDiscoveryPending, now)
	}

	db, err := openOpenCodeDB(c.dbPath)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorArtifactMissing, now)
	}
	defer db.Close()

	session, found, err := db.sessionByDirectory(ctx, dir)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorSourceReadFailed, now)
	}
	if !found {
		return c.fail(ctx, source, domain.UsageErrorSourceDiscoveryPending, now)
	}

	state, err := decodeOpenCodeState(source.Source)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorInvalidParserState, now)
	}

	parts, nextSeq, err := db.partsAfter(ctx, session.ID, state.LastSeq, openCodePartBatch)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorSourceReadFailed, now)
	}

	parsed := readOpenCode(source, session, parts, nextSeq, now, state)
	if parsed.err != nil {
		return c.fail(ctx, source, domain.UsageErrorInvalidParserState, now)
	}

	// Tool-call persistence is a secondary, best-effort telemetry concern: a
	// failure here must never block this poll's usage/cost events from
	// committing via ApplyUsageChunk below. Capture the error and record it
	// afterward instead of returning early.
	toolCalls := make([]domain.SessionToolCall, 0, len(parts))
	for _, part := range parts {
		if call, ok := decodeOpenCodeToolCall(source.SessionID, part, now); ok {
			toolCalls = append(toolCalls, call)
		}
	}
	var toolCallErr error
	if len(toolCalls) > 0 {
		toolCallErr = c.store.UpsertSessionToolCalls(ctx, toolCalls)
	}

	// ByteOffset is meaningless for a database source and stays zero; the real
	// cursor rides in ParserStateJSON. Passing the source's own offset keeps the
	// store's optimistic-concurrency check intact.
	if err := c.store.ApplyUsageChunk(
		ctx,
		source.Source.ID,
		source.Source.ByteOffset,
		source.Source.UpdatedAt,
		parsed.Cursor,
		parsed.Events,
	); err != nil {
		return err
	}

	if toolCallErr != nil {
		return c.fail(ctx, source, domain.UsageErrorSourceReadFailed, now)
	}

	stored, err := c.store.ListSessionToolCalls(ctx, source.SessionID)
	if err != nil {
		return nil
	}
	effort := deriveEffort(stored, firstObserved(stored), lastObserved(stored), state.Compactions)
	effort.FilesChanged = session.SummaryFiles
	effort.LinesAdded = session.SummaryAdditions
	effort.LinesRemoved = session.SummaryDeletions
	return c.store.UpsertSessionEffort(ctx, source.SessionID, effort, now)
}

// firstObserved returns the earliest observation time among calls, preferring
// StartedAt when present since it is a tighter lower bound than ObservedAt.
func firstObserved(calls []domain.SessionToolCall) time.Time {
	var first time.Time
	for _, call := range calls {
		t := call.ObservedAt
		if call.StartedAt != nil {
			t = *call.StartedAt
		}
		if first.IsZero() || t.Before(first) {
			first = t
		}
	}
	return first
}

// lastObserved returns the latest observation time among calls, preferring
// EndedAt when present since it is a tighter upper bound than StartedAt or
// ObservedAt; StartedAt is used only when EndedAt is absent. This mirrors
// unionSpanMillis in effort.go so DurationMS never falls below ActiveMS.
func lastObserved(calls []domain.SessionToolCall) time.Time {
	var last time.Time
	for _, call := range calls {
		t := call.ObservedAt
		switch {
		case call.EndedAt != nil:
			t = *call.EndedAt
		case call.StartedAt != nil:
			t = *call.StartedAt
		}
		if t.After(last) {
			last = t
		}
	}
	return last
}

func (c *OpenCodeCollector) fail(
	ctx context.Context, source domain.UsageSourceContext, code string, now time.Time,
) error {
	_, err := c.store.MarkUsageSourceFailure(
		ctx, source.Source.ID, source.Source.FailureCount+1, code, now.Add(time.Minute), now,
	)
	return err
}

func decodeOpenCodeState(source domain.UsageSourceRecord) (*openCodeParserStateV1, error) {
	state := &openCodeParserStateV1{}
	if source.ParserStateJSON == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(source.ParserStateJSON), state); err != nil {
		return nil, err
	}
	return state, nil
}

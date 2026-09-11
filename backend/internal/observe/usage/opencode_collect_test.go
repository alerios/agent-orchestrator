package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeOpenCodeStore struct {
	applied   [][]domain.ModelUsageEvent
	cursors   []domain.SourceCursorState
	failures  []string
	sourceCtx domain.UsageSourceContext
}

func (f *fakeOpenCodeStore) GetUsageSourceForIngestion(
	context.Context, int64,
) (domain.UsageSourceContext, bool, error) {
	return f.sourceCtx, true, nil
}

func (f *fakeOpenCodeStore) ApplyUsageChunk(
	_ context.Context, _ int64, _ int64, _ time.Time,
	cursor domain.SourceCursorState, events []domain.ModelUsageEvent,
) error {
	f.applied = append(f.applied, events)
	f.cursors = append(f.cursors, cursor)
	f.sourceCtx.Source.ParserStateJSON = cursor.ParserStateJSON
	return nil
}

func (f *fakeOpenCodeStore) MarkUsageSourceFailure(
	_ context.Context, _, _ int64, code string, _, _ time.Time,
) (bool, error) {
	f.failures = append(f.failures, code)
	return true, nil
}

func TestOpenCodeCollectorPersistsEventsOnce(t *testing.T) {
	store := &fakeOpenCodeStore{sourceCtx: domain.UsageSourceContext{
		BindingState: domain.UsageBindingActive,
		SessionID:    domain.SessionID("sess-1"),
		Source:       domain.UsageSourceRecord{ID: 1, Kind: domain.UsageSourceOpenCodeDB},
	}}
	collector := NewOpenCodeCollector(
		store,
		filepath.Join("testdata", "opencode-fixture.db"),
		func(domain.SessionID) (string, bool) { return "/tmp/wt/paid", true },
	)

	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if len(store.applied) != 1 || len(store.applied[0]) != 1 {
		t.Fatalf("first pass applied = %v, want exactly one event", store.applied)
	}

	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if len(store.applied) != 2 || len(store.applied[1]) != 0 {
		t.Fatalf("second pass applied = %v, want a no-event commit (cursor only)", store.applied)
	}
}

func TestOpenCodeCollectorRecordsFailureForMissingDatabase(t *testing.T) {
	store := &fakeOpenCodeStore{sourceCtx: domain.UsageSourceContext{
		SessionID: domain.SessionID("sess-1"),
		Source:    domain.UsageSourceRecord{ID: 1, Kind: domain.UsageSourceOpenCodeDB},
	}}
	collector := NewOpenCodeCollector(
		store,
		filepath.Join("testdata", "absent.db"),
		func(domain.SessionID) (string, bool) { return "/tmp/wt/paid", true },
	)

	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("Collect returned %v, want nil — a missing database must not fail the session", err)
	}
	if len(store.failures) != 1 || store.failures[0] != domain.UsageErrorArtifactMissing {
		t.Fatalf("failures = %v, want one artifact_missing", store.failures)
	}
	if len(store.applied) != 0 {
		t.Fatalf("applied = %v, want no writes", store.applied)
	}
}

func TestOpenCodeCollectorRecordsFailureWhenSessionNotBound(t *testing.T) {
	store := &fakeOpenCodeStore{sourceCtx: domain.UsageSourceContext{
		SessionID: domain.SessionID("sess-1"),
		Source:    domain.UsageSourceRecord{ID: 1, Kind: domain.UsageSourceOpenCodeDB},
	}}
	collector := NewOpenCodeCollector(
		store,
		filepath.Join("testdata", "opencode-fixture.db"),
		func(domain.SessionID) (string, bool) { return "/tmp/wt/never-used", true },
	)

	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(store.failures) != 1 || store.failures[0] != domain.UsageErrorSourceDiscoveryPending {
		t.Fatalf("failures = %v, want one source_discovery_pending", store.failures)
	}
}

package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeOpenCodeStore struct {
	applied      [][]domain.ModelUsageEvent
	cursors      []domain.SourceCursorState
	failures     []string
	sourceCtx    domain.UsageSourceContext
	toolCalls    []domain.SessionToolCall
	effort       domain.SessionEffort
	effortAt     time.Time
	effortWrites int
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

func (f *fakeOpenCodeStore) UpsertSessionToolCalls(_ context.Context, calls []domain.SessionToolCall) error {
	f.toolCalls = append(f.toolCalls, calls...)
	return nil
}

func (f *fakeOpenCodeStore) ListSessionToolCalls(
	_ context.Context, _ domain.SessionID,
) ([]domain.SessionToolCall, error) {
	return f.toolCalls, nil
}

func (f *fakeOpenCodeStore) UpsertSessionEffort(
	_ context.Context, _ domain.SessionID, effort domain.SessionEffort, at time.Time,
) error {
	f.effort = effort
	f.effortAt = at
	f.effortWrites++
	return nil
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

// failingToolCallStore wraps fakeOpenCodeStore but fails every
// UpsertSessionToolCalls call, to prove that a tool-call-store failure never
// blocks the usage/cost commit via ApplyUsageChunk.
type failingToolCallStore struct {
	*fakeOpenCodeStore
}

func (f *failingToolCallStore) UpsertSessionToolCalls(context.Context, []domain.SessionToolCall) error {
	return errors.New("boom: tool call store unavailable")
}

func TestOpenCodeCollectorToolCallFailureDoesNotBlockUsageEvents(t *testing.T) {
	inner := &fakeOpenCodeStore{sourceCtx: domain.UsageSourceContext{
		BindingState: domain.UsageBindingActive,
		SessionID:    domain.SessionID("sess-1"),
		Source:       domain.UsageSourceRecord{ID: 1, Kind: domain.UsageSourceOpenCodeDB},
	}}
	store := &failingToolCallStore{fakeOpenCodeStore: inner}
	collector := NewOpenCodeCollector(
		store,
		filepath.Join("testdata", "opencode-fixture.db"),
		func(domain.SessionID) (string, bool) { return "/tmp/wt/paid", true },
	)

	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("Collect returned %v, want nil — a tool-call-store failure must not fail the session", err)
	}

	if len(inner.applied) != 1 || len(inner.applied[0]) != 1 {
		t.Fatalf("applied = %v, want the usage chunk committed despite the tool-call failure", inner.applied)
	}
	if len(inner.failures) != 1 || inner.failures[0] != domain.UsageErrorSourceReadFailed {
		t.Fatalf("failures = %v, want one source_read_failed recorded for the tool-call failure", inner.failures)
	}
}

// buildOpenCodeDB creates a minimal, writable opencode-shaped sqlite database
// at path with a single session row, so a test can incrementally append parts
// between polls to reproduce cross-poll accumulation scenarios.
func buildOpenCodeDB(t *testing.T, path, sessionID, directory string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
CREATE TABLE session (
  id text PRIMARY KEY, project_id text NOT NULL, parent_id text, slug text NOT NULL,
  directory text NOT NULL, title text NOT NULL, version text NOT NULL,
  summary_files integer, summary_additions integer, summary_deletions integer,
  time_created integer NOT NULL, time_updated integer NOT NULL, time_compacting integer,
  agent text, model text, cost real DEFAULT 0 NOT NULL,
  tokens_input integer DEFAULT 0 NOT NULL, tokens_output integer DEFAULT 0 NOT NULL,
  tokens_reasoning integer DEFAULT 0 NOT NULL, tokens_cache_read integer DEFAULT 0 NOT NULL,
  tokens_cache_write integer DEFAULT 0 NOT NULL
);
CREATE TABLE session_message (
  id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL,
  time_created integer NOT NULL, time_updated integer NOT NULL,
  data text NOT NULL, seq integer NOT NULL
);
CREATE TABLE part (
  id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL,
  time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL
);`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sessionID, "proj_1", nil, "s", directory, "Session", "1.0",
		0, 0, 0, int64(1_773_336_000_000), int64(1_773_336_000_000), nil,
		"ao-agent", `{"id":"claude-sonnet-4-6","providerID":"anthropic","variant":"default"}`,
		0.0, int64(0), int64(0), int64(0), int64(0), int64(0),
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return db
}

// addOpenCodePart inserts one more part (and its owning message, at the given
// seq) into an already-open writable opencode database.
func addOpenCodePart(t *testing.T, db *sql.DB, sessionID string, seq int64, payload string) {
	t.Helper()
	messageID := fmt.Sprintf("msg_%d", seq)
	ts := 1_773_336_000_000 + seq
	if _, err := db.Exec(
		`INSERT INTO session_message VALUES (?,?,?,?,?,?,?)`,
		messageID, sessionID, "assistant", ts, ts, "{}", seq,
	); err != nil {
		t.Fatalf("insert session_message: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO part VALUES (?,?,?,?,?,?)`,
		fmt.Sprintf("prt_%d", seq), messageID, sessionID, ts, ts, payload,
	); err != nil {
		t.Fatalf("insert part: %v", err)
	}
}

// TestOpenCodeCollectorAccumulatesCompactionsAcrossPolls is the regression
// guard for the compaction-overwrite bug: partsAfter only ever returns parts
// NEW since the last poll, so a per-poll compaction count must be accumulated
// in durable parser state rather than replacing the stored rollup outright.
// Without accumulation, polls 3-4 (which see zero NEW compaction parts) would
// overwrite session_effort_rollups.compactions back to 0.
func TestOpenCodeCollectorAccumulatesCompactionsAcrossPolls(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	sessionID := "ses_1"
	directory := "/tmp/wt/compaction-test"
	db := buildOpenCodeDB(t, dbPath, sessionID, directory)

	store := &fakeOpenCodeStore{sourceCtx: domain.UsageSourceContext{
		BindingState: domain.UsageBindingActive,
		SessionID:    domain.SessionID("sess-1"),
		Source:       domain.UsageSourceRecord{ID: 1, Kind: domain.UsageSourceOpenCodeDB},
	}}
	collector := NewOpenCodeCollector(
		store, dbPath,
		func(domain.SessionID) (string, bool) { return directory, true },
	)

	// Poll 1: no parts at all yet.
	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("poll 1: %v", err)
	}

	// Poll 2: exactly one compaction part appears.
	addOpenCodePart(t, db, sessionID, 1, `{"type":"compaction","auto":true,"overflow":true}`)
	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("poll 2: %v", err)
	}

	// Polls 3 and 4: new, non-compaction parts arrive; zero NEW compaction
	// parts in either poll.
	addOpenCodePart(t, db, sessionID, 2, `{"type":"tool","callID":"call_a","tool":"bash","state":{"status":"completed","title":"echo hi"}}`)
	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("poll 3: %v", err)
	}
	addOpenCodePart(t, db, sessionID, 3, `{"type":"tool","callID":"call_b","tool":"read","state":{"status":"completed","title":"read file"}}`)
	if err := collector.Collect(context.Background(), 1); err != nil {
		t.Fatalf("poll 4: %v", err)
	}

	if store.effort.Compactions != 1 {
		t.Fatalf("compactions = %d after 4 polls, want 1 (the session-lifetime total, not the last poll's delta)", store.effort.Compactions)
	}
}

package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUsageNativeSessionIDIsStableHashForOpenCode(t *testing.T) {
	session := domain.SessionRecord{
		ID:      domain.SessionID("my.app-3"),
		Harness: domain.HarnessOpenCode,
	}
	got1 := usageNativeSessionID(session)
	got2 := usageNativeSessionID(session)
	if got1 != got2 {
		t.Fatalf("usageNativeSessionID not stable: %q vs %q", got1, got2)
	}
	if !nativeUsageIDPattern.MatchString(got1) {
		t.Fatalf("synthetic opencode native id %q does not match nativeUsageIDPattern", got1)
	}

	other := domain.SessionRecord{
		ID:      domain.SessionID("my.app-4"),
		Harness: domain.HarnessOpenCode,
	}
	if usageNativeSessionID(other) == got1 {
		t.Fatalf("different session ids produced the same synthetic native id %q", got1)
	}
}

func TestDiscoverOpenCodePathReturnsPathWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture db: %v", err)
	}
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dir}, nil)

	got, err := collector.discoverOpenCodePath(context.Background())
	if err != nil {
		t.Fatalf("discoverOpenCodePath: %v", err)
	}
	if got != dbPath {
		t.Fatalf("discoverOpenCodePath = %q, want %q", got, dbPath)
	}
}

func TestDiscoverOpenCodePathReturnsEmptyWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dir}, nil)

	got, err := collector.discoverOpenCodePath(context.Background())
	if err != nil {
		t.Fatalf("discoverOpenCodePath: %v", err)
	}
	if got != "" {
		t.Fatalf("discoverOpenCodePath = %q, want empty", got)
	}
}

func TestValidateSourcePathAcceptsDBForOpenCode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture db: %v", err)
	}
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dir}, nil)

	resolved, _, size, err := collector.validateSourcePath(context.Background(), domain.HarnessOpenCode, dbPath)
	if err != nil {
		t.Fatalf("validateSourcePath: %v", err)
	}
	if resolved == "" || size == 0 {
		t.Fatalf("validateSourcePath returned resolved=%q size=%d", resolved, size)
	}
}

func TestValidateSourcePathStillRejectsDBForOtherHarnesses(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "session.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture db: %v", err)
	}
	collector := NewCollector(collectorTestStore(t), SourceRoots{ClaudeProjects: dir}, nil)

	_, _, _, err := collector.validateSourcePath(context.Background(), domain.HarnessClaudeCode, dbPath)
	if err == nil {
		t.Fatal("validateSourcePath: expected rejection for .db path on Claude harness, got nil error")
	}
}

// TestValidateSourcePathRejectsJSONLForOpenCode is the symmetric case to
// TestValidateSourcePathStillRejectsDBForOtherHarnesses: opencode only ever
// reads its shared .db, never a .jsonl transcript.
func TestValidateSourcePathRejectsJSONLForOpenCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.jsonl")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dir}, nil)

	_, _, _, err := collector.validateSourcePath(context.Background(), domain.HarnessOpenCode, path)
	if err == nil {
		t.Fatal("validateSourcePath: expected rejection for .jsonl path on opencode harness, got nil error")
	}
}

func TestValidateSourceAttributionOpenCodeDB(t *testing.T) {
	binding := domain.UsageBindingRecord{
		Harness:      domain.HarnessOpenCode,
		NativeRootID: "oc-native-root",
	}

	if err := validateSourceAttribution(
		binding, domain.UsageSourceOpenCodeDB, "oc-native-root", "", "/tmp/opencode.db", "",
	); err != nil {
		t.Fatalf("expected accepted opencode attribution, got %v", err)
	}

	wrongHarness := binding
	wrongHarness.Harness = domain.HarnessCodex
	if err := validateSourceAttribution(
		wrongHarness, domain.UsageSourceOpenCodeDB, "oc-native-root", "", "/tmp/opencode.db", "",
	); err == nil {
		t.Fatal("expected rejection for wrong harness, got nil error")
	}

	if err := validateSourceAttribution(
		binding, domain.UsageSourceOpenCodeDB, "mismatched-native-root", "", "/tmp/opencode.db", "",
	); err == nil {
		t.Fatal("expected rejection for mismatched native root id, got nil error")
	}

	if err := validateSourceAttribution(
		binding, domain.UsageSourceOpenCodeDB, "oc-native-root", "subagent-1", "/tmp/opencode.db", "",
	); err == nil {
		t.Fatal("expected rejection for non-empty subagent id, got nil error")
	}
}

// TestDiscoverPathDispatchesOpenCodeThroughNativeIDGuard exercises discoverPath's
// actual switch dispatch (not discoverOpenCodePath directly), so the
// nativeUsageIDPattern guard at the top of discoverPath is genuinely
// exercised against opencode's hashed native id.
func TestDiscoverPathDispatchesOpenCodeThroughNativeIDGuard(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture db: %v", err)
	}
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dir}, nil)

	session := domain.SessionRecord{ID: domain.SessionID("my.app-3"), Harness: domain.HarnessOpenCode}
	nativeID := usageNativeSessionID(session)

	got, err := collector.discoverPath(context.Background(), domain.HarnessOpenCode, nativeID)
	if err != nil {
		t.Fatalf("discoverPath: %v", err)
	}
	if got != dbPath {
		t.Fatalf("discoverPath = %q, want %q", got, dbPath)
	}
}

// TestCollectorBackfillsOpenCodeSessionAgainstRealStore is the persistence-level
// regression test for the Critical finding: without migration 0136 widening the
// usage_bindings.harness / usage_sources.kind CHECK constraints, this fails with
// "CHECK constraint failed: harness IN (...)" the same way BackfillActive would
// wedge in production. It exercises backfillSession/registerSource against a
// real (test) SQLite store, not just the pure helper functions.
func TestCollectorBackfillsOpenCodeSessionAgainstRealStore(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessOpenCode, "unused-native-field", false)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture db: %v", err)
	}
	collector := NewCollector(store, SourceRoots{OpenCodeHome: dir}, nil)

	if err := collector.BackfillActive(context.Background()); err != nil {
		t.Fatalf("BackfillActive: %v", err)
	}

	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	if bindings[0].Harness != domain.HarnessOpenCode {
		t.Fatalf("binding harness = %q, want opencode", bindings[0].Harness)
	}

	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
	if sources[0].Kind != domain.UsageSourceOpenCodeDB {
		t.Fatalf("source kind = %q, want opencode_db", sources[0].Kind)
	}
}

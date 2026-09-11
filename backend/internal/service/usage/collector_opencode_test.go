package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUsageNativeSessionIDReturnsAOSessionIDForOpenCode(t *testing.T) {
	session := domain.SessionRecord{
		ID:      domain.SessionID("p1-open-1"),
		Harness: domain.HarnessOpenCode,
	}
	got := usageNativeSessionID(session)
	if got != "p1-open-1" {
		t.Fatalf("usageNativeSessionID = %q, want %q", got, "p1-open-1")
	}
	if !nativeUsageIDPattern.MatchString(got) {
		t.Fatalf("synthetic opencode native id %q does not match nativeUsageIDPattern", got)
	}
}

func TestDiscoverOpenCodePathReturnsPathWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture db: %v", err)
	}
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dbPath}, nil)

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
	dbPath := filepath.Join(dir, "opencode.db")
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dbPath}, nil)

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
	collector := NewCollector(collectorTestStore(t), SourceRoots{OpenCodeHome: dbPath}, nil)

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

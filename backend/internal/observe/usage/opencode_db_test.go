package usage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenCodeDBSessionByDirectory(t *testing.T) {
	db, err := openOpenCodeDB(filepath.Join("testdata", "opencode-fixture.db"))
	if err != nil {
		t.Fatalf("openOpenCodeDB: %v", err)
	}
	defer db.Close()

	got, ok, err := db.sessionByDirectory(context.Background(), "/tmp/wt/paid")
	if err != nil || !ok {
		t.Fatalf("sessionByDirectory = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.ID != "ses_paid" {
		t.Fatalf("ID = %q, want ses_paid", got.ID)
	}
	if got.ModelID != "claude-sonnet-4-6" || got.ProviderID != "anthropic" {
		t.Fatalf("model = (%q, %q), want (claude-sonnet-4-6, anthropic)", got.ModelID, got.ProviderID)
	}
	if got.TokensCacheRead != 412_000 {
		t.Fatalf("TokensCacheRead = %d, want 412000", got.TokensCacheRead)
	}
}

func TestOpenCodeDBMissingDirectory(t *testing.T) {
	db, err := openOpenCodeDB(filepath.Join("testdata", "opencode-fixture.db"))
	if err != nil {
		t.Fatalf("openOpenCodeDB: %v", err)
	}
	defer db.Close()

	_, ok, err := db.sessionByDirectory(context.Background(), "/tmp/wt/nope")
	if err != nil {
		t.Fatalf("sessionByDirectory err = %v, want nil", err)
	}
	if ok {
		t.Fatal("ok = true for an unknown directory, want false")
	}
}

func TestOpenCodeDBPartsAfterCursor(t *testing.T) {
	db, err := openOpenCodeDB(filepath.Join("testdata", "opencode-fixture.db"))
	if err != nil {
		t.Fatalf("openOpenCodeDB: %v", err)
	}
	defer db.Close()

	parts, next, err := db.partsAfter(context.Background(), "ses_paid", 2, 10)
	if err != nil {
		t.Fatalf("partsAfter: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2 (seq 3 and 4)", len(parts))
	}
	if next != 4 {
		t.Fatalf("next = %d, want 4", next)
	}
}

func TestOpenCodeDBRejectsMissingFile(t *testing.T) {
	if _, err := openOpenCodeDB(filepath.Join("testdata", "absent.db")); err == nil {
		t.Fatal("openOpenCodeDB(absent) = nil error, want an error")
	}
}

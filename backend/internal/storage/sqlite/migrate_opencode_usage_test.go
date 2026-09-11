package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateAllowsOpenCodeUsageBindingAndSource verifies migration 0136 widens
// the usage_bindings.harness and usage_sources.kind CHECK constraints to permit
// 'opencode' and 'opencode_db' respectively. Without this migration, any insert
// for an opencode session fails with "CHECK constraint failed: harness IN (...)",
// which is exactly the runtime failure mode the Critical review finding
// identified in backfillSession/BackfillActive.
func TestMigrateAllowsOpenCodeUsageBindingAndSource(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO projects (id, path, display_name, registered_at)
VALUES ('opencode-project', '/tmp/opencode-project', 'opencode-project', CURRENT_TIMESTAMP);
INSERT INTO sessions (
    id, project_id, num, harness, activity_last_at, workspace_path, branch, created_at, updated_at
)
VALUES (
    'opencode-session', 'opencode-project', 1, 'opencode', CURRENT_TIMESTAMP,
    '/tmp/opencode-session', 'test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);
INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at)
VALUES ('opencode-session', 'opencode', 'native-root', 'active', CURRENT_TIMESTAMP);
INSERT INTO usage_sources (binding_id, kind, artifact_path, state, updated_at)
VALUES (last_insert_rowid(), 'opencode_db', '/tmp/opencode.db', 'active', CURRENT_TIMESTAMP);
`); err != nil {
		t.Fatalf("insert opencode binding/source: %v", err)
	}

	var harness, kind string
	if err := db.QueryRow(`
SELECT ub.harness, us.kind
FROM usage_bindings ub JOIN usage_sources us ON us.binding_id = ub.id
WHERE ub.session_id = 'opencode-session'`).Scan(&harness, &kind); err != nil {
		t.Fatalf("read opencode binding/source: %v", err)
	}
	if harness != "opencode" || kind != "opencode_db" {
		t.Fatalf("harness=%q kind=%q, want opencode/opencode_db", harness, kind)
	}
}

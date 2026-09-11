package sqlite

import "testing"

func TestMigrateSessionToolCalls(t *testing.T) {
	db := openMigratedTestDB(t)

	if _, err := db.Exec(`
		INSERT INTO session_tool_calls
			(session_id, source_kind, provider_call_id, tool_name, is_mcp, mcp_server,
			 input_summary, outcome, started_at, ended_at, duration_ms, exit_code, observed_at)
		VALUES ('sess-1', 'opencode_db', 'call_a', 'bash', 0, '', 'npm test',
			'completed', 1773336859125, 1773336863340, 4215, 0, 1773336863340)`); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}

	// Timing is optional: a call with none must be storable.
	if _, err := db.Exec(`
		INSERT INTO session_tool_calls
			(session_id, source_kind, provider_call_id, tool_name, is_mcp, mcp_server,
			 input_summary, outcome, observed_at)
		VALUES ('sess-1', 'claude_main', 'call_b', 'Read', 0, '', 'src/main.go',
			'completed', 1773336864000)`); err != nil {
		t.Fatalf("insert untimed tool call: %v", err)
	}

	var nullDurations int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM session_tool_calls WHERE duration_ms IS NULL`,
	).Scan(&nullDurations); err != nil {
		t.Fatalf("count null durations: %v", err)
	}
	if nullDurations != 1 {
		t.Fatalf("null durations = %d, want 1 — unknown timing must stay NULL", nullDurations)
	}

	// The unique constraint makes re-reading a source idempotent.
	_, err := db.Exec(`
		INSERT INTO session_tool_calls
			(session_id, source_kind, provider_call_id, tool_name, is_mcp, mcp_server,
			 input_summary, outcome, observed_at)
		VALUES ('sess-1', 'opencode_db', 'call_a', 'bash', 0, '', 'npm test',
			'completed', 1773336863340)`)
	if err == nil {
		t.Fatal("duplicate (session, source_kind, call id) inserted, want a uniqueness error")
	}

	if _, err := db.Exec(`
		INSERT INTO session_effort_rollups
			(session_id, duration_ms, active_ms, idle_ms, tool_calls, files_read,
			 files_changed, lines_added, lines_removed, commands_run, tests_run,
			 compactions, timing_available, updated_at)
		VALUES ('sess-1', 1440000, 900000, 540000, 146, 38, 12, 420, 87, 29, 4, 2, 1, 1773336900000)`); err != nil {
		t.Fatalf("insert effort rollup: %v", err)
	}
}

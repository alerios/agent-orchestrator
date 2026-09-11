-- name: UpsertSessionToolCall :exec
INSERT INTO session_tool_calls (
    session_id, source_kind, provider_call_id, tool_name, is_mcp, mcp_server,
    input_summary, outcome, started_at, ended_at, duration_ms, exit_code, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, source_kind, provider_call_id) DO UPDATE SET
    outcome = excluded.outcome,
    ended_at = COALESCE(excluded.ended_at, session_tool_calls.ended_at),
    duration_ms = COALESCE(excluded.duration_ms, session_tool_calls.duration_ms),
    exit_code = COALESCE(excluded.exit_code, session_tool_calls.exit_code);

-- name: ListSessionToolCalls :many
SELECT session_id, source_kind, provider_call_id, tool_name, is_mcp, mcp_server,
       input_summary, outcome, started_at, ended_at, duration_ms, exit_code, observed_at
FROM session_tool_calls
WHERE session_id = ?
ORDER BY observed_at ASC, id ASC;

-- name: SessionToolMix :many
SELECT tool_name,
       COUNT(*) AS calls,
       SUM(CASE WHEN outcome = 'failed' THEN 1 ELSE 0 END) AS failed_calls,
       SUM(duration_ms) AS total_duration_ms,
       COUNT(duration_ms) AS timed_calls
FROM session_tool_calls
WHERE session_id = ?
GROUP BY tool_name
ORDER BY total_duration_ms DESC, calls DESC;

-- name: UpsertSessionEffort :exec
INSERT INTO session_effort_rollups (
    session_id, duration_ms, active_ms, idle_ms, tool_calls, files_read,
    files_changed, lines_added, lines_removed, commands_run, tests_run,
    compactions, timing_available, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    duration_ms = excluded.duration_ms,
    active_ms = excluded.active_ms,
    idle_ms = excluded.idle_ms,
    tool_calls = excluded.tool_calls,
    files_read = excluded.files_read,
    files_changed = excluded.files_changed,
    lines_added = excluded.lines_added,
    lines_removed = excluded.lines_removed,
    commands_run = excluded.commands_run,
    tests_run = excluded.tests_run,
    compactions = excluded.compactions,
    timing_available = excluded.timing_available,
    updated_at = excluded.updated_at;

-- name: GetSessionEffort :one
SELECT duration_ms, active_ms, idle_ms, tool_calls, files_read, files_changed,
       lines_added, lines_removed, commands_run, tests_run, compactions, timing_available
FROM session_effort_rollups WHERE session_id = ?;

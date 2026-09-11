-- +goose Up
-- +goose StatementBegin
CREATE TABLE session_tool_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    provider_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    is_mcp INTEGER NOT NULL DEFAULT 0,
    mcp_server TEXT NOT NULL DEFAULT '',
    input_summary TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT 'unknown',
    started_at INTEGER,
    ended_at INTEGER,
    duration_ms INTEGER,
    exit_code INTEGER,
    observed_at INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_session_tool_calls_identity
    ON session_tool_calls (session_id, source_kind, provider_call_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_session_tool_calls_session
    ON session_tool_calls (session_id, observed_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE session_effort_rollups (
    session_id TEXT PRIMARY KEY,
    duration_ms INTEGER,
    active_ms INTEGER,
    idle_ms INTEGER,
    tool_calls INTEGER NOT NULL DEFAULT 0,
    files_read INTEGER NOT NULL DEFAULT 0,
    files_changed INTEGER NOT NULL DEFAULT 0,
    lines_added INTEGER NOT NULL DEFAULT 0,
    lines_removed INTEGER NOT NULL DEFAULT 0,
    commands_run INTEGER NOT NULL DEFAULT 0,
    tests_run INTEGER NOT NULL DEFAULT 0,
    compactions INTEGER NOT NULL DEFAULT 0,
    timing_available INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE session_effort_rollups;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_session_tool_calls_session;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX idx_session_tool_calls_identity;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE session_tool_calls;
-- +goose StatementEnd

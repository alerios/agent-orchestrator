# Metrics Phase 3: Tool-Call Telemetry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record durable tool-call facts and per-session effort counters, and render them as the Effort and Tool mix blocks in the Metrics tab — so a worker that is "busy but not progressing" can be diagnosed.

**Architecture:** Two new append-only/derived tables written inside the ingestion transaction that already commits usage events. The opencode reader supplies exact call timings from `part.state.time`; Claude Code and Codex supply names and outcomes without reliable timing. A new API block exposes tool mix and effort, and the Metrics tab renders it sorted by time spent, falling back to call count where timing is absent.

**Tech Stack:** Go, SQLite + goose migrations + sqlc, TypeScript/React, Vitest, openapi-typescript.

**Spec:** `docs/superpowers/specs/2026-09-11-session-metrics-and-efficiency-scoring-design.md`

**Depends on:** Phase 1 (the Metrics tab exists) and Phase 2 (`opencode_db.go`, its `partsAfter` query, and the committed fixture database).

## Global Constraints

- Nullable means unknown. A tool call with no timing stores `NULL` start/end/duration — never `0`.
- **Tool output is never stored.** It is unbounded and frequently sensitive. Only the tool name, a bounded input summary, and the outcome are persisted.
- Input summaries are bounded to 512 bytes after redaction, using the same redaction helper AO already applies to activity text. Find it with `grep -rn "func.*[Rr]edact" backend/internal/domain/text.go backend/internal/service/ | head`.
- `(session_id, source_kind, provider_call_id)` is UNIQUE. Re-reading a source after a crash must be idempotent.
- Absent data renders as an explicit unavailable state, never as zeros.
- **Read `DESIGN.md` before any visual decision**, starting with its "Reference boundary and migration stance" banner (corrected 2026-09-11: an earlier draft cited a "clone agent-orchestrator verbatim" banner that does not exist in this file). The real banner says existing styling is migration debt, not precedent, and warns against copying legacy inconsistency into new work. Build new UI from the existing `@aoagents/product-ui` primitives and `components/ui/*` shadcn components where one fits; do not introduce new visual patterns without explicit approval.
- When demoing a frontend change, run `ao preview [url]` from inside the session so it renders in the inspector rail's Browser tab, and say "check the Browser tab" in your reply — the panel badges as unseen rather than stealing focus.
- No hardcoded English in `frontend/src/renderer`; all copy via i18n keys in all eight locale files.
- After changing Go response types run `npm run api` to regenerate `openapi.yaml` and `frontend/src/api/schema.ts`. Never hand-edit either file.
- Run `npm run lint` before every backend commit.

### Definitions pinned by the spec

- **Active vs idle time.** Duration is wall-clock first to last observed event. *Active* is the union of intervals covered by observed activity: tool-call spans where timing exists, otherwise the gap between consecutive events when that gap is under `activeGapThreshold` (default 120s). *Idle* is duration minus active. Where a source provides **no** timing at all, both are unavailable — not equal to duration.
- **Tests run.** Counted from command-tool invocations whose command matches a test-runner pattern list. A heuristic, labelled as such in the UI, and never used as a governance gate.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/storage/sqlite/migrations/0136_session_tool_calls.sql` | Create: both new tables and their indexes |
| `backend/internal/storage/sqlite/queries/tool_calls.sql` | Create: sqlc queries |
| `backend/internal/domain/toolcall.go` | Create: `SessionToolCall`, `ToolOutcome`, `SessionEffort`, `ToolMixEntry` |
| `backend/internal/observe/usage/opencode_tools.go` | Create: decode opencode `part` rows into `SessionToolCall` |
| `backend/internal/observe/usage/effort.go` | Create: active/idle and test-run derivation |
| `backend/internal/storage/sqlite/store/tool_call_store.go` | Create: writes and read models |
| `backend/internal/service/usage/effort.go` | Create: the effort/tool-mix read service |
| `backend/internal/httpd/controllers/usage.go` | Modify: new endpoint + response types |
| `frontend/src/renderer/hooks/useSessionEffort.ts` | Create: query hook |
| `frontend/src/renderer/components/SessionInspectorMetrics.tsx` | Modify: Effort and Tool mix blocks |
| `frontend/src/renderer/i18n/*.json` | Modify: new keys, all 8 locales |

---

### Task 1: Schema and domain types

**Files:**
- Create: `backend/internal/storage/sqlite/migrations/0136_session_tool_calls.sql`
- Create: `backend/internal/domain/toolcall.go`
- Create: `backend/internal/storage/sqlite/migrate_session_tool_calls_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `domain.ToolOutcome` with values `ToolOutcomeCompleted`, `ToolOutcomeFailed`, `ToolOutcomeDenied`, `ToolOutcomeUnknown`
  - `domain.SessionToolCall{ SessionID domain.SessionID; SourceKind domain.UsageSourceKind; ProviderCallID, ToolName, MCPServer, InputSummary string; IsMCP bool; Outcome ToolOutcome; StartedAt, EndedAt *time.Time; DurationMS, ExitCode *int64 }`
  - `domain.SessionEffort{ DurationMS, ActiveMS, IdleMS *int64; ToolCalls, FilesRead, FilesChanged, LinesAdded, LinesRemoved, CommandsRun, TestsRun, Compactions int64; TimingAvailable bool }`
  - `domain.ToolMixEntry{ ToolName string; Calls int64; TotalDurationMS *int64; FailedCalls int64 }`

Tasks 2-6 consume all four.

- [ ] **Step 1: Write the failing migration test**

Create `backend/internal/storage/sqlite/migrate_session_tool_calls_test.go`, following the shape of the sibling `migrate_*_test.go` files:

```go
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
```

Match `openMigratedTestDB` to whatever helper the neighbouring migration tests already use (check `migrate_test.go`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/storage/sqlite/ -run SessionToolCalls -v`

Expected: FAIL — `no such table: session_tool_calls`.

- [ ] **Step 3: Write the migration**

Create `backend/internal/storage/sqlite/migrations/0136_session_tool_calls.sql`:

```sql
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
```

`duration_ms`, `active_ms`, `idle_ms`, `started_at`, `ended_at` and `exit_code` are deliberately nullable with no default: that is how "unknown" is expressed.

- [ ] **Step 4: Add the domain types**

Create `backend/internal/domain/toolcall.go`:

```go
package domain

import "time"

// ToolOutcome is how a tool call ended. Unknown is a real, storable state: a
// transcript may record that a tool ran without recording how it finished.
type ToolOutcome string

// Tool outcomes.
const (
	ToolOutcomeCompleted ToolOutcome = "completed"
	ToolOutcomeFailed    ToolOutcome = "failed"
	ToolOutcomeDenied    ToolOutcome = "denied"
	ToolOutcomeUnknown   ToolOutcome = "unknown"
)

// SessionToolCall is one durable tool-call fact. Pointer fields are nil when
// the source did not report them; a non-nil zero is a reported zero. Tool
// OUTPUT is deliberately absent: it is unbounded and often sensitive.
type SessionToolCall struct {
	SessionID      SessionID
	SourceKind     UsageSourceKind
	ProviderCallID string
	ToolName       string
	IsMCP          bool
	MCPServer      string
	InputSummary   string
	Outcome        ToolOutcome
	StartedAt      *time.Time
	EndedAt        *time.Time
	DurationMS     *int64
	ExitCode       *int64
	ObservedAt     time.Time
}

// SessionEffort is the derived per-session counter block. TimingAvailable
// false means ActiveMS and IdleMS are unavailable rather than zero: a session
// with no timing is not a session that was idle.
type SessionEffort struct {
	DurationMS      *int64
	ActiveMS        *int64
	IdleMS          *int64
	ToolCalls       int64
	FilesRead       int64
	FilesChanged    int64
	LinesAdded      int64
	LinesRemoved    int64
	CommandsRun     int64
	TestsRun        int64
	Compactions     int64
	TimingAvailable bool
}

// ToolMixEntry is one row of the tool breakdown. TotalDurationMS is nil when
// no call for that tool carried timing, which is what forces the UI to fall
// back to count ordering and say so.
type ToolMixEntry struct {
	ToolName        string
	Calls           int64
	FailedCalls     int64
	TotalDurationMS *int64
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd backend && go test ./internal/storage/sqlite/ -run SessionToolCalls -v`

Expected: PASS.

- [ ] **Step 6: Verify no migration numbering conflict**

Run: `ls backend/internal/storage/sqlite/migrations | tail -3`

Expected: `0136_session_tool_calls.sql` is the highest number. If another branch already claimed 0136, renumber to the next free number — the sibling `migrate_*_renumber_test.go` files exist because this happens.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/storage/sqlite/migrations/0136_session_tool_calls.sql backend/internal/domain/toolcall.go backend/internal/storage/sqlite/migrate_session_tool_calls_test.go
git commit -m "feat(usage): add session tool-call and effort rollup tables"
```

---

### Task 2: Decode opencode tool parts

**Files:**
- Create: `backend/internal/observe/usage/opencode_tools.go`
- Create: `backend/internal/observe/usage/opencode_tools_test.go`

**Interfaces:**
- Consumes: `openCodePart` (Phase 2 Task 2); `domain.SessionToolCall`, `domain.ToolOutcome` (Task 1).
- Produces: `func decodeOpenCodeToolCall(sessionID domain.SessionID, part openCodePart, now time.Time) (domain.SessionToolCall, bool)` and `func openCodeCompactionCount(parts []openCodePart) int64`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/observe/usage/opencode_tools_test.go`:

```go
func TestDecodeOpenCodeToolCallWithTiming(t *testing.T) {
	part := openCodePart{Seq: 1, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_a","tool":"bash",
		"state":{"status":"completed","title":"npm test",
		"time":{"start":1773336859125,"end":1773336863340}}}`)}

	got, ok := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.ToolName != "bash" || got.ProviderCallID != "call_a" {
		t.Fatalf("tool = (%q, %q), want (bash, call_a)", got.ToolName, got.ProviderCallID)
	}
	if got.Outcome != domain.ToolOutcomeCompleted {
		t.Fatalf("Outcome = %q, want completed", got.Outcome)
	}
	if got.DurationMS == nil || *got.DurationMS != 4215 {
		t.Fatalf("DurationMS = %v, want 4215", got.DurationMS)
	}
	if got.SourceKind != domain.UsageSourceOpenCodeDB {
		t.Fatalf("SourceKind = %q, want opencode_db", got.SourceKind)
	}
}

func TestDecodeOpenCodeToolCallWithoutTimingStaysUnknown(t *testing.T) {
	part := openCodePart{Seq: 2, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_b","tool":"read",
		"state":{"status":"completed","title":"read file"}}`)}

	got, ok := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.DurationMS != nil {
		t.Fatalf("DurationMS = %v, want nil — absent timing must not become zero", got.DurationMS)
	}
	if got.StartedAt != nil || got.EndedAt != nil {
		t.Fatal("StartedAt/EndedAt set, want nil for an untimed call")
	}
}

func TestDecodeOpenCodeToolCallErrorStatusIsFailed(t *testing.T) {
	part := openCodePart{Seq: 3, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_c","tool":"bash",
		"state":{"status":"error","title":"exit 1",
		"time":{"start":1773336870000,"end":1773336871000}}}`)}

	got, _ := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if got.Outcome != domain.ToolOutcomeFailed {
		t.Fatalf("Outcome = %q, want failed", got.Outcome)
	}
}

func TestDecodeOpenCodeToolCallNeverStoresOutput(t *testing.T) {
	part := openCodePart{Seq: 4, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_d","tool":"read",
		"state":{"status":"completed","title":"t","output":"SECRET-TOKEN-VALUE"}}`)}

	got, _ := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if strings.Contains(got.InputSummary, "SECRET-TOKEN-VALUE") {
		t.Fatal("tool output leaked into InputSummary")
	}
}

func TestDecodeOpenCodeToolCallIgnoresNonToolParts(t *testing.T) {
	part := openCodePart{Seq: 5, Type: "step-finish", Data: []byte(`{"type":"step-finish"}`)}
	if _, ok := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0)); ok {
		t.Fatal("ok = true for a non-tool part, want false")
	}
}

func TestDecodeOpenCodeToolCallDetectsMCPTools(t *testing.T) {
	part := openCodePart{Seq: 6, Type: "tool", Data: []byte(`{
		"type":"tool","callID":"call_e","tool":"mcp__playwright__browser_click",
		"state":{"status":"completed","title":"click"}}`)}

	got, _ := decodeOpenCodeToolCall("sess-1", part, time.Unix(0, 0))
	if !got.IsMCP || got.MCPServer != "playwright" {
		t.Fatalf("MCP = (%v, %q), want (true, playwright)", got.IsMCP, got.MCPServer)
	}
}

func TestOpenCodeCompactionCount(t *testing.T) {
	parts := []openCodePart{
		{Seq: 1, Type: "tool"},
		{Seq: 2, Type: "compaction"},
		{Seq: 3, Type: "compaction"},
	}
	if got := openCodeCompactionCount(parts); got != 2 {
		t.Fatalf("openCodeCompactionCount = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/observe/usage/ -run OpenCodeTool -v`

Expected: FAIL — `decodeOpenCodeToolCall` undefined.

- [ ] **Step 3: Implement the decoder**

Create `backend/internal/observe/usage/opencode_tools.go`:

```go
package usage

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const maxToolInputSummary = 512

type openCodeToolPart struct {
	CallID string `json:"callID"`
	Tool   string `json:"tool"`
	Type   string `json:"type"`
	State  struct {
		Status string `json:"status"`
		Title  string `json:"title"`
		Time   *struct {
			End   int64 `json:"end"`
			Start int64 `json:"start"`
		} `json:"time"`
	} `json:"state"`
}

// decodeOpenCodeToolCall converts one opencode part into a durable tool-call
// fact. Only `state.title` is used as the summary — never `state.output`,
// which is unbounded and frequently sensitive.
func decodeOpenCodeToolCall(
	sessionID domain.SessionID, part openCodePart, now time.Time,
) (domain.SessionToolCall, bool) {
	if part.Type != "tool" {
		return domain.SessionToolCall{}, false
	}
	var decoded openCodeToolPart
	if err := json.Unmarshal(part.Data, &decoded); err != nil {
		return domain.SessionToolCall{}, false
	}
	if strings.TrimSpace(decoded.Tool) == "" || strings.TrimSpace(decoded.CallID) == "" {
		return domain.SessionToolCall{}, false
	}

	call := domain.SessionToolCall{
		InputSummary:   boundedToolSummary(decoded.State.Title),
		ObservedAt:     now,
		Outcome:        openCodeToolOutcome(decoded.State.Status),
		ProviderCallID: decoded.CallID,
		SessionID:      sessionID,
		SourceKind:     domain.UsageSourceOpenCodeDB,
		ToolName:       decoded.Tool,
	}
	call.IsMCP, call.MCPServer = mcpToolIdentity(decoded.Tool)

	// Timing is optional. Only a start-and-end pair that makes sense becomes a
	// duration; anything else stays unknown.
	if t := decoded.State.Time; t != nil && t.Start > 0 && t.End >= t.Start {
		started := time.UnixMilli(t.Start).UTC()
		ended := time.UnixMilli(t.End).UTC()
		duration := t.End - t.Start
		call.StartedAt = &started
		call.EndedAt = &ended
		call.DurationMS = &duration
	}
	return call, true
}

func openCodeToolOutcome(status string) domain.ToolOutcome {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "success":
		return domain.ToolOutcomeCompleted
	case "error", "failed":
		return domain.ToolOutcomeFailed
	case "denied", "rejected":
		return domain.ToolOutcomeDenied
	default:
		return domain.ToolOutcomeUnknown
	}
}

// mcpToolIdentity recognizes the mcp__<server>__<tool> naming convention.
func mcpToolIdentity(tool string) (bool, string) {
	if !strings.HasPrefix(tool, "mcp__") {
		return false, ""
	}
	parts := strings.SplitN(strings.TrimPrefix(tool, "mcp__"), "__", 2)
	if len(parts) != 2 || parts[0] == "" {
		return true, ""
	}
	return true, parts[0]
}

func boundedToolSummary(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) <= maxToolInputSummary {
		return trimmed
	}
	return trimmed[:maxToolInputSummary]
}

func openCodeCompactionCount(parts []openCodePart) int64 {
	var count int64
	for _, part := range parts {
		if part.Type == "compaction" {
			count++
		}
	}
	return count
}
```

Apply AO's existing redaction helper to `boundedToolSummary`'s input if one exists (see the Global Constraints note); truncation alone is not redaction.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/observe/usage/ -run 'OpenCodeTool|OpenCodeCompaction' -v`

Expected: PASS, all seven tests.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/observe/usage/opencode_tools.go backend/internal/observe/usage/opencode_tools_test.go
git commit -m "feat(usage): decode opencode tool-call parts with optional timing"
```

---

### Task 3: Derive effort counters

**Files:**
- Create: `backend/internal/observe/usage/effort.go`
- Create: `backend/internal/observe/usage/effort_test.go`

**Interfaces:**
- Consumes: `domain.SessionToolCall`, `domain.SessionEffort` (Task 1).
- Produces:
  - `const activeGapThreshold = 120 * time.Second`
  - `var testRunnerPatterns = []string{...}`
  - `func deriveEffort(calls []domain.SessionToolCall, firstEvent, lastEvent time.Time, compactions int64) domain.SessionEffort`
  - `func isTestCommand(summary string) bool`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/observe/usage/effort_test.go`:

```go
func timedCall(tool string, startMS, endMS int64) domain.SessionToolCall {
	started := time.UnixMilli(startMS).UTC()
	ended := time.UnixMilli(endMS).UTC()
	duration := endMS - startMS
	return domain.SessionToolCall{
		DurationMS: &duration, EndedAt: &ended, Outcome: domain.ToolOutcomeCompleted,
		StartedAt: &started, ToolName: tool,
	}
}

func TestDeriveEffortUnionsOverlappingCallSpans(t *testing.T) {
	// Two overlapping 4s calls cover 6s of wall clock, not 8s.
	calls := []domain.SessionToolCall{
		timedCall("bash", 1_000, 5_000),
		timedCall("bash", 3_000, 7_000),
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(10_000).UTC(), 0)

	if !got.TimingAvailable {
		t.Fatal("TimingAvailable = false, want true")
	}
	if got.ActiveMS == nil || *got.ActiveMS != 6_000 {
		t.Fatalf("ActiveMS = %v, want 6000 (union, not sum)", got.ActiveMS)
	}
	if got.DurationMS == nil || *got.DurationMS != 10_000 {
		t.Fatalf("DurationMS = %v, want 10000", got.DurationMS)
	}
	if got.IdleMS == nil || *got.IdleMS != 4_000 {
		t.Fatalf("IdleMS = %v, want 4000", got.IdleMS)
	}
}

func TestDeriveEffortLeavesTimingUnavailableWithNoTimedCalls(t *testing.T) {
	calls := []domain.SessionToolCall{
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "Read"},
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "Edit"},
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(10_000).UTC(), 0)

	if got.TimingAvailable {
		t.Fatal("TimingAvailable = true, want false")
	}
	if got.ActiveMS != nil || got.IdleMS != nil {
		t.Fatalf("Active/Idle = (%v, %v), want both nil — no timing is not 'fully idle'", got.ActiveMS, got.IdleMS)
	}
	if got.ToolCalls != 2 {
		t.Fatalf("ToolCalls = %d, want 2 — counts work without timing", got.ToolCalls)
	}
}

func TestDeriveEffortCountsTestRuns(t *testing.T) {
	calls := []domain.SessionToolCall{
		{InputSummary: "npm test", Outcome: domain.ToolOutcomeCompleted, ToolName: "bash"},
		{InputSummary: "go test ./...", Outcome: domain.ToolOutcomeCompleted, ToolName: "bash"},
		{InputSummary: "ls -la", Outcome: domain.ToolOutcomeCompleted, ToolName: "bash"},
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(1_000).UTC(), 0)

	if got.CommandsRun != 3 {
		t.Fatalf("CommandsRun = %d, want 3", got.CommandsRun)
	}
	if got.TestsRun != 2 {
		t.Fatalf("TestsRun = %d, want 2", got.TestsRun)
	}
}

func TestDeriveEffortCountsReadsAndEdits(t *testing.T) {
	calls := []domain.SessionToolCall{
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "read"},
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "Read"},
		{Outcome: domain.ToolOutcomeCompleted, ToolName: "edit"},
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(1_000).UTC(), 0)

	if got.FilesRead != 2 {
		t.Fatalf("FilesRead = %d, want 2 (tool names are case-insensitive)", got.FilesRead)
	}
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
}

func TestDeriveEffortCarriesCompactions(t *testing.T) {
	got := deriveEffort(nil, time.UnixMilli(0).UTC(), time.UnixMilli(1_000).UTC(), 3)
	if got.Compactions != 3 {
		t.Fatalf("Compactions = %d, want 3", got.Compactions)
	}
}

func TestIsTestCommand(t *testing.T) {
	for _, command := range []string{"npm test", "go test ./...", "pytest -q", "npx vitest run"} {
		if !isTestCommand(command) {
			t.Errorf("isTestCommand(%q) = false, want true", command)
		}
	}
	for _, command := range []string{"npm run build", "go build ./...", "ls"} {
		if isTestCommand(command) {
			t.Errorf("isTestCommand(%q) = true, want false", command)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/observe/usage/ -run 'Effort|TestCommand' -v`

Expected: FAIL — `deriveEffort` undefined.

- [ ] **Step 3: Implement the derivation**

Create `backend/internal/observe/usage/effort.go`:

```go
package usage

import (
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// activeGapThreshold bounds how long a gap between observed events still
// counts as the agent working. Longer gaps are idle.
const activeGapThreshold = 120 * time.Second

// testRunnerPatterns recognizes test commands. This is a HEURISTIC: a project
// with a custom test command undercounts, which is why tests-run is displayed
// as effort and never used as a governance gate.
var testRunnerPatterns = []string{
	"go test", "npm test", "npm run test", "pnpm test", "yarn test",
	"pytest", "vitest", "jest", "cargo test", "dotnet test", "mvn test", "rspec",
}

var readToolNames = map[string]bool{"read": true, "grep": true, "glob": true, "search": true}
var editToolNames = map[string]bool{"edit": true, "write": true, "multiedit": true, "patch": true}
var commandToolNames = map[string]bool{"bash": true, "shell": true, "run": true, "terminal": true}

// deriveEffort computes the per-session counter block. Active time is the
// UNION of timed tool spans, so overlapping calls are not double counted.
// When no call carries timing, active and idle stay unavailable rather than
// being guessed from wall clock.
func deriveEffort(
	calls []domain.SessionToolCall, firstEvent, lastEvent time.Time, compactions int64,
) domain.SessionEffort {
	out := domain.SessionEffort{Compactions: compactions, ToolCalls: int64(len(calls))}

	if !lastEvent.Before(firstEvent) {
		duration := lastEvent.Sub(firstEvent).Milliseconds()
		out.DurationMS = &duration
	}

	spans := make([][2]int64, 0, len(calls))
	for _, call := range calls {
		name := strings.ToLower(strings.TrimSpace(call.ToolName))
		switch {
		case readToolNames[name]:
			out.FilesRead++
		case editToolNames[name]:
			out.FilesChanged++
		case commandToolNames[name]:
			out.CommandsRun++
			if isTestCommand(call.InputSummary) {
				out.TestsRun++
			}
		}
		if call.StartedAt != nil && call.EndedAt != nil {
			spans = append(spans, [2]int64{call.StartedAt.UnixMilli(), call.EndedAt.UnixMilli()})
		}
	}

	if len(spans) == 0 {
		return out
	}
	out.TimingAvailable = true
	active := unionSpanMillis(spans)
	out.ActiveMS = &active
	if out.DurationMS != nil {
		idle := *out.DurationMS - active
		if idle < 0 {
			idle = 0
		}
		out.IdleMS = &idle
	}
	return out
}

// unionSpanMillis measures the total wall-clock time covered by any span,
// merging overlaps. Gaps shorter than activeGapThreshold are treated as
// continued work, matching the spec's definition of active time.
func unionSpanMillis(spans [][2]int64) int64 {
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	gap := activeGapThreshold.Milliseconds()
	var (
		total          int64
		start, end     = spans[0][0], spans[0][1]
	)
	for _, span := range spans[1:] {
		if span[0] <= end+gap {
			if span[1] > end {
				end = span[1]
			}
			continue
		}
		total += end - start
		start, end = span[0], span[1]
	}
	return total + end - start
}

func isTestCommand(summary string) bool {
	lowered := strings.ToLower(summary)
	for _, pattern := range testRunnerPatterns {
		if strings.Contains(lowered, pattern) {
			return true
		}
	}
	return false
}
```

Note the deliberate interaction: `unionSpanMillis` merges spans separated by less than `activeGapThreshold`, which is why the overlap test expects 6,000ms — the two spans overlap outright. Add a case for two spans separated by more than the threshold if you want that branch covered explicitly.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/observe/usage/ -run 'Effort|TestCommand' -v`

Expected: PASS, all six tests.

- [ ] **Step 5: Add the threshold-gap test the implementation implies**

```go
func TestDeriveEffortSplitsSpansSeparatedByALongGap(t *testing.T) {
	// 121s apart: beyond activeGapThreshold, so the gap is idle, not active.
	calls := []domain.SessionToolCall{
		timedCall("bash", 0, 1_000),
		timedCall("bash", 122_000, 123_000),
	}
	got := deriveEffort(calls, time.UnixMilli(0).UTC(), time.UnixMilli(123_000).UTC(), 0)

	if got.ActiveMS == nil || *got.ActiveMS != 2_000 {
		t.Fatalf("ActiveMS = %v, want 2000 — a gap over the threshold is idle", got.ActiveMS)
	}
}
```

Run: `cd backend && go test ./internal/observe/usage/ -run LongGap -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/observe/usage/effort.go backend/internal/observe/usage/effort_test.go
git commit -m "feat(usage): derive session effort counters with union-based active time"
```

---

### Task 4: Persist tool calls and rollups

**Files:**
- Create: `backend/internal/storage/sqlite/queries/tool_calls.sql`
- Create: `backend/internal/storage/sqlite/store/tool_call_store.go`
- Create: `backend/internal/storage/sqlite/store/tool_call_store_test.go`
- Modify: `backend/internal/observe/usage/opencode_collect.go` — write tool calls in the same pass

**Interfaces:**
- Consumes: types from Task 1; `decodeOpenCodeToolCall`, `openCodeCompactionCount` (Task 2); `deriveEffort` (Task 3); `OpenCodeCollector` (Phase 2 Task 4).
- Produces:
  - `func (s *Store) UpsertSessionToolCalls(ctx context.Context, calls []domain.SessionToolCall) error`
  - `func (s *Store) ListSessionToolCalls(ctx context.Context, sessionID domain.SessionID) ([]domain.SessionToolCall, error)`
  - `func (s *Store) UpsertSessionEffort(ctx context.Context, sessionID domain.SessionID, effort domain.SessionEffort, at time.Time) error`
  - `func (s *Store) GetSessionEffort(ctx context.Context, sessionID domain.SessionID) (domain.SessionEffort, bool, error)`
  - `func (s *Store) SessionToolMix(ctx context.Context, sessionID domain.SessionID) ([]domain.ToolMixEntry, error)`

Tasks 5-6 consume the read methods.

- [ ] **Step 1: Write the queries**

Create `backend/internal/storage/sqlite/queries/tool_calls.sql`:

```sql
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
ORDER BY total_duration_ms DESC NULLS LAST, calls DESC;

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
```

`SUM(duration_ms)` over an all-NULL group returns NULL, which is exactly the "no timing for this tool" signal the UI needs. `timed_calls` lets the read layer distinguish "no timing at all" from "some calls timed".

- [ ] **Step 2: Generate the sqlc code**

Run: `npm run sqlc`

Expected: new methods appear in `backend/internal/storage/sqlite/gen/`. If sqlc errors on `NULLS LAST`, drop it — SQLite sorts NULLs last for `DESC` by default — and re-run.

- [ ] **Step 3: Write the failing store test**

Create `backend/internal/storage/sqlite/store/tool_call_store_test.go`:

```go
func TestUpsertAndListSessionToolCalls(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	duration := int64(4215)
	started := time.UnixMilli(1_773_336_859_125).UTC()
	ended := time.UnixMilli(1_773_336_863_340).UTC()
	calls := []domain.SessionToolCall{
		{
			DurationMS: &duration, EndedAt: &ended, InputSummary: "npm test",
			ObservedAt: ended, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "call_a", SessionID: "sess-1",
			SourceKind: domain.UsageSourceOpenCodeDB, StartedAt: &started, ToolName: "bash",
		},
		{
			InputSummary: "src/main.go", ObservedAt: ended,
			Outcome: domain.ToolOutcomeCompleted, ProviderCallID: "call_b",
			SessionID: "sess-1", SourceKind: domain.UsageSourceClaudeMain, ToolName: "Read",
		},
	}

	if err := store.UpsertSessionToolCalls(ctx, calls); err != nil {
		t.Fatalf("UpsertSessionToolCalls: %v", err)
	}
	// Idempotent: re-applying the same batch must not duplicate rows.
	if err := store.UpsertSessionToolCalls(ctx, calls); err != nil {
		t.Fatalf("second UpsertSessionToolCalls: %v", err)
	}

	got, err := store.ListSessionToolCalls(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListSessionToolCalls: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(calls) = %d, want 2 after a repeated batch", len(got))
	}
	var untimed int
	for _, call := range got {
		if call.DurationMS == nil {
			untimed++
		}
	}
	if untimed != 1 {
		t.Fatalf("untimed calls = %d, want 1 — NULL timing must round-trip as nil", untimed)
	}
}

func TestSessionToolMixOrdersByTimeThenCount(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	observed := time.UnixMilli(1_773_336_863_340).UTC()

	slow := int64(9_000)
	fast := int64(100)
	if err := store.UpsertSessionToolCalls(ctx, []domain.SessionToolCall{
		{DurationMS: &slow, ObservedAt: observed, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "a", SessionID: "sess-1", SourceKind: domain.UsageSourceOpenCodeDB, ToolName: "bash"},
		{DurationMS: &fast, ObservedAt: observed, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "b", SessionID: "sess-1", SourceKind: domain.UsageSourceOpenCodeDB, ToolName: "read"},
		{DurationMS: &fast, ObservedAt: observed, Outcome: domain.ToolOutcomeCompleted,
			ProviderCallID: "c", SessionID: "sess-1", SourceKind: domain.UsageSourceOpenCodeDB, ToolName: "read"},
	}); err != nil {
		t.Fatalf("UpsertSessionToolCalls: %v", err)
	}

	mix, err := store.SessionToolMix(ctx, "sess-1")
	if err != nil {
		t.Fatalf("SessionToolMix: %v", err)
	}
	if len(mix) != 2 || mix[0].ToolName != "bash" {
		t.Fatalf("mix = %+v, want bash first (9000ms beats 2 calls totalling 200ms)", mix)
	}
	if mix[1].Calls != 2 {
		t.Fatalf("read calls = %d, want 2", mix[1].Calls)
	}
}

func TestSessionEffortRoundTripsUnavailableTiming(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	duration := int64(10_000)

	effort := domain.SessionEffort{
		DurationMS: &duration, ToolCalls: 5, TimingAvailable: false,
	}
	if err := store.UpsertSessionEffort(ctx, "sess-1", effort, time.UnixMilli(1).UTC()); err != nil {
		t.Fatalf("UpsertSessionEffort: %v", err)
	}

	got, ok, err := store.GetSessionEffort(ctx, "sess-1")
	if err != nil || !ok {
		t.Fatalf("GetSessionEffort = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.ActiveMS != nil || got.IdleMS != nil {
		t.Fatalf("Active/Idle = (%v, %v), want both nil", got.ActiveMS, got.IdleMS)
	}
	if got.TimingAvailable {
		t.Fatal("TimingAvailable = true, want false")
	}
}
```

Use the store package's existing test-store helper rather than `newTestStore` if it is named differently (check a sibling `*_store_test.go`).

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run 'ToolCall|ToolMix|SessionEffort' -v`

Expected: FAIL — the store methods do not exist.

- [ ] **Step 5: Implement the store methods**

Create `backend/internal/storage/sqlite/store/tool_call_store.go`, wrapping the generated queries and converting between `*int64`/`*time.Time` and `sql.NullInt64`. Follow the null-handling helpers already used in `usage_store.go` (`grep -n 'sql.NullInt64' backend/internal/storage/sqlite/store/usage_store.go | head`) rather than inventing new ones. `UpsertSessionToolCalls` must run the whole batch in one transaction so a partial batch is never visible.

For `SessionToolMix`, map `timed_calls == 0` to `TotalDurationMS = nil` even when SQL returned a non-NULL sum, so "no timing" is unambiguous.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run 'ToolCall|ToolMix|SessionEffort' -v`

Expected: PASS, all three tests.

- [ ] **Step 7: Write tool calls from the opencode collector**

In `opencode_collect.go`'s `Collect`, after `readOpenCode` and before `ApplyUsageChunk`, decode and persist the tool calls and the rollup:

```go
	toolCalls := make([]domain.SessionToolCall, 0, len(parts))
	for _, part := range parts {
		if call, ok := decodeOpenCodeToolCall(source.SessionID, part, now); ok {
			toolCalls = append(toolCalls, call)
		}
	}
	if len(toolCalls) > 0 {
		if err := c.store.UpsertSessionToolCalls(ctx, toolCalls); err != nil {
			return c.fail(ctx, source, domain.UsageErrorSourceReadFailed, now)
		}
	}
```

Then, after the chunk commits, recompute the rollup from all stored calls for the session:

```go
	stored, err := c.store.ListSessionToolCalls(ctx, source.SessionID)
	if err != nil {
		return nil
	}
	effort := deriveEffort(stored, firstObserved(stored), lastObserved(stored), state.Compactions)
	effort.FilesChanged = session.SummaryFiles
	effort.LinesAdded = session.SummaryAdditions
	effort.LinesRemoved = session.SummaryDeletions
	return c.store.UpsertSessionEffort(ctx, source.SessionID, effort, now)
```

opencode's own `summary_files/_additions/_deletions` are authoritative for churn and override the tool-name heuristic, which only counts edit *calls*. Add `firstObserved`/`lastObserved` helpers returning the min and max `ObservedAt` (falling back to `StartedAt` when present). Extend `openCodeCollectorStore` with the three new store methods.

- [ ] **Step 8: Run the tests to verify nothing regressed**

Run: `cd backend && go test ./internal/observe/usage/ ./internal/storage/sqlite/... -count=1`

Expected: PASS. Phase 2's `TestOpenCodeCollectorPersistsEventsOnce` needs its fake store extended with the new methods — update it.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/storage/sqlite backend/internal/observe/usage/opencode_collect.go
git commit -m "feat(usage): persist tool calls and session effort rollups"
```

---

### Task 5: Expose effort and tool mix over the API

**Files:**
- Modify: `backend/internal/httpd/controllers/usage.go`
- Create: `backend/internal/service/usage/effort.go`
- Test: `backend/internal/httpd/controllers/usage_test.go`

**Interfaces:**
- Consumes: store read methods from Task 4.
- Produces: `GET /api/v1/usage/sessions/{sessionId}/effort` returning
  `SessionEffortResponse{ effort: {durationMs, activeMs, idleMs, toolCalls, filesRead, filesChanged, linesAdded, linesRemoved, commandsRun, testsRun, compactions, timingAvailable}, toolMix: [{toolName, calls, failedCalls, totalDurationMs}] }`, where every `*Ms` field is `number | null`.

Task 6 consumes this response.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/httpd/controllers/usage_test.go`:

```go
func TestGetSessionEffortReturnsNullForUnknownTiming(t *testing.T) {
	controller := &controllers.UsageController{
		Svc:    stubUsageSummaryService{},
		Effort: stubEffortService{effort: domain.SessionEffort{ToolCalls: 5, TimingAvailable: false}},
	}
	router := chi.NewRouter()
	controller.Register(router)

	request := httptest.NewRequest(http.MethodGet, "/usage/sessions/sess-1/effort", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Effort struct {
			ActiveMs        *int64 `json:"activeMs"`
			ToolCalls       int64  `json:"toolCalls"`
			TimingAvailable bool   `json:"timingAvailable"`
		} `json:"effort"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Effort.ActiveMs != nil {
		t.Fatalf("activeMs = %v, want null", body.Effort.ActiveMs)
	}
	if body.Effort.ToolCalls != 5 || body.Effort.TimingAvailable {
		t.Fatalf("effort = %+v, want 5 calls and timingAvailable false", body.Effort)
	}
}

func TestGetSessionEffortIsNotImplementedWithoutService(t *testing.T) {
	controller := &controllers.UsageController{Svc: stubUsageSummaryService{}}
	router := chi.NewRouter()
	controller.Register(router)

	request := httptest.NewRequest(http.MethodGet, "/usage/sessions/sess-1/effort", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code == http.StatusOK {
		t.Fatal("status = 200 with no effort service, want a not-implemented status")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run SessionEffort -v`

Expected: FAIL — no `Effort` field and no route.

- [ ] **Step 3: Add the service and the route**

Create `backend/internal/service/usage/effort.go`:

```go
package usage

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type effortStore interface {
	GetSessionEffort(context.Context, domain.SessionID) (domain.SessionEffort, bool, error)
	SessionToolMix(context.Context, domain.SessionID) ([]domain.ToolMixEntry, error)
}

// EffortService reads the derived effort block and tool mix for one session.
type EffortService struct{ store effortStore }

// NewEffortService builds the read service.
func NewEffortService(store effortStore) *EffortService { return &EffortService{store: store} }

// Get returns the session's effort counters and tool mix. A session with no
// recorded telemetry returns found=false so callers render "unavailable"
// rather than a block of zeros.
func (s *EffortService) Get(
	ctx context.Context, id domain.SessionID,
) (domain.SessionEffort, []domain.ToolMixEntry, bool, error) {
	effort, found, err := s.store.GetSessionEffort(ctx, id)
	if err != nil || !found {
		return domain.SessionEffort{}, nil, false, err
	}
	mix, err := s.store.SessionToolMix(ctx, id)
	if err != nil {
		return domain.SessionEffort{}, nil, false, err
	}
	return effort, mix, true, nil
}
```

In `controllers/usage.go`, add the interface, the field, the route and the response types:

```go
// EffortService is the controller-facing effort read contract.
type EffortService interface {
	Get(context.Context, domain.SessionID) (domain.SessionEffort, []domain.ToolMixEntry, bool, error)
}

// SessionEffortResponse is the effort and tool-mix read model. Every nullable
// millisecond field is a pointer: null means AO could not measure it.
type SessionEffortResponse struct {
	Effort  EffortResponse      `json:"effort"`
	ToolMix []ToolMixResponse   `json:"toolMix"`
}

// EffortResponse carries the per-session counters.
type EffortResponse struct {
	ActiveMs        *int64 `json:"activeMs"`
	Compactions     int64  `json:"compactions"`
	CommandsRun     int64  `json:"commandsRun"`
	DurationMs      *int64 `json:"durationMs"`
	FilesChanged    int64  `json:"filesChanged"`
	FilesRead       int64  `json:"filesRead"`
	IdleMs          *int64 `json:"idleMs"`
	LinesAdded      int64  `json:"linesAdded"`
	LinesRemoved    int64  `json:"linesRemoved"`
	TestsRun        int64  `json:"testsRun"`
	TimingAvailable bool   `json:"timingAvailable"`
	ToolCalls       int64  `json:"toolCalls"`
}

// ToolMixResponse is one tool's share of the session.
type ToolMixResponse struct {
	Calls           int64  `json:"calls"`
	FailedCalls     int64  `json:"failedCalls"`
	ToolName        string `json:"toolName"`
	TotalDurationMs *int64 `json:"totalDurationMs"`
}
```

Add `Effort EffortService` to `UsageController`, register `r.Get("/usage/sessions/{sessionId}/effort", c.getSessionEffort)`, and follow the existing `getSession` shape: `apispec.NotImplemented` when the service is nil, `envelope.WriteError` on error, `envelope.WriteJSON` on success. Return 404 via the envelope when `found` is false.

Wire the service in `backend/internal/httpd/api.go` beside `usage: &controllers.UsageController{Svc: deps.UsageSummary}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/httpd/... -run 'SessionEffort|Parity' -v`

Expected: PASS. `TestRouteSpecParity` will fail until the spec is regenerated — that is the next step.

- [ ] **Step 5: Regenerate the API contract**

Run: `npm run api`

Expected: `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts` both change. Confirm the effort path appears:

Run: `grep -n 'effort' backend/internal/httpd/apispec/openapi.yaml | head`

- [ ] **Step 6: Run the full backend suite and linter**

Run: `npm run lint`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/httpd backend/internal/service/usage/effort.go frontend/src/api/schema.ts
git commit -m "feat(api): expose session effort counters and tool mix"
```

---

### Task 6: Render the Effort and Tool mix blocks

**Files:**
- Create: `frontend/src/renderer/hooks/useSessionEffort.ts`
- Modify: `frontend/src/renderer/components/SessionInspectorMetrics.tsx`
- Modify: `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx`
- Modify: `frontend/src/renderer/i18n/*.json`

**Interfaces:**
- Consumes: `GET /api/v1/usage/sessions/{sessionId}/effort` (Task 5); `MetricsView` (Phase 1 Task 3).
- Produces: no exported API beyond `useSessionEffort`.

- [ ] **Step 1: Add the i18n keys**

Run from the repository root:

```bash
python3 - <<'PY'
import json, pathlib

keys = {
    "inspector.metrics.effort.title": "Effort",
    "inspector.metrics.effort.duration": "Duration",
    "inspector.metrics.effort.active": "Active",
    "inspector.metrics.effort.idle": "Idle",
    "inspector.metrics.effort.timingUnavailable": "This agent does not report tool timing, so active and idle time are unknown.",
    "inspector.metrics.effort.toolCalls": "Tool calls",
    "inspector.metrics.effort.filesRead": "Files read",
    "inspector.metrics.effort.filesChanged": "Files changed",
    "inspector.metrics.effort.linesChanged": "Lines changed",
    "inspector.metrics.effort.commandsRun": "Commands run",
    "inspector.metrics.effort.testsRun": "Tests run",
    "inspector.metrics.effort.testsRunNote": "Detected from command names; a custom test command may not be counted.",
    "inspector.metrics.effort.compactions": "Compactions",
    "inspector.metrics.tools.title": "Tool mix",
    "inspector.metrics.tools.sortedByTime": "Sorted by time spent",
    "inspector.metrics.tools.sortedByCount": "Sorted by call count — this agent does not report tool timing",
    "inspector.metrics.tools.calls": "{{count}} calls",
    "inspector.metrics.tools.failed": "{{count}} failed",
    "inspector.metrics.tools.noTiming": "no timing",
    "inspector.metrics.tools.empty": "No tool calls recorded for this session.",
}

for path in sorted(pathlib.Path("frontend/src/renderer/i18n").glob("*.json")):
    data = json.loads(path.read_text(encoding="utf-8"))
    for key, value in keys.items():
        data.setdefault(key, value)
    path.write_text(
        json.dumps({k: data[k] for k in sorted(data)}, ensure_ascii=False, indent="\t") + "\n",
        encoding="utf-8",
    )
    print("updated", path)
PY
```

Verify: `grep -l '"inspector.metrics.tools.title"' frontend/src/renderer/i18n/*.json | wc -l` → `8`

- [ ] **Step 2: Write the failing test**

Add to `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx`:

```tsx
it("sorts the tool mix by time spent when timing exists", () => {
  mockEffort = {
    data: {
      effort: { ...baseEffort, timingAvailable: true, activeMs: 9100, idleMs: 900, durationMs: 10000 },
      toolMix: [
        { calls: 1, failedCalls: 0, toolName: "bash", totalDurationMs: 9000 },
        { calls: 2, failedCalls: 0, toolName: "read", totalDurationMs: 200 },
      ],
    },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  const rows = screen.getAllByTestId("tool-mix-row");
  expect(rows[0]).toHaveTextContent("bash");
  expect(screen.getByText(/Sorted by time spent/i)).toBeInTheDocument();
});

it("falls back to count ordering and says so when timing is absent", () => {
  mockEffort = {
    data: {
      effort: { ...baseEffort, timingAvailable: false, activeMs: null, idleMs: null, durationMs: 10000 },
      toolMix: [
        { calls: 2, failedCalls: 0, toolName: "read", totalDurationMs: null },
        { calls: 1, failedCalls: 0, toolName: "bash", totalDurationMs: null },
      ],
    },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/Sorted by call count/i)).toBeInTheDocument();
  expect(screen.getAllByTestId("tool-mix-row")[0]).toHaveTextContent("read");
});

it("never renders zero for unmeasured active time", () => {
  mockEffort = {
    data: {
      effort: { ...baseEffort, timingAvailable: false, activeMs: null, idleMs: null, durationMs: 10000 },
      toolMix: [],
    },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/active and idle time are unknown/i)).toBeInTheDocument();
  expect(screen.queryByTestId("effort-active-value")).not.toBeInTheDocument();
});

it("states when no tool calls were recorded", () => {
  mockEffort = { data: { effort: baseEffort, toolMix: [] }, isError: false, isLoading: false };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/No tool calls recorded/i)).toBeInTheDocument();
});
```

Add `const baseEffort = { activeMs: null, commandsRun: 0, compactions: 0, durationMs: null, filesChanged: 0, filesRead: 0, idleMs: null, linesAdded: 0, linesRemoved: 0, testsRun: 0, timingAvailable: false, toolCalls: 0 };` and a `vi.mock("../hooks/useSessionEffort", () => ({ useSessionEffort: () => mockEffort }))` beside the existing usage mock.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: FAIL — `useSessionEffort` does not exist.

- [ ] **Step 4: Add the query hook**

Create `frontend/src/renderer/hooks/useSessionEffort.ts`:

```ts
import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { sessionUsageQueryRoot } from "./useSessionUsageSummaries";

export type SessionEffort = components["schemas"]["SessionEffortResponse"];

export const sessionEffortQueryKey = (sessionId: string) =>
	[...sessionUsageQueryRoot, "effort", sessionId] as const;

export async function fetchSessionEffort(sessionId: string): Promise<SessionEffort> {
	const { data, error } = await apiClient.GET("/api/v1/usage/sessions/{sessionId}/effort", {
		params: { path: { sessionId } },
	});
	if (error) throw error;
	return data;
}

export function useSessionEffort(sessionId: string, enabled = true) {
	return useQuery({
		enabled: enabled && Boolean(sessionId),
		queryFn: () => fetchSessionEffort(sessionId),
		queryKey: sessionEffortQueryKey(sessionId),
		retry: 1,
	});
}
```

- [ ] **Step 5: Add the blocks to MetricsView**

In `SessionInspectorMetrics.tsx`, call the hook and render two new sections after the cost block and before Coverage:

```tsx
function EffortBlock({ effort }: { effort: SessionEffort["effort"] }) {
	const { t } = useTranslation();
	return (
		<Section title={t("inspector.metrics.effort.title")}>
			<dl className="grid grid-cols-2 gap-1.5">
				<EffortStat labelKey="inspector.metrics.effort.duration" value={formatDurationMs(effort.durationMs)} />
				{effort.timingAvailable ? (
					<>
						<EffortStat
							labelKey="inspector.metrics.effort.active"
							testId="effort-active-value"
							value={formatDurationMs(effort.activeMs)}
						/>
						<EffortStat labelKey="inspector.metrics.effort.idle" value={formatDurationMs(effort.idleMs)} />
					</>
				) : null}
				<EffortStat labelKey="inspector.metrics.effort.toolCalls" value={String(effort.toolCalls)} />
				<EffortStat labelKey="inspector.metrics.effort.filesRead" value={String(effort.filesRead)} />
				<EffortStat labelKey="inspector.metrics.effort.filesChanged" value={String(effort.filesChanged)} />
				<EffortStat labelKey="inspector.metrics.effort.commandsRun" value={String(effort.commandsRun)} />
				<EffortStat labelKey="inspector.metrics.effort.testsRun" value={String(effort.testsRun)} />
				<EffortStat labelKey="inspector.metrics.effort.compactions" value={String(effort.compactions)} />
			</dl>
			{effort.timingAvailable ? null : (
				<p className={inspectorEmptyClass}>{t("inspector.metrics.effort.timingUnavailable")}</p>
			)}
		</Section>
	);
}

function ToolMixBlock({ mix, timingAvailable }: { mix: SessionEffort["toolMix"]; timingAvailable: boolean }) {
	const { t } = useTranslation();
	if (mix.length === 0) {
		return (
			<Section title={t("inspector.metrics.tools.title")}>
				<p className={inspectorEmptyClass}>{t("inspector.metrics.tools.empty")}</p>
			</Section>
		);
	}
	// The API already orders by total duration; without timing that ordering is
	// meaningless, so re-sort by calls and say which ordering is in use.
	const rows = timingAvailable ? mix : [...mix].sort((a, b) => b.calls - a.calls);
	const totalCalls = rows.reduce((sum, row) => sum + row.calls, 0);
	return (
		<Section title={t("inspector.metrics.tools.title")}>
			<p className={inspectorEmptyClass}>
				{timingAvailable
					? t("inspector.metrics.tools.sortedByTime")
					: t("inspector.metrics.tools.sortedByCount")}
			</p>
			<ul className="flex flex-col gap-1">
				{rows.map((row) => (
					<li className="flex items-baseline justify-between gap-2" data-testid="tool-mix-row" key={row.toolName}>
						<span className="truncate font-medium">{row.toolName}</span>
						<span className="shrink-0 text-2xs text-settings-muted">
							{t("inspector.metrics.tools.calls", { count: row.calls })}
							{totalCalls > 0 ? ` · ${Math.round((row.calls / totalCalls) * 100)}%` : ""}
							{row.totalDurationMs === null
								? ` · ${t("inspector.metrics.tools.noTiming")}`
								: ` · ${formatDurationMs(row.totalDurationMs)}`}
							{row.failedCalls > 0 ? ` · ${t("inspector.metrics.tools.failed", { count: row.failedCalls })}` : ""}
						</span>
					</li>
				))}
			</ul>
		</Section>
	);
}
```

Add a `formatDurationMs(value: number | null): string` helper in `frontend/src/renderer/lib/` beside the existing `format-time.ts`, returning a localized dash for `null` — and add its key to all eight locales if it needs copy.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: PASS, all tests including Phase 1's.

- [ ] **Step 7: Verify typecheck and the copy rule**

Run: `npm run frontend:typecheck && npm --prefix frontend test -- renderer-coverage`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/renderer
git commit -m "feat(inspector): render effort and tool mix in the metrics tab"
```

---

### Task 7: The Context health block

**Files:**
- Modify: `frontend/src/renderer/components/SessionInspectorMetrics.tsx`
- Modify: `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx`
- Modify: `frontend/src/renderer/i18n/*.json`
- Reference: `frontend/src/renderer/components/chat/ContextMeter.tsx:131` — `ContextMeter({ usage?, rateLimits?, className? })`

**Interfaces:**
- Consumes: the `effort.compactions` field (Task 5); `useConversation`'s usage/rate-limit data, which already feeds `ContextMeter` in the chat header.
- Produces: no exported API.

This is the spec's Metrics block 2. It is separate from Effort because context pressure is **advisory** — it must never read as a failure state — whereas the effort counters are neutral facts.

- [ ] **Step 1: Find how ContextMeter gets its data today**

Run: `grep -rn "ContextMeter" frontend/src/renderer --include=*.tsx | grep -v ContextMeter.tsx`

Read the call site and note which hook supplies `usage` and `rateLimits`. Reuse that hook here rather than adding a new query — the data is already in the renderer.

- [ ] **Step 2: Add the i18n keys**

```bash
python3 - <<'PY'
import json, pathlib

keys = {
    "inspector.metrics.context.title": "Context health",
    "inspector.metrics.context.compactions": "Compactions",
    "inspector.metrics.context.lastAuto": "Last compaction was automatic",
    "inspector.metrics.context.lastOverflow": "Last compaction was triggered by overflow",
    "inspector.metrics.context.none": "No compactions yet.",
    "inspector.metrics.context.unavailable": "Context usage is not reported for this session.",
    "inspector.metrics.context.advisory": "Context pressure is informational: AO compacts and continues.",
}

for path in sorted(pathlib.Path("frontend/src/renderer/i18n").glob("*.json")):
    data = json.loads(path.read_text(encoding="utf-8"))
    for key, value in keys.items():
        data.setdefault(key, value)
    path.write_text(
        json.dumps({k: data[k] for k in sorted(data)}, ensure_ascii=False, indent="\t") + "\n",
        encoding="utf-8",
    )
    print("updated", path)
PY
```

Verify: `grep -l '"inspector.metrics.context.title"' frontend/src/renderer/i18n/*.json | wc -l` → `8`

- [ ] **Step 3: Write the failing test**

Add to `SessionInspectorMetrics.test.tsx`:

```tsx
it("shows the context meter and compaction count", () => {
  mockConversation = { usage: { inputTokens: 67000, contextWindow: 100000 } };
  mockEffort = {
    data: { effort: { ...baseEffort, compactions: 2 }, toolMix: [] },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/Context health/i)).toBeInTheDocument();
  expect(screen.getByTestId("context-compactions")).toHaveTextContent("2");
  // Advisory, never a failure.
  expect(screen.getByText(/informational/i)).toBeInTheDocument();
});

it("states that context usage is unreported rather than showing an empty meter", () => {
  mockConversation = { usage: undefined };
  mockEffort = { data: { effort: baseEffort, toolMix: [] }, isError: false, isLoading: false };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/not reported for this session/i)).toBeInTheDocument();
});

it("says there have been no compactions rather than rendering a bare zero", () => {
  mockConversation = { usage: { inputTokens: 1000, contextWindow: 100000 } };
  mockEffort = {
    data: { effort: { ...baseEffort, compactions: 0 }, toolMix: [] },
    isError: false,
    isLoading: false,
  };
  render(<MetricsView session={session} />);
  expect(screen.getByText(/No compactions yet/i)).toBeInTheDocument();
});
```

Add a `vi.mock` for whichever conversation hook Step 1 identified, exposing `mockConversation`. Match `ConversationUsage`'s real field names from `frontend/src/renderer/types/conversation.ts` — the shape above is illustrative and must be corrected to the actual type.

- [ ] **Step 4: Run the tests to verify they fail**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: FAIL — no Context health block.

- [ ] **Step 5: Add the block**

In `SessionInspectorMetrics.tsx`, insert between the cost block and the Effort block:

```tsx
function ContextHealthBlock({
	compactions,
	usage,
	rateLimits,
}: {
	compactions: number;
	usage?: ConversationUsage;
	rateLimits?: ConversationRateLimits;
}) {
	const { t } = useTranslation();
	return (
		<Section title={t("inspector.metrics.context.title")}>
			{usage ? (
				<ContextMeter rateLimits={rateLimits} usage={usage} />
			) : (
				<p className={inspectorEmptyClass}>{t("inspector.metrics.context.unavailable")}</p>
			)}
			{compactions > 0 ? (
				<div className="mt-1.5 flex items-baseline justify-between gap-2">
					<span className="text-2xs text-settings-muted">{t("inspector.metrics.context.compactions")}</span>
					<span className="font-semibold" data-testid="context-compactions">
						{compactions}
					</span>
				</div>
			) : (
				<p className={`mt-1.5 ${inspectorEmptyClass}`}>{t("inspector.metrics.context.none")}</p>
			)}
			{/* Context pressure is advisory. Saying so prevents a full meter from
			    reading as a broken session. */}
			<p className={`mt-1 ${inspectorEmptyClass}`}>{t("inspector.metrics.context.advisory")}</p>
		</Section>
	);
}
```

`ContextMeter` returns `null` when it has neither usage nor an actionable quota, which is why the `usage ?` guard is needed here — otherwise the section would render a title above nothing.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: PASS, all tests.

- [ ] **Step 7: Verify typecheck and the copy rule**

Run: `npm run frontend:typecheck && npm --prefix frontend test -- renderer-coverage`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/renderer
git commit -m "feat(inspector): add advisory context health block to the metrics tab"
```

---

### Task 8: Verify against a real opencode session

**Files:** none — manual gate.

- [ ] **Step 1: Run a real opencode worker**

Start AO, create an opencode worker, and give it a task that reads files and runs a test command.

- [ ] **Step 2: Confirm tool calls were recorded**

```bash
python3 - <<'PY'
import os, sqlite3
# Use the AO data directory this build actually uses; ~/.ao/dev/data for a dev build.
path = os.path.expanduser("~/.ao/dev/data/ao.db")
db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
for row in db.execute(
    "SELECT tool_name, outcome, duration_ms FROM session_tool_calls "
    "ORDER BY observed_at DESC LIMIT 10"
):
    print(row)
print("---")
for row in db.execute("SELECT * FROM session_effort_rollups LIMIT 5"):
    print(row)
PY
```

Confirm the database filename against `backend/internal/storage/sqlite/db.go` before running.

Expected: real tool names with non-null `duration_ms` (opencode reports timing), and one rollup row.

- [ ] **Step 3: Confirm the UI**

Open the session's Metrics tab.

Expected: Effort shows duration, active and idle; Tool mix is sorted by time spent and says so; test runs are counted.

- [ ] **Step 4: Confirm honest degradation on Claude Code**

Open a Claude Code session's Metrics tab.

Expected: tool counts where available, the "does not report tool timing" note, count-based ordering. **No zeros standing in for unknown timing.**

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "fix(usage): address tool telemetry verification findings"
```

---

## Phase Completion

```bash
npm run lint
npm run frontend:typecheck
npm --prefix frontend test
```

All three must pass. Phase 4 (scoring) consumes `domain.SessionEffort`, `domain.ToolMixEntry` and `domain.SessionToolCall` directly.

## Open question this phase answers

The spec defers one question to real data: whether Claude Code and Codex transcripts carry enough timing for the time-sorted tool mix, or whether those harnesses should default to count ordering. Step 4 of Task 8 is the measurement. Record the finding in the spec's Open Questions section when the phase closes.

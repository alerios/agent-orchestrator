# Metrics Phase 2: opencode Usage Source Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ingest opencode token and cost usage into AO's existing usage pipeline, so opencode sessions show the same metrics as Claude Code and Codex.

**Architecture:** opencode stores everything in one SQLite database at `~/.local/share/opencode/opencode.db`, with per-session token counters and cost already aggregated. A new `UsageSourceKind` (`opencode_db`) and a new reader open that database read-only and emit `domain.ModelUsageEvent` rows through the store contract the existing ingestor already uses (`ApplyUsageChunk`). Binding is an exact match of opencode's `session.directory` against AO's worktree path.

**Tech Stack:** Go 1.x, SQLite (`mode=ro`, WAL), sqlc-generated queries, `go test`, golangci-lint.

**Spec:** `docs/superpowers/specs/2026-09-11-session-metrics-and-efficiency-scoring-design.md`

## Global Constraints

- **Read-only, always.** The opencode database belongs to another application. Open it with `mode=ro` in the DSN and never issue a write, `PRAGMA` that mutates, or migration. A WAL-mode database being actively written must not be opened with `immutable=1`, which would read stale data.
- Nullable means unknown. `domain.UsageTokenMetrics` fields are `*int64`: a nil pointer is "not measured", a pointer to zero is "measured as zero". Never substitute one for the other.
- `cost = 0.0` from a local model is a **known zero**, not missing data. Local-model opencode sessions legitimately cost nothing.
- Ingestion failures must never fail a session. Record them via `MarkUsageSourceFailure` with a `domain.UsageError*` code and let the existing retry schedule handle it.
- Events from opencode are `domain.UsageMeasurementNativeReported` — opencode reports its own counters.
- AO prices the token vector through its own catalog (`backend/internal/pricing/`) for cross-harness comparability. opencode's `session.cost` is retained for reconciliation only and must not overwrite AO's estimate.
- Run `npm run lint` (which is `go test ./... && golangci-lint run`) before every commit in this phase.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/domain/usage.go` | Modify: add `UsageSourceOpenCodeDB` kind and an `opencode` provider identifier |
| `backend/internal/service/usage/capabilities.go` | Modify: `SupportedHarness` accepts `HarnessOpenCode` |
| `backend/internal/service/usage/collector.go:1834` | Modify: `sourceKindForHarness` maps `HarnessOpenCode` → `UsageSourceOpenCodeDB` |
| `backend/internal/observe/usage/opencode_db.go` | Create: read-only database access — open, schema probe, row queries |
| `backend/internal/observe/usage/opencode_reader.go` | Create: translate opencode rows into `[]domain.ModelUsageEvent` + cursor |
| `backend/internal/observe/usage/opencode_collect.go` | Create: the ingest loop for database-backed sources (the file-chunk `Ingestor` cannot serve them) |
| `backend/internal/observe/usage/testdata/opencode-fixture.db` | Create: committed fixture database |
| `backend/internal/observe/usage/opencode_reader_test.go` | Create: reader unit tests |
| `backend/internal/observe/usage/opencode_collect_test.go` | Create: cursor, resume and failure tests |

**Why a separate collect path.** `Ingestor.Ingest` (`backend/internal/observe/usage/ingestor.go:110`) reads a byte range out of a JSONL file and hands records to `parseRecordsWithState`. opencode has no such file. Rather than fake byte offsets, `opencode_collect.go` reuses the same *store* contract (`GetUsageSourceForIngestion`, `ApplyUsageChunk`, `MarkUsageSourceFailure`) while supplying its own `(session_id, seq)` cursor. The durable tables, the transaction boundary and the crash semantics are therefore identical; only the record source differs.

---

### Task 1: Add the source kind and enable the harness

**Files:**
- Modify: `backend/internal/domain/usage.go:13-19` (kind constants), `:118-124` (provider identifiers)
- Modify: `backend/internal/service/usage/capabilities.go:6-13`
- Modify: `backend/internal/service/usage/collector.go:1834-1845`
- Test: `backend/internal/service/usage/capabilities_test.go`, `backend/internal/service/usage/collector_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `domain.UsageSourceOpenCodeDB domain.UsageSourceKind = "opencode_db"`; `domain.UsageProviderOpenCode domain.UsageProviderID = "opencode"`; `SupportedHarness(domain.HarnessOpenCode) == true`; `sourceKindForHarness(domain.HarnessOpenCode) == (domain.UsageSourceOpenCodeDB, true)`. Tasks 2-4 depend on all four.

- [ ] **Step 1: Write the failing test**

Create or extend `backend/internal/service/usage/capabilities_test.go`:

```go
func TestSupportedHarnessIncludesOpenCode(t *testing.T) {
	if !usage.SupportedHarness(domain.HarnessOpenCode) {
		t.Fatal("SupportedHarness(opencode) = false, want true")
	}
}

func TestSourceKindForOpenCode(t *testing.T) {
	kind, ok := usage.SourceKindForHarnessForTest(domain.HarnessOpenCode)
	if !ok || kind != domain.UsageSourceOpenCodeDB {
		t.Fatalf("sourceKindForHarness(opencode) = (%q, %v), want (opencode_db, true)", kind, ok)
	}
}
```

`sourceKindForHarness` is unexported. Add an export-for-test shim in the package (`export_test.go`) rather than exporting the function itself:

```go
// export_test.go
package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func SourceKindForHarnessForTest(h domain.AgentHarness) (domain.UsageSourceKind, bool) {
	return sourceKindForHarness(h)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/service/usage/ -run 'OpenCode' -v`

Expected: FAIL — `domain.UsageSourceOpenCodeDB` undefined.

- [ ] **Step 3: Add the constants and the mappings**

In `backend/internal/domain/usage.go`, add to the `UsageSourceKind` block:

```go
	UsageSourceOpenCodeDB     UsageSourceKind = "opencode_db"
```

Add to the provider block:

```go
	// UsageProviderOpenCode is opencode's own usage vocabulary. opencode
	// reports a normalized token vector and cost per session regardless of
	// which upstream provider served the model, so its counters are neither
	// OpenAI's nor Anthropic's shape.
	UsageProviderOpenCode UsageProviderID = "opencode"
```

In `capabilities.go`:

```go
	case domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessKimi, domain.HarnessOpenCode:
		return true
```

In `collector.go`:

```go
	case domain.HarnessOpenCode:
		return domain.UsageSourceOpenCodeDB, true
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./internal/service/usage/ -run 'OpenCode' -v`

Expected: PASS.

- [ ] **Step 5: Check nothing else switched exhaustively on the kind**

Run: `cd backend && go build ./... && go test ./internal/observe/usage/ ./internal/service/usage/`

Expected: builds and passes. `parseRecordsWithState` (`observe/usage/parser.go:41`) has a `default` branch that records `UsageErrorUnsupportedSourceFormat`, so an `opencode_db` source reaching the file parser degrades safely until Task 4 routes it away. That is the correct intermediate state.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/domain/usage.go backend/internal/service/usage/
git commit -m "feat(usage): register opencode_db as a certified usage source kind"
```

---

### Task 2: Build the fixture database and read-only access layer

**Files:**
- Create: `backend/internal/observe/usage/testdata/opencode-fixture.db`
- Create: `backend/internal/observe/usage/testdata/make-opencode-fixture.py`
- Create: `backend/internal/observe/usage/opencode_db.go`
- Test: `backend/internal/observe/usage/opencode_db_test.go`

**Interfaces:**
- Consumes: `domain.UsageSourceOpenCodeDB` from Task 1.
- Produces:
  - `type openCodeDB struct{ ... }`
  - `func openOpenCodeDB(path string) (*openCodeDB, error)`
  - `func (d *openCodeDB) Close() error`
  - `func (d *openCodeDB) sessionByDirectory(ctx context.Context, dir string) (openCodeSession, bool, error)`
  - `func (d *openCodeDB) partsAfter(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]openCodePart, int64, error)`
  - `type openCodeSession struct { ID, Directory, Agent, ModelID, ProviderID string; Cost float64; TokensInput, TokensOutput, TokensReasoning, TokensCacheRead, TokensCacheWrite int64; ParentID string; TimeCompacting *int64 }`
  - `type openCodePart struct { Seq int64; Type string; Data []byte }`

Task 3 consumes every one of these.

- [ ] **Step 1: Write the fixture generator**

Create `backend/internal/observe/usage/testdata/make-opencode-fixture.py`. Committing the generator alongside the binary fixture means the fixture is reproducible and reviewable:

```python
#!/usr/bin/env python3
"""Regenerate opencode-fixture.db. Run from this directory: python3 make-opencode-fixture.py"""
import json, os, sqlite3

OUT = os.path.join(os.path.dirname(__file__), "opencode-fixture.db")
if os.path.exists(OUT):
    os.remove(OUT)

db = sqlite3.connect(OUT)
db.executescript("""
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
);
""")

# A paid session with cache usage, on a real cloud model.
db.execute(
    "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    ("ses_paid", "proj_1", None, "paid", "/tmp/wt/paid", "Paid session", "1.0",
     3, 120, 14, 1_773_336_000_000, 1_773_336_900_000, None,
     "ao-paid", json.dumps({"id": "claude-sonnet-4-6", "providerID": "anthropic", "variant": "default"}),
     2.84, 184_000, 23_000, 0, 412_000, 9_000),
)
# A local-model session: cost is a KNOWN zero, not missing.
db.execute(
    "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    ("ses_local", "proj_1", None, "local", "/tmp/wt/local", "Local session", "1.0",
     0, 0, 0, 1_773_337_000_000, 1_773_337_100_000, 1_773_337_090_000,
     "ao-local", json.dumps({"id": "gemma4:e2b", "providerID": "ai-local-models-cpu", "variant": "default"}),
     0.0, 1_130, 778, 0, 19_990, 0),
)
# A child session, to prove parent_id round-trips.
db.execute(
    "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    ("ses_child", "proj_1", "ses_paid", "child", "/tmp/wt/paid", "Child", "1.0",
     0, 0, 0, 1_773_336_500_000, 1_773_336_600_000, None,
     "ao-paid", json.dumps({"id": "claude-haiku-4-5", "providerID": "anthropic", "variant": "default"}),
     0.05, 2_000, 400, 0, 0, 0),
)

parts = [
    # seq 1: a tool call WITH timing.
    ("ses_paid", 1, {"type": "tool", "callID": "call_a", "tool": "bash",
                     "state": {"status": "completed", "title": "npm test",
                               "time": {"start": 1_773_336_859_125, "end": 1_773_336_863_340}}}),
    # seq 2: a tool call WITHOUT timing — must stay unknown, not zero.
    ("ses_paid", 2, {"type": "tool", "callID": "call_b", "tool": "read",
                     "state": {"status": "completed", "title": "read file"}}),
    # seq 3: a failed tool call.
    ("ses_paid", 3, {"type": "tool", "callID": "call_c", "tool": "bash",
                     "state": {"status": "error", "title": "exit 1",
                               "time": {"start": 1_773_336_870_000, "end": 1_773_336_871_000}}}),
    # seq 4: per-step token vector.
    ("ses_paid", 4, {"type": "step-finish", "reason": "tool-calls", "cost": 0,
                     "tokens": {"total": 16958, "input": 16881, "output": 77,
                                "reasoning": 0, "cache": {"read": 0, "write": 0}}}),
    # seq 5: an automatic overflow compaction.
    ("ses_local", 5, {"type": "compaction", "auto": True, "overflow": True}),
]
for index, (session_id, seq, payload) in enumerate(parts):
    message_id = f"msg_{seq}"
    db.execute(
        "INSERT OR IGNORE INTO session_message VALUES (?,?,?,?,?,?,?)",
        (message_id, session_id, "assistant", 1_773_336_000_000 + seq,
         1_773_336_000_000 + seq, "{}", seq),
    )
    db.execute(
        "INSERT INTO part VALUES (?,?,?,?,?,?)",
        (f"prt_{index}", message_id, session_id,
         1_773_336_000_000 + seq, 1_773_336_000_000 + seq, json.dumps(payload)),
    )

db.commit()
db.close()
print("wrote", OUT)
```

- [ ] **Step 2: Generate the fixture**

Run: `cd backend/internal/observe/usage/testdata && python3 make-opencode-fixture.py`

Expected: `wrote .../opencode-fixture.db`

- [ ] **Step 3: Write the failing test**

Create `backend/internal/observe/usage/opencode_db_test.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `cd backend && go test ./internal/observe/usage/ -run OpenCodeDB -v`

Expected: FAIL — `openOpenCodeDB` undefined.

- [ ] **Step 5: Implement the access layer**

Create `backend/internal/observe/usage/opencode_db.go`:

```go
package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// requiredOpenCodeColumns are the session columns this reader cannot work
// without. opencode adds columns over time; a database missing any of these is
// an unsupported generation rather than a corrupt file.
var requiredOpenCodeColumns = []string{
	"directory", "cost", "tokens_input", "tokens_output",
	"tokens_cache_read", "tokens_cache_write", "model",
}

type openCodeSession struct {
	ID               string
	Directory        string
	Agent            string
	ModelID          string
	ProviderID       string
	ParentID         string
	Cost             float64
	TokensInput      int64
	TokensOutput     int64
	TokensReasoning  int64
	TokensCacheRead  int64
	TokensCacheWrite int64
	SummaryFiles     int64
	SummaryAdditions int64
	SummaryDeletions int64
	TimeCompacting   *int64
}

type openCodePart struct {
	Seq  int64
	Type string
	Data []byte
}

type openCodeDB struct{ db *sql.DB }

// openOpenCodeDB opens opencode's database read-only. The file belongs to
// another process that may be writing it, so immutable=1 is deliberately NOT
// used: it would serve stale pages and ignore the WAL.
func openOpenCodeDB(path string) (*openCodeDB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("opencode db unavailable: %w", err)
	}
	dsn := "file:" + url.PathEscape(path) + "?mode=ro&_busy_timeout=2000&_txlock=deferred"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open opencode db: %w", err)
	}
	handle := &openCodeDB{db: db}
	if err := handle.probeSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return handle, nil
}

func (d *openCodeDB) Close() error { return d.db.Close() }

func (d *openCodeDB) probeSchema(ctx context.Context) error {
	rows, err := d.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('session')`)
	if err != nil {
		return fmt.Errorf("probe opencode schema: %w", err)
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("probe opencode schema: %w", err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("probe opencode schema: %w", err)
	}
	for _, column := range requiredOpenCodeColumns {
		if !present[column] {
			return fmt.Errorf("opencode schema missing column %q", column)
		}
	}
	return nil
}

const openCodeSessionQuery = `
SELECT id, directory, COALESCE(agent, ''), COALESCE(model, ''), COALESCE(parent_id, ''),
       cost, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write,
       COALESCE(summary_files, 0), COALESCE(summary_additions, 0), COALESCE(summary_deletions, 0),
       time_compacting
FROM session WHERE directory = ? ORDER BY time_updated DESC LIMIT 1`

func (d *openCodeDB) sessionByDirectory(ctx context.Context, dir string) (openCodeSession, bool, error) {
	var (
		out      openCodeSession
		modelRaw string
	)
	err := d.db.QueryRowContext(ctx, openCodeSessionQuery, dir).Scan(
		&out.ID, &out.Directory, &out.Agent, &modelRaw, &out.ParentID,
		&out.Cost, &out.TokensInput, &out.TokensOutput, &out.TokensReasoning,
		&out.TokensCacheRead, &out.TokensCacheWrite,
		&out.SummaryFiles, &out.SummaryAdditions, &out.SummaryDeletions,
		&out.TimeCompacting,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return openCodeSession{}, false, nil
		}
		return openCodeSession{}, false, fmt.Errorf("read opencode session: %w", err)
	}
	out.ModelID, out.ProviderID = decodeOpenCodeModel(modelRaw)
	return out, true, nil
}

// decodeOpenCodeModel reads opencode's {"id","providerID","variant"} model
// object. A malformed value yields empty identifiers so the event is stored
// unattributed rather than priced against a guess.
func decodeOpenCodeModel(raw string) (modelID, providerID string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	var decoded struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return "", ""
	}
	return decoded.ID, decoded.ProviderID
}

const openCodePartsQuery = `
SELECT m.seq, p.data
FROM part p JOIN session_message m ON m.id = p.message_id
WHERE p.session_id = ? AND m.seq > ?
ORDER BY m.seq ASC LIMIT ?`

// partsAfter returns parts ordered by message sequence, plus the highest
// sequence read. A caller that commits then stores the returned sequence can
// resume exactly where it stopped.
func (d *openCodeDB) partsAfter(
	ctx context.Context, sessionID string, afterSeq int64, limit int,
) ([]openCodePart, int64, error) {
	rows, err := d.db.QueryContext(ctx, openCodePartsQuery, sessionID, afterSeq, limit)
	if err != nil {
		return nil, afterSeq, fmt.Errorf("read opencode parts: %w", err)
	}
	defer rows.Close()
	out := make([]openCodePart, 0, limit)
	next := afterSeq
	for rows.Next() {
		var (
			seq  int64
			data []byte
		)
		if err := rows.Scan(&seq, &data); err != nil {
			return nil, afterSeq, fmt.Errorf("read opencode parts: %w", err)
		}
		var typed struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &typed)
		out = append(out, openCodePart{Data: data, Seq: seq, Type: typed.Type})
		if seq > next {
			next = seq
		}
	}
	if err := rows.Err(); err != nil {
		return nil, afterSeq, fmt.Errorf("read opencode parts: %w", err)
	}
	return out, next, nil
}
```

Confirm the SQLite driver import name matches the rest of the backend before compiling. Run `grep -rn 'sql.Open(' backend/internal/storage/sqlite/db.go | head -3` and use the same driver string and import path (for example `modernc.org/sqlite` registering `"sqlite"`).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/observe/usage/ -run OpenCodeDB -v`

Expected: PASS, all four tests.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/observe/usage/opencode_db.go backend/internal/observe/usage/opencode_db_test.go backend/internal/observe/usage/testdata
git commit -m "feat(usage): add read-only opencode database access layer"
```

---

### Task 3: Translate opencode rows into usage events

**Files:**
- Create: `backend/internal/observe/usage/opencode_reader.go`
- Create: `backend/internal/observe/usage/opencode_reader_test.go`

**Interfaces:**
- Consumes: `openCodeSession`, `openCodePart`, `openCodeDB` from Task 2; `domain.UsageSourceOpenCodeDB` and `domain.UsageProviderOpenCode` from Task 1.
- Produces:
  - `type openCodeParserStateV1 struct { NativeSessionID string \`json:"native_session_id"\`; LastSeq int64 \`json:"last_seq"\`; EmittedSessionTotals bool \`json:"emitted_session_totals"\` }`
  - `func readOpenCode(source domain.UsageSourceContext, session openCodeSession, parts []openCodePart, nextSeq int64, now time.Time, state *openCodeParserStateV1) parseResult`

Task 4 calls `readOpenCode` and persists `parseResult.Cursor`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/observe/usage/opencode_reader_test.go`:

```go
func TestReadOpenCodeEmitsSessionTokenVector(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{
		ID: "ses_paid", ModelID: "claude-sonnet-4-6", ProviderID: "anthropic",
		TokensInput: 184_000, TokensOutput: 23_000, TokensCacheRead: 412_000,
		TokensCacheWrite: 9_000, Cost: 2.84,
	}
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	got := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)

	if got.err != nil {
		t.Fatalf("err = %v, want nil", got.err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(got.Events))
	}
	event := got.Events[0]
	if event.ModelID != "claude-sonnet-4-6" {
		t.Fatalf("ModelID = %q, want claude-sonnet-4-6", event.ModelID)
	}
	if event.MeasurementKind != domain.UsageMeasurementNativeReported {
		t.Fatalf("MeasurementKind = %q, want native_reported", event.MeasurementKind)
	}
	if event.Tokens.CachedInputTokens == nil || *event.Tokens.CachedInputTokens != 412_000 {
		t.Fatalf("CachedInputTokens = %v, want 412000", event.Tokens.CachedInputTokens)
	}
	// Cache WRITES belong to uncached input, per UsageTokenMetrics' contract.
	if event.Tokens.UncachedInputTokens == nil || *event.Tokens.UncachedInputTokens != 193_000 {
		t.Fatalf("UncachedInputTokens = %v, want 193000 (184000 input + 9000 cache write)", event.Tokens.UncachedInputTokens)
	}
}

func TestReadOpenCodeLocalModelZeroCostIsKnown(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{
		ID: "ses_local", ModelID: "gemma4:e2b", ProviderID: "ai-local-models-cpu",
		TokensInput: 1_130, TokensOutput: 778, TokensCacheRead: 19_990, Cost: 0,
	}
	state := &openCodeParserStateV1{NativeSessionID: "ses_local"}
	got := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)

	if len(got.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(got.Events))
	}
	// A known-zero token count is a pointer to zero, never nil.
	if got.Events[0].Tokens.OutputTokens == nil {
		t.Fatal("OutputTokens = nil, want a pointer to 778")
	}
}

func TestReadOpenCodeAdvancesCursorOverParts() {
}

func TestReadOpenCodeIsIdempotentOnRepeatedTotals(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 10, TokensOutput: 2}
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}

	first := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)
	second := readOpenCode(openCodeSourceContextForTest(), session, nil, 0, now, state)

	if len(first.Events) != 1 {
		t.Fatalf("first pass len(Events) = %d, want 1", len(first.Events))
	}
	if len(second.Events) != 0 {
		t.Fatalf("second pass len(Events) = %d, want 0 — unchanged totals must not re-emit", len(second.Events))
	}
}

func TestReadOpenCodeEmitsDeltaWhenTotalsGrow(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	base := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 10, TokensOutput: 2}
	readOpenCode(openCodeSourceContextForTest(), base, nil, 0, now, state)

	grown := base
	grown.TokensInput = 30
	grown.TokensOutput = 5
	got := readOpenCode(openCodeSourceContextForTest(), grown, nil, 0, now, state)

	if len(got.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(got.Events))
	}
	if got.Events[0].Tokens.InputTokens == nil || *got.Events[0].Tokens.InputTokens != 20 {
		t.Fatalf("InputTokens = %v, want the delta 20", got.Events[0].Tokens.InputTokens)
	}
}

func TestReadOpenCodeRejectsNonMonotonicTotals(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	base := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 100, TokensOutput: 10}
	readOpenCode(openCodeSourceContextForTest(), base, nil, 0, now, state)

	shrunk := base
	shrunk.TokensInput = 5
	got := readOpenCode(openCodeSourceContextForTest(), shrunk, nil, 0, now, state)

	if len(got.Events) != 0 {
		t.Fatalf("len(Events) = %d, want 0 for shrinking totals", len(got.Events))
	}
	if got.Cursor.LastErrorCode != domain.UsageErrorNonMonotonicCumulativeUsage {
		t.Fatalf("LastErrorCode = %q, want non_monotonic_cumulative_usage", got.Cursor.LastErrorCode)
	}
}
```

Replace the stub `TestReadOpenCodeAdvancesCursorOverParts` above with this real test:

```go
func TestReadOpenCodeAdvancesCursorOverParts(t *testing.T) {
	now := time.Unix(1_773_337_000, 0).UTC()
	session := openCodeSession{ID: "ses_paid", ModelID: "m", TokensInput: 10, TokensOutput: 2}
	state := &openCodeParserStateV1{NativeSessionID: "ses_paid"}
	parts := []openCodePart{
		{Data: []byte(`{"type":"compaction","auto":true,"overflow":true}`), Seq: 5, Type: "compaction"},
	}

	got := readOpenCode(openCodeSourceContextForTest(), session, parts, 5, now, state)

	if got.err != nil {
		t.Fatalf("err = %v, want nil", got.err)
	}
	if state.LastSeq != 5 {
		t.Fatalf("state.LastSeq = %d, want 5", state.LastSeq)
	}
	var decoded openCodeParserStateV1
	if err := json.Unmarshal([]byte(got.Cursor.ParserStateJSON), &decoded); err != nil {
		t.Fatalf("decode cursor state: %v", err)
	}
	if decoded.LastSeq != 5 {
		t.Fatalf("persisted LastSeq = %d, want 5", decoded.LastSeq)
	}
	if got.Cursor.ByteOffset != 0 {
		t.Fatalf("ByteOffset = %d, want 0 — a database source has no byte offset", got.Cursor.ByteOffset)
	}
}

func openCodeSourceContextForTest() domain.UsageSourceContext {
	return domain.UsageSourceContext{
		BindingState: domain.UsageBindingActive,
		NativeRootID: "ses_paid",
		SessionID:    domain.SessionID("sess-1"),
		Source: domain.UsageSourceRecord{
			ID:   1,
			Kind: domain.UsageSourceOpenCodeDB,
		},
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/observe/usage/ -run ReadOpenCode -v`

Expected: FAIL — `readOpenCode` and `openCodeParserStateV1` undefined.

- [ ] **Step 3: Implement the reader**

Create `backend/internal/observe/usage/opencode_reader.go`:

```go
package usage

import (
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// openCodeParserStateV1 is the durable cursor for a database-backed source.
// opencode reports CUMULATIVE per-session totals, so the previously emitted
// totals must be remembered to emit deltas rather than re-counting.
type openCodeParserStateV1 struct {
	NativeSessionID  string `json:"native_session_id"`
	LastSeq          int64  `json:"last_seq"`
	EmittedInput     int64  `json:"emitted_input"`
	EmittedOutput    int64  `json:"emitted_output"`
	EmittedCacheRead int64  `json:"emitted_cache_read"`
	EmittedCacheWrite int64 `json:"emitted_cache_write"`
	EmittedReasoning int64  `json:"emitted_reasoning"`
	HasEmitted       bool   `json:"has_emitted"`
	Compactions      int64  `json:"compactions"`
}

// readOpenCode turns one poll of opencode's database into usage events and a
// durable cursor. Parts are scanned for compaction markers; token accounting
// comes from the session row, which opencode keeps authoritative.
func readOpenCode(
	source domain.UsageSourceContext,
	session openCodeSession,
	parts []openCodePart,
	nextSeq int64,
	now time.Time,
	state *openCodeParserStateV1,
) parseResult {
	result := parseResult{Cursor: cursorFromSource(source.Source, 0, now)}

	for _, part := range parts {
		if part.Type == "compaction" {
			state.Compactions++
		}
		if part.Seq > state.LastSeq {
			state.LastSeq = part.Seq
		}
	}
	if nextSeq > state.LastSeq {
		state.LastSeq = nextSeq
	}

	if event, ok := openCodeDeltaEvent(source, session, now, state, &result); ok {
		result.Events = append(result.Events, event)
	}

	encoded, err := json.Marshal(state)
	if err != nil {
		result.err = err
		return result
	}
	result.Cursor.ParserStateJSON = string(encoded)
	return result
}

func openCodeDeltaEvent(
	source domain.UsageSourceContext,
	session openCodeSession,
	now time.Time,
	state *openCodeParserStateV1,
	result *parseResult,
) (domain.ModelUsageEvent, bool) {
	// opencode's counters only ever grow within a session. A decrease means the
	// row was replaced (a reused id, a reset) and the delta cannot be trusted.
	if state.HasEmitted && (session.TokensInput < state.EmittedInput ||
		session.TokensOutput < state.EmittedOutput ||
		session.TokensCacheRead < state.EmittedCacheRead ||
		session.TokensCacheWrite < state.EmittedCacheWrite ||
		session.TokensReasoning < state.EmittedReasoning) {
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
		return domain.ModelUsageEvent{}, false
	}

	deltaInput := session.TokensInput - state.EmittedInput
	deltaOutput := session.TokensOutput - state.EmittedOutput
	deltaCacheRead := session.TokensCacheRead - state.EmittedCacheRead
	deltaCacheWrite := session.TokensCacheWrite - state.EmittedCacheWrite
	deltaReasoning := session.TokensReasoning - state.EmittedReasoning
	if state.HasEmitted &&
		deltaInput == 0 && deltaOutput == 0 && deltaCacheRead == 0 &&
		deltaCacheWrite == 0 && deltaReasoning == 0 {
		return domain.ModelUsageEvent{}, false
	}

	// Cache writes are part of uncached input, per UsageTokenMetrics' contract:
	// their provider-specific split stays in the bounded provider usage object.
	uncached := deltaInput + deltaCacheWrite
	totalInput := uncached + deltaCacheRead
	output := deltaOutput + deltaReasoning

	state.EmittedInput = session.TokensInput
	state.EmittedOutput = session.TokensOutput
	state.EmittedCacheRead = session.TokensCacheRead
	state.EmittedCacheWrite = session.TokensCacheWrite
	state.EmittedReasoning = session.TokensReasoning
	state.HasEmitted = true

	providerUsage, err := json.Marshal(map[string]any{
		"cost":               session.Cost,
		"tokens_cache_read":  session.TokensCacheRead,
		"tokens_cache_write": session.TokensCacheWrite,
		"tokens_input":       session.TokensInput,
		"tokens_output":      session.TokensOutput,
		"tokens_reasoning":   session.TokensReasoning,
	})
	if err != nil {
		providerUsage = nil
	}

	return domain.ModelUsageEvent{
		BillingProviderID:     session.ProviderID,
		BillingProviderSource: domain.ObservedBillingProviderSource(session.ProviderID),
		MeasurementKind:       domain.UsageMeasurementNativeReported,
		ModelID:               session.ModelID,
		ObservedAt:            now,
		ProviderID:            domain.UsageProviderOpenCode,
		ProviderUsageJSON:     string(providerUsage),
		SessionID:             source.SessionID,
		SourceID:              source.Source.ID,
		Tokens: domain.UsageTokenMetrics{
			CachedInputTokens:   ptrInt64(deltaCacheRead),
			InputTokens:         ptrInt64(totalInput),
			OutputTokens:        ptrInt64(output),
			UncachedInputTokens: ptrInt64(uncached),
		},
	}, true
}

func ptrInt64(v int64) *int64 { return &v }
```

Check `domain.ModelUsageEvent`'s actual field set with `grep -n 'type ModelUsageEvent' -A 30 backend/internal/domain/usage.go` and align the literal exactly; drop fields that do not exist and fill any required ones this snippet omits. If `ptrInt64` already exists in the package, reuse it rather than redeclaring.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/observe/usage/ -run ReadOpenCode -v`

Expected: PASS, all six tests.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/observe/usage/opencode_reader.go backend/internal/observe/usage/opencode_reader_test.go
git commit -m "feat(usage): translate opencode session rows into usage events"
```

---

### Task 4: Collect and persist, with crash-safe resume

**Files:**
- Create: `backend/internal/observe/usage/opencode_collect.go`
- Create: `backend/internal/observe/usage/opencode_collect_test.go`

**Interfaces:**
- Consumes: `openOpenCodeDB`, `sessionByDirectory`, `partsAfter` (Task 2); `readOpenCode`, `openCodeParserStateV1` (Task 3).
- Produces: `func (c *OpenCodeCollector) Collect(ctx context.Context, sourceID int64) error`, and `func NewOpenCodeCollector(store openCodeCollectorStore, dbPath string, workspacePath func(domain.SessionID) (string, bool)) *OpenCodeCollector`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/observe/usage/opencode_collect_test.go`:

```go
type fakeOpenCodeStore struct {
	applied    [][]domain.ModelUsageEvent
	cursors    []domain.SourceCursorState
	failures   []string
	sourceCtx  domain.UsageSourceContext
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/observe/usage/ -run OpenCodeCollector -v`

Expected: FAIL — `NewOpenCodeCollector` undefined.

- [ ] **Step 3: Implement the collector**

Create `backend/internal/observe/usage/opencode_collect.go`:

```go
package usage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const openCodePartBatch = 500

// openCodeCollectorStore is the subset of the usage store a database-backed
// source needs. It is deliberately the same contract the file-chunk Ingestor
// uses, so transaction boundaries and crash semantics are identical.
type openCodeCollectorStore interface {
	GetUsageSourceForIngestion(context.Context, int64) (domain.UsageSourceContext, bool, error)
	ApplyUsageChunk(context.Context, int64, int64, time.Time, domain.SourceCursorState, []domain.ModelUsageEvent) error
	MarkUsageSourceFailure(context.Context, int64, int64, string, time.Time, time.Time) (bool, error)
}

// OpenCodeCollector ingests one opencode-backed usage source per call.
type OpenCodeCollector struct {
	store         openCodeCollectorStore
	dbPath        string
	workspacePath func(domain.SessionID) (string, bool)
	now           func() time.Time
}

// NewOpenCodeCollector builds a collector reading the opencode database at
// dbPath. workspacePath resolves an AO session to its worktree path, which is
// how an opencode session is identified (opencode stores it in
// session.directory).
func NewOpenCodeCollector(
	store openCodeCollectorStore,
	dbPath string,
	workspacePath func(domain.SessionID) (string, bool),
) *OpenCodeCollector {
	return &OpenCodeCollector{
		dbPath:        dbPath,
		now:           func() time.Time { return time.Now().UTC() },
		store:         store,
		workspacePath: workspacePath,
	}
}

// Collect polls opencode for one source and commits events plus cursor in a
// single transaction. Every failure is recorded against the source and
// returns nil: a telemetry problem must never fail a session.
func (c *OpenCodeCollector) Collect(ctx context.Context, sourceID int64) error {
	now := c.now()
	source, ok, err := c.store.GetUsageSourceForIngestion(ctx, sourceID)
	if err != nil || !ok {
		return err
	}

	dir, ok := c.workspacePath(source.SessionID)
	if !ok || dir == "" {
		return c.fail(ctx, source, domain.UsageErrorSourceDiscoveryPending, now)
	}

	db, err := openOpenCodeDB(c.dbPath)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorArtifactMissing, now)
	}
	defer db.Close()

	session, found, err := db.sessionByDirectory(ctx, dir)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorSourceReadFailed, now)
	}
	if !found {
		return c.fail(ctx, source, domain.UsageErrorSourceDiscoveryPending, now)
	}

	state, err := decodeOpenCodeState(source.Source)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorInvalidParserState, now)
	}

	parts, nextSeq, err := db.partsAfter(ctx, session.ID, state.LastSeq, openCodePartBatch)
	if err != nil {
		return c.fail(ctx, source, domain.UsageErrorSourceReadFailed, now)
	}

	parsed := readOpenCode(source, session, parts, nextSeq, now, state)
	if parsed.err != nil {
		return c.fail(ctx, source, domain.UsageErrorInvalidParserState, now)
	}

	// ByteOffset is meaningless for a database source and stays zero; the real
	// cursor rides in ParserStateJSON. Passing the source's own offset keeps the
	// store's optimistic-concurrency check intact.
	return c.store.ApplyUsageChunk(
		ctx,
		source.Source.ID,
		source.Source.ByteOffset,
		source.Source.UpdatedAt,
		parsed.Cursor,
		parsed.Events,
	)
}

func (c *OpenCodeCollector) fail(
	ctx context.Context, source domain.UsageSourceContext, code string, now time.Time,
) error {
	_, err := c.store.MarkUsageSourceFailure(
		ctx, source.Source.ID, source.Source.FailureCount+1, code, now.Add(time.Minute), now,
	)
	return err
}

func decodeOpenCodeState(source domain.UsageSourceRecord) (*openCodeParserStateV1, error) {
	state := &openCodeParserStateV1{}
	if source.ParserStateJSON == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(source.ParserStateJSON), state); err != nil {
		return nil, err
	}
	return state, nil
}
```

Verify `domain.UsageSourceRecord` really exposes `ParserStateJSON`, `ByteOffset`, `FailureCount` and `UpdatedAt` with `grep -n 'type UsageSourceRecord' -A 25 backend/internal/domain/usage.go`, and adjust field names to match.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/observe/usage/ -run OpenCodeCollector -v`

Expected: PASS, all three tests.

- [ ] **Step 5: Run the whole usage package**

Run: `cd backend && go test ./internal/observe/usage/ ./internal/service/usage/`

Expected: PASS. Nothing existing should regress — the new code is additive.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/observe/usage/opencode_collect.go backend/internal/observe/usage/opencode_collect_test.go
git commit -m "feat(usage): collect opencode usage with crash-safe cursor resume"
```

---

### Task 5: Wire the collector into the pipeline and verify end to end

**Files:**
- Modify: `backend/internal/observe/usage/coordinator.go` — route `UsageSourceOpenCodeDB` sources to `OpenCodeCollector` instead of the file ingestor
- Modify: `backend/internal/service/usage/collector.go` — set the opencode source's `ArtifactPath` to the opencode database path during binding
- Modify: `backend/internal/service/usage/collector.go:70` `DefaultSourceRoots` — include the opencode data directory
- Test: `backend/internal/observe/usage/coordinator_test.go`

**Interfaces:**
- Consumes: `OpenCodeCollector` from Task 4; `sourceKindForHarness` mapping from Task 1.
- Produces: no new exported API. Behaviour: an opencode session gains a usage binding and its metrics appear via the existing `GET /api/v1/usage/sessions/{sessionId}`.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/observe/usage/coordinator_test.go`:

```go
func TestCoordinatorRoutesOpenCodeSourcesToDatabaseCollector(t *testing.T) {
	fileIngests := 0
	dbIngests := 0
	coordinator := newCoordinatorForTest(t, coordinatorHooksForTest{
		ingestFile: func(int64) { fileIngests++ },
		ingestDB:   func(int64) { dbIngests++ },
	})

	coordinator.ingestSourceForTest(context.Background(), domain.UsageSourceRecord{
		ID: 7, Kind: domain.UsageSourceOpenCodeDB,
	})

	if dbIngests != 1 || fileIngests != 0 {
		t.Fatalf("ingests = (db %d, file %d), want (1, 0)", dbIngests, fileIngests)
	}
}
```

Read `coordinator.go` first and shape this test to its actual seams — the existing `coordinator_test.go` already constructs coordinators, so reuse its helpers and naming rather than inventing `newCoordinatorForTest` if an equivalent exists.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/observe/usage/ -run OpenCodeSources -v`

Expected: FAIL — opencode sources currently go to the file ingestor.

- [ ] **Step 3: Route by source kind in the coordinator**

In `coordinator.go`, where a source is dispatched for ingestion, branch on kind:

```go
	if source.Kind == domain.UsageSourceOpenCodeDB {
		return c.openCode.Collect(ctx, source.ID)
	}
	return c.ingestor.Ingest(ctx, source.ID)
```

Add an `openCode *OpenCodeCollector` field to the coordinator and to `CoordinatorConfig`, threading it from `NewPipeline`. A nil collector must skip opencode sources rather than panic:

```go
	if source.Kind == domain.UsageSourceOpenCodeDB {
		if c.openCode == nil {
			return nil
		}
		return c.openCode.Collect(ctx, source.ID)
	}
```

- [ ] **Step 4: Point the binding at the opencode database**

In `service/usage/collector.go`, in the `switch session.Harness` blocks around lines 632-655 and 376-400, add an `opencode` case whose `ArtifactPath` is the opencode database. Resolve it from `SourceRoots` rather than hardcoding a home path, following how the Codex and Kimi roots are resolved in `DefaultSourceRoots` (`:70`). The default location is `$XDG_DATA_HOME/opencode/opencode.db`, falling back to `~/.local/share/opencode/opencode.db`.

The path must pass the existing `validateSourcePath` check, so add the opencode root to the permitted roots — otherwise ingestion records `UsageErrorArtifactPathRejected`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/observe/usage/ ./internal/service/usage/ -v -run 'OpenCode|Root'`

Expected: PASS.

- [ ] **Step 6: Run the full backend suite and linter**

Run: `npm run lint`

Expected: `go test ./...` passes and golangci-lint reports no findings.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/observe/usage/coordinator.go backend/internal/observe/usage/coordinator_test.go backend/internal/service/usage/collector.go
git commit -m "feat(usage): route opencode sources through the database collector"
```

- [ ] **Step 8: Verify against real opencode data**

Start AO, create a worker session on the opencode harness in a project, and let it do some work.

Then confirm AO bound the session and recorded events:

```bash
python3 - <<'PY'
import os, sqlite3
path = os.path.expanduser("~/.local/share/opencode/opencode.db")
db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
for row in db.execute(
    "SELECT id, directory, agent, cost, tokens_input, tokens_output "
    "FROM session ORDER BY time_updated DESC LIMIT 5"
):
    print(row)
PY
```

Expected: a row whose `directory` is the AO worktree path for the session you just ran. Then open that session's Metrics tab (built in Phase 1).

Expected: tokens and cost render. For a local model, cost shows `$0.00`; for a cloud model, a real figure.

- [ ] **Step 9: Commit any fixes**

```bash
git add -A
git commit -m "fix(usage): address opencode ingestion verification findings"
```

---

## Phase Completion

```bash
npm run lint
cd backend && go test ./internal/observe/usage/ ./internal/service/usage/ -count=1
```

Both must pass. Phase 3 (tool-call telemetry) builds directly on `opencode_db.go`'s `partsAfter` and the fixture from Task 2.

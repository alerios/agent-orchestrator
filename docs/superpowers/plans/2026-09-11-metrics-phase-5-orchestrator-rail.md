# Metrics Phase 5: Orchestrator Metrics Rail Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give orchestrator sessions a Metrics-only inspector rail showing a project-scoped roll-up — worker spend, orchestrator-to-worker ratio, cost per merged PR, and a worker list sorted by cost — so the most expensive session in a project stops being the least observable.

**Architecture:** A project-scoped aggregation endpoint over the usage events AO already stores, joined to session kind and delivery outcome. The frontend stops suppressing the rail for orchestrators and instead renders a single Metrics tab with the roll-up. Attribution is by `project_id`, which is sound because AO enforces one active orchestrator per project; the orchestrator *generation* caveat is surfaced in the UI rather than hidden.

**Tech Stack:** Go, SQLite + sqlc, TypeScript/React, Vitest, openapi-typescript.

**Spec:** `docs/superpowers/specs/2026-09-11-session-metrics-and-efficiency-scoring-design.md`

**Depends on:** Phase 1 (the Metrics tab and `metricsView` slot) and Phase 3 (the effort endpoint). Phase 4 is not required but its scores render here too if present.

## Global Constraints

- **Attribution is by `project_id`, not by a parent link.** AO enforces one active orchestrator per project — `EnsureOrchestrator` returns the incumbent (`backend/internal/service/session/service.go:478`), `activeOrchestratorSessionID` takes the first non-terminated one (`backend/internal/session_manager/manager.go:4102`), and replacement retires the old one. Do **not** add a `parent_session_id` column; the spec rules it out as unnecessary.
- **Generations must be visible.** Restarting an orchestrator retires the old session and creates a new one, so project-lifetime totals span several orchestrator generations. The UI shows "this orchestrator" and "project all-time across N generations" as two clearly separated figures. Never present a project total as if it belonged to the current orchestrator.
- Unmeasured sessions count as **unavailable**, never as zero, in every aggregate. A cost total over a project where some sessions had no usage source is a lower bound and must say so.
- Cost per merged PR divides by merged PRs only. Zero merged PRs yields unavailable, not infinity and not zero.
- Orchestrators get **only** the Metrics tab. PRs, Reviews, Browser and Files do not apply.
- The rail must be **collapsed by default** for orchestrators, preserving the existing full-workspace intent recorded at `SessionView.tsx:1080`.
- **Read `DESIGN.md` before any visual decision**, starting with its "clone agent-orchestrator verbatim" banner — that banner governs the current look and supersedes the older design-reference framing. Build new UI from the existing `@aoagents/product-ui` primitives and `components/ui/*` shadcn components where one fits; do not introduce new visual patterns without explicit approval.
- When demoing a frontend change, run `ao preview [url]` from inside the session so it renders in the inspector rail's Browser tab, and say "check the Browser tab" in your reply — the panel badges as unseen rather than stealing focus.
- No hardcoded English in `frontend/src/renderer`; all copy via i18n keys in all eight locale files.
- Run `npm run lint` before every backend commit; `npm run api` after changing response types.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/storage/sqlite/queries/usage.sql` | Modify: project roll-up queries |
| `backend/internal/domain/usage.go` | Modify: add `ProjectUsageRollup` and `WorkerUsageRow` read models |
| `backend/internal/storage/sqlite/store/usage_store.go` | Modify: the roll-up read methods |
| `backend/internal/service/usage/rollup.go` | Create: ratio, cost-per-merged-PR and generation assembly |
| `backend/internal/httpd/controllers/usage.go` | Modify: `GET /usage/projects/{projectId}/rollup` |
| `frontend/src/renderer/hooks/useProjectUsageRollup.ts` | Create: query hook |
| `frontend/src/renderer/components/OrchestratorMetrics.tsx` | Create: the roll-up panel |
| `frontend/src/renderer/components/SessionInspector.tsx` | Modify: orchestrator tab set |
| `frontend/src/renderer/components/SessionView.tsx:1080` | Modify: give orchestrators a rail |
| `frontend/src/renderer/i18n/*.json` | Modify: new keys, all 8 locales |

---

### Task 1: Project roll-up read models and queries

**Files:**
- Modify: `backend/internal/domain/usage.go`
- Modify: `backend/internal/storage/sqlite/queries/usage.sql`
- Create: `backend/internal/storage/sqlite/store/usage_rollup_test.go`

**Interfaces:**
- Consumes: the existing `usage_events` table and `domain.UsageMetricTotals`.
- Produces:
  - `domain.WorkerUsageRow{ SessionID domain.SessionID; Title string; Harness domain.AgentHarness; ModelID string; DurationMS *int64; EstimatedCost *EstimatedCost; Outcome WorkerOutcome; Measured bool }`
  - `domain.WorkerOutcome` with `WorkerOutcomeActive`, `WorkerOutcomeMerged`, `WorkerOutcomeAbandoned`, `WorkerOutcomeFailed`
  - `domain.ProjectUsageRollup{ ProjectID; OrchestratorTotals, WorkerTotals UsageMetricTotals; Workers []WorkerUsageRow; MergedPRs, OrchestratorGenerations int64; UnmeasuredSessions int64 }`
  - `func (s *Store) ProjectUsageRollup(ctx context.Context, id domain.ProjectID) (domain.ProjectUsageRollup, error)`

Tasks 2-4 consume all of these.

- [ ] **Step 1: Add the read models**

In `backend/internal/domain/usage.go`, after `SessionUsageSummary`:

```go
// WorkerOutcome is how a worker session ended, as far as AO's delivery facts
// show. It is the denominator vocabulary for orchestrator efficiency.
type WorkerOutcome string

// Worker outcomes.
const (
	WorkerOutcomeActive    WorkerOutcome = "active"
	WorkerOutcomeMerged    WorkerOutcome = "merged"
	WorkerOutcomeAbandoned WorkerOutcome = "abandoned"
	WorkerOutcomeFailed    WorkerOutcome = "failed"
)

// WorkerUsageRow is one worker's line in the orchestrator roll-up. Measured
// is false when AO had no certified usage source for the session: its cost is
// unknown, not zero, and it must not be summed into a total silently.
type WorkerUsageRow struct {
	DurationMS    *int64
	EstimatedCost *EstimatedCost
	Harness       AgentHarness
	Measured      bool
	ModelID       string
	Outcome       WorkerOutcome
	SessionID     SessionID
	Title         string
}

// ProjectUsageRollup is the orchestrator-rail read model.
//
// Attribution is by project, which is exact because AO runs one active
// orchestrator per project. OrchestratorGenerations counts how many
// orchestrator sessions the project has had, so a lifetime total is never
// mistaken for the current orchestrator's own spend. UnmeasuredSessions is the
// count whose usage AO could not observe, which makes every total a stated
// lower bound rather than a false precision.
type ProjectUsageRollup struct {
	MergedPRs               int64
	OrchestratorGenerations int64
	OrchestratorTotals      UsageMetricTotals
	ProjectID               ProjectID
	UnmeasuredSessions      int64
	WorkerTotals            UsageMetricTotals
	Workers                 []WorkerUsageRow
}
```

- [ ] **Step 2: Write the failing store test**

Create `backend/internal/storage/sqlite/store/usage_rollup_test.go`:

```go
func TestProjectUsageRollupSplitsOrchestratorFromWorkers(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	seedRollupFixture(t, store)

	got, err := store.ProjectUsageRollup(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ProjectUsageRollup: %v", err)
	}
	if got.OrchestratorTotals.ProcessedTokens == nil {
		t.Fatal("OrchestratorTotals.ProcessedTokens = nil, want the orchestrator's own tokens")
	}
	if got.WorkerTotals.ProcessedTokens == nil {
		t.Fatal("WorkerTotals.ProcessedTokens = nil, want the workers' tokens")
	}
	if *got.OrchestratorTotals.ProcessedTokens == *got.WorkerTotals.ProcessedTokens {
		t.Fatal("orchestrator and worker totals are identical — the split is not happening")
	}
}

func TestProjectUsageRollupCountsOrchestratorGenerations(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	seedRollupFixture(t, store) // seeds 2 orchestrators: one retired, one active

	got, err := store.ProjectUsageRollup(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ProjectUsageRollup: %v", err)
	}
	if got.OrchestratorGenerations != 2 {
		t.Fatalf("OrchestratorGenerations = %d, want 2", got.OrchestratorGenerations)
	}
}

func TestProjectUsageRollupCountsUnmeasuredSessionsSeparately(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	seedRollupFixture(t, store) // includes one droid worker with no usage source

	got, err := store.ProjectUsageRollup(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ProjectUsageRollup: %v", err)
	}
	if got.UnmeasuredSessions < 1 {
		t.Fatalf("UnmeasuredSessions = %d, want at least 1", got.UnmeasuredSessions)
	}
	for _, worker := range got.Workers {
		if !worker.Measured && worker.EstimatedCost != nil {
			t.Fatalf("worker %s is unmeasured but carries a cost — unknown must not become a number", worker.SessionID)
		}
	}
}

func TestProjectUsageRollupSortsWorkersByCostDescending(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	seedRollupFixture(t, store)

	got, err := store.ProjectUsageRollup(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ProjectUsageRollup: %v", err)
	}
	var last int64 = 1 << 62
	for _, worker := range got.Workers {
		if worker.EstimatedCost == nil {
			continue // unmeasured rows sort last
		}
		if worker.EstimatedCost.Nanos > last {
			t.Fatalf("workers are not cost-descending: %d after %d", worker.EstimatedCost.Nanos, last)
		}
		last = worker.EstimatedCost.Nanos
	}
}

func TestProjectUsageRollupEmptyProject(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	got, err := store.ProjectUsageRollup(ctx, "proj-empty")
	if err != nil {
		t.Fatalf("ProjectUsageRollup on an empty project: %v", err)
	}
	if len(got.Workers) != 0 || got.MergedPRs != 0 {
		t.Fatalf("got = %+v, want an empty rollup, not an error", got)
	}
	if got.WorkerTotals.EstimatedCost != nil {
		t.Fatal("WorkerTotals.EstimatedCost is set for an empty project, want nil rather than $0")
	}
}
```

Write `seedRollupFixture` to insert, via the store's existing session and usage-event helpers: one retired orchestrator, one active orchestrator with usage, two measured workers with different costs (one with a merged PR, one abandoned), and one `droid` worker with no usage events. Inspect a sibling `*_store_test.go` for the established seeding helpers before writing raw SQL.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run ProjectUsageRollup -v`

Expected: FAIL — `ProjectUsageRollup` undefined.

- [ ] **Step 4: Add the queries**

Append to `backend/internal/storage/sqlite/queries/usage.sql`. Check the real column names in the migrations for `usage_events` and `sessions` first (`grep -n 'CREATE TABLE usage_events' -A 25 backend/internal/storage/sqlite/migrations/*.sql`) and align:

```sql
-- name: ProjectUsageTotalsByKind :many
SELECT s.kind AS kind,
       SUM(e.input_tokens) AS input_tokens,
       SUM(e.cached_input_tokens) AS cached_input_tokens,
       SUM(e.output_tokens) AS output_tokens,
       SUM(e.estimated_cost_nanos) AS estimated_cost_nanos,
       COUNT(DISTINCT e.session_id) AS measured_sessions
FROM usage_events e
JOIN sessions s ON s.id = e.session_id
WHERE s.project_id = ?
GROUP BY s.kind;

-- name: ProjectWorkerUsageRows :many
SELECT s.id AS session_id,
       s.title AS title,
       s.harness AS harness,
       SUM(e.estimated_cost_nanos) AS estimated_cost_nanos,
       COUNT(e.id) AS event_count
FROM sessions s
LEFT JOIN usage_events e ON e.session_id = s.id
WHERE s.project_id = ? AND s.kind = 'worker'
GROUP BY s.id
ORDER BY estimated_cost_nanos DESC NULLS LAST, s.id ASC;

-- name: ProjectOrchestratorGenerationCount :one
SELECT COUNT(*) FROM sessions WHERE project_id = ? AND kind = 'orchestrator';
```

`event_count = 0` is how an unmeasured worker is identified — do not rely on a null cost alone, since a measured session may legitimately have a zero cost (a local model).

Merged-PR counting should reuse AO's existing PR read path rather than a new join, so the definition of "merged" stays in one place. Find it with `grep -rn 'Merged' backend/internal/storage/sqlite/queries/*.sql | head`.

- [ ] **Step 5: Generate and implement**

Run: `npm run sqlc`

Then add `ProjectUsageRollup` to `backend/internal/storage/sqlite/store/usage_store.go`, composing the three queries plus the merged-PR count. Set `Measured: eventCount > 0` per worker and leave `EstimatedCost` nil whenever `Measured` is false. Count `UnmeasuredSessions` from the same signal. Derive the totals' `EstimatedCost` with the existing helper used by `SessionUsageSummary` so coverage semantics stay consistent — do not construct `EstimatedCost` by hand.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run ProjectUsageRollup -v`

Expected: PASS, all five tests.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/domain/usage.go backend/internal/storage/sqlite
git commit -m "feat(usage): add project-scoped usage rollup read model"
```

---

### Task 2: Derive the orchestrator efficiency figures

**Files:**
- Create: `backend/internal/service/usage/rollup.go`
- Create: `backend/internal/service/usage/rollup_test.go`

**Interfaces:**
- Consumes: `domain.ProjectUsageRollup` (Task 1).
- Produces:
  - `type OrchestratorEfficiency struct { CostPerMergedPRNanos *int64; IsLowerBound bool; OrchestratorShare *float64; WorkerShare *float64 }`
  - `func DeriveOrchestratorEfficiency(rollup domain.ProjectUsageRollup) OrchestratorEfficiency`

Tasks 3-4 consume both.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/service/usage/rollup_test.go`:

```go
func costOf(nanos int64) *domain.EstimatedCost {
	return &domain.EstimatedCost{Nanos: nanos}
}

func TestDeriveOrchestratorEfficiencySplitsShare(t *testing.T) {
	got := usage.DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          2,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(3_000_000_000)},
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(9_000_000_000)},
	})

	if got.OrchestratorShare == nil || *got.OrchestratorShare != 0.25 {
		t.Fatalf("OrchestratorShare = %v, want 0.25", got.OrchestratorShare)
	}
	if got.WorkerShare == nil || *got.WorkerShare != 0.75 {
		t.Fatalf("WorkerShare = %v, want 0.75", got.WorkerShare)
	}
	if got.CostPerMergedPRNanos == nil || *got.CostPerMergedPRNanos != 6_000_000_000 {
		t.Fatalf("CostPerMergedPRNanos = %v, want 6000000000 (12bn / 2)", got.CostPerMergedPRNanos)
	}
}

func TestDeriveOrchestratorEfficiencyNoMergedPRs(t *testing.T) {
	got := usage.DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          0,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(3_000_000_000)},
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(9_000_000_000)},
	})

	if got.CostPerMergedPRNanos != nil {
		t.Fatalf("CostPerMergedPRNanos = %v, want nil — dividing by zero merged PRs is not zero cost", got.CostPerMergedPRNanos)
	}
}

func TestDeriveOrchestratorEfficiencyUnknownCostsStayUnknown(t *testing.T) {
	got := usage.DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{MergedPRs: 3})

	if got.OrchestratorShare != nil || got.WorkerShare != nil {
		t.Fatalf("shares = (%v, %v), want both nil with no cost data", got.OrchestratorShare, got.WorkerShare)
	}
	if got.CostPerMergedPRNanos != nil {
		t.Fatal("CostPerMergedPRNanos is set with no cost data, want nil")
	}
}

func TestDeriveOrchestratorEfficiencyFlagsLowerBound(t *testing.T) {
	got := usage.DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          1,
		UnmeasuredSessions: 2,
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(1_000_000_000)},
	})

	if !got.IsLowerBound {
		t.Fatal("IsLowerBound = false with 2 unmeasured sessions, want true")
	}
}

func TestDeriveOrchestratorEfficiencyZeroTotalIsNotAShare(t *testing.T) {
	got := usage.DeriveOrchestratorEfficiency(domain.ProjectUsageRollup{
		MergedPRs:          1,
		OrchestratorTotals: domain.UsageMetricTotals{EstimatedCost: costOf(0)},
		WorkerTotals:       domain.UsageMetricTotals{EstimatedCost: costOf(0)},
	})

	if got.OrchestratorShare != nil {
		t.Fatalf("OrchestratorShare = %v, want nil — a 0/0 split has no meaning", got.OrchestratorShare)
	}
	// Cost per merged PR over a genuinely free project IS zero, and that is a
	// real answer for local models.
	if got.CostPerMergedPRNanos == nil || *got.CostPerMergedPRNanos != 0 {
		t.Fatalf("CostPerMergedPRNanos = %v, want 0 for a measured, genuinely free project", got.CostPerMergedPRNanos)
	}
}
```

Check `domain.EstimatedCost`'s real field names (`grep -n 'type EstimatedCost' -A 12 backend/internal/domain/usage.go`) and adjust `costOf`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/service/usage/ -run OrchestratorEfficiency -v`

Expected: FAIL — `DeriveOrchestratorEfficiency` undefined.

- [ ] **Step 3: Implement the derivation**

Create `backend/internal/service/usage/rollup.go`:

```go
package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// OrchestratorEfficiency is the derived answer to "is this orchestration
// shape paying off". Every field is nil when the evidence does not support it:
// a missing ratio is more useful than a fabricated one.
type OrchestratorEfficiency struct {
	CostPerMergedPRNanos *int64
	IsLowerBound         bool
	OrchestratorShare    *float64
	WorkerShare          *float64
}

// DeriveOrchestratorEfficiency computes the spend split and cost per merged
// PR for one project.
//
// IsLowerBound is true whenever any session in the project had no certified
// usage source, because the totals then omit real spend. A zero total is
// treated as "no basis for a share" rather than a 0/100 split, but a zero cost
// per merged PR is a real answer: local-model projects genuinely cost nothing.
func DeriveOrchestratorEfficiency(rollup domain.ProjectUsageRollup) OrchestratorEfficiency {
	out := OrchestratorEfficiency{IsLowerBound: rollup.UnmeasuredSessions > 0}

	orchestrator, hasOrchestrator := costNanos(rollup.OrchestratorTotals.EstimatedCost)
	workers, hasWorkers := costNanos(rollup.WorkerTotals.EstimatedCost)
	if !hasOrchestrator && !hasWorkers {
		return out
	}

	total := orchestrator + workers
	if total > 0 {
		orchestratorShare := float64(orchestrator) / float64(total)
		workerShare := float64(workers) / float64(total)
		out.OrchestratorShare = &orchestratorShare
		out.WorkerShare = &workerShare
	}

	if rollup.MergedPRs > 0 {
		perPR := total / rollup.MergedPRs
		out.CostPerMergedPRNanos = &perPR
	}
	return out
}

func costNanos(cost *domain.EstimatedCost) (int64, bool) {
	if cost == nil {
		return 0, false
	}
	return cost.Nanos, true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/service/usage/ -run OrchestratorEfficiency -v`

Expected: PASS, all five tests.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/usage/rollup.go backend/internal/service/usage/rollup_test.go
git commit -m "feat(usage): derive orchestrator spend share and cost per merged PR"
```

---

### Task 3: Expose the roll-up endpoint

**Files:**
- Modify: `backend/internal/httpd/controllers/usage.go`
- Modify: `backend/internal/httpd/api.go`
- Test: `backend/internal/httpd/controllers/usage_test.go`

**Interfaces:**
- Consumes: `ProjectUsageRollup` (Task 1), `DeriveOrchestratorEfficiency` (Task 2).
- Produces: `GET /api/v1/usage/projects/{projectId}/rollup` returning
  `{ orchestratorTotals, workerTotals, efficiency: { orchestratorShare, workerShare, costPerMergedPrNanos, isLowerBound }, workers: [{ sessionId, title, harness, modelId, durationMs, estimatedCost, outcome, measured }], mergedPrs, orchestratorGenerations, unmeasuredSessions }`, every nullable field emitted as `null`.

Task 4 consumes this.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/httpd/controllers/usage_test.go`:

```go
func TestGetProjectRollupEmitsNullsForUnknowns(t *testing.T) {
	controller := &controllers.UsageController{
		Svc: stubUsageSummaryService{},
		Rollup: stubRollupService{rollup: domain.ProjectUsageRollup{
			MergedPRs:               0,
			OrchestratorGenerations: 2,
			ProjectID:               "proj-1",
			UnmeasuredSessions:      1,
			Workers: []domain.WorkerUsageRow{
				{Harness: domain.HarnessDroid, Measured: false, Outcome: domain.WorkerOutcomeActive, SessionID: "w-1"},
			},
		}},
	}
	router := chi.NewRouter()
	controller.Register(router)

	request := httptest.NewRequest(http.MethodGet, "/usage/projects/proj-1/rollup", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Efficiency struct {
			CostPerMergedPrNanos *int64 `json:"costPerMergedPrNanos"`
			IsLowerBound         bool   `json:"isLowerBound"`
		} `json:"efficiency"`
		OrchestratorGenerations int64 `json:"orchestratorGenerations"`
		Workers                 []struct {
			EstimatedCost any  `json:"estimatedCost"`
			Measured      bool `json:"measured"`
		} `json:"workers"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Efficiency.CostPerMergedPrNanos != nil {
		t.Fatalf("costPerMergedPrNanos = %v, want null with zero merged PRs", body.Efficiency.CostPerMergedPrNanos)
	}
	if !body.Efficiency.IsLowerBound {
		t.Fatal("isLowerBound = false, want true with an unmeasured session")
	}
	if body.OrchestratorGenerations != 2 {
		t.Fatalf("orchestratorGenerations = %d, want 2", body.OrchestratorGenerations)
	}
	if len(body.Workers) != 1 || body.Workers[0].Measured || body.Workers[0].EstimatedCost != nil {
		t.Fatalf("workers = %+v, want one unmeasured worker with null cost", body.Workers)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run ProjectRollup -v`

Expected: FAIL — no route, no `Rollup` field.

- [ ] **Step 3: Add the response types and route**

In `controllers/usage.go`, add the interface, response types (mirroring the field list above, with `*int64`/`*float64` for every nullable), a `Rollup RollupService` field, and register `r.Get("/usage/projects/{projectId}/rollup", c.getProjectRollup)`. Follow `getSession`'s existing error and not-implemented handling exactly. Wire the service in `api.go` beside the existing usage controller construction.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./internal/httpd/... -run ProjectRollup -v`

Expected: PASS.

- [ ] **Step 5: Regenerate the contract**

Run: `npm run api`

Verify: `grep -n 'rollup' backend/internal/httpd/apispec/openapi.yaml | head`

- [ ] **Step 6: Run the full backend suite and linter**

Run: `npm run lint`

Expected: PASS, including `TestRouteSpecParity`.

- [ ] **Step 7: Commit**

```bash
git add backend frontend/src/api/schema.ts
git commit -m "feat(api): expose the project usage rollup endpoint"
```

---

### Task 4: Give orchestrators a Metrics rail

**Files:**
- Create: `frontend/src/renderer/hooks/useProjectUsageRollup.ts`
- Create: `frontend/src/renderer/components/OrchestratorMetrics.tsx`
- Create: `frontend/src/renderer/components/OrchestratorMetrics.test.tsx`
- Modify: `frontend/src/renderer/components/SessionInspector.tsx`
- Modify: `frontend/src/renderer/components/SessionView.tsx:1080-1081`
- Modify: `frontend/src/renderer/i18n/*.json`

**Interfaces:**
- Consumes: the roll-up endpoint (Task 3); the `metricsView` slot and `"metrics"` view (Phase 1 Task 1).
- Produces: `OrchestratorMetrics({ session })`; `SessionInspector` accepts the orchestrator tab set.

- [ ] **Step 1: Add the i18n keys**

```bash
python3 - <<'PY'
import json, pathlib

keys = {
    "inspector.metrics.rollup.thisOrchestrator": "This orchestrator",
    "inspector.metrics.rollup.workers": "Workers, this project",
    "inspector.metrics.rollup.workerCount": "{{count}} workers",
    "inspector.metrics.rollup.split": "Orchestrator / worker spend",
    "inspector.metrics.rollup.splitUnavailable": "Not enough cost data to split orchestrator and worker spend.",
    "inspector.metrics.rollup.costPerMergedPr": "Cost per merged PR",
    "inspector.metrics.rollup.costPerMergedPrUnavailable": "No merged pull requests yet.",
    "inspector.metrics.rollup.lowerBound": "{{count}} sessions could not be measured, so these totals are a lower bound.",
    "inspector.metrics.rollup.generations": "Project all-time across {{count}} orchestrator generations.",
    "inspector.metrics.rollup.outcome.active": "Active",
    "inspector.metrics.rollup.outcome.merged": "Merged",
    "inspector.metrics.rollup.outcome.abandoned": "Abandoned",
    "inspector.metrics.rollup.outcome.failed": "Failed",
    "inspector.metrics.rollup.unmeasured": "Not measurable",
    "inspector.metrics.rollup.empty": "This project has no worker sessions yet.",
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

Verify: `grep -l '"inspector.metrics.rollup.generations"' frontend/src/renderer/i18n/*.json | wc -l` → `8`

- [ ] **Step 2: Write the failing test**

Create `frontend/src/renderer/components/OrchestratorMetrics.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OrchestratorMetrics } from "./OrchestratorMetrics";

let mockRollup: unknown;

vi.mock("../hooks/useProjectUsageRollup", () => ({
  useProjectUsageRollup: () => mockRollup,
}));

const session = { id: "orch-1", kind: "orchestrator", projectId: "proj-1" } as never;

describe("OrchestratorMetrics", () => {
  it("states the generation count so a project total is never mistaken for this orchestrator", () => {
    mockRollup = {
      data: {
        efficiency: { costPerMergedPrNanos: 6_000_000_000, isLowerBound: false, orchestratorShare: 0.25, workerShare: 0.75 },
        mergedPrs: 2,
        orchestratorGenerations: 3,
        orchestratorTotals: { estimatedCost: { nanos: 3_000_000_000 }, processedTokens: 100 },
        unmeasuredSessions: 0,
        workerTotals: { estimatedCost: { nanos: 9_000_000_000 }, processedTokens: 900 },
        workers: [],
      },
      isError: false,
      isLoading: false,
    };
    render(<OrchestratorMetrics session={session} />);
    expect(screen.getByText(/across 3 orchestrator generations/i)).toBeInTheDocument();
    expect(screen.getByText(/25%/)).toBeInTheDocument();
  });

  it("flags totals as a lower bound when sessions were unmeasurable", () => {
    mockRollup = {
      data: {
        efficiency: { costPerMergedPrNanos: null, isLowerBound: true, orchestratorShare: null, workerShare: null },
        mergedPrs: 0,
        orchestratorGenerations: 1,
        orchestratorTotals: { estimatedCost: null, processedTokens: null },
        unmeasuredSessions: 2,
        workerTotals: { estimatedCost: null, processedTokens: null },
        workers: [
          { durationMs: null, estimatedCost: null, harness: "droid", measured: false, modelId: "", outcome: "active", sessionId: "w-1", title: "Untracked" },
        ],
      },
      isError: false,
      isLoading: false,
    };
    render(<OrchestratorMetrics session={session} />);
    expect(screen.getByText(/lower bound/i)).toBeInTheDocument();
    expect(screen.getByText(/No merged pull requests yet/i)).toBeInTheDocument();
    expect(screen.getByText(/Not measurable/i)).toBeInTheDocument();
  });

  it("says the project has no workers rather than rendering an empty table", () => {
    mockRollup = {
      data: {
        efficiency: { costPerMergedPrNanos: null, isLowerBound: false, orchestratorShare: null, workerShare: null },
        mergedPrs: 0,
        orchestratorGenerations: 1,
        orchestratorTotals: { estimatedCost: null, processedTokens: null },
        unmeasuredSessions: 0,
        workerTotals: { estimatedCost: null, processedTokens: null },
        workers: [],
      },
      isError: false,
      isLoading: false,
    };
    render(<OrchestratorMetrics session={session} />);
    expect(screen.getByText(/no worker sessions yet/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npm --prefix frontend test -- OrchestratorMetrics`

Expected: FAIL — module not found.

- [ ] **Step 4: Add the hook**

Create `frontend/src/renderer/hooks/useProjectUsageRollup.ts`, mirroring `useSessionEffort.ts` from Phase 3:

```ts
import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { sessionUsageQueryRoot } from "./useSessionUsageSummaries";

export type ProjectUsageRollup = components["schemas"]["ProjectUsageRollupResponse"];

export const projectUsageRollupQueryKey = (projectId: string) =>
	[...sessionUsageQueryRoot, "project-rollup", projectId] as const;

export async function fetchProjectUsageRollup(projectId: string): Promise<ProjectUsageRollup> {
	const { data, error } = await apiClient.GET("/api/v1/usage/projects/{projectId}/rollup", {
		params: { path: { projectId } },
	});
	if (error) throw error;
	return data;
}

export function useProjectUsageRollup(projectId: string, enabled = true) {
	return useQuery({
		enabled: enabled && Boolean(projectId),
		queryFn: () => fetchProjectUsageRollup(projectId),
		queryKey: projectUsageRollupQueryKey(projectId),
		retry: 1,
	});
}
```

- [ ] **Step 5: Build the panel**

Create `frontend/src/renderer/components/OrchestratorMetrics.tsx` with four sections: *This orchestrator* (its own cost, tokens, duration), *Workers, this project* (outcome counts, total worker spend, the spend split as two percentages), *Cost per merged PR*, and the *worker list* (rows of title, harness, model, duration, cost, outcome; unmeasured rows show the "Not measurable" label instead of a cost and are not clickable to a cost breakdown). Close with the lower-bound line when `isLowerBound`, and always the generations line.

Reuse `Section`, `inspectorEmptyClass` and `formatEstimatedCost` from the existing imports so the rail looks identical to the worker Metrics tab.

- [ ] **Step 6: Give SessionInspector an orchestrator tab set**

In `SessionInspector.tsx`, accept a new optional prop `variant?: "worker" | "orchestrator"` defaulting to `"worker"`. When `"orchestrator"`:

```tsx
	const availableViewDefs =
		variant === "orchestrator"
			? VIEW_DEFS.filter((entry) => entry.id === "metrics")
			: reviewsAvailable
				? VIEW_DEFS
				: VIEW_DEFS.filter((entry) => entry.id !== "reviews");
```

and make the default view follow suit — the existing fallback to `"summary"` must become `availableViewDefs[0].id`, or an orchestrator lands on a tab that is not in its own tab list. Pass `metricsView={<OrchestratorMetrics session={session} />}` for the orchestrator variant.

- [ ] **Step 7: Stop suppressing the rail for orchestrators**

In `SessionView.tsx`, replace lines 1080-1081:

```tsx
	// Orchestrators get the full workspace width by default, but still need a
	// rail: they are a project's longest-lived and most expensive session, so
	// their metrics have to be reachable. The rail starts collapsed for them.
	const hasInspector = Boolean(session);
```

Pass `variant={isOrchestrator ? "orchestrator" : "worker"}` to `<SessionInspector>` (around line 1988), and ensure the rail's default open state is `false` for orchestrators — find where `isInspectorOpen` / `setInspectorOpenForSession` is initialized and default orchestrators to closed so existing worker behaviour is untouched.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `npm --prefix frontend test -- 'OrchestratorMetrics|SessionInspector|SessionView'`

Expected: PASS. Any existing test asserting that orchestrators have no rail must be updated — that is the intended behaviour change; update the assertion to "orchestrators have a collapsed, Metrics-only rail".

- [ ] **Step 9: Verify typecheck and the copy rule**

Run: `npm run frontend:typecheck && npm --prefix frontend test -- renderer-coverage`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add frontend/src/renderer
git commit -m "feat(inspector): give orchestrators a collapsed metrics-only rail"
```

---

### Task 5: Verify the roll-up against a real project

**Files:** none — manual gate.

- [ ] **Step 1: Open an orchestrator with real workers**

Use a project that has run several workers, at least one merged.

Expected: the rail exists, starts collapsed, and expands to a single Metrics tab.

- [ ] **Step 2: Check the arithmetic by hand**

Note the per-worker costs shown on the board cards and add them up.

Expected: the worker total matches. If a worker with no usage source exists, the lower-bound line appears and that worker shows "Not measurable" rather than `$0.00`.

- [ ] **Step 3: Check the generation line**

Restart the project orchestrator, then reopen the new orchestrator's Metrics tab.

Expected: the generation count increments, and "this orchestrator" shows only the new session's own spend — not the project's.

- [ ] **Step 4: Check cost per merged PR**

Expected: total project cost divided by merged PR count. On a project with no merges, "No merged pull requests yet" — not `$0.00`.

- [ ] **Step 5: Confirm workers are unaffected**

Open a worker session.

Expected: the rail behaves exactly as before — open by default, all five tabs.

- [ ] **Step 6: Commit any fixes**

```bash
git add -A
git commit -m "fix(usage): address orchestrator rollup verification findings"
```

---

## Phase Completion

```bash
npm run lint
npm run product-ui:check
npm run frontend:typecheck
npm --prefix frontend test
```

All four must pass.

## Closing the spec

With this phase done, all five phases of the spec are implemented. Update the spec's Open Questions section with the two findings the work produced:

1. Whether Claude Code and Codex carry enough tool timing for time-sorted ordering (measured in Phase 3, Task 8, Step 4).
2. Whether hooks are worth adding as an optional enrichment for live tool latency, now that transcript-derived freshness can be judged in practice.

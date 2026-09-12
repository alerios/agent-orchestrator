package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestProjectUsageRollupSplitsOrchestratorFromWorkers(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	seedRollupFixture(t, store)

	got, err := store.ProjectUsageRollup(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ProjectUsageRollup: %v", err)
	}
	if got.ProjectID != "proj-1" {
		t.Fatalf("ProjectID = %q, want proj-1", got.ProjectID)
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
	if got.OrchestratorTotals.EstimatedCost == nil || got.WorkerTotals.EstimatedCost == nil {
		t.Fatalf("totals cost = orch:%+v worker:%+v, want both priced",
			got.OrchestratorTotals.EstimatedCost, got.WorkerTotals.EstimatedCost)
	}
	if got.MergedPRs != 1 {
		t.Fatalf("MergedPRs = %d, want 1", got.MergedPRs)
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
	unmeasured := 0
	for _, worker := range got.Workers {
		if !worker.Measured {
			unmeasured++
			if worker.EstimatedCost != nil {
				t.Fatalf("worker %s is unmeasured but carries a cost — unknown must not become a number", worker.SessionID)
			}
		}
	}
	if unmeasured != 1 {
		t.Fatalf("unmeasured worker rows = %d, want 1 (the droid worker)", unmeasured)
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
	if len(got.Workers) != 3 {
		t.Fatalf("worker rows = %d, want 3", len(got.Workers))
	}
	var last int64 = 1 << 62
	seenUnpriced := false
	for _, worker := range got.Workers {
		if worker.EstimatedCost == nil {
			seenUnpriced = true
			continue // unmeasured rows sort last
		}
		if seenUnpriced {
			t.Fatalf("priced worker %s sorted after an unpriced row", worker.SessionID)
		}
		if worker.EstimatedCost.TotalNanos > last {
			t.Fatalf("workers are not cost-descending: %d after %d", worker.EstimatedCost.TotalNanos, last)
		}
		last = worker.EstimatedCost.TotalNanos
	}
}

// The rail shows delivery facts next to spend, so each worker row must carry
// the session's own identity and outcome rather than only its cost.
func TestProjectUsageRollupCarriesWorkerIdentityAndOutcome(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	seedRollupFixture(t, store)

	got, err := store.ProjectUsageRollup(ctx, "proj-1")
	if err != nil {
		t.Fatalf("ProjectUsageRollup: %v", err)
	}
	byTitle := make(map[string]domain.WorkerUsageRow, len(got.Workers))
	for _, worker := range got.Workers {
		byTitle[worker.Title] = worker
	}
	expensive, ok := byTitle["expensive merged worker"]
	if !ok {
		t.Fatalf("worker titles = %v, want the merged worker", byTitle)
	}
	if expensive.Outcome != domain.WorkerOutcomeMerged {
		t.Fatalf("merged worker outcome = %q, want merged", expensive.Outcome)
	}
	if expensive.Harness != domain.HarnessClaudeCode || expensive.ModelID != "claude-x" {
		t.Fatalf("merged worker identity = %+v, want claude-code/claude-x", expensive)
	}
	if expensive.DurationMS == nil || *expensive.DurationMS != 90_000 {
		t.Fatalf("merged worker DurationMS = %v, want 90000", expensive.DurationMS)
	}

	abandoned, ok := byTitle["abandoned worker"]
	if !ok {
		t.Fatalf("worker titles = %v, want the abandoned worker", byTitle)
	}
	if abandoned.Outcome != domain.WorkerOutcomeAbandoned {
		t.Fatalf("abandoned worker outcome = %q, want abandoned", abandoned.Outcome)
	}

	droid, ok := byTitle["unmeasured droid worker"]
	if !ok {
		t.Fatalf("worker titles = %v, want the droid worker", byTitle)
	}
	if droid.Outcome != domain.WorkerOutcomeActive {
		t.Fatalf("live droid worker outcome = %q, want active", droid.Outcome)
	}
	// Effort timing is a separate fact from usage: a worker AO never measured
	// for tokens can still have a known wall-clock duration.
	if droid.DurationMS != nil {
		t.Fatalf("droid worker DurationMS = %v, want nil", droid.DurationMS)
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
	if got.OrchestratorTotals.EstimatedCost != nil {
		t.Fatal("OrchestratorTotals.EstimatedCost is set for an empty project, want nil rather than $0")
	}
	if got.OrchestratorGenerations != 0 || got.UnmeasuredSessions != 0 {
		t.Fatalf("got = %+v, want zero counts", got)
	}
}

// seedRollupFixture builds one project with: a retired orchestrator (no usage),
// an active orchestrator with usage, two measured workers at different costs
// (one merged, one abandoned), and one droid worker AO never measured.
func seedRollupFixture(t *testing.T, s *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	seedProject(t, s, "proj-1")

	// A retired orchestrator generation: it exists as a delivery fact even
	// though AO collected no usage for it.
	seedRollupSession(t, s, rollupSessionSpec{
		title: "retired orchestrator", kind: domain.KindOrchestrator,
		harness: domain.HarnessClaudeCode, model: "claude-x", terminated: true,
	})

	activeOrchestrator := seedRollupSession(t, s, rollupSessionSpec{
		title: "active orchestrator", kind: domain.KindOrchestrator,
		harness: domain.HarnessClaudeCode, model: "claude-x",
	})
	seedRollupUsage(t, s, activeOrchestrator, now, 1_000, 400, 5_000_000)

	merged := seedRollupSession(t, s, rollupSessionSpec{
		title: "expensive merged worker", kind: domain.KindWorker,
		harness: domain.HarnessClaudeCode, model: "claude-x", terminated: true,
		durationMS: ptrRollupInt64(90_000),
	})
	seedRollupUsage(t, s, merged, now, 8_000, 2_000, 90_000_000)
	writeRollupPR(t, s, merged, "https://example.test/pr/1", 1, now, true, false)

	abandoned := seedRollupSession(t, s, rollupSessionSpec{
		title: "abandoned worker", kind: domain.KindWorker,
		harness: domain.HarnessCodex, model: "gpt-5", terminated: true,
		durationMS: ptrRollupInt64(30_000),
	})
	seedRollupUsage(t, s, abandoned, now, 500, 100, 1_000_000)

	// A droid worker: AO has no certified usage source for droid at all, so its
	// spend is unknown rather than zero.
	seedRollupSession(t, s, rollupSessionSpec{
		title: "unmeasured droid worker", kind: domain.KindWorker,
		harness: domain.HarnessDroid, model: "droid-default",
	})

	// A second project's spend must never leak into proj-1's roll-up.
	seedProject(t, s, "proj-2")
	other, err := s.CreateSession(ctx, func() domain.SessionRecord {
		rec := sampleRecord("proj-2")
		rec.DisplayName = "other project worker"
		rec.Harness = domain.HarnessClaudeCode
		rec.Metadata.Model = "claude-x"
		return rec
	}())
	mustNoError(t, err, "create other-project session")
	seedRollupUsage(t, s, other, now, 777_000, 777_000, 777_000_000)
	writeRollupPR(t, s, other, "https://example.test/pr/99", 99, now, true, false)
}

type rollupSessionSpec struct {
	title      string
	kind       domain.SessionKind
	harness    domain.AgentHarness
	model      string
	terminated bool
	durationMS *int64
}

func seedRollupSession(t *testing.T, s *sqlite.Store, spec rollupSessionSpec) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	rec := sampleRecord("proj-1")
	rec.DisplayName = spec.title
	rec.Kind = spec.kind
	rec.Harness = spec.harness
	rec.Metadata.Model = spec.model
	rec.IsTerminated = spec.terminated
	got, err := s.CreateSession(ctx, rec)
	mustNoError(t, err, "create rollup session "+spec.title)
	if spec.durationMS != nil {
		mustNoError(t, s.UpsertSessionEffort(ctx, got.ID, domain.SessionEffort{
			DurationMS: spec.durationMS, TimingAvailable: true,
		}, time.Unix(1700000000, 0).UTC()), "seed effort for "+spec.title)
	}
	return got
}

// seedRollupUsage writes one priced usage event for the session through the
// normal collector write path, so the fixture exercises real storage rules.
func seedRollupUsage(
	t *testing.T,
	s *sqlite.Store,
	session domain.SessionRecord,
	now time.Time,
	inputTokens, outputTokens, costNanos int64,
) {
	t.Helper()
	source := seedUsageSource(t, s, session, now)
	event := rollupUsageEvent(session.Harness, string(session.ID)+"-event", inputTokens, outputTokens, costNanos)
	mustNoError(t, s.ApplyUsageChunk(
		context.Background(), source.ID, 0, source.UpdatedAt,
		domain.SourceCursorState{ByteOffset: 64, State: domain.UsageSourceActive, UpdatedAt: now},
		[]domain.ModelUsageEvent{event},
	), "apply rollup usage chunk")
}

func rollupUsageEvent(
	harness domain.AgentHarness,
	key string,
	inputTokens, outputTokens, costNanos int64,
) domain.ModelUsageEvent {
	event := anthropicUsageEvent(key, inputTokens, 0, 0, outputTokens)
	event.BillingProviderID = "anthropic"
	if harness == domain.HarnessCodex {
		event = usageEvent(key, canonicalUsageTokens(inputTokens, 0, inputTokens, outputTokens))
		event.BillingProviderID = "openai"
	}
	event.BillingProviderSource = domain.UsageBillingProviderObserved
	total := costNanos
	event.Costs = domain.UsageEventCosts{EstimatedCostNanos: &total, PricingVersion: "rollup-test"}
	return event
}

func writeRollupPR(
	t *testing.T,
	s *sqlite.Store,
	session domain.SessionRecord,
	url string,
	number int,
	now time.Time,
	merged, closed bool,
) {
	t.Helper()
	mustNoError(t, s.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: url, SessionID: session.ID, Number: number, Merged: merged, Closed: closed,
		SourceBranch: "feat/x", TargetBranch: "main", UpdatedAt: now, ObservedAt: now,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve), "write rollup PR "+url)
}

func ptrRollupInt64(v int64) *int64 { return &v }

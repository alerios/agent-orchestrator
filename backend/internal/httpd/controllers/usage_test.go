package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type stubUsageSummaryService struct{}

func (stubUsageSummaryService) ListCompact(context.Context, domain.ProjectID) ([]domain.CompactSessionUsage, error) {
	return nil, nil
}

func (stubUsageSummaryService) Get(context.Context, domain.SessionID) (domain.SessionUsageSummary, error) {
	return domain.SessionUsageSummary{}, nil
}

type stubEffortService struct {
	effort   domain.SessionEffort
	mix      []domain.ToolMixEntry
	notFound bool
	err      error
}

func (s stubEffortService) Get(context.Context, domain.SessionID) (domain.SessionEffort, []domain.ToolMixEntry, bool, error) {
	if s.err != nil || s.notFound {
		return domain.SessionEffort{}, nil, false, s.err
	}
	return s.effort, s.mix, true, nil
}

type fakeUsageSummaryService struct {
	projectID domain.ProjectID
	sessionID domain.SessionID
	items     []domain.CompactSessionUsage
	detail    domain.SessionUsageSummary
	err       error
}

func (f *fakeUsageSummaryService) ListCompact(_ context.Context, projectID domain.ProjectID) ([]domain.CompactSessionUsage, error) {
	f.projectID = projectID
	return f.items, f.err
}

func (f *fakeUsageSummaryService) Get(_ context.Context, sessionID domain.SessionID) (domain.SessionUsageSummary, error) {
	f.sessionID = sessionID
	return f.detail, f.err
}

func newUsageTestServer(t *testing.T, svc *fakeUsageSummaryService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{UsageSummary: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUsageAPIListsCompactProjectUsage(t *testing.T) {
	inputCost := int64(300000000)
	processed := int64(12300)
	unavailableProcessed := int64(3)
	svc := &fakeUsageSummaryService{items: []domain.CompactSessionUsage{
		{
			SessionID: "reverb-12", ProcessedTokens: &processed, Incomplete: true,
			EstimatedCost: &domain.EstimatedCost{
				TotalNanos: 420000000, InputNanos: &inputCost,
				Coverage:            domain.EstimatedCostCoveragePartial,
				ProviderAttribution: domain.EstimatedCostProviderAttributionInferred,
			},
		},
		{SessionID: "unavailable", ProcessedTokens: &unavailableProcessed},
	}}
	srv := newUsageTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/usage/sessions?projectId=reverb", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.projectID != "reverb" {
		t.Fatalf("project id = %q, want reverb", svc.projectID)
	}
	var got struct {
		Sessions []struct {
			SessionID       string          `json:"sessionId"`
			ProcessedTokens int64           `json:"processedTokens"`
			TotalTokens     int64           `json:"totalTokens"`
			Incomplete      bool            `json:"incomplete"`
			EstimatedCost   json.RawMessage `json:"estimatedCost"`
		} `json:"sessions"`
	}
	mustJSON(t, body, &got)
	if len(got.Sessions) != 2 || got.Sessions[0].SessionID != "reverb-12" ||
		got.Sessions[0].ProcessedTokens != 12300 || got.Sessions[0].TotalTokens != 12300 ||
		!got.Sessions[0].Incomplete {
		t.Fatalf("response = %+v", got)
	}
	var cost struct {
		TotalNanos          int64  `json:"totalNanos"`
		InputNanos          *int64 `json:"inputNanos"`
		CachedInputNanos    *int64 `json:"cachedInputNanos"`
		Coverage            string `json:"coverage"`
		ProviderAttribution string `json:"providerAttribution"`
	}
	mustJSON(t, got.Sessions[0].EstimatedCost, &cost)
	if cost.TotalNanos != 420000000 || cost.InputNanos == nil || *cost.InputNanos != 300000000 ||
		cost.CachedInputNanos != nil || cost.Coverage != "partial" || cost.ProviderAttribution != "inferred" {
		t.Fatalf("estimated cost = %+v", cost)
	}
	if string(got.Sessions[1].EstimatedCost) != "null" {
		t.Fatalf("unavailable estimatedCost = %s, want explicit null", got.Sessions[1].EstimatedCost)
	}
}

func TestUsageAPIShowsDetailedEstimatedCostAndProviderAttribution(t *testing.T) {
	input := int64(1000)
	uncached := int64(600)
	output := int64(200)
	zero := int64(0)
	cachedInput := int64(400)
	processed := int64(1200)
	svc := &fakeUsageSummaryService{detail: domain.SessionUsageSummary{
		SessionID: "reverb-12", Incomplete: true,
		Totals: domain.UsageMetricTotals{
			InputTokens: &input, CachedInputTokens: &cachedInput, UncachedInputTokens: &uncached,
			OutputTokens: &output, ProcessedTokens: &processed,
			EstimatedCost: &domain.EstimatedCost{
				TotalNanos: 135, InputNanos: &input, CachedInputNanos: &zero,
				OutputNanos: &output, Coverage: domain.EstimatedCostCoveragePartial,
				ProviderAttribution: domain.EstimatedCostProviderAttributionMixed,
			},
		},
		Harnesses: []domain.HarnessUsageSummary{{
			Harness: domain.HarnessCodex,
			Models: []domain.ModelUsageSummary{{
				ModelID: "gpt-5.6",
				Totals: domain.UsageMetricTotals{EstimatedCost: &domain.EstimatedCost{
					TotalNanos: 0, InputNanos: &zero, CachedInputNanos: &zero,
					OutputNanos: &zero, Coverage: domain.EstimatedCostCoverageComplete,
					ProviderAttribution: domain.EstimatedCostProviderAttributionObserved,
				}},
			}},
		}},
	}}
	srv := newUsageTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/usage/sessions/reverb-12", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.sessionID != "reverb-12" {
		t.Fatalf("session id = %q", svc.sessionID)
	}
	// Provider-shaped counters and per-metric provenance are no longer projected
	// onto this boundary; the bounded provider object owns them now.
	for _, forbidden := range []string{
		`"cost"`, `"valueNanos"`, `"pricingVersion"`,
		`"provenance"`, `"providerDetails"`, `"cacheWriteTokens"`, `"reasoningTokens"`,
	} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("detailed usage exposed %s: %s", forbidden, body)
		}
	}
	var got struct {
		SessionID  string `json:"sessionId"`
		Incomplete bool   `json:"incomplete"`
		Totals     struct {
			InputTokens         int64 `json:"inputTokens"`
			CachedInputTokens   int64 `json:"cachedInputTokens"`
			UncachedInputTokens int64 `json:"uncachedInputTokens"`
			OutputTokens        int64 `json:"outputTokens"`
			ProcessedTokens     int64 `json:"processedTokens"`
			CacheReadTokens     int64 `json:"cacheReadTokens"`
			EstimatedCost       struct {
				TotalNanos          int64  `json:"totalNanos"`
				InputNanos          *int64 `json:"inputNanos"`
				CachedInputNanos    *int64 `json:"cachedInputNanos"`
				Coverage            string `json:"coverage"`
				ProviderAttribution string `json:"providerAttribution"`
			} `json:"estimatedCost"`
		} `json:"totals"`
		Harnesses []struct {
			Models []struct {
				ProviderID string `json:"providerId"`
				ModelID    string `json:"modelId"`
				Totals     struct {
					EstimatedCost struct {
						TotalNanos          int64  `json:"totalNanos"`
						Coverage            string `json:"coverage"`
						ProviderAttribution string `json:"providerAttribution"`
					} `json:"estimatedCost"`
				} `json:"totals"`
			} `json:"models"`
		} `json:"harnesses"`
	}
	mustJSON(t, body, &got)
	if got.SessionID != "reverb-12" || !got.Incomplete || got.Totals.InputTokens != 1000 ||
		got.Totals.EstimatedCost.TotalNanos != 135 ||
		got.Totals.EstimatedCost.InputNanos == nil || *got.Totals.EstimatedCost.InputNanos != 1000 ||
		got.Totals.EstimatedCost.CachedInputNanos == nil || *got.Totals.EstimatedCost.CachedInputNanos != 0 ||
		got.Totals.EstimatedCost.Coverage != "partial" ||
		got.Totals.EstimatedCost.ProviderAttribution != "mixed" ||
		got.Totals.CachedInputTokens != 400 || got.Totals.UncachedInputTokens != 600 ||
		got.Totals.OutputTokens != 200 ||
		got.Totals.ProcessedTokens != 1200 || got.Totals.CacheReadTokens != 400 ||
		len(got.Harnesses) != 1 || len(got.Harnesses[0].Models) != 1 ||
		got.Harnesses[0].Models[0].ModelID != "gpt-5.6" ||
		got.Harnesses[0].Models[0].Totals.EstimatedCost.TotalNanos != 0 ||
		got.Harnesses[0].Models[0].Totals.EstimatedCost.Coverage != "complete" ||
		got.Harnesses[0].Models[0].Totals.EstimatedCost.ProviderAttribution != "observed" {
		t.Fatalf("response = %+v", got)
	}
}

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

type stubRollupService struct {
	rollup domain.ProjectUsageRollup
	err    error
}

func (s stubRollupService) ProjectUsageRollup(
	context.Context, domain.ProjectID,
) (domain.ProjectUsageRollup, error) {
	return s.rollup, s.err
}

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
			CostPerMergedPrNanos *int64   `json:"costPerMergedPrNanos"`
			IsLowerBound         bool     `json:"isLowerBound"`
			OrchestratorShare    *float64 `json:"orchestratorShare"`
			WorkerShare          *float64 `json:"workerShare"`
		} `json:"efficiency"`
		OrchestratorGenerations int64 `json:"orchestratorGenerations"`
		Workers                 []struct {
			DurationMs    any  `json:"durationMs"`
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
	if body.Efficiency.OrchestratorShare != nil || body.Efficiency.WorkerShare != nil {
		t.Fatal("shares should be null when neither side is measured")
	}
	if !body.Efficiency.IsLowerBound {
		t.Fatal("isLowerBound = false, want true with an unmeasured session")
	}
	if body.OrchestratorGenerations != 2 {
		t.Fatalf("orchestratorGenerations = %d, want 2", body.OrchestratorGenerations)
	}
	if len(body.Workers) != 1 || body.Workers[0].Measured ||
		body.Workers[0].EstimatedCost != nil || body.Workers[0].DurationMs != nil {
		t.Fatalf("workers = %+v, want one unmeasured worker with null cost and duration", body.Workers)
	}
}

func TestGetProjectRollupNotImplementedWithoutService(t *testing.T) {
	controller := &controllers.UsageController{Svc: stubUsageSummaryService{}}
	router := chi.NewRouter()
	controller.Register(router)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/projects/proj-1/rollup", nil))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
}

func TestGetProjectRollupPropagatesServiceError(t *testing.T) {
	controller := &controllers.UsageController{
		Svc:    stubUsageSummaryService{},
		Rollup: stubRollupService{err: errors.New("boom")},
	}
	router := chi.NewRouter()
	controller.Register(router)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/projects/proj-1/rollup", nil))
	if recorder.Code < 400 {
		t.Fatalf("status = %d, want an error status", recorder.Code)
	}
}

func TestGetProjectRollupSharesAndCostPerMergedPR(t *testing.T) {
	orchestratorCost := domain.EstimatedCost{
		TotalNanos: 750, Coverage: domain.EstimatedCostCoverageComplete,
	}
	workerCost := domain.EstimatedCost{
		TotalNanos: 250, Coverage: domain.EstimatedCostCoverageComplete,
	}
	duration := int64(1234)
	controller := &controllers.UsageController{
		Svc: stubUsageSummaryService{},
		Rollup: stubRollupService{rollup: domain.ProjectUsageRollup{
			MergedPRs:               4,
			OrchestratorGenerations: 1,
			OrchestratorTotals:      domain.UsageMetricTotals{EstimatedCost: &orchestratorCost},
			ProjectID:               "proj-1",
			WorkerTotals:            domain.UsageMetricTotals{EstimatedCost: &workerCost},
			Workers: []domain.WorkerUsageRow{{
				DurationMS: &duration, EstimatedCost: &workerCost, Harness: domain.HarnessClaudeCode,
				Measured: true, ModelID: "claude-x", Outcome: domain.WorkerOutcomeMerged,
				SessionID: "w-1", Title: "worker one",
			}},
		}},
	}
	router := chi.NewRouter()
	controller.Register(router)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/projects/proj-1/rollup", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Efficiency struct {
			CostPerMergedPrNanos *int64   `json:"costPerMergedPrNanos"`
			IsLowerBound         bool     `json:"isLowerBound"`
			OrchestratorShare    *float64 `json:"orchestratorShare"`
			WorkerShare          *float64 `json:"workerShare"`
		} `json:"efficiency"`
		MergedPrs int64  `json:"mergedPrs"`
		ProjectID string `json:"projectId"`
		Workers   []struct {
			DurationMs    *int64 `json:"durationMs"`
			EstimatedCost *struct {
				TotalNanos int64 `json:"totalNanos"`
			} `json:"estimatedCost"`
			Harness   string `json:"harness"`
			Measured  bool   `json:"measured"`
			ModelID   string `json:"modelId"`
			Outcome   string `json:"outcome"`
			SessionID string `json:"sessionId"`
			Title     string `json:"title"`
		} `json:"workers"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Efficiency.OrchestratorShare == nil || *body.Efficiency.OrchestratorShare != 0.75 {
		t.Fatalf("orchestratorShare = %v, want 0.75", body.Efficiency.OrchestratorShare)
	}
	if body.Efficiency.WorkerShare == nil || *body.Efficiency.WorkerShare != 0.25 {
		t.Fatalf("workerShare = %v, want 0.25", body.Efficiency.WorkerShare)
	}
	if body.Efficiency.CostPerMergedPrNanos == nil || *body.Efficiency.CostPerMergedPrNanos != 250 {
		t.Fatalf("costPerMergedPrNanos = %v, want 250", body.Efficiency.CostPerMergedPrNanos)
	}
	if body.Efficiency.IsLowerBound {
		t.Fatal("isLowerBound = true, want false with full coverage and no unmeasured sessions")
	}
	if body.MergedPrs != 4 || body.ProjectID != "proj-1" {
		t.Fatalf("mergedPrs = %d, projectId = %q", body.MergedPrs, body.ProjectID)
	}
	worker := body.Workers[0]
	if worker.DurationMs == nil || *worker.DurationMs != 1234 || worker.EstimatedCost == nil ||
		worker.EstimatedCost.TotalNanos != 250 || worker.Harness != "claude-code" || !worker.Measured ||
		worker.ModelID != "claude-x" || worker.Outcome != "merged" || worker.SessionID != "w-1" ||
		worker.Title != "worker one" {
		t.Fatalf("worker = %+v", worker)
	}
}

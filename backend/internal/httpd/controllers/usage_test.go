package controllers_test

import (
	"context"
	"encoding/json"
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

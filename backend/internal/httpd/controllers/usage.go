package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// UsageSummaryService is the controller-facing compact usage read contract.
type UsageSummaryService interface {
	ListCompact(context.Context, domain.ProjectID) ([]domain.CompactSessionUsage, error)
	Get(context.Context, domain.SessionID) (domain.SessionUsageSummary, error)
}

// EffortService is the controller-facing effort read contract.
type EffortService interface {
	Get(context.Context, domain.SessionID) (domain.SessionEffort, []domain.ToolMixEntry, bool, error)
}

// UsageController owns compact dashboard usage routes.
type UsageController struct {
	Svc    UsageSummaryService
	Effort EffortService
}

// Register mounts usage routes on the supplied router.
func (c *UsageController) Register(r chi.Router) {
	r.Get("/usage/sessions", c.listSessions)
	r.Get("/usage/sessions/{sessionId}", c.getSession)
	r.Get("/usage/sessions/{sessionId}/effort", c.getSessionEffort)
}

func (c *UsageController) listSessions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions")
		return
	}
	items, err := c.Svc.ListCompact(r.Context(), domain.ProjectID(r.URL.Query().Get("projectId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]CompactSessionUsageResponse, 0, len(items))
	for _, item := range items {
		var totalTokens int64
		if item.ProcessedTokens != nil {
			totalTokens = *item.ProcessedTokens
		}
		out = append(out, CompactSessionUsageResponse{
			SessionID: item.SessionID, ProcessedTokens: item.ProcessedTokens,
			TotalTokens: totalTokens, Incomplete: item.Incomplete,
			EstimatedCost: estimatedCostResponse(item.EstimatedCost),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, ListCompactSessionUsageResponse{Sessions: out})
}

func (c *UsageController) getSession(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions/{sessionId}")
		return
	}
	summary, err := c.Svc.Get(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, sessionUsageResponse(summary))
}

func (c *UsageController) getSessionEffort(w http.ResponseWriter, r *http.Request) {
	if c.Effort == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions/{sessionId}/effort")
		return
	}
	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	effort, mix, found, err := c.Effort.Get(r.Context(), sessionID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !found {
		envelope.WriteError(w, r, apierr.NotFound("SESSION_EFFORT_NOT_FOUND", "No recorded telemetry for this session"))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, sessionEffortResponse(effort, mix))
}

func sessionEffortResponse(effort domain.SessionEffort, mix []domain.ToolMixEntry) SessionEffortResponse {
	toolMix := make([]ToolMixResponse, 0, len(mix))
	for _, entry := range mix {
		toolMix = append(toolMix, ToolMixResponse{
			ToolName: entry.ToolName, Calls: entry.Calls, FailedCalls: entry.FailedCalls,
			TotalDurationMs: entry.TotalDurationMS,
		})
	}
	return SessionEffortResponse{
		Effort: EffortResponse{
			DurationMs: effort.DurationMS, ActiveMs: effort.ActiveMS, IdleMs: effort.IdleMS,
			ToolCalls: effort.ToolCalls, FilesRead: effort.FilesRead, FilesChanged: effort.FilesChanged,
			LinesAdded: effort.LinesAdded, LinesRemoved: effort.LinesRemoved,
			CommandsRun: effort.CommandsRun, TestsRun: effort.TestsRun,
			Compactions: effort.Compactions, TimingAvailable: effort.TimingAvailable,
		},
		ToolMix: toolMix,
	}
}

func sessionUsageResponse(summary domain.SessionUsageSummary) SessionUsageResponse {
	harnesses := make([]UsageHarnessResponse, 0, len(summary.Harnesses))
	for _, harness := range summary.Harnesses {
		models := make([]UsageModelResponse, 0, len(harness.Models))
		for _, model := range harness.Models {
			models = append(models, UsageModelResponse{
				ModelID: model.ModelID, Totals: usageTotalsResponse(model.Totals),
			})
		}
		harnesses = append(harnesses, UsageHarnessResponse{
			Harness: string(harness.Harness), Totals: usageTotalsResponse(harness.Totals), Models: models,
		})
	}
	return SessionUsageResponse{
		SessionID: summary.SessionID, Incomplete: summary.Incomplete,
		Totals: usageTotalsResponse(summary.Totals), Harnesses: harnesses,
	}
}

func usageTotalsResponse(totals domain.UsageMetricTotals) UsageTotalsResponse {
	return UsageTotalsResponse{
		InputTokens: totals.InputTokens, CachedInputTokens: totals.CachedInputTokens,
		UncachedInputTokens: totals.UncachedInputTokens,
		OutputTokens:        totals.OutputTokens, ProcessedTokens: totals.ProcessedTokens,
		CacheReadTokens: totals.CachedInputTokens,
		EstimatedCost:   estimatedCostResponse(totals.EstimatedCost),
	}
}

// SessionEffortResponse is the effort and tool-mix read model. Every nullable
// millisecond field is a pointer: null means AO could not measure it.
type SessionEffortResponse struct {
	Effort  EffortResponse    `json:"effort"`
	ToolMix []ToolMixResponse `json:"toolMix"`
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

func estimatedCostResponse(cost *domain.EstimatedCost) *EstimatedCostResponse {
	if cost == nil {
		return nil
	}
	return &EstimatedCostResponse{
		TotalNanos: cost.TotalNanos, InputNanos: cost.InputNanos,
		CachedInputNanos: cost.CachedInputNanos, OutputNanos: cost.OutputNanos,
		Coverage:            string(cost.Coverage),
		ProviderAttribution: string(cost.ProviderAttribution),
	}
}

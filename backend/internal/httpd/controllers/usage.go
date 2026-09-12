package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scoring"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
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

// ScorecardService is the controller-facing efficiency-scorecard read
// contract.
type ScorecardService interface {
	Get(context.Context, domain.SessionID) (scoring.Scorecard, error)
}

// RollupService is the controller-facing orchestrator-rail read contract.
type RollupService interface {
	ProjectUsageRollup(context.Context, domain.ProjectID) (domain.ProjectUsageRollup, error)
}

// UsageController owns compact dashboard usage routes.
type UsageController struct {
	Svc       UsageSummaryService
	Effort    EffortService
	Scorecard ScorecardService
	Rollup    RollupService
}

// Register mounts usage routes on the supplied router.
func (c *UsageController) Register(r chi.Router) {
	r.Get("/usage/sessions", c.listSessions)
	r.Get("/usage/sessions/{sessionId}", c.getSession)
	r.Get("/usage/sessions/{sessionId}/effort", c.getSessionEffort)
	r.Get("/usage/projects/{projectId}/rollup", c.getProjectRollup)
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
	var card scoring.Scorecard
	if c.Scorecard != nil {
		card, err = c.Scorecard.Get(r.Context(), sessionID)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
	}
	envelope.WriteJSON(w, http.StatusOK, sessionEffortResponse(effort, mix, card))
}

func (c *UsageController) getProjectRollup(w http.ResponseWriter, r *http.Request) {
	if c.Rollup == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/projects/{projectId}/rollup")
		return
	}
	projectID := domain.ProjectID(chi.URLParam(r, "projectId"))
	rollup, err := c.Rollup.ProjectUsageRollup(r.Context(), projectID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, projectUsageRollupResponse(rollup))
}

func projectUsageRollupResponse(rollup domain.ProjectUsageRollup) ProjectUsageRollupResponse {
	efficiency := usage.DeriveOrchestratorEfficiency(rollup)
	workers := make([]WorkerUsageRowResponse, 0, len(rollup.Workers))
	for _, worker := range rollup.Workers {
		workers = append(workers, WorkerUsageRowResponse{
			DurationMs: worker.DurationMS, EstimatedCost: estimatedCostResponse(worker.EstimatedCost),
			Harness: string(worker.Harness), Measured: worker.Measured, ModelID: worker.ModelID,
			Outcome: string(worker.Outcome), SessionID: worker.SessionID, Title: worker.Title,
		})
	}
	return ProjectUsageRollupResponse{
		Efficiency: OrchestratorEfficiencyResponse{
			CostPerMergedPrNanos: efficiency.CostPerMergedPRNanos,
			IsLowerBound:         efficiency.IsLowerBound,
			OrchestratorShare:    efficiency.OrchestratorShare,
			WorkerShare:          efficiency.WorkerShare,
		},
		MergedPrs:               rollup.MergedPRs,
		OrchestratorGenerations: rollup.OrchestratorGenerations,
		OrchestratorTotals:      usageTotalsResponse(rollup.OrchestratorTotals),
		ProjectID:               rollup.ProjectID,
		UnmeasuredSessions:      rollup.UnmeasuredSessions,
		WorkerTotals:            usageTotalsResponse(rollup.WorkerTotals),
		Workers:                 workers,
	}
}

// ProjectUsageRollupResponse is the orchestrator-rail read model for one
// project. orchestratorTotals and workerTotals are stated lower bounds
// whenever unmeasuredSessions is nonzero: that count spans sessions of either
// kind, so it taints both sides rather than the workers list alone.
type ProjectUsageRollupResponse struct {
	Efficiency              OrchestratorEfficiencyResponse `json:"efficiency"`
	MergedPrs               int64                          `json:"mergedPrs" minimum:"0" description:"Merged pull requests attributed to this project. Only ever a divisor for costPerMergedPrNanos."`
	OrchestratorGenerations int64                          `json:"orchestratorGenerations" minimum:"0" description:"How many orchestrator sessions the project has had, so a lifetime total is not mistaken for the current orchestrator's own spend."`
	OrchestratorTotals      UsageTotalsResponse            `json:"orchestratorTotals"`
	ProjectID               domain.ProjectID               `json:"projectId"`
	UnmeasuredSessions      int64                          `json:"unmeasuredSessions" minimum:"0" description:"Sessions of either kind whose usage AO could not observe. Nonzero makes both totals lower bounds."`
	WorkerTotals            UsageTotalsResponse            `json:"workerTotals"`
	Workers                 []WorkerUsageRowResponse       `json:"workers"`
}

// OrchestratorEfficiencyResponse is the derived spend split and delivery cost.
// Every ratio is null rather than zero when the evidence does not support it.
type OrchestratorEfficiencyResponse struct {
	CostPerMergedPrNanos *int64   `json:"costPerMergedPrNanos" minimum:"0" format:"int64" description:"Combined spend divided by merged PRs. Null with no merged PRs to divide by, which is distinct from a real zero."`
	IsLowerBound         bool     `json:"isLowerBound" description:"True when the underlying spend is known to be understated: unmeasured sessions, or partial cost coverage on either side."`
	OrchestratorShare    *float64 `json:"orchestratorShare" minimum:"0" maximum:"1" description:"Orchestrator fraction of combined spend. Null when there is no measured basis for a share."`
	WorkerShare          *float64 `json:"workerShare" minimum:"0" maximum:"1" description:"Worker fraction of combined spend. Null when there is no measured basis for a share."`
}

// WorkerUsageRowResponse is one worker's line in the roll-up. measured is
// false when AO had no certified usage source: the cost is unknown, not zero,
// and estimatedCost is then null.
type WorkerUsageRowResponse struct {
	DurationMs    *int64                 `json:"durationMs" minimum:"0" format:"int64" description:"Wall-clock duration. Null when AO could not measure it."`
	EstimatedCost *EstimatedCostResponse `json:"estimatedCost"`
	Harness       string                 `json:"harness"`
	Measured      bool                   `json:"measured" description:"Whether AO had a certified usage source for this session."`
	ModelID       string                 `json:"modelId"`
	Outcome       string                 `json:"outcome" enum:"active,merged,abandoned,failed"`
	SessionID     domain.SessionID       `json:"sessionId"`
	Title         string                 `json:"title"`
}

func sessionEffortResponse(
	effort domain.SessionEffort, mix []domain.ToolMixEntry, card scoring.Scorecard,
) SessionEffortResponse {
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
		ToolMix:   toolMix,
		Scorecard: scorecardResponse(card),
	}
}

func scorecardResponse(card scoring.Scorecard) ScorecardResponse {
	factors := make([]FactorScoreResponse, 0, len(card.Factors))
	for _, factor := range card.Factors {
		evidence := make(map[string]float64, len(factor.Evidence))
		for key, value := range factor.Evidence {
			evidence[key] = value
		}
		factors = append(factors, FactorScoreResponse{
			AbsentReason: factor.AbsentReason, Evidence: evidence,
			Factor: string(factor.Factor), Present: factor.Present, Score: factor.Score,
		})
	}
	return ScorecardResponse{
		Factors: factors, Overall: card.Overall, RubricVersion: card.RubricVersion,
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
	Effort    EffortResponse    `json:"effort"`
	ToolMix   []ToolMixResponse `json:"toolMix"`
	Scorecard ScorecardResponse `json:"scorecard"`
}

// ScorecardResponse is the efficiency scorecard. Every factor is listed even
// when absent, so the UI can state what could not be measured.
type ScorecardResponse struct {
	Factors       []FactorScoreResponse `json:"factors"`
	Overall       *int                  `json:"overall"`
	RubricVersion string                `json:"rubricVersion"`
}

// FactorScoreResponse is one factor. Score is meaningless when present is
// false; absentReason then says what was missing.
type FactorScoreResponse struct {
	AbsentReason string             `json:"absentReason"`
	Evidence     map[string]float64 `json:"evidence"`
	Factor       string             `json:"factor"`
	Present      bool               `json:"present"`
	Score        int                `json:"score"`
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

import { useTranslation } from "react-i18next";
import { InspectorSection as Section, inspectorEmptyClass } from "@aoagents/product-ui";
import { AgentAvatar } from "./AgentAvatar";
import {
	formatHarnessName,
	formatModelName,
	formatTelemetryTokenValue,
} from "./SessionInspectorMetrics";
import { useProjectUsageRollup, type ProjectWorkerUsageRow } from "../hooks/useProjectUsageRollup";
import { useSessionEffort } from "../hooks/useSessionEffort";
import { useSessionUsage } from "../hooks/useSessionUsage";
import { formatCostNanos, formatEstimatedCost, type EstimatedCost } from "../lib/format-cost";
import { formatDurationMs } from "../lib/format-time";
import type { MessageKey } from "../i18n";
import type { WorkspaceSession } from "../types/workspace";

const outcomeLabelKeys: Record<ProjectWorkerUsageRow["outcome"], MessageKey> = {
	abandoned: "inspector.metrics.rollup.outcome.abandoned",
	active: "inspector.metrics.rollup.outcome.active",
	failed: "inspector.metrics.rollup.outcome.failed",
	merged: "inspector.metrics.rollup.outcome.merged",
};

const outcomeOrder: ProjectWorkerUsageRow["outcome"][] = ["active", "merged", "failed", "abandoned"];

/**
 * Project-scoped cost roll-up for an orchestrator session.
 *
 * Two separations carry the whole panel. The first is temporal: the roll-up's
 * `orchestratorTotals` is a project-lifetime sum across every orchestrator the
 * project has had, so it can never be printed as "this orchestrator". This
 * session's own figures come from its own usage/effort instead, and the
 * generations line names how many orchestrators the project total spans.
 *
 * The second is unknown-versus-zero. An unmeasured session makes both totals
 * lower bounds, so `isLowerBound` is rendered inside the spend split — not only
 * beside cost per merged PR — because an unknown orchestrator-side cost would
 * otherwise print as a precise-looking 0% share. Cost per merged PR divides by
 * merged PRs only and the backend sends null with none, which stays "no merged
 * pull requests yet" rather than $0.00.
 */
export function OrchestratorMetrics({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const projectId = session.workspaceId;
	const rollupQuery = useProjectUsageRollup(projectId, true);
	const ownUsageQuery = useSessionUsage(session.id, true);
	const ownEffortQuery = useSessionEffort(session.id, true);
	const rollup = rollupQuery.data;

	if (rollupQuery.isLoading) {
		return (
			<div role="tabpanel">
				<Section title={t("inspector.metrics")}>
					<p className={inspectorEmptyClass}>{t("inspector.metrics.loading")}</p>
				</Section>
			</div>
		);
	}
	if (rollupQuery.isError || !rollup) {
		return (
			<div role="tabpanel">
				<Section title={t("inspector.metrics")}>
					<p className={inspectorEmptyClass}>{t("inspector.metrics.rollup.error")}</p>
				</Section>
			</div>
		);
	}

	const ownTotals = ownUsageQuery.data?.totals;
	const ownDurationMs = ownEffortQuery.data?.effort.durationMs ?? null;
	const workers = rollup.workers;

	return (
		<div role="tabpanel">
			<Section title={t("inspector.metrics.rollup.thisOrchestrator")}>
				<dl className="grid grid-cols-2 gap-1.5">
					<Stat
						label={t("inspector.usage.estimatedCost")}
						testId="rollup-this-orchestrator-cost"
						value={formatEstimatedCost(ownTotals?.estimatedCost)}
					/>
					<Stat
						label={t("inspector.usage.processedTokens")}
						value={formatTokens(ownTotals?.processedTokens)}
					/>
					<Stat
						label={t("inspector.metrics.effort.duration")}
						value={ownDurationMs === null ? null : formatDurationMs(ownDurationMs)}
					/>
				</dl>
				{/* Says out loud that the figures below are not this session's, so
				    the project total is never read as this orchestrator's bill. */}
				<p className={`mt-1.5 ${inspectorEmptyClass}`}>{t("inspector.metrics.rollup.thisOrchestratorNote")}</p>
			</Section>

			<Section title={t("inspector.metrics.rollup.orchestratorAllTime")}>
				<dl className="grid grid-cols-2 gap-1.5">
					<Stat
						label={t("inspector.usage.estimatedCost")}
						testId="rollup-orchestrator-alltime-cost"
						value={formatEstimatedCost(rollup.orchestratorTotals.estimatedCost)}
					/>
					<Stat
						label={t("inspector.usage.processedTokens")}
						value={formatTokens(rollup.orchestratorTotals.processedTokens)}
					/>
				</dl>
			</Section>

			<Section title={t("inspector.metrics.rollup.workers")}>
				<dl className="grid grid-cols-2 gap-1.5">
					<Stat
						label={t("inspector.usage.estimatedCost")}
						testId="rollup-worker-cost"
						value={formatEstimatedCost(rollup.workerTotals.estimatedCost)}
					/>
					<Stat
						label={t("inspector.usage.processedTokens")}
						value={formatTokens(rollup.workerTotals.processedTokens)}
					/>
				</dl>
				<p className={`mt-1.5 ${inspectorEmptyClass}`}>
					{t("inspector.metrics.rollup.workerCount", { count: workers.length })}
				</p>
				<ul className="mt-1 flex flex-col gap-0.5">
					{outcomeOrder.map((outcome) => (
						<li className="flex items-baseline justify-between gap-2" key={outcome}>
							<span className="truncate text-2xs text-settings-muted">{t(outcomeLabelKeys[outcome])}</span>
							<span className="shrink-0 font-mono text-sm-md text-settings-label" data-testid={`rollup-outcome-${outcome}`}>
								{workers.filter((worker) => worker.outcome === outcome).length}
							</span>
						</li>
					))}
				</ul>
			</Section>

			<div data-testid="rollup-split">
				<Section title={t("inspector.metrics.rollup.split")}>
					{rollup.efficiency.orchestratorShare === null || rollup.efficiency.workerShare === null ? (
						<p className={inspectorEmptyClass}>{t("inspector.metrics.rollup.splitUnavailable")}</p>
					) : (
						<dl className="grid grid-cols-2 gap-1.5">
							<Stat
								label={t("inspector.metrics.rollup.splitOrchestrator")}
								testId="rollup-split-orchestrator"
								value={formatShare(rollup.efficiency.orchestratorShare)}
							/>
							<Stat
								label={t("inspector.metrics.rollup.splitWorkers")}
								testId="rollup-split-workers"
								value={formatShare(rollup.efficiency.workerShare)}
							/>
						</dl>
					)}
					{/* The caveat lives here rather than only beside cost per PR: a
					    share is a ratio of two sums, so an unmeasured session can
					    render as a misleadingly precise 0%. */}
					{rollup.efficiency.isLowerBound ? (
						<p className={`mt-1.5 ${inspectorEmptyClass}`} data-testid="rollup-lower-bound">
							{t("inspector.metrics.rollup.lowerBound", { count: rollup.unmeasuredSessions })}
						</p>
					) : null}
				</Section>
			</div>

			<div data-testid="rollup-cost-per-merged-pr">
				<Section title={t("inspector.metrics.rollup.costPerMergedPr")}>
					{rollup.efficiency.costPerMergedPrNanos === null ? (
						// Null here means "no merged PRs to divide by". Never $0.00.
						<p className={inspectorEmptyClass}>{t("inspector.metrics.rollup.costPerMergedPrUnavailable")}</p>
					) : (
						<dl className="grid grid-cols-2 gap-1.5">
							<Stat
								label={t("inspector.metrics.rollup.costPerMergedPr")}
								testId="rollup-cost-per-merged-pr-value"
								value={formatCostNanos(rollup.efficiency.costPerMergedPrNanos)}
							/>
							<Stat label={t("inspector.metrics.rollup.mergedPrs")} value={String(rollup.mergedPrs)} />
						</dl>
					)}
				</Section>
			</div>

			<Section title={t("inspector.metrics.rollup.workerList")}>
				{workers.length === 0 ? (
					<p className={inspectorEmptyClass}>{t("inspector.metrics.rollup.empty")}</p>
				) : (
					<ul className="flex flex-col gap-1">
						{workers.map((worker) => (
							<WorkerRow key={worker.sessionId} worker={worker} />
						))}
					</ul>
				)}
			</Section>

			<p className={`px-1 ${inspectorEmptyClass}`} data-testid="rollup-generations">
				{t("inspector.metrics.rollup.generations", { count: rollup.orchestratorGenerations })}
			</p>
		</div>
	);
}

function WorkerRow({ worker }: { worker: ProjectWorkerUsageRow }) {
	const { t } = useTranslation();
	const cost = formatEstimatedCost(worker.estimatedCost as EstimatedCost | null);
	const modelName = worker.modelId ? formatModelName(worker.modelId) : null;
	return (
		<li className="flex flex-col gap-0.5" data-testid="rollup-worker-row">
			<div className="flex items-baseline justify-between gap-2">
				<span className="truncate font-medium text-settings-label" title={worker.title}>
					{worker.title}
				</span>
				<span className="shrink-0 font-mono text-2xs text-settings-label">
					{/* An unmeasured worker has no cost to show. Saying "not
					    measurable" beside a priced neighbour keeps the gap from
					    reading as a free session. */}
					{worker.measured && cost ? cost : t("inspector.metrics.rollup.unmeasured")}
				</span>
			</div>
			<span className="flex min-w-0 items-center gap-1 text-2xs text-settings-muted">
				<AgentAvatar className="size-3.5 shrink-0" decorative provider={worker.harness} />
				<span className="truncate">
					{[
						formatHarnessName(worker.harness),
						modelName,
						worker.durationMs === null ? null : formatDurationMs(worker.durationMs),
						t(outcomeLabelKeys[worker.outcome]),
					]
						.filter(Boolean)
						.join(" · ")}
				</span>
			</span>
		</li>
	);
}

function Stat({ label, testId, value }: { label: string; testId?: string; value: string | null }) {
	const { t } = useTranslation();
	const unavailable = t("inspector.usage.metricUnavailable", { label });
	return (
		<div className="min-w-0">
			<dt className="truncate text-2xs text-settings-muted">{label}</dt>
			<dd
				aria-label={value === null ? unavailable : undefined}
				className="mt-0.5 truncate font-mono text-sm-md text-settings-label"
				data-testid={testId}
				title={value === null ? unavailable : undefined}
			>
				{value ?? t("usage.unavailable")}
			</dd>
		</div>
	);
}

function formatTokens(processedTokens: number | null | undefined): string | null {
	if (typeof processedTokens !== "number" || !Number.isFinite(processedTokens)) return null;
	return formatTelemetryTokenValue(processedTokens);
}

// Shares arrive as fractions of combined spend. Null is handled by the caller —
// it means "no measured basis", which is not the same as a real 0%.
function formatShare(share: number): string {
	return `${Math.round(share * 100)}%`;
}

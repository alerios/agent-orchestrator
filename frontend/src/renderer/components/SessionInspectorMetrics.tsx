import { useTranslation } from "react-i18next";
import { useId, useState, type ReactNode } from "react";
import { InspectorSection as Section, inspectorEmptyClass } from "@aoagents/product-ui";
import { ChevronDown, ChevronRight, Info } from "lucide-react";
import { AgentAvatar } from "./AgentAvatar";
import { ContextMeter } from "./chat/ContextMeter";
import { useConversation } from "../hooks/useConversation";
import { useSessionEffort, type SessionEffort } from "../hooks/useSessionEffort";
import { useSessionUsage, type SessionUsage } from "../hooks/useSessionUsage";
import { formatEstimatedCost, type EstimatedCost } from "../lib/format-cost";
import { formatDurationMs } from "../lib/format-time";
import { formatTokenCount } from "../lib/format-token-count";
import type { MessageKey } from "../i18n";
import type { ConversationRateLimits, ConversationUsage } from "../types/conversation";
import type { WorkspaceSession } from "../types/workspace";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";

export function MetricsView({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	// Always fetch: the usage block is no longer developer-gated.
	const usageQuery = useSessionUsage(session.id, true);
	const usage = usageQuery.data;
	const hasUsage = hasMeaningfulSessionUsage(usage);
	const effortQuery = useSessionEffort(session.id, true);
	const effortData = effortQuery.data;
	const { snapshot } = useConversation(session.id);
	return (
		<TooltipProvider>
			<div role="tabpanel">
				{usageQuery.isLoading ? (
					<Section title={t("inspector.usage.title")}>
						<p className={inspectorEmptyClass}>{t("inspector.metrics.loading")}</p>
					</Section>
				) : usageQuery.isError ? (
					<Section title={t("inspector.usage.title")}>
						<p className={inspectorEmptyClass}>{t("inspector.usage.processedTokensUnavailable")}</p>
					</Section>
				) : hasUsage && usage ? (
					<Section title={t("inspector.usage.title")}>
						<UsageCostTelemetry usage={usage} />
					</Section>
				) : (
					<Section title={t("inspector.metrics")}>
						<p className={inspectorEmptyClass}>{t("inspector.metrics.noData")}</p>
					</Section>
				)}
				{effortData ? (
					<ContextHealthBlock
						compactions={effortData.effort.compactions}
						rateLimits={snapshot?.rateLimits}
						usage={snapshot?.usage}
					/>
				) : null}
				{effortQuery.isLoading ? null : effortQuery.isError ? (
					<Section title={t("inspector.metrics.effort.title")}>
						<p className={inspectorEmptyClass}>{t("inspector.metrics.effort.noData")}</p>
					</Section>
				) : effortData ? (
					<>
						<EffortBlock effort={effortData.effort} />
						<ToolMixBlock mix={effortData.toolMix} timingAvailable={effortData.effort.timingAvailable} />
						{effortData.scorecard ? <ScoresBlock scorecard={effortData.scorecard} /> : null}
					</>
				) : null}
				{hasUsage && usage ? (
					<Section title={t("inspector.metrics.coverage.title")}>
						<p className={inspectorEmptyClass}>
							{usage.incomplete
								? t("inspector.metrics.coverage.partial")
								: t("inspector.metrics.coverage.full")}
						</p>
					</Section>
				) : null}
			</div>
		</TooltipProvider>
	);
}

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
				<div className="min-w-0">
					<dt className="truncate text-2xs text-settings-muted">{t("inspector.metrics.effort.linesChanged")}</dt>
					<dd className="mt-0.5 truncate font-mono text-sm-md text-settings-label" data-testid="effort-lines-changed-value">
						<span className="text-success">+{effort.linesAdded}</span>{" "}
						<span className="text-destructive">-{effort.linesRemoved}</span>
					</dd>
				</div>
				<EffortStat labelKey="inspector.metrics.effort.commandsRun" value={String(effort.commandsRun)} />
				<div className="min-w-0">
					<dt className="flex items-center gap-1 truncate text-2xs text-settings-muted">
						{t("inspector.metrics.effort.testsRun")}
						<Tooltip>
							<TooltipTrigger asChild>
								<button
									aria-label={t("inspector.metrics.effort.testsRunNote")}
									className="rounded-sm text-settings-muted outline-none transition-colors hover:text-settings-label focus-visible:ring-1 focus-visible:ring-ring"
									type="button"
								>
									<Info aria-hidden="true" className="size-3" />
								</button>
							</TooltipTrigger>
							<TooltipContent className="max-w-64 text-left" side="top">
								<p>{t("inspector.metrics.effort.testsRunNote")}</p>
							</TooltipContent>
						</Tooltip>
					</dt>
					<dd className="mt-0.5 truncate font-mono text-sm-md text-settings-label">{effort.testsRun}</dd>
				</div>
				<EffortStat labelKey="inspector.metrics.effort.compactions" value={String(effort.compactions)} />
			</dl>
			{effort.timingAvailable ? null : (
				<p className={inspectorEmptyClass}>{t("inspector.metrics.effort.timingUnavailable")}</p>
			)}
		</Section>
	);
}

function EffortStat({ labelKey, testId, value }: { labelKey: MessageKey; testId?: string; value: string }) {
	const { t } = useTranslation();
	return (
		<div className="min-w-0">
			<dt className="truncate text-2xs text-settings-muted">{t(labelKey)}</dt>
			<dd className="mt-0.5 truncate font-mono text-sm-md text-settings-label" data-testid={testId}>
				{value}
			</dd>
		</div>
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

const scoreFactorLabelKeys: Record<string, MessageKey> = {
	delivery_efficiency: "inspector.metrics.scores.factor.delivery_efficiency",
	exploratory_overhead: "inspector.metrics.scores.factor.exploratory_overhead",
	governance: "inspector.metrics.scores.factor.governance",
	steering_load: "inspector.metrics.scores.factor.steering_load",
	token_efficiency: "inspector.metrics.scores.factor.token_efficiency",
};

function ScoresBlock({ scorecard }: { scorecard: SessionEffort["scorecard"] }) {
	const { t } = useTranslation();
	if (scorecard.factors.length === 0) {
		return (
			<Section title={t("inspector.metrics.scores.title")}>
				<p className={inspectorEmptyClass}>{t("inspector.metrics.scores.noScores")}</p>
			</Section>
		);
	}
	return (
		<Section title={t("inspector.metrics.scores.title")}>
			<div className="mb-1.5 flex items-baseline justify-between gap-2">
				<span className="text-2xs text-settings-muted">{t("inspector.metrics.scores.overall")}</span>
				<span className="font-semibold">{scorecard.overall === null ? "—" : scorecard.overall}</span>
			</div>
			{scorecard.overall === null ? (
				<p className={inspectorEmptyClass}>{t("inspector.metrics.scores.overallUnavailable")}</p>
			) : null}
			<ul className="flex flex-col gap-1.5">
				{scorecard.factors.map((factor) => (
					<li className="flex flex-col gap-0.5" key={factor.factor}>
						<div className="flex items-baseline justify-between gap-2">
							<span className="truncate">
								{scoreFactorLabelKeys[factor.factor] ? t(scoreFactorLabelKeys[factor.factor]) : factor.factor}
							</span>
							{factor.present ? (
								<span className="shrink-0 font-semibold" data-testid={`score-value-${factor.factor}`}>
									{factor.score}
								</span>
							) : null}
						</div>
						{factor.present ? (
							// The evidence is not optional detail: it is what makes the
							// score auditable.
							<span className="text-2xs text-settings-muted" data-testid={`score-evidence-${factor.factor}`}>
								{Object.entries(factor.evidence ?? {})
									.map(([name, value]) => `${name} ${formatEvidenceValue(value)}`)
									.join(" · ")}
							</span>
						) : (
							<span className={inspectorEmptyClass}>
								{t("inspector.metrics.scores.unavailable", { reason: factor.absentReason })}
							</span>
						)}
					</li>
				))}
			</ul>
			<p className={`mt-1.5 ${inspectorEmptyClass}`}>
				{t("inspector.metrics.scores.rubric", { version: scorecard.rubricVersion })}
			</p>
		</Section>
	);
}

function formatEvidenceValue(value: number): string {
	if (Number.isInteger(value)) return String(value);
	return value.toFixed(2);
}

function UsageCostTelemetry({ usage }: { usage: SessionUsage }) {
	const { t } = useTranslation();
	const processedTokens = usageProcessedTokens(usage.totals);
	const exactProcessed = processedTokens?.toLocaleString("en-US");
	const estimatedCost = formatEstimatedCost(usage.totals.estimatedCost);
	const showsAgentCost = usage.harnesses.some((harness) => harness.totals.estimatedCost !== null);

	return (
		<div>
			<div className="grid grid-cols-2 gap-4">
				<div className="min-w-0">
					<p className="text-2xs text-settings-muted">{t("inspector.usage.processedTokens")}</p>
					<p
						aria-label={
							processedTokens === null
								? t("inspector.usage.processedTokensUnavailable")
								: t("inspector.usage.processedTokensAria", { count: exactProcessed })
						}
						className="mt-0.5 truncate font-mono text-md-sm font-medium text-settings-label"
						title={processedTokens === null ? undefined : t("inspector.usage.processedTokensAria", { count: exactProcessed })}
					>
						{processedTokens === null ? t("inspector.usage.noUsageYet") : formatTelemetryTokenValue(processedTokens)}
					</p>
				</div>
				<div className="min-w-0 text-right">
					<div className="flex items-center justify-end gap-1">
						<p className="text-2xs text-settings-muted">{t("inspector.usage.estimatedCost")}</p>
						<EstimatedCostInfo cost={usage.totals.estimatedCost} />
					</div>
					<p className="mt-0.5 truncate font-mono text-sm-md font-medium text-settings-label">
						{estimatedCost ?? t("usage.unavailable")}
					</p>
				</div>
			</div>

			<div className="mt-3">
				<div className="rounded-lg border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-2.5 py-2.5">
					<UsageMetrics totals={usage.totals} />
				</div>
			</div>

			{usage.harnesses.length === 1 ? (
				<UsageAgentAttribution harness={usage.harnesses[0]} />
			) : usage.harnesses.length > 1 ? (
				<div className="mt-2 border-t border-(--color-border-settings-input) pt-1.5">
					<div
						className={`grid ${usageRowColumns(showsAgentCost)} items-center gap-2 px-1 pb-0.5 text-2xs text-settings-muted`}
					>
						<span>{t("inspector.usage.agent")}</span>
						<span className="text-right">{t("inspector.usage.tokens")}</span>
						{showsAgentCost ? <span className="text-right">{t("inspector.usage.cost")}</span> : null}
					</div>
					{usage.harnesses.map((harness, index) => (
						<UsageProviderRow
							harness={harness}
							key={`${harness.harness}:${index}`}
							showCost={showsAgentCost}
						/>
					))}
				</div>
			) : null}
		</div>
	);
}

function UsageAgentAttribution({ harness }: { harness: SessionUsage["harnesses"][number] }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const detailID = useId();
	const harnessName = formatHarnessName(harness.harness);
	const canExpand = harness.models.length > 1;
	const modelSummary =
		harness.models.length === 1
			? formatModelName(harness.models[0].modelId)
			: harness.models.length > 1
				? t("inspector.usage.models", { count: harness.models.length })
				: null;
	const modelSummaryTitle = harness.models.length === 1 ? harness.models[0].modelId : modelSummary;
	const attribution = (
		<>
			<AgentAvatar className="size-4" decorative provider={harness.harness} />
			<span className="shrink-0 text-sm-md text-settings-label">{harnessName}</span>
			{modelSummary ? (
				<>
					<span aria-hidden="true" className="text-settings-muted">
						·
					</span>
					<span className="truncate text-2xs text-settings-muted" title={modelSummaryTitle ?? undefined}>
						{modelSummary}
					</span>
				</>
			) : null}
		</>
	);

	return (
		<div className="mt-2 border-t border-(--color-border-settings-input) pt-1.5">
			{canExpand ? (
				<>
					<button
						aria-controls={detailID}
						aria-expanded={open}
						aria-label={t("inspector.usage.providerDetails", { name: harnessName })}
						className="flex w-full min-w-0 items-center gap-1.5 rounded-md px-1 py-0.5 text-left outline-none transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring"
						onClick={() => setOpen((current) => !current)}
						type="button"
					>
						{open ? (
							<ChevronDown aria-hidden="true" className="size-3 shrink-0 text-settings-muted" />
						) : (
							<ChevronRight aria-hidden="true" className="size-3 shrink-0 text-settings-muted" />
						)}
						{attribution}
					</button>
					{open ? (
						<div
							aria-label={t("inspector.usage.providerPeek", { name: harnessName })}
							className="mx-1 my-0.5 border-l border-(--color-border-settings-input) py-0.5 pl-2"
							id={detailID}
							role="region"
						>
							<ProviderUsageDetails harness={harness} />
						</div>
					) : null}
				</>
			) : (
				<div className="flex min-w-0 items-center gap-1.5 px-1 py-0.5">{attribution}</div>
			)}
		</div>
	);
}

function UsageProviderRow({
	harness,
	showCost,
}: {
	harness: SessionUsage["harnesses"][number];
	showCost: boolean;
}) {
	const { t } = useTranslation();
	const harnessName = formatHarnessName(harness.harness);

	return (
		<UsageDisclosureRow
			detailsLabel={t("inspector.usage.providerDetails", { name: harnessName })}
			icon={<AgentAvatar className="size-4" decorative provider={harness.harness} />}
			name={harnessName}
			nameClassName="text-sm-md"
			regionLabel={t("inspector.usage.providerPeek", { name: harnessName })}
			showCost={showCost}
			totals={harness.totals}
		>
			<ProviderUsageDetails harness={harness} />
		</UsageDisclosureRow>
	);
}

function ProviderUsageDetails({ harness }: { harness: SessionUsage["harnesses"][number] }) {
	const { t } = useTranslation();
	const showCost = harness.models.some((model) => model.totals.estimatedCost !== null);

	return (
		<div>
			{harness.models.length > 0 ? (
				harness.models.map((model, index) => (
					<UsageModelRow
						key={`${model.modelId}:${index}`}
						model={model}
						showCost={showCost}
					/>
				))
			) : (
				<p className="px-1 py-1 text-2xs text-settings-muted">{t("inspector.usage.noModelTelemetry")}</p>
			)}
		</div>
	);
}

function UsageModelRow({
	model,
	showCost,
}: {
	model: SessionUsage["harnesses"][number]["models"][number];
	showCost: boolean;
}) {
	const { t } = useTranslation();
	const modelName = formatModelName(model.modelId);

	return (
		<UsageDisclosureRow
			detailsLabel={t("inspector.usage.modelDetails", { name: modelName })}
			name={modelName}
			nameClassName="text-2xs"
			nameTitle={model.modelId}
			regionLabel={t("inspector.usage.modelPeek", { name: modelName })}
			showCost={showCost}
			totals={model.totals}
		>
			<UsageMetrics totals={model.totals} />
		</UsageDisclosureRow>
	);
}

// usageRowColumns keeps the disclosure rows aligned with their header. The cost
// column is dropped entirely when no row in the list has an estimate, so an
// install without pricing shows no empty column at all.
function usageRowColumns(showCost: boolean): string {
	return showCost ? "grid-cols-[minmax(0,1fr)_4.5rem_5.5rem]" : "grid-cols-[minmax(0,1fr)_4.5rem]";
}

function UsageDisclosureRow({
	children,
	detailsLabel,
	icon,
	name,
	nameClassName,
	nameTitle,
	regionLabel,
	showCost,
	totals,
}: {
	children: ReactNode;
	detailsLabel: string;
	icon?: ReactNode;
	name: string;
	nameClassName: string;
	nameTitle?: string;
	regionLabel: string;
	showCost: boolean;
	totals: SessionUsage["totals"];
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const detailID = useId();
	const processedTokens = usageProcessedTokens(totals);
	const exactProcessed = processedTokens?.toLocaleString("en-US");

	return (
		<div className="px-1 py-0.5">
			<button
				aria-controls={detailID}
				aria-expanded={open}
				aria-label={detailsLabel}
				className={`grid w-full ${usageRowColumns(showCost)} items-center gap-2 rounded-md px-1 py-1 text-left outline-none transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring`}
				onClick={() => setOpen((current) => !current)}
				type="button"
			>
				<span className={`flex min-w-0 items-center gap-1 text-settings-label ${nameClassName}`}>
					{open ? (
						<ChevronDown aria-hidden="true" className="size-3 shrink-0 text-settings-muted" />
					) : (
						<ChevronRight aria-hidden="true" className="size-3 shrink-0 text-settings-muted" />
					)}
					{icon}
					<span className="truncate" title={nameTitle}>{name}</span>
				</span>
				<span
					className="text-right font-mono text-2xs text-settings-label"
					title={processedTokens === null ? undefined : t("inspector.usage.processedTokensAria", { count: exactProcessed })}
				>
					{processedTokens === null ? "—" : formatTelemetryTokenValue(processedTokens)}
				</span>
				{showCost ? <UsageCostValue cost={totals.estimatedCost} /> : null}
			</button>
			{open ? (
				<div
					aria-label={regionLabel}
					className="mx-1 mb-0.5 border-l border-(--color-border-settings-input) py-0.5 pl-2"
					id={detailID}
					role="region"
				>
					{children}
				</div>
			) : null}
		</div>
	);
}

// UsageCostValue renders one row's cost inside a column that some sibling row
// already justified. Once the column is on screen the absence is a real answer
// about that agent, so it says so in words — a dash beside a priced neighbour
// reads as a rendering gap rather than "this one could not be priced".
function UsageCostValue({ cost }: { cost: EstimatedCost | null }) {
	const { t } = useTranslation();
	const value = formatEstimatedCost(cost);
	const label = value ?? t("inspector.usage.metricUnavailable", { label: t("inspector.usage.cost") });
	return (
		<span aria-label={label} className="text-right font-mono text-2xs text-settings-label" title={label}>
			{value ?? t("usage.unavailable")}
		</span>
	);
}

/**
 * Contextual disclosure for the estimated-cost heading.
 *
 * Coverage never reaches the presented value as a qualifier, so this is where a
 * partial estimate says so — in words, next to the heading, rather than as a `≥`
 * the reader has to decode. Hover and keyboard focus both open it.
 */
function EstimatedCostInfo({ cost }: { cost: EstimatedCost | null }) {
	const { t } = useTranslation();
	const label = t("usage.estimatedCostInfoLabel");
	const providerInfoKey = cost?.providerAttribution === "inferred"
		? "usage.estimatedCostInfoInferred"
		: cost?.providerAttribution === "mixed"
			? "usage.estimatedCostInfoMixed"
			: "usage.estimatedCostInfo";
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={label}
					className="rounded-sm text-settings-muted outline-none transition-colors hover:text-settings-label focus-visible:ring-1 focus-visible:ring-ring"
					type="button"
				>
					<Info aria-hidden="true" className="size-3" />
				</button>
			</TooltipTrigger>
			{/* Opens upward: the figure it explains sits directly under the heading,
			    so a downward tooltip covers the very number the reader came for. */}
			<TooltipContent className="max-w-64 text-left" side="top">
				<p>{t(providerInfoKey)}</p>
				{cost?.coverage === "partial" ? (
					<p className="mt-1.5">{t("usage.estimatedCostInfoPartial")}</p>
				) : null}
			</TooltipContent>
		</Tooltip>
	);
}

function UsageMetrics({ totals }: { totals: SessionUsage["totals"] }) {
	const { t } = useTranslation();
	const cacheHitRate = formatCacheHitRate(totals.cachedInputTokens, totals.inputTokens);
	return (
		<dl className="grid grid-cols-2 gap-x-4 gap-y-2 @max-[300px]/inspector:grid-cols-1" data-testid="session-usage-metrics">
			<UsageMetric label={t("inspector.usage.uncachedInputTokens")} metric={totals.uncachedInputTokens} />
			<UsageMetric label={t("inspector.usage.cachedInputTokens")} metric={totals.cachedInputTokens} />
			<UsageMetric label={t("inspector.usage.outputTokens")} metric={totals.outputTokens} />
			<UsageRateMetric rate={cacheHitRate} />
		</dl>
	);
}

function UsageRateMetric({ rate }: { rate: string | null }) {
	const { t } = useTranslation();
	const label = t("inspector.usage.cacheHitRate");
	const description =
		rate === null
			? t("inspector.usage.metricUnavailable", { label })
			: t("inspector.usage.cacheHitRateDescription", { rate });
	return (
		<div className="min-w-0">
			<dt className="truncate text-2xs text-settings-muted">{label}</dt>
			<dd
				aria-label={description}
				className="mt-0.5 truncate font-mono text-sm-md text-settings-label"
				title={description}
			>
				{rate === null ? "—" : `${rate}%`}
			</dd>
		</div>
	);
}

function UsageMetric({ label, metric }: { label: string; metric: number | null | undefined }) {
	const { t } = useTranslation();
	const value = typeof metric === "number" && Number.isFinite(metric) ? metric : null;
	const exactValue = value?.toLocaleString("en-US");
	const accessibleLabel =
		value === null
			? t("inspector.usage.metricUnavailable", { label })
			: t("inspector.usage.metricAria", { label, count: exactValue });
	return (
		<div className="min-w-0">
			<dt className="truncate text-2xs text-settings-muted">{label}</dt>
			<dd
				aria-label={accessibleLabel}
				className="mt-0.5 truncate font-mono text-sm-md text-settings-label"
				title={
					value === null
						? t("inspector.usage.metricUnavailable", { label })
						: t("inspector.usage.tokensExact", { count: exactValue })
				}
			>
				{value === null ? "—" : formatTelemetryTokenValue(value)}
			</dd>
		</div>
	);
}

function formatCacheHitRate(
	cachedInputTokens: number | null | undefined,
	inputTokens: number | null | undefined,
): string | null {
	if (
		typeof cachedInputTokens !== "number" ||
		!Number.isFinite(cachedInputTokens) ||
		typeof inputTokens !== "number" ||
		!Number.isFinite(inputTokens) ||
		inputTokens <= 0
	) {
		return null;
	}
	const percentage = Math.min(100, Math.max(0, (cachedInputTokens / inputTokens) * 100));
	return percentage.toFixed(1).replace(/\.0$/, "");
}

const usageMetricKeys = [
	"processedTokens",
	"inputTokens",
	"cachedInputTokens",
	"uncachedInputTokens",
	"outputTokens",
] as const;

function usageScopes(usage: SessionUsage): SessionUsage["totals"][] {
	return [
		usage.totals,
		...usage.harnesses.flatMap((harness) => [
			harness.totals,
			...harness.models.map((model) => model.totals),
		]),
	];
}

function hasMeaningfulSessionUsage(usage?: SessionUsage): usage is SessionUsage {
	if (!usage) return false;
	return usageScopes(usage).some((totals) =>
		totals.estimatedCost !== null || usageMetricKeys.some((key) => (totals[key] ?? 0) > 0),
	);
}

export function formatTelemetryTokenValue(totalTokens: number): string {
	return formatTokenCount(totalTokens).replace(/ tok$/, "");
}

function usageProcessedTokens(totals: SessionUsage["totals"]): number | null {
	return totals.processedTokens;
}

export function formatHarnessName(harness: string): string {
	const knownNames: Record<string, string> = {
		"claude-code": "Claude",
		claude: "Claude",
		codex: "Codex",
		glm: "GLM",
		kimi: "Kimi",
	};
	if (knownNames[harness]) return knownNames[harness];
	return harness
		.split(/[-_]/)
		.filter(Boolean)
		.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
		.join(" ");
}

// The billing provider stays out of the display name: the row already sits
// under its agent, so the prefix only repeats context the reader has. The exact
// model id remains available as the title.
export function formatModelName(modelID: string): string {
	let parts = modelID.trim().split(/[-_]+/).filter(Boolean);
	const isClaude = parts[0]?.toLowerCase() === "claude";
	if (isClaude) {
		parts = parts.slice(1);
		if (/^\d{8}$/.test(parts.at(-1) ?? "")) parts = parts.slice(0, -1);
		const familyIndex = parts.findIndex((part) => ["haiku", "sonnet", "opus"].includes(part.toLowerCase()));
		if (familyIndex >= 0) {
			const family = parts[familyIndex];
			parts = [family, ...parts.slice(0, familyIndex), ...parts.slice(familyIndex + 1)];
		}
	}

	const formatted: string[] = [];
	for (let index = 0; index < parts.length; index += 1) {
		const part = parts[index];
		const next = parts[index + 1];
		if (/^\d+$/.test(part) && /^\d+$/.test(next ?? "")) {
			formatted.push(`${part}.${next}`);
			index += 1;
			continue;
		}
		const normalized = part.toLowerCase();
		formatted.push(normalized === "gpt" || normalized === "glm" ? normalized.toUpperCase() : `${part.charAt(0).toUpperCase()}${part.slice(1)}`);
	}
	return formatted.join(" ") || modelID;
}

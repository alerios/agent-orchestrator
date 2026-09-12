import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OrchestratorMetrics } from "./OrchestratorMetrics";

let mockRollup: unknown;
// "This orchestrator" cannot come from the roll-up: orchestratorTotals is a
// project-lifetime sum across every generation. The current orchestrator's own
// spend is its own session usage/effort, so those hooks are mocked too.
let mockUsage: unknown = { data: undefined, isError: false, isLoading: false };
let mockEffort: unknown = { data: undefined, isError: false, isLoading: false };

vi.mock("../hooks/useProjectUsageRollup", () => ({
	useProjectUsageRollup: () => mockRollup,
}));

vi.mock("../hooks/useSessionUsage", () => ({
	useSessionUsage: () => mockUsage,
}));

vi.mock("../hooks/useSessionEffort", () => ({
	useSessionEffort: () => mockEffort,
}));

const session = { id: "orch-1", kind: "orchestrator", workspaceId: "proj-1" } as never;

describe("OrchestratorMetrics", () => {
	it("states the generation count so a project total is never mistaken for this orchestrator", () => {
		mockRollup = {
			data: {
				efficiency: {
					costPerMergedPrNanos: 6_000_000_000,
					isLowerBound: false,
					orchestratorShare: 0.25,
					workerShare: 0.75,
				},
				mergedPrs: 2,
				orchestratorGenerations: 3,
				orchestratorTotals: { estimatedCost: { totalNanos: 3_000_000_000 }, processedTokens: 100 },
				projectId: "proj-1",
				unmeasuredSessions: 0,
				workerTotals: { estimatedCost: { totalNanos: 9_000_000_000 }, processedTokens: 900 },
				workers: [],
			},
			isError: false,
			isLoading: false,
		};
		render(<OrchestratorMetrics session={session} />);
		expect(screen.getByText(/across 3 orchestrator generations/i)).toBeInTheDocument();
		expect(screen.getByText("25%")).toBeInTheDocument();
		expect(screen.getByText("75%")).toBeInTheDocument();
		expect(screen.getByText("$6.00")).toBeInTheDocument();
	});

	it("separates this orchestrator's own spend from the project all-time total", () => {
		mockUsage = {
			data: { harnesses: [], totals: { estimatedCost: { totalNanos: 1_000_000_000 }, processedTokens: 50 } },
			isError: false,
			isLoading: false,
		};
		mockRollup = {
			data: {
				efficiency: {
					costPerMergedPrNanos: null,
					isLowerBound: false,
					orchestratorShare: null,
					workerShare: null,
				},
				mergedPrs: 0,
				orchestratorGenerations: 3,
				orchestratorTotals: { estimatedCost: { totalNanos: 3_000_000_000 }, processedTokens: 100 },
				projectId: "proj-1",
				unmeasuredSessions: 0,
				workerTotals: { estimatedCost: null, processedTokens: null },
				workers: [],
			},
			isError: false,
			isLoading: false,
		};
		render(<OrchestratorMetrics session={session} />);
		// The session's own $1.00 and the project's all-time $3.00 are distinct
		// figures under distinct headings, never the same number twice.
		expect(screen.getByTestId("rollup-this-orchestrator-cost")).toHaveTextContent("$1.00");
		expect(screen.getByTestId("rollup-orchestrator-alltime-cost")).toHaveTextContent("$3.00");
		mockUsage = { data: undefined, isError: false, isLoading: false };
	});

	it("flags totals as a lower bound when sessions were unmeasurable", () => {
		mockRollup = {
			data: {
				efficiency: { costPerMergedPrNanos: null, isLowerBound: true, orchestratorShare: null, workerShare: null },
				mergedPrs: 0,
				orchestratorGenerations: 1,
				orchestratorTotals: { estimatedCost: null, processedTokens: null },
				projectId: "proj-1",
				unmeasuredSessions: 2,
				workerTotals: { estimatedCost: null, processedTokens: null },
				workers: [
					{
						durationMs: null,
						estimatedCost: null,
						harness: "droid",
						measured: false,
						modelId: "",
						outcome: "active",
						sessionId: "w-1",
						title: "Untracked",
					},
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

	it("puts the lower-bound caveat beside the spend split, not only beside cost per PR", () => {
		mockRollup = {
			data: {
				efficiency: { costPerMergedPrNanos: null, isLowerBound: true, orchestratorShare: 0, workerShare: 1 },
				mergedPrs: 0,
				orchestratorGenerations: 1,
				orchestratorTotals: { estimatedCost: null, processedTokens: null },
				projectId: "proj-1",
				unmeasuredSessions: 1,
				workerTotals: { estimatedCost: { totalNanos: 2_000_000_000 }, processedTokens: 10 },
				workers: [],
			},
			isError: false,
			isLoading: false,
		};
		render(<OrchestratorMetrics session={session} />);
		// A 0% orchestrator share on top of an unmeasured session is not a fact
		// about the orchestrator being free; the caveat has to sit in the split.
		const split = screen.getByTestId("rollup-split");
		expect(split).toHaveTextContent(/lower bound/i);
	});

	it("never renders a zero or infinite cost per merged PR when nothing merged", () => {
		mockRollup = {
			data: {
				efficiency: { costPerMergedPrNanos: null, isLowerBound: false, orchestratorShare: 0.5, workerShare: 0.5 },
				mergedPrs: 0,
				orchestratorGenerations: 1,
				orchestratorTotals: { estimatedCost: { totalNanos: 1_000_000_000 }, processedTokens: 10 },
				projectId: "proj-1",
				unmeasuredSessions: 0,
				workerTotals: { estimatedCost: { totalNanos: 1_000_000_000 }, processedTokens: 10 },
				workers: [],
			},
			isError: false,
			isLoading: false,
		};
		render(<OrchestratorMetrics session={session} />);
		const perPr = screen.getByTestId("rollup-cost-per-merged-pr");
		expect(perPr).toHaveTextContent(/No merged pull requests yet/i);
		expect(perPr.textContent).not.toMatch(/\$0\.00|Infinity|NaN/);
	});

	it("says the project has no workers rather than rendering an empty table", () => {
		mockRollup = {
			data: {
				efficiency: { costPerMergedPrNanos: null, isLowerBound: false, orchestratorShare: null, workerShare: null },
				mergedPrs: 0,
				orchestratorGenerations: 1,
				orchestratorTotals: { estimatedCost: null, processedTokens: null },
				projectId: "proj-1",
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

	it("lists measured workers with their outcome and cost", () => {
		mockRollup = {
			data: {
				efficiency: { costPerMergedPrNanos: 500_000_000, isLowerBound: false, orchestratorShare: 0.2, workerShare: 0.8 },
				mergedPrs: 1,
				orchestratorGenerations: 1,
				orchestratorTotals: { estimatedCost: { totalNanos: 100_000_000 }, processedTokens: 10 },
				projectId: "proj-1",
				unmeasuredSessions: 0,
				workerTotals: { estimatedCost: { totalNanos: 400_000_000 }, processedTokens: 40 },
				workers: [
					{
						durationMs: 60_000,
						estimatedCost: { totalNanos: 400_000_000 },
						harness: "claude-code",
						measured: true,
						modelId: "claude-opus-4-20250514",
						outcome: "merged",
						sessionId: "w-1",
						title: "Add the rail",
					},
				],
			},
			isError: false,
			isLoading: false,
		};
		render(<OrchestratorMetrics session={session} />);
		const row = screen.getByTestId("rollup-worker-row");
		expect(row).toHaveTextContent("Add the rail");
		expect(row).toHaveTextContent("Merged");
		expect(row).toHaveTextContent("$0.40");
		expect(row.textContent).not.toMatch(/Not measurable/i);
	});

	it("reports a failed roll-up instead of rendering blank sections", () => {
		mockRollup = { data: undefined, isError: true, isLoading: false };
		render(<OrchestratorMetrics session={session} />);
		expect(screen.getByText(/roll-up is unavailable/i)).toBeInTheDocument();
	});
});

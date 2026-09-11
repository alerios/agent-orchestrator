import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MetricsView } from "./SessionInspectorMetrics";

const session = { harness: "claude-code", id: "sess-1" } as never;

vi.mock("../hooks/useSessionUsage", () => ({
	useSessionUsage: () => mockUsage,
}));

vi.mock("../hooks/useSessionEffort", () => ({
	useSessionEffort: () => mockEffort,
}));

let mockUsage: unknown;
let mockEffort: unknown = { data: undefined, isError: false, isLoading: false };

const baseEffort = {
	activeMs: null,
	commandsRun: 0,
	compactions: 0,
	durationMs: null,
	filesChanged: 0,
	filesRead: 0,
	idleMs: null,
	linesAdded: 0,
	linesRemoved: 0,
	testsRun: 0,
	timingAvailable: false,
	toolCalls: 0,
};

describe("MetricsView", () => {
	it("renders cost and tokens without developer mode", () => {
		mockUsage = {
			data: {
				harnesses: [],
				incomplete: false,
				sessionId: "sess-1",
				totals: {
					estimatedCost: { currency: "USD", nanos: 2840000000 },
					processedTokens: 207000,
				},
			},
			isError: false,
			isLoading: false,
		};
		render(<MetricsView session={session} />);
		expect(screen.getByTestId("session-usage-metrics")).toBeInTheDocument();
	});

	it("states that no telemetry exists rather than rendering zeros", () => {
		mockUsage = { data: undefined, isError: false, isLoading: false };
		render(<MetricsView session={session} />);
		expect(screen.getByText(/No telemetry recorded/i)).toBeInTheDocument();
		expect(screen.queryByTestId("session-usage-metrics")).not.toBeInTheDocument();
	});

	it("does not assert no telemetry while the usage query is still loading", () => {
		mockUsage = { data: undefined, isError: false, isLoading: true };
		render(<MetricsView session={session} />);
		expect(screen.queryByText(/No telemetry recorded/i)).not.toBeInTheDocument();
	});

	it("flags partial coverage as a lower bound", () => {
		mockUsage = {
			data: {
				harnesses: [],
				incomplete: true,
				sessionId: "sess-1",
				totals: { estimatedCost: null, processedTokens: 1000 },
			},
			isError: false,
			isLoading: false,
		};
		render(<MetricsView session={session} />);
		expect(screen.getByText(/lower bound/i)).toBeInTheDocument();
	});

	it("sorts the tool mix by time spent when timing exists", () => {
		mockEffort = {
			data: {
				effort: { ...baseEffort, timingAvailable: true, activeMs: 9100, idleMs: 900, durationMs: 10000 },
				toolMix: [
					{ calls: 1, failedCalls: 0, toolName: "bash", totalDurationMs: 9000 },
					{ calls: 2, failedCalls: 0, toolName: "read", totalDurationMs: 200 },
				],
			},
			isError: false,
			isLoading: false,
		};
		render(<MetricsView session={session} />);
		const rows = screen.getAllByTestId("tool-mix-row");
		expect(rows[0]).toHaveTextContent("bash");
		expect(screen.getByText(/Sorted by time spent/i)).toBeInTheDocument();
	});

	it("falls back to count ordering and says so when timing is absent", () => {
		mockEffort = {
			data: {
				effort: { ...baseEffort, timingAvailable: false, activeMs: null, idleMs: null, durationMs: 10000 },
				toolMix: [
					{ calls: 2, failedCalls: 0, toolName: "read", totalDurationMs: null },
					{ calls: 1, failedCalls: 0, toolName: "bash", totalDurationMs: null },
				],
			},
			isError: false,
			isLoading: false,
		};
		render(<MetricsView session={session} />);
		expect(screen.getByText(/Sorted by call count/i)).toBeInTheDocument();
		expect(screen.getAllByTestId("tool-mix-row")[0]).toHaveTextContent("read");
	});

	it("never renders zero for unmeasured active time", () => {
		mockEffort = {
			data: {
				effort: { ...baseEffort, timingAvailable: false, activeMs: null, idleMs: null, durationMs: 10000 },
				toolMix: [],
			},
			isError: false,
			isLoading: false,
		};
		render(<MetricsView session={session} />);
		expect(screen.getByText(/active and idle time are unknown/i)).toBeInTheDocument();
		expect(screen.queryByTestId("effort-active-value")).not.toBeInTheDocument();
	});

	it("states when no tool calls were recorded", () => {
		mockEffort = { data: { effort: baseEffort, toolMix: [] }, isError: false, isLoading: false };
		render(<MetricsView session={session} />);
		expect(screen.getByText(/No tool calls recorded/i)).toBeInTheDocument();
	});
});

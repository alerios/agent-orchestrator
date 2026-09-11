import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MetricsView } from "./SessionInspectorMetrics";

const session = { harness: "claude-code", id: "sess-1" } as never;

vi.mock("../hooks/useSessionUsage", () => ({
	useSessionUsage: () => mockUsage,
}));

let mockUsage: unknown;

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
});

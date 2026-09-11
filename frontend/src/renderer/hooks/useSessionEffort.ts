import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { sessionUsageQueryRoot } from "./useSessionUsageSummaries";

export type SessionEffort = components["schemas"]["ControllersSessionEffortResponse"];

export const sessionEffortQueryKey = (sessionId: string) =>
	[...sessionUsageQueryRoot, "effort", sessionId] as const;

export async function fetchSessionEffort(sessionId: string): Promise<SessionEffort> {
	const { data, error } = await apiClient.GET("/api/v1/usage/sessions/{sessionId}/effort", {
		params: { path: { sessionId } },
	});
	if (error) throw error;
	return data;
}

export function useSessionEffort(sessionId: string, enabled = true) {
	return useQuery({
		enabled: enabled && Boolean(sessionId),
		queryFn: () => fetchSessionEffort(sessionId),
		queryKey: sessionEffortQueryKey(sessionId),
		retry: 1,
	});
}

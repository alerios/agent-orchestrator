import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { sessionUsageQueryRoot } from "./useSessionUsageSummaries";

export type ProjectUsageRollup = components["schemas"]["ControllersProjectUsageRollupResponse"];
export type ProjectWorkerUsageRow = components["schemas"]["ControllersWorkerUsageRowResponse"];

export const projectUsageRollupQueryKey = (projectId: string) =>
	[...sessionUsageQueryRoot, "project-rollup", projectId] as const;

export async function fetchProjectUsageRollup(projectId: string): Promise<ProjectUsageRollup> {
	const { data, error } = await apiClient.GET("/api/v1/usage/projects/{projectId}/rollup", {
		params: { path: { projectId } },
	});
	if (error) throw error;
	return data;
}

export function useProjectUsageRollup(projectId: string, enabled = true) {
	return useQuery({
		enabled: enabled && Boolean(projectId),
		queryFn: () => fetchProjectUsageRollup(projectId),
		queryKey: projectUsageRollupQueryKey(projectId),
		retry: 1,
	});
}

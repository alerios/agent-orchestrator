package usage

import (
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// activeGapThreshold bounds how long a gap between observed events still
// counts as the agent working. Longer gaps are idle.
const activeGapThreshold = 120 * time.Second

// testRunnerPatterns recognizes test commands. This is a HEURISTIC: a project
// with a custom test command undercounts, which is why tests-run is displayed
// as effort and never used as a governance gate.
var testRunnerPatterns = []string{
	"go test", "npm test", "npm run test", "pnpm test", "yarn test",
	"pytest", "vitest", "jest", "cargo test", "dotnet test", "mvn test", "rspec",
}

// deriveEffort computes the per-session counter block. Active time is the
// UNION of timed tool spans, so overlapping calls are not double counted.
// When no call carries timing, active and idle stay unavailable rather than
// being guessed from wall clock.
func deriveEffort(
	calls []domain.SessionToolCall, firstEvent, lastEvent time.Time, compactions int64,
) domain.SessionEffort {
	out := domain.SessionEffort{Compactions: compactions, ToolCalls: int64(len(calls))}

	if !lastEvent.Before(firstEvent) {
		duration := lastEvent.Sub(firstEvent).Milliseconds()
		out.DurationMS = &duration
	}

	spans := make([][2]int64, 0, len(calls))
	for _, call := range calls {
		switch domain.ClassifyTool(call.ToolName) {
		case domain.ToolKindRead:
			out.FilesRead++
		case domain.ToolKindEdit:
			out.FilesChanged++
		case domain.ToolKindCommand:
			out.CommandsRun++
			if isTestCommand(call.InputSummary) {
				out.TestsRun++
			}
		}
		if call.StartedAt != nil && call.EndedAt != nil {
			spans = append(spans, [2]int64{call.StartedAt.UnixMilli(), call.EndedAt.UnixMilli()})
		}
	}

	if len(spans) == 0 {
		return out
	}
	out.TimingAvailable = true
	active := unionSpanMillis(spans)
	out.ActiveMS = &active
	if out.DurationMS != nil {
		idle := *out.DurationMS - active
		if idle < 0 {
			idle = 0
		}
		out.IdleMS = &idle
	}
	return out
}

// unionSpanMillis measures the total wall-clock time covered by any span,
// merging overlaps. Gaps shorter than activeGapThreshold are treated as
// continued work, matching the spec's definition of active time.
func unionSpanMillis(spans [][2]int64) int64 {
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	gap := activeGapThreshold.Milliseconds()
	var (
		total      int64
		start, end = spans[0][0], spans[0][1]
	)
	for _, span := range spans[1:] {
		if span[0] <= end+gap {
			if span[1] > end {
				end = span[1]
			}
			continue
		}
		total += end - start
		start, end = span[0], span[1]
	}
	return total + end - start
}

func isTestCommand(summary string) bool {
	lowered := strings.ToLower(summary)
	for _, pattern := range testRunnerPatterns {
		if strings.Contains(lowered, pattern) {
			return true
		}
	}
	return false
}

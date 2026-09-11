package usage

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const maxToolInputSummary = 512

type openCodeToolPart struct {
	CallID string `json:"callID"`
	Tool   string `json:"tool"`
	Type   string `json:"type"`
	State  struct {
		Status string `json:"status"`
		Title  string `json:"title"`
		Time   *struct {
			End   int64 `json:"end"`
			Start int64 `json:"start"`
		} `json:"time"`
	} `json:"state"`
}

// decodeOpenCodeToolCall converts one opencode part into a durable tool-call
// fact. Only `state.title` is used as the summary — never `state.output`,
// which is unbounded and frequently sensitive.
func decodeOpenCodeToolCall(
	sessionID domain.SessionID, part openCodePart, now time.Time,
) (domain.SessionToolCall, bool) {
	if part.Type != "tool" {
		return domain.SessionToolCall{}, false
	}
	var decoded openCodeToolPart
	if err := json.Unmarshal(part.Data, &decoded); err != nil {
		return domain.SessionToolCall{}, false
	}
	if strings.TrimSpace(decoded.Tool) == "" || strings.TrimSpace(decoded.CallID) == "" {
		return domain.SessionToolCall{}, false
	}

	call := domain.SessionToolCall{
		InputSummary:   boundedToolSummary(decoded.State.Title),
		ObservedAt:     now,
		Outcome:        openCodeToolOutcome(decoded.State.Status),
		ProviderCallID: decoded.CallID,
		SessionID:      sessionID,
		SourceKind:     domain.UsageSourceOpenCodeDB,
		ToolName:       decoded.Tool,
	}
	call.IsMCP, call.MCPServer = mcpToolIdentity(decoded.Tool)

	// Timing is optional. Only a start-and-end pair that makes sense becomes a
	// duration; anything else stays unknown.
	if t := decoded.State.Time; t != nil && t.Start > 0 && t.End >= t.Start {
		started := time.UnixMilli(t.Start).UTC()
		ended := time.UnixMilli(t.End).UTC()
		duration := t.End - t.Start
		call.StartedAt = &started
		call.EndedAt = &ended
		call.DurationMS = &duration
	}
	return call, true
}

func openCodeToolOutcome(status string) domain.ToolOutcome {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "success":
		return domain.ToolOutcomeCompleted
	case "error", "failed":
		return domain.ToolOutcomeFailed
	case "denied", "rejected":
		return domain.ToolOutcomeDenied
	default:
		return domain.ToolOutcomeUnknown
	}
}

// mcpToolIdentity recognizes the mcp__<server>__<tool> naming convention.
func mcpToolIdentity(tool string) (bool, string) {
	if !strings.HasPrefix(tool, "mcp__") {
		return false, ""
	}
	parts := strings.SplitN(strings.TrimPrefix(tool, "mcp__"), "__", 2)
	if len(parts) != 2 || parts[0] == "" {
		return true, ""
	}
	return true, parts[0]
}

func boundedToolSummary(raw string) string {
	trimmed := domain.SanitizeControlChars(strings.TrimSpace(raw))
	if len(trimmed) <= maxToolInputSummary {
		return trimmed
	}
	return trimmed[:maxToolInputSummary]
}

func openCodeCompactionCount(parts []openCodePart) int64 {
	var count int64
	for _, part := range parts {
		if part.Type == "compaction" {
			count++
		}
	}
	return count
}

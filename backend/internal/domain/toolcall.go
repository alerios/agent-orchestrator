package domain

import "time"

// ToolOutcome is how a tool call ended. Unknown is a real, storable state: a
// transcript may record that a tool ran without recording how it finished.
type ToolOutcome string

// Tool outcomes.
const (
	ToolOutcomeCompleted ToolOutcome = "completed"
	ToolOutcomeFailed    ToolOutcome = "failed"
	ToolOutcomeDenied    ToolOutcome = "denied"
	ToolOutcomeUnknown   ToolOutcome = "unknown"
)

// SessionToolCall is one durable tool-call fact. Pointer fields are nil when
// the source did not report them; a non-nil zero is a reported zero. Tool
// OUTPUT is deliberately absent: it is unbounded and often sensitive.
type SessionToolCall struct {
	SessionID      SessionID
	SourceKind     UsageSourceKind
	ProviderCallID string
	ToolName       string
	IsMCP          bool
	MCPServer      string
	InputSummary   string
	Outcome        ToolOutcome
	StartedAt      *time.Time
	EndedAt        *time.Time
	DurationMS     *int64
	ExitCode       *int64
	ObservedAt     time.Time
}

// SessionEffort is the derived per-session counter block. TimingAvailable
// false means ActiveMS and IdleMS are unavailable rather than zero: a session
// with no timing is not a session that was idle.
type SessionEffort struct {
	DurationMS      *int64
	ActiveMS        *int64
	IdleMS          *int64
	ToolCalls       int64
	FilesRead       int64
	FilesChanged    int64
	LinesAdded      int64
	LinesRemoved    int64
	CommandsRun     int64
	TestsRun        int64
	Compactions     int64
	TimingAvailable bool
}

// ToolMixEntry is one row of the tool breakdown. TotalDurationMS is nil when
// no call for that tool carried timing, which is what forces the UI to fall
// back to count ordering and say so.
type ToolMixEntry struct {
	ToolName        string
	Calls           int64
	FailedCalls     int64
	TotalDurationMS *int64
}

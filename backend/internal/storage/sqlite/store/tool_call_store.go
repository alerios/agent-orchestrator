package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertSessionToolCalls durably records a batch of tool-call facts in a
// single transaction, so a partial batch is never visible. Re-applying the
// same batch (same session_id, source_kind, provider_call_id) is idempotent:
// the ON CONFLICT clause in the generated query updates only outcome/timing
// fields, never duplicating rows.
func (s *Store) UpsertSessionToolCalls(ctx context.Context, calls []domain.SessionToolCall) error {
	if len(calls) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "upsert session tool calls", func(q *gen.Queries) error {
		for _, call := range calls {
			if err := q.UpsertSessionToolCall(ctx, toolCallInsertParams(call)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("upsert session tool calls: %w", err)
	}
	return nil
}

// ListSessionToolCalls returns every durable tool-call fact for a session,
// ordered by observation time.
func (s *Store) ListSessionToolCalls(ctx context.Context, sessionID domain.SessionID) ([]domain.SessionToolCall, error) {
	rows, err := s.qr.ListSessionToolCalls(ctx, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list session tool calls for session %s: %w", sessionID, err)
	}
	out := make([]domain.SessionToolCall, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.SessionToolCall{
			SessionID:      domain.SessionID(row.SessionID),
			SourceKind:     domain.UsageSourceKind(row.SourceKind),
			ProviderCallID: row.ProviderCallID,
			ToolName:       row.ToolName,
			IsMCP:          row.IsMcp != 0,
			MCPServer:      row.McpServer,
			InputSummary:   row.InputSummary,
			Outcome:        domain.ToolOutcome(row.Outcome),
			StartedAt:      nullMillisTimePtr(row.StartedAt),
			EndedAt:        nullMillisTimePtr(row.EndedAt),
			DurationMS:     nullInt64Ptr(row.DurationMs),
			ExitCode:       nullInt64Ptr(row.ExitCode),
			ObservedAt:     millisToTime(row.ObservedAt),
		})
	}
	return out, nil
}

// SessionToolMix returns the per-tool breakdown for a session, ordered by
// total duration (tools with no timed calls sort last), then call count.
// timed_calls distinguishes "no timing for this tool" (TotalDurationMS nil)
// from "some calls timed" so the read layer never fakes a zero duration.
func (s *Store) SessionToolMix(ctx context.Context, sessionID domain.SessionID) ([]domain.ToolMixEntry, error) {
	rows, err := s.qr.SessionToolMix(ctx, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("session tool mix for session %s: %w", sessionID, err)
	}
	out := make([]domain.ToolMixEntry, 0, len(rows))
	for _, row := range rows {
		entry := domain.ToolMixEntry{
			ToolName:    row.ToolName,
			Calls:       row.Calls,
			FailedCalls: int64(row.FailedCalls.Float64),
		}
		if row.TimedCalls > 0 && row.TotalDurationMs.Valid {
			total := int64(row.TotalDurationMs.Float64)
			entry.TotalDurationMS = &total
		}
		out = append(out, entry)
	}
	return out, nil
}

// UpsertSessionEffort replaces the durable effort rollup for a session.
func (s *Store) UpsertSessionEffort(ctx context.Context, sessionID domain.SessionID, effort domain.SessionEffort, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.UpsertSessionEffort(ctx, gen.UpsertSessionEffortParams{
		SessionID:       string(sessionID),
		DurationMs:      ptrInt64ToNull(effort.DurationMS),
		ActiveMs:        ptrInt64ToNull(effort.ActiveMS),
		IdleMs:          ptrInt64ToNull(effort.IdleMS),
		ToolCalls:       effort.ToolCalls,
		FilesRead:       effort.FilesRead,
		FilesChanged:    effort.FilesChanged,
		LinesAdded:      effort.LinesAdded,
		LinesRemoved:    effort.LinesRemoved,
		CommandsRun:     effort.CommandsRun,
		TestsRun:        effort.TestsRun,
		Compactions:     effort.Compactions,
		TimingAvailable: boolToInt64(effort.TimingAvailable),
		UpdatedAt:       timeOrNow(at).UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("upsert session effort for session %s: %w", sessionID, err)
	}
	return nil
}

// GetSessionEffort returns the durable effort rollup for a session, or
// ok=false when none has been recorded yet.
func (s *Store) GetSessionEffort(ctx context.Context, sessionID domain.SessionID) (domain.SessionEffort, bool, error) {
	row, err := s.qr.GetSessionEffort(ctx, string(sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionEffort{}, false, nil
	}
	if err != nil {
		return domain.SessionEffort{}, false, fmt.Errorf("get session effort for session %s: %w", sessionID, err)
	}
	effort := domain.SessionEffort{
		DurationMS:      nullInt64Ptr(row.DurationMs),
		ToolCalls:       row.ToolCalls,
		FilesRead:       row.FilesRead,
		FilesChanged:    row.FilesChanged,
		LinesAdded:      row.LinesAdded,
		LinesRemoved:    row.LinesRemoved,
		CommandsRun:     row.CommandsRun,
		TestsRun:        row.TestsRun,
		Compactions:     row.Compactions,
		TimingAvailable: row.TimingAvailable != 0,
	}
	if effort.TimingAvailable {
		effort.ActiveMS = nullInt64Ptr(row.ActiveMs)
		effort.IdleMS = nullInt64Ptr(row.IdleMs)
	}
	return effort, true, nil
}

func toolCallInsertParams(call domain.SessionToolCall) gen.UpsertSessionToolCallParams {
	return gen.UpsertSessionToolCallParams{
		SessionID:      string(call.SessionID),
		SourceKind:     string(call.SourceKind),
		ProviderCallID: call.ProviderCallID,
		ToolName:       call.ToolName,
		IsMcp:          boolToInt64(call.IsMCP),
		McpServer:      call.MCPServer,
		InputSummary:   call.InputSummary,
		Outcome:        string(call.Outcome),
		StartedAt:      ptrTimeToNullMillis(call.StartedAt),
		EndedAt:        ptrTimeToNullMillis(call.EndedAt),
		DurationMs:     ptrInt64ToNull(call.DurationMS),
		ExitCode:       ptrInt64ToNull(call.ExitCode),
		ObservedAt:     timeOrNow(call.ObservedAt).UnixMilli(),
	}
}

func boolToInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func millisToTime(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

func ptrTimeToNullMillis(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func nullMillisTimePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}

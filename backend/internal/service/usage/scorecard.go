package usage

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scoring"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// scorecardStore is everything ScorecardService reads to assemble
// scoring.Facts for one session.
type scorecardStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetSessionUsageSummary(ctx context.Context, id domain.SessionID) (domain.SessionUsageSummary, error)
	GetSessionEffort(ctx context.Context, id domain.SessionID) (domain.SessionEffort, bool, error)
	ListSessionToolCalls(ctx context.Context, id domain.SessionID) ([]domain.SessionToolCall, error)
	GetDisplayPRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, bool, error)
	// HasConversation reports whether AO holds a structured conversation
	// timeline for this session (Chat-mode sessions do; terminal sessions may
	// not). It must never be derived from a turn count: an absent timeline and
	// a one-shot terminal session both have zero observable turns, and only
	// the former is "not measurable".
	HasConversation(ctx context.Context, id domain.SessionID) (bool, error)
	// ConversationTurns returns the session's conversation turns, ordered by
	// sequence, for deriving steering-load facts (UserTurns, Interrupts). It
	// is only ever called when HasConversation is true.
	ConversationTurns(ctx context.Context, id domain.SessionID) ([]domain.ConversationTurn, error)
	// ConversationMessages returns the session's conversation messages, used
	// alongside ConversationTurns to determine each turn's Origin (human vs
	// automation/daemon) via the triggering message's TurnID. It is only ever
	// called when HasConversation is true.
	ConversationMessages(ctx context.Context, id domain.SessionID) ([]domain.ConversationMessage, error)
	// GetProject loads a project record so governance facts can tell a
	// scratch project's legitimate "no branch" from a branch state that is
	// genuinely unknown for another reason.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// ScorecardService assembles scoring.Facts from durable session data and
// scores them via scoring.Score. It never stores the result: the rubric can
// change and every past session is re-scored on next read.
type ScorecardService struct{ store scorecardStore }

// NewScorecardService builds the assembly service.
func NewScorecardService(store scorecardStore) *ScorecardService {
	return &ScorecardService{store: store}
}

// Get assembles and scores one session's scorecard.
func (s *ScorecardService) Get(ctx context.Context, id domain.SessionID) (scoring.Scorecard, error) {
	session, _, err := s.store.GetSession(ctx, id)
	if err != nil {
		return scoring.Scorecard{}, err
	}

	facts := scoring.Facts{
		// SupportedHarness is the single source of truth for measurability, so
		// scoring and ingestion can never disagree about a harness.
		HasCertifiedUsageSource: SupportedHarness(session.Harness),
	}
	if !facts.HasCertifiedUsageSource {
		return scoring.Score(facts), nil
	}

	usageSummary, err := s.store.GetSessionUsageSummary(ctx, id)
	if err != nil {
		return scoring.Scorecard{}, err
	}
	facts.InputTokens = usageSummary.Totals.InputTokens
	facts.CachedInputTokens = usageSummary.Totals.CachedInputTokens
	facts.OutputTokens = usageSummary.Totals.OutputTokens

	effort, _, err := s.store.GetSessionEffort(ctx, id)
	if err != nil {
		return scoring.Scorecard{}, err
	}
	facts.Effort = effort

	calls, err := s.store.ListSessionToolCalls(ctx, id)
	if err != nil {
		return scoring.Scorecard{}, err
	}
	facts.Calls = calls
	facts.RepeatedEditTargets, facts.EditTargets = reworkCounts(calls)
	facts.RejectedApprovals = countDeniedCalls(calls)

	hasConversation, err := s.store.HasConversation(ctx, id)
	if err != nil {
		return scoring.Scorecard{}, err
	}
	facts.HasConversation = hasConversation
	if hasConversation {
		turns, err := s.store.ConversationTurns(ctx, id)
		if err != nil {
			return scoring.Scorecard{}, err
		}
		messages, err := s.store.ConversationMessages(ctx, id)
		if err != nil {
			return scoring.Scorecard{}, err
		}
		facts.UserTurns, facts.Interrupts = turnCounts(turns, messages)
	}

	pr, hasPR, err := s.store.GetDisplayPRFactsForSession(ctx, id)
	if err != nil {
		return scoring.Scorecard{}, err
	}
	facts.HasPR = hasPR
	if hasPR {
		facts.PRMerged = pr.Merged
		facts.CI = pr.CI
		facts.Review = pr.Review
	}
	facts.SessionStart = session.CreatedAt

	// OnBranch: an empty branch is a real, deliberate fact for a scratch
	// project (scratch work never uses branches). For any other project kind
	// an empty branch can also mean the branch state is genuinely unknown
	// (e.g. a failed spawn's teardown clears Metadata.Branch along with the
	// workspace it destroyed) — that must not silently score as "no branch".
	project, hasProject, err := s.store.GetProject(ctx, string(session.ProjectID))
	if err != nil {
		return scoring.Scorecard{}, err
	}
	projectKind := domain.ProjectKindSingleRepo
	if hasProject {
		projectKind = project.Kind.WithDefault()
	}
	switch {
	case session.Metadata.Branch != "":
		facts.OnBranch = true
	case projectKind == domain.ProjectKindScratch:
		facts.OnBranch = false
	default:
		facts.GovernanceAbsentReason = "branch state unknown for this session"
	}

	return scoring.Score(facts), nil
}

// reworkCounts groups edit-tool calls by their InputSummary — the tool call's
// free-form, human-readable title (e.g. "read file", "npm test"), not
// necessarily a file path. A summary edited three or more times is a
// repeated edit target. A call whose source did not report a summary (empty
// InputSummary) is excluded from both the numerator and the denominator
// rather than guessed at. Precise file-level rework detection would require
// the tool decoder to report an actual path, which not all sources currently
// do.
func reworkCounts(calls []domain.SessionToolCall) (repeatedEditTargets, editTargets int64) {
	editsBySummary := make(map[string]int64)
	for _, call := range calls {
		if domain.ClassifyTool(call.ToolName) != domain.ToolKindEdit {
			continue
		}
		if call.InputSummary == "" {
			continue
		}
		editsBySummary[call.InputSummary]++
	}
	for _, edits := range editsBySummary {
		editTargets++
		if edits >= 3 {
			repeatedEditTargets++
		}
	}
	return repeatedEditTargets, editTargets
}

// countDeniedCalls counts tool calls the user (or an approval policy) denied.
func countDeniedCalls(calls []domain.SessionToolCall) int64 {
	var denied int64
	for _, call := range calls {
		if call.Outcome == domain.ToolOutcomeDenied {
			denied++
		}
	}
	return denied
}

// turnCounts derives steering-load turn facts from a conversation's turns and
// messages. Not every turn is human-initiated: RelayChatTurnWithID
// (orchestrator-to-worker relays) and RetryTurn (AO-initiated retries) create
// turns with Origin: domain.MessageOriginAutomation, and counting those as
// UserTurns would score an orchestrator-driven session no human ever touched
// as if it were heavily human-steered. Only a turn whose triggering message
// has Origin == domain.MessageOriginHuman counts toward UserTurns. Each turn
// has exactly one triggering (role=user) message, joined here by TurnID.
//
// Interrupts is unrelated to who initiated a turn — it counts how a turn
// ended (TurnStateInterrupted) — so it is unaffected by origin and still
// counted over every turn.
func turnCounts(turns []domain.ConversationTurn, messages []domain.ConversationMessage) (userTurns, interrupts int64) {
	originByTurn := make(map[string]domain.MessageOrigin, len(messages))
	for _, msg := range messages {
		if msg.TurnID == "" || msg.Role != domain.MessageRoleUser {
			continue
		}
		originByTurn[msg.TurnID] = msg.Origin
	}
	for _, turn := range turns {
		if originByTurn[turn.ID] == domain.MessageOriginHuman {
			userTurns++
		}
		if turn.State == domain.TurnStateInterrupted {
			interrupts++
		}
	}
	return userTurns, interrupts
}

// scorecardRawStore is the subset of the durable store ScorecardStoreAdapter
// needs beyond usage-summary aggregation, which it delegates to a
// *SummaryReader instead of duplicating.
type scorecardRawStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetSessionEffort(ctx context.Context, id domain.SessionID) (domain.SessionEffort, bool, error)
	ListSessionToolCalls(ctx context.Context, id domain.SessionID) ([]domain.SessionToolCall, error)
	GetDisplayPRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, bool, error)
	ConversationForSession(ctx context.Context, id domain.SessionID) (domain.ConversationRecord, error)
	HasConversationTurns(ctx context.Context, conversationID string) (bool, error)
	LoadConversationSnapshot(ctx context.Context, conversationID string) (store.ConversationSnapshot, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// usageSummaryGetter is the narrow SummaryReader contract ScorecardStoreAdapter
// needs.
type usageSummaryGetter interface {
	Get(ctx context.Context, id domain.SessionID) (domain.SessionUsageSummary, error)
}

// ScorecardStoreAdapter composes the durable session store and the existing
// usage-summary reader into the scorecardStore contract ScorecardService
// needs, so production wiring does not have to duplicate summary aggregation.
type ScorecardStoreAdapter struct {
	Store   scorecardRawStore
	Summary usageSummaryGetter
}

// NewScorecardStoreAdapter builds the adapter.
func NewScorecardStoreAdapter(store scorecardRawStore, summary usageSummaryGetter) ScorecardStoreAdapter {
	return ScorecardStoreAdapter{Store: store, Summary: summary}
}

// GetSession delegates to the durable store.
func (a ScorecardStoreAdapter) GetSession(
	ctx context.Context, id domain.SessionID,
) (domain.SessionRecord, bool, error) {
	return a.Store.GetSession(ctx, id)
}

// GetSessionUsageSummary delegates to the usage summary reader.
func (a ScorecardStoreAdapter) GetSessionUsageSummary(
	ctx context.Context, id domain.SessionID,
) (domain.SessionUsageSummary, error) {
	return a.Summary.Get(ctx, id)
}

// GetSessionEffort delegates to the durable store.
func (a ScorecardStoreAdapter) GetSessionEffort(
	ctx context.Context, id domain.SessionID,
) (domain.SessionEffort, bool, error) {
	return a.Store.GetSessionEffort(ctx, id)
}

// ListSessionToolCalls delegates to the durable store.
func (a ScorecardStoreAdapter) ListSessionToolCalls(
	ctx context.Context, id domain.SessionID,
) ([]domain.SessionToolCall, error) {
	return a.Store.ListSessionToolCalls(ctx, id)
}

// GetDisplayPRFactsForSession delegates to the durable store.
func (a ScorecardStoreAdapter) GetDisplayPRFactsForSession(
	ctx context.Context, id domain.SessionID,
) (domain.PRFacts, bool, error) {
	return a.Store.GetDisplayPRFactsForSession(ctx, id)
}

// HasConversation reports whether AO holds a structured conversation timeline
// for this session. A session with no conversation record at all (a terminal
// session that never opened one) is "not measurable", not "zero turns".
func (a ScorecardStoreAdapter) HasConversation(ctx context.Context, id domain.SessionID) (bool, error) {
	conv, err := a.Store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.Store.HasConversationTurns(ctx, conv.ID)
}

// ConversationTurns loads the session's conversation and returns its turns.
// Callers must only invoke this after HasConversation has reported true.
func (a ScorecardStoreAdapter) ConversationTurns(
	ctx context.Context, id domain.SessionID,
) ([]domain.ConversationTurn, error) {
	conv, err := a.Store.ConversationForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.Store.LoadConversationSnapshot(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	return snapshot.Turns, nil
}

// ConversationMessages loads the session's conversation and returns its
// messages. Callers must only invoke this after HasConversation has reported
// true.
func (a ScorecardStoreAdapter) ConversationMessages(
	ctx context.Context, id domain.SessionID,
) ([]domain.ConversationMessage, error) {
	conv, err := a.Store.ConversationForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.Store.LoadConversationSnapshot(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	return snapshot.Messages, nil
}

// GetProject delegates to the durable store.
func (a ScorecardStoreAdapter) GetProject(
	ctx context.Context, id string,
) (domain.ProjectRecord, bool, error) {
	return a.Store.GetProject(ctx, id)
}

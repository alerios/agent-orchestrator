package usage

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type effortStore interface {
	GetSessionEffort(context.Context, domain.SessionID) (domain.SessionEffort, bool, error)
	SessionToolMix(context.Context, domain.SessionID) ([]domain.ToolMixEntry, error)
}

// EffortService reads the derived effort block and tool mix for one session.
type EffortService struct{ store effortStore }

// NewEffortService builds the read service.
func NewEffortService(store effortStore) *EffortService { return &EffortService{store: store} }

// Get returns the session's effort counters and tool mix. A session with no
// recorded telemetry returns found=false so callers render "unavailable"
// rather than a block of zeros.
func (s *EffortService) Get(
	ctx context.Context, id domain.SessionID,
) (domain.SessionEffort, []domain.ToolMixEntry, bool, error) {
	effort, found, err := s.store.GetSessionEffort(ctx, id)
	if err != nil || !found {
		return domain.SessionEffort{}, nil, false, err
	}
	mix, err := s.store.SessionToolMix(ctx, id)
	if err != nil {
		return domain.SessionEffort{}, nil, false, err
	}
	return effort, mix, true, nil
}

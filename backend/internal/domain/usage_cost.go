package domain

import (
	"fmt"
	"math"
)

// The estimated-cost derivation lives in the domain because two independent
// readers need byte-identical coverage semantics: the per-session usage summary
// service and the project-scoped roll-up read directly from storage. Storage
// cannot reach the service layer (the scorecard service already imports the
// store), so the shared rule has to sit below both. Every function here is pure
// arithmetic over domain aggregates.

// DeriveEstimatedCost turns one scope's raw SQL cost aggregate into the
// user-facing estimate. It returns nil rather than a zero estimate whenever the
// scope has nothing AO actually observed: no events at all, or a partial scope
// whose known lower bound is still zero. An unmeasured scope must read as
// unknown, never as $0.
func DeriveEstimatedCost(raw UsageCostAggregate) (*EstimatedCost, error) {
	if err := validateUsageCostAggregate(raw); err != nil {
		return nil, err
	}
	if raw.EventCount == 0 {
		return nil, nil
	}
	coverage := EstimatedCostCoverageComplete
	total := raw.PricedTotalNanos
	if raw.PricedEventCount != raw.EventCount {
		coverage = EstimatedCostCoveragePartial
		var err error
		for _, component := range []struct {
			name  string
			value int64
		}{
			{"input cost", raw.UnpricedKnownInputNanos},
			{"cached input cost", raw.UnpricedKnownCachedInputNanos},
			{"output cost", raw.UnpricedKnownOutputNanos},
		} {
			total, err = CheckedUsageAdd(component.name, total, component.value)
			if err != nil {
				return nil, err
			}
		}
		if total == 0 {
			return nil, nil
		}
	}
	providerAttribution, err := estimatedCostProviderAttribution(raw)
	if err != nil {
		return nil, err
	}
	return &EstimatedCost{
		TotalNanos:          total,
		InputNanos:          knownComponent(raw.EventCount, raw.KnownInputCount, raw.KnownInputNanos),
		CachedInputNanos:    knownComponent(raw.EventCount, raw.KnownCachedInputCount, raw.KnownCachedInputNanos),
		OutputNanos:         knownComponent(raw.EventCount, raw.KnownOutputCount, raw.KnownOutputNanos),
		Coverage:            coverage,
		ProviderAttribution: providerAttribution,
	}, nil
}

// DeriveUsageMetricTotals assembles one scope's metric block from already
// null-corrected token counters and its raw cost aggregate. ProcessedTokens
// stays nil unless both halves are known, because a half-known sum would
// under-report rather than abstain.
func DeriveUsageMetricTotals(tokens UsageTokenMetrics, cost UsageCostAggregate) (UsageMetricTotals, error) {
	estimate, err := DeriveEstimatedCost(cost)
	if err != nil {
		return UsageMetricTotals{}, err
	}
	totals := UsageMetricTotals{
		InputTokens:         tokens.InputTokens,
		CachedInputTokens:   tokens.CachedInputTokens,
		UncachedInputTokens: tokens.UncachedInputTokens,
		OutputTokens:        tokens.OutputTokens,
		EstimatedCost:       estimate,
	}
	if tokens.InputTokens != nil && tokens.OutputTokens != nil {
		processed, err := CheckedUsageAdd("processed tokens", *tokens.InputTokens, *tokens.OutputTokens)
		if err != nil {
			return UsageMetricTotals{}, err
		}
		totals.ProcessedTokens = &processed
	}
	return totals, nil
}

// MergeUsageCostAggregate adds src into dst with overflow and invariant checks.
func MergeUsageCostAggregate(dst *UsageCostAggregate, src UsageCostAggregate) error {
	if err := validateUsageCostAggregate(src); err != nil {
		return err
	}
	fields := []struct {
		name string
		dst  *int64
		src  int64
	}{
		{"cost event count", &dst.EventCount, src.EventCount},
		{"priced event count", &dst.PricedEventCount, src.PricedEventCount},
		{"priced total cost", &dst.PricedTotalNanos, src.PricedTotalNanos},
		{"observed cost event count", &dst.ObservedCostEventCount, src.ObservedCostEventCount},
		{"inferred cost event count", &dst.InferredCostEventCount, src.InferredCostEventCount},
		{"known input count", &dst.KnownInputCount, src.KnownInputCount},
		{"known input cost", &dst.KnownInputNanos, src.KnownInputNanos},
		{"unpriced known input cost", &dst.UnpricedKnownInputNanos, src.UnpricedKnownInputNanos},
		{"known cached input count", &dst.KnownCachedInputCount, src.KnownCachedInputCount},
		{"known cached input cost", &dst.KnownCachedInputNanos, src.KnownCachedInputNanos},
		{"unpriced known cached input cost", &dst.UnpricedKnownCachedInputNanos, src.UnpricedKnownCachedInputNanos},
		{"known output count", &dst.KnownOutputCount, src.KnownOutputCount},
		{"known output cost", &dst.KnownOutputNanos, src.KnownOutputNanos},
		{"unpriced known output cost", &dst.UnpricedKnownOutputNanos, src.UnpricedKnownOutputNanos},
	}
	for _, field := range fields {
		value, err := CheckedUsageAdd(field.name, *field.dst, field.src)
		if err != nil {
			return err
		}
		*field.dst = value
	}
	return nil
}

// CheckedUsageAdd adds two nonnegative usage counters, refusing overflow.
func CheckedUsageAdd(label string, left, right int64) (int64, error) {
	if left < 0 || right < 0 {
		return 0, fmt.Errorf("usage %s must be nonnegative", label)
	}
	if left > math.MaxInt64-right {
		return 0, fmt.Errorf("usage %s overflows int64", label)
	}
	return left + right, nil
}

func estimatedCostProviderAttribution(raw UsageCostAggregate) (EstimatedCostProviderAttribution, error) {
	switch {
	case raw.ObservedCostEventCount > 0 && raw.InferredCostEventCount > 0:
		return EstimatedCostProviderAttributionMixed, nil
	case raw.InferredCostEventCount > 0:
		return EstimatedCostProviderAttributionInferred, nil
	case raw.ObservedCostEventCount > 0:
		return EstimatedCostProviderAttributionObserved, nil
	default:
		return "", fmt.Errorf("usage estimated cost has no provider attribution")
	}
}

func knownComponent(eventCount, knownCount, value int64) *int64 {
	if eventCount == knownCount {
		return &value
	}
	return nil
}

func validateUsageCostAggregate(raw UsageCostAggregate) error {
	values := []struct {
		name  string
		value int64
	}{
		{"event count", raw.EventCount}, {"priced event count", raw.PricedEventCount}, {"priced total cost", raw.PricedTotalNanos},
		{"observed cost event count", raw.ObservedCostEventCount}, {"inferred cost event count", raw.InferredCostEventCount},
		{"known input count", raw.KnownInputCount}, {"known input cost", raw.KnownInputNanos}, {"unpriced known input cost", raw.UnpricedKnownInputNanos},
		{"known cached input count", raw.KnownCachedInputCount}, {"known cached input cost", raw.KnownCachedInputNanos}, {"unpriced known cached input cost", raw.UnpricedKnownCachedInputNanos},
		{"known output count", raw.KnownOutputCount}, {"known output cost", raw.KnownOutputNanos}, {"unpriced known output cost", raw.UnpricedKnownOutputNanos},
	}
	for _, item := range values {
		if item.value < 0 {
			return fmt.Errorf("usage %s must be nonnegative", item.name)
		}
	}
	if raw.PricedEventCount > raw.EventCount || raw.KnownInputCount > raw.EventCount ||
		raw.KnownCachedInputCount > raw.EventCount || raw.KnownOutputCount > raw.EventCount ||
		raw.ObservedCostEventCount > raw.EventCount || raw.InferredCostEventCount > raw.EventCount ||
		raw.InferredCostEventCount > raw.EventCount-raw.ObservedCostEventCount {
		return fmt.Errorf("usage cost coverage count exceeds event count")
	}
	if raw.UnpricedKnownInputNanos > raw.KnownInputNanos ||
		raw.UnpricedKnownCachedInputNanos > raw.KnownCachedInputNanos ||
		raw.UnpricedKnownOutputNanos > raw.KnownOutputNanos {
		return fmt.Errorf("usage unpriced component cost exceeds known component cost")
	}
	return nil
}

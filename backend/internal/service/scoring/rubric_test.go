package scoring

import (
	"testing"
)

func TestFactorWeightsCoverEveryFactor(t *testing.T) {
	factors := []Factor{
		FactorTokenEfficiency,
		FactorExploratoryOverhead,
		FactorSteeringLoad,
		FactorDeliveryEfficiency,
		FactorGovernance,
	}
	for _, factor := range factors {
		if WeightForTest(factor) <= 0 {
			t.Errorf("weight for %q = 0, want a positive weight", factor)
		}
	}
}

func TestRubricVersionIsSet(t *testing.T) {
	if RubricVersion == "" {
		t.Fatal("RubricVersion is empty")
	}
}

package scoring

// WeightForTest exposes the unexported weight table to the package's
// external test file.
func WeightForTest(f Factor) float64 { return factorWeights[f] }

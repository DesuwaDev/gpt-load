package degradation

// VerdictReason explains why an attribution was judged degraded.
type VerdictReason string

const (
	// ReasonLowProbability means the expected label held the top spot but below
	// the configured confidence floor.
	ReasonLowProbability VerdictReason = "low_probability"
	// ReasonModelMismatch means a different enrolled label scored higher.
	ReasonModelMismatch VerdictReason = "model_mismatch"
	// ReasonExpectedNotEnrolled means the expected label is absent from the bank,
	// so the run cannot be judged.
	ReasonExpectedNotEnrolled VerdictReason = "expected_not_enrolled"
)

// Rule is the operator-configured pass condition for one monitored target.
type Rule struct {
	// ExpectedModel is the enrolled bank label the target is supposed to be.
	ExpectedModel string
	// MinProbability is the attribution floor, expressed as a fraction in (0, 1].
	MinProbability float64
}

// Verdict is the judged outcome of one attribution.
type Verdict struct {
	Degraded            bool            `json:"degraded"`
	Conclusive          bool            `json:"conclusive"`
	Reasons             []VerdictReason `json:"reasons"`
	ExpectedModel       string          `json:"expected_model"`
	ExpectedProbability float64         `json:"expected_probability"`
	LeadingModel        string          `json:"leading_model"`
	LeadingProbability  float64         `json:"leading_probability"`
	MinProbability      float64         `json:"min_probability"`
}

// Evaluate judges an attribution against a rule. A target is degraded when its
// expected label falls below the floor, or when any other enrolled label
// outscores it — the second condition is what catches a silent swap to a
// cheaper sibling that still clears the floor.
func Evaluate(attribution Attribution, rule Rule) Verdict {
	verdict := Verdict{
		ExpectedModel:      rule.ExpectedModel,
		LeadingModel:       attribution.Prediction,
		LeadingProbability: attribution.Probability,
		MinProbability:     rule.MinProbability,
	}
	expected, enrolled := attribution.ProbabilityOf(rule.ExpectedModel)
	if !enrolled {
		verdict.Reasons = []VerdictReason{ReasonExpectedNotEnrolled}
		return verdict
	}
	verdict.Conclusive = true
	verdict.ExpectedProbability = expected
	if attribution.Prediction != rule.ExpectedModel && attribution.Probability > expected {
		verdict.Degraded = true
		verdict.Reasons = append(verdict.Reasons, ReasonModelMismatch)
	}
	if expected < rule.MinProbability {
		verdict.Degraded = true
		verdict.Reasons = append(verdict.Reasons, ReasonLowProbability)
	}
	return verdict
}

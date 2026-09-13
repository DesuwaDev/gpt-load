package degradation

import (
	"errors"
	"math"
	"sort"
)

// ErrNoUsableSample means every response was a refusal, a truncation, or was
// otherwise too short to score.
var ErrNoUsableSample = errors.New("degradation: no response carried enough numbers to score")

// Sample is one collected response together with the length that was asked for.
type Sample struct {
	Text          string
	ExpectedCount int
}

// SampleDiagnostic explains why a response was kept or dropped.
type SampleDiagnostic struct {
	Index          int  `json:"index"`
	ParsedNumbers  int  `json:"parsed_numbers"`
	MinimumNumbers int  `json:"minimum_numbers"`
	Accepted       bool `json:"accepted"`
}

// ModelScore is one enrolled label's standing for an attribution.
type ModelScore struct {
	Model             string  `json:"model"`
	DisplayName       string  `json:"display_name"`
	Family            string  `json:"family"`
	Probability       float64 `json:"probability"`
	ProfileSimilarity float64 `json:"profile_similarity"`
	Score             float64 `json:"score"`
}

// FamilyScore is the rolled-up standing of a model family.
type FamilyScore struct {
	Family      string  `json:"family"`
	DisplayName string  `json:"display_name"`
	Probability float64 `json:"probability"`
}

// Attribution is the outcome of scoring one or more responses.
type Attribution struct {
	Prediction         string             `json:"prediction"`
	PredictionName     string             `json:"prediction_name"`
	Probability        float64            `json:"probability"`
	UsedSamples        int                `json:"used_samples"`
	Results            []ModelScore       `json:"results"`
	Families           []FamilyScore      `json:"families"`
	Diagnostics        []SampleDiagnostic `json:"diagnostics"`
	CalibrationQueries int                `json:"calibration_queries"`
	CalibrationBeta    float64            `json:"calibration_beta"`
	CalibrationCVAcc   float64            `json:"calibration_cv_accuracy"`
	Method             string             `json:"method"`
}

// ProbabilityOf returns the attributed probability for an enrolled label.
func (a Attribution) ProbabilityOf(model string) (float64, bool) {
	for _, result := range a.Results {
		if result.Model == model {
			return result.Probability, true
		}
	}
	return 0, false
}

// minimumNumbers is the acceptance floor for one response. A response that only
// partially answers is still usable as long as it carries enough of the run.
func minimumNumbers(bank *Bank, expectedCount int) int {
	floor := bank.MinimumValidNumbers
	if expectedCount > 0 {
		if scaled := int(math.Ceil(float64(expectedCount) * 0.55)); scaled > floor {
			return scaled
		}
	}
	return floor
}

// Analyze scores collected responses against the bank. Diagnostics are returned
// even when nothing was usable, so callers can explain the failure.
func (b *Bank) Analyze(samples []Sample) (Attribution, error) {
	diagnostics := make([]SampleDiagnostic, 0, len(samples))
	models := len(b.Models)
	combined := make([]float64, models)
	pooled := make([]int, Dimension)
	used := 0
	for index, sample := range samples {
		numbers := ParseNumbers(sample.Text)
		minimum := minimumNumbers(b, sample.ExpectedCount)
		accepted := len(numbers) >= minimum
		diagnostics = append(diagnostics, SampleDiagnostic{
			Index:          index,
			ParsedNumbers:  len(numbers),
			MinimumNumbers: minimum,
			Accepted:       accepted,
		})
		if !accepted {
			continue
		}
		used++
		for model, score := range b.sampleScores(numbers) {
			combined[model] += score
		}
		for _, value := range numbers {
			pooled[value-valueMin]++
		}
	}
	if used == 0 {
		return Attribution{Diagnostics: diagnostics, Method: b.MethodName()}, ErrNoUsableSample
	}
	for model := range combined {
		combined[model] /= float64(used)
	}

	queries, calibration := b.calibrationFor(used)
	weighted := make([]float64, models)
	for model, score := range combined {
		weighted[model] = calibration.Beta * score
	}
	probabilities := softmax(weighted)

	results := make([]ModelScore, models)
	for index, model := range b.Models {
		results[index] = ModelScore{
			Model:             model.ID,
			DisplayName:       model.DisplayName,
			Family:            model.Family,
			Probability:       probabilities[index],
			ProfileSimilarity: jsSimilarity(pooled, model.Counts),
			Score:             combined[index],
		}
	}
	sort.SliceStable(results, func(left, right int) bool {
		return results[left].Probability > results[right].Probability
	})

	return Attribution{
		Prediction:         results[0].Model,
		PredictionName:     results[0].DisplayName,
		Probability:        results[0].Probability,
		UsedSamples:        used,
		Results:            results,
		Families:           familyScores(b, results),
		Diagnostics:        diagnostics,
		CalibrationQueries: queries,
		CalibrationBeta:    calibration.Beta,
		CalibrationCVAcc:   calibration.CVAccuracy,
		Method:             b.MethodName(),
	}, nil
}

func familyScores(bank *Bank, results []ModelScore) []FamilyScore {
	order := make([]string, 0, 2)
	names := map[string]string{}
	for _, model := range bank.Models {
		family := model.Family
		if family == "" {
			family = "models"
		}
		if _, seen := names[family]; !seen {
			order = append(order, family)
			names[family] = family
		}
		if model.FamilyName != "" {
			names[family] = model.FamilyName
		}
	}
	totals := map[string]float64{}
	for _, result := range results {
		family := result.Family
		if family == "" {
			family = "models"
		}
		totals[family] += result.Probability
	}
	families := make([]FamilyScore, 0, len(order))
	for _, family := range order {
		families = append(families, FamilyScore{
			Family:      family,
			DisplayName: names[family],
			Probability: totals[family],
		})
	}
	sort.SliceStable(families, func(left, right int) bool {
		return families[left].Probability > families[right].Probability
	})
	return families
}

package degradation_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"testing"

	"gpt-load/internal/degradation"
)

// golden.json is produced by a pure-Python transcription of ModelTrace's
// fingerprint.py run against the embedded bank, so these cases pin the Go port
// to the original math rather than to itself.
type goldenFile struct {
	Cases []struct {
		Name    string `json:"name"`
		Samples []struct {
			Text          string `json:"text"`
			ExpectedCount int    `json:"expected_count"`
		} `json:"samples"`
		Expected struct {
			UsedSamples        int     `json:"used_samples"`
			CalibrationQueries int     `json:"calibration_queries"`
			CalibrationBeta    float64 `json:"calibration_beta"`
			Diagnostics        []struct {
				Index          int  `json:"index"`
				ParsedNumbers  int  `json:"parsed_numbers"`
				MinimumNumbers int  `json:"minimum_numbers"`
				Accepted       bool `json:"accepted"`
			} `json:"diagnostics"`
			Results []struct {
				Model             string  `json:"model"`
				Probability       float64 `json:"probability"`
				ProfileSimilarity float64 `json:"profile_similarity"`
				Score             float64 `json:"score"`
			} `json:"results"`
		} `json:"expected"`
	} `json:"cases"`
	Parsing []struct {
		Text     string `json:"text"`
		Expected []int  `json:"expected"`
	} `json:"parsing"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	payload, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	golden := goldenFile{}
	if err := json.Unmarshal(payload, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func loadBank(t *testing.T) *degradation.Bank {
	t.Helper()
	bank, err := degradation.EmbeddedBank()
	if err != nil {
		t.Fatal(err)
	}
	return bank
}

func TestEmbeddedBankShape(t *testing.T) {
	t.Parallel()
	bank := loadBank(t)
	if len(bank.Models) != 13 {
		t.Fatalf("bank carries %d models, want 13", len(bank.Models))
	}
	if _, ok := bank.Model("gpt-6-astra"); !ok {
		t.Fatal("bank is missing gpt-6-astra")
	}
	if bank.MinimumValidNumbers != 80 {
		t.Fatalf("minimum valid numbers = %d, want 80", bank.MinimumValidNumbers)
	}
}

func TestParseNumbersMatchesReference(t *testing.T) {
	t.Parallel()
	for _, test := range loadGolden(t).Parsing {
		if parsed := degradation.ParseNumbers(test.Text); !slices.Equal(parsed, test.Expected) {
			t.Errorf("ParseNumbers(%q) = %v, want %v", test.Text, parsed, test.Expected)
		}
	}
}

func TestAnalyzeMatchesReference(t *testing.T) {
	t.Parallel()
	bank := loadBank(t)
	for _, test := range loadGolden(t).Cases {
		t.Run(test.Name, func(t *testing.T) {
			samples := make([]degradation.Sample, 0, len(test.Samples))
			for _, sample := range test.Samples {
				samples = append(samples, degradation.Sample{Text: sample.Text, ExpectedCount: sample.ExpectedCount})
			}
			attribution, err := bank.Analyze(samples)
			if test.Expected.UsedSamples == 0 {
				if !errors.Is(err, degradation.ErrNoUsableSample) {
					t.Fatalf("err = %v, want ErrNoUsableSample", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if attribution.UsedSamples != test.Expected.UsedSamples {
				t.Fatalf("used samples = %d, want %d", attribution.UsedSamples, test.Expected.UsedSamples)
			}
			if attribution.CalibrationQueries != test.Expected.CalibrationQueries {
				t.Fatalf("calibration queries = %d, want %d", attribution.CalibrationQueries, test.Expected.CalibrationQueries)
			}
			if math.Abs(attribution.CalibrationBeta-test.Expected.CalibrationBeta) > 1e-12 {
				t.Fatalf("calibration beta = %v, want %v", attribution.CalibrationBeta, test.Expected.CalibrationBeta)
			}
			if len(attribution.Diagnostics) != len(test.Expected.Diagnostics) {
				t.Fatalf("diagnostics = %d, want %d", len(attribution.Diagnostics), len(test.Expected.Diagnostics))
			}
			for index, want := range test.Expected.Diagnostics {
				got := attribution.Diagnostics[index]
				if got.ParsedNumbers != want.ParsedNumbers || got.MinimumNumbers != want.MinimumNumbers || got.Accepted != want.Accepted {
					t.Fatalf("diagnostic %d = %+v, want %+v", index, got, want)
				}
			}
			if len(attribution.Results) != len(test.Expected.Results) {
				t.Fatalf("results = %d, want %d", len(attribution.Results), len(test.Expected.Results))
			}
			var probabilityTotal float64
			for index, want := range test.Expected.Results {
				got := attribution.Results[index]
				if got.Model != want.Model {
					t.Fatalf("result %d is %q, want %q", index, got.Model, want.Model)
				}
				if math.Abs(got.Probability-want.Probability) > 1e-9 {
					t.Errorf("%s probability = %v, want %v", got.Model, got.Probability, want.Probability)
				}
				if math.Abs(got.Score-want.Score) > 1e-9 {
					t.Errorf("%s score = %v, want %v", got.Model, got.Score, want.Score)
				}
				if math.Abs(got.ProfileSimilarity-want.ProfileSimilarity) > 1e-9 {
					t.Errorf("%s similarity = %v, want %v", got.Model, got.ProfileSimilarity, want.ProfileSimilarity)
				}
				probabilityTotal += got.Probability
			}
			if math.Abs(probabilityTotal-1) > 1e-9 {
				t.Fatalf("probabilities sum to %v", probabilityTotal)
			}
			if attribution.Prediction != test.Expected.Results[0].Model {
				t.Fatalf("prediction = %q, want %q", attribution.Prediction, test.Expected.Results[0].Model)
			}
		})
	}
}

func TestGenerateChallenges(t *testing.T) {
	t.Parallel()
	challenges, err := degradation.GenerateChallenges(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(challenges) != 3 {
		t.Fatalf("generated %d challenges, want 3", len(challenges))
	}
	seen := map[int]bool{}
	for _, challenge := range challenges {
		if challenge.ExpectedCount < 292 || challenge.ExpectedCount > 332 {
			t.Fatalf("expected count %d out of range", challenge.ExpectedCount)
		}
		if seen[challenge.ExpectedCount] {
			t.Fatalf("duplicate length %d", challenge.ExpectedCount)
		}
		seen[challenge.ExpectedCount] = true
		if challenge.ID == "" || challenge.Prompt == "" {
			t.Fatal("challenge is missing an id or prompt")
		}
	}
	if capped, err := degradation.GenerateChallenges(99); err != nil {
		t.Fatal(err)
	} else if len(capped) != degradation.MaxSamplesPerRun {
		t.Fatalf("capped to %d challenges, want %d", len(capped), degradation.MaxSamplesPerRun)
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	attribution := degradation.Attribution{
		Prediction:  "gpt-5.6-luna",
		Probability: 0.62,
		Results: []degradation.ModelScore{
			{Model: "gpt-5.6-luna", Probability: 0.62},
			{Model: "gpt-6-astra", Probability: 0.31},
		},
	}
	for _, test := range []struct {
		name       string
		attributed degradation.Attribution
		rule       degradation.Rule
		degraded   bool
		conclusive bool
		reasons    []degradation.VerdictReason
	}{
		{
			name:       "another model leads",
			attributed: attribution,
			rule:       degradation.Rule{ExpectedModel: "gpt-6-astra", MinProbability: 0.75},
			degraded:   true,
			conclusive: true,
			reasons:    []degradation.VerdictReason{degradation.ReasonModelMismatch, degradation.ReasonLowProbability},
		},
		{
			name: "expected model leads but below the floor",
			attributed: degradation.Attribution{
				Prediction:  "gpt-6-astra",
				Probability: 0.6,
				Results:     []degradation.ModelScore{{Model: "gpt-6-astra", Probability: 0.6}},
			},
			rule:       degradation.Rule{ExpectedModel: "gpt-6-astra", MinProbability: 0.75},
			degraded:   true,
			conclusive: true,
			reasons:    []degradation.VerdictReason{degradation.ReasonLowProbability},
		},
		{
			name: "healthy",
			attributed: degradation.Attribution{
				Prediction:  "gpt-6-astra",
				Probability: 0.92,
				Results:     []degradation.ModelScore{{Model: "gpt-6-astra", Probability: 0.92}},
			},
			rule:       degradation.Rule{ExpectedModel: "gpt-6-astra", MinProbability: 0.75},
			conclusive: true,
		},
		{
			name:       "expected model is not enrolled",
			attributed: attribution,
			rule:       degradation.Rule{ExpectedModel: "mystery-model", MinProbability: 0.75},
			reasons:    []degradation.VerdictReason{degradation.ReasonExpectedNotEnrolled},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			verdict := degradation.Evaluate(test.attributed, test.rule)
			if verdict.Degraded != test.degraded {
				t.Fatalf("degraded = %v, want %v", verdict.Degraded, test.degraded)
			}
			if verdict.Conclusive != test.conclusive {
				t.Fatalf("conclusive = %v, want %v", verdict.Conclusive, test.conclusive)
			}
			if !slices.Equal(verdict.Reasons, test.reasons) {
				t.Fatalf("reasons = %v, want %v", verdict.Reasons, test.reasons)
			}
		})
	}
}

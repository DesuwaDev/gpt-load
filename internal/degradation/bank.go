// Package degradation implements the model attribution ("降智") detection used by
// the monitor surface: it asks a credential for a long run of unconstrained
// integers and scores the resulting distribution against a reference bank.
//
// The statistical method and bankdata/unified_bank.json are ported from
// ModelTrace (https://github.com/xqy2006/ModelTrace), MIT licensed,
// Copyright (c) 2026 xqy2006. See bankdata/LICENSE for the original terms.
package degradation

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

const (
	valueMin = 1
	valueMax = 355
	// Dimension is the size of the marginal count vector, one slot per legal value.
	Dimension = valueMax - valueMin + 1
	alpha     = 0.5

	orderedBlockChunks = 4
	orderedBlockBins   = 16
	lastDigitBins      = 10
	// orderedFeatureDimension is four positional blocks of value bins plus the
	// final-digit distribution.
	orderedFeatureDimension = orderedBlockChunks*orderedBlockBins + lastDigitBins

	// MaxCalibratedSamples is the largest sample count the bank calibrates for;
	// runs beyond it reuse the same beta.
	MaxCalibratedSamples = 3
)

//go:embed bankdata/unified_bank.json
var bankFiles embed.FS

// BankModel is one enrolled reference model.
type BankModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Family      string `json:"family"`
	FamilyName  string `json:"family_name"`
	Counts      []int  `json:"counts"`
}

// Calibration maps a sample count to the softmax temperature validated for it.
type Calibration struct {
	Beta       float64 `json:"beta"`
	CVAccuracy float64 `json:"cv_accuracy"`
}

// HellingerArtifact scores the order-independent value histogram.
type HellingerArtifact struct {
	FeatureMean   []float64   `json:"feature_mean"`
	FeatureScale  []float64   `json:"feature_scale"`
	NuisanceBasis [][]float64 `json:"nuisance_basis"`
	Centroids     [][]float64 `json:"centroids"`
}

// OrderedBlockArtifact scores positional structure within the emitted run.
type OrderedBlockArtifact struct {
	Weight               float64       `json:"weight"`
	FeatureMean          []float64     `json:"feature_mean"`
	FeatureScale         []float64     `json:"feature_scale"`
	NuisanceBasis        [][]float64   `json:"nuisance_basis"`
	Centroids            [][]float64   `json:"centroids"`
	EnvironmentCentroids [][][]float64 `json:"environment_centroids"`
}

type robustArtifact struct {
	ModelOrder    []string             `json:"model_order"`
	Hellinger     HellingerArtifact    `json:"hellinger"`
	OrderedBlocks OrderedBlockArtifact `json:"ordered_blocks"`
}

type bankMethod struct {
	Name string `json:"name"`
}

// Bank is the reference fingerprint bank.
type Bank struct {
	Schema              string                 `json:"schema"`
	BuiltAt             string                 `json:"built_at"`
	Method              bankMethod             `json:"method"`
	RecommendedQueries  int                    `json:"recommended_queries"`
	MinimumValidNumbers int                    `json:"minimum_valid_numbers"`
	Models              []BankModel            `json:"models"`
	Robust              robustArtifact         `json:"robust"`
	Calibration         map[string]Calibration `json:"calibration"`
}

var embeddedBank = sync.OnceValues(func() (*Bank, error) {
	payload, err := bankFiles.ReadFile("bankdata/unified_bank.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded bank: %w", err)
	}
	return ParseBank(payload)
})

// EmbeddedBank returns the bank compiled into the binary. The result is shared
// and must be treated as read-only.
func EmbeddedBank() (*Bank, error) {
	return embeddedBank()
}

// ParseBank decodes and validates a bank payload. Every dimension is checked up
// front so a mismatched bank fails loudly instead of scoring garbage.
func ParseBank(payload []byte) (*Bank, error) {
	bank := &Bank{}
	if err := json.Unmarshal(payload, bank); err != nil {
		return nil, fmt.Errorf("decode bank: %w", err)
	}
	if err := bank.validate(); err != nil {
		return nil, err
	}
	return bank, nil
}

func (b *Bank) validate() error {
	models := len(b.Robust.ModelOrder)
	if models == 0 {
		return fmt.Errorf("bank: empty model order")
	}
	if len(b.Models) != models {
		return fmt.Errorf("bank: %d models for %d ordered labels", len(b.Models), models)
	}
	for index, model := range b.Models {
		if model.ID != b.Robust.ModelOrder[index] {
			return fmt.Errorf("bank: model %d is %q but the order says %q", index, model.ID, b.Robust.ModelOrder[index])
		}
		if len(model.Counts) != Dimension {
			return fmt.Errorf("bank: model %q has %d counts, want %d", model.ID, len(model.Counts), Dimension)
		}
	}
	if err := validateArtifact("hellinger", Dimension, models,
		b.Robust.Hellinger.FeatureMean, b.Robust.Hellinger.FeatureScale,
		b.Robust.Hellinger.NuisanceBasis, b.Robust.Hellinger.Centroids); err != nil {
		return err
	}
	if err := validateArtifact("ordered_blocks", orderedFeatureDimension, models,
		b.Robust.OrderedBlocks.FeatureMean, b.Robust.OrderedBlocks.FeatureScale,
		b.Robust.OrderedBlocks.NuisanceBasis, b.Robust.OrderedBlocks.Centroids); err != nil {
		return err
	}
	for index, environment := range b.Robust.OrderedBlocks.EnvironmentCentroids {
		if err := validateMatrix(fmt.Sprintf("ordered_blocks.environment_centroids[%d]", index), environment, models, orderedFeatureDimension); err != nil {
			return err
		}
	}
	for _, scale := range [][]float64{b.Robust.Hellinger.FeatureScale, b.Robust.OrderedBlocks.FeatureScale} {
		for index, value := range scale {
			if value == 0 {
				return fmt.Errorf("bank: feature scale %d is zero", index)
			}
		}
	}
	for _, key := range []string{"1", "2", "3"} {
		calibration, ok := b.Calibration[key]
		if !ok {
			return fmt.Errorf("bank: missing calibration for %s queries", key)
		}
		if calibration.Beta <= 0 {
			return fmt.Errorf("bank: calibration %s has non-positive beta", key)
		}
	}
	if b.MinimumValidNumbers <= 0 {
		b.MinimumValidNumbers = 80
	}
	if b.RecommendedQueries <= 0 {
		b.RecommendedQueries = MaxCalibratedSamples
	}
	return nil
}

func validateArtifact(name string, dimension, models int, mean, scale []float64, basis, centroids [][]float64) error {
	if len(mean) != dimension {
		return fmt.Errorf("bank: %s.feature_mean has %d entries, want %d", name, len(mean), dimension)
	}
	if len(scale) != dimension {
		return fmt.Errorf("bank: %s.feature_scale has %d entries, want %d", name, len(scale), dimension)
	}
	for index, row := range basis {
		if len(row) != dimension {
			return fmt.Errorf("bank: %s.nuisance_basis[%d] has %d entries, want %d", name, index, len(row), dimension)
		}
	}
	return validateMatrix(name+".centroids", centroids, models, dimension)
}

func validateMatrix(name string, matrix [][]float64, rows, columns int) error {
	if len(matrix) != rows {
		return fmt.Errorf("bank: %s has %d rows, want %d", name, len(matrix), rows)
	}
	for index, row := range matrix {
		if len(row) != columns {
			return fmt.Errorf("bank: %s[%d] has %d entries, want %d", name, index, len(row), columns)
		}
	}
	return nil
}

// ModelIDs returns the enrolled labels in bank order.
func (b *Bank) ModelIDs() []string {
	ids := make([]string, len(b.Models))
	for index, model := range b.Models {
		ids[index] = model.ID
	}
	return ids
}

// Model looks up an enrolled label.
func (b *Bank) Model(id string) (BankModel, bool) {
	for _, model := range b.Models {
		if model.ID == id {
			return model, true
		}
	}
	return BankModel{}, false
}

// MethodName is the human-readable scoring method recorded by the bank.
func (b *Bank) MethodName() string {
	if b.Method.Name == "" {
		return "Ordered-block + nuisance-Hellinger"
	}
	return b.Method.Name
}

// calibrationFor returns the temperature validated for the given sample count,
// clamped to the calibrated range.
func (b *Bank) calibrationFor(samples int) (int, Calibration) {
	key := samples
	if key < 1 {
		key = 1
	}
	if key > MaxCalibratedSamples {
		key = MaxCalibratedSamples
	}
	return key, b.Calibration[fmt.Sprint(key)]
}

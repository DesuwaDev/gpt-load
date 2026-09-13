package degradation

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var digitRun = regexp.MustCompile(`[0-9]+`)

// ParseNumbers extracts the longest run of in-range integers from a response.
// Alphabetic separators end a run, which keeps prose preambles and trailing
// commentary from merging into the sampled sequence. Out-of-range values are
// dropped without breaking the run they sit in.
func ParseNumbers(text string) []int {
	var runs [][]int
	var current []int
	previousEnd := 0
	for _, match := range digitRun.FindAllStringIndex(text, -1) {
		separator := text[previousEnd:match[0]]
		if len(current) > 0 && strings.ContainsFunc(separator, unicode.IsLetter) {
			runs = append(runs, current)
			current = nil
		}
		if value, err := strconv.Atoi(text[match[0]:match[1]]); err == nil && value >= valueMin && value <= valueMax {
			current = append(current, value)
		}
		previousEnd = match[1]
	}
	if len(current) > 0 {
		runs = append(runs, current)
	}
	longest := []int{}
	for _, run := range runs {
		if len(run) > len(longest) {
			longest = run
		}
	}
	return longest
}

// CountNumbers builds the marginal value histogram.
func CountNumbers(numbers []int) []int {
	counts := make([]int, Dimension)
	for _, number := range numbers {
		if number >= valueMin && number <= valueMax {
			counts[number-valueMin]++
		}
	}
	return counts
}

// standardize converts a score vector to z-scores. The scale floor keeps a
// degenerate vector from producing infinities.
func standardize(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	var total float64
	for _, value := range values {
		total += value
	}
	mean := total / float64(len(values))
	var variance float64
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	variance /= float64(len(values))
	scale := math.Max(math.Sqrt(variance), 1e-12)
	result := make([]float64, len(values))
	for index, value := range values {
		result[index] = (value - mean) / scale
	}
	return result
}

func hellingerFeature(counts []int) []float64 {
	feature := make([]float64, len(counts))
	var total float64
	for index, count := range counts {
		feature[index] = float64(count) + alpha
		total += feature[index]
	}
	for index := range feature {
		feature[index] = math.Sqrt(feature[index] / total)
	}
	return feature
}

// splitSections mirrors numpy.array_split: the first Ntotal%sections chunks take
// one extra element.
func splitSections(total, sections int) [][2]int {
	size, extras := total/sections, total%sections
	bounds := make([][2]int, 0, sections)
	start := 0
	for index := range sections {
		length := size
		if index < extras {
			length++
		}
		bounds = append(bounds, [2]int{start, start + length})
		start += length
	}
	return bounds
}

// valueBin maps a value onto one of the 16 equal-width bins spanning [1, 356).
// Bin edges are exact in binary (355/16 = 22.1875) and no legal integer lands on
// an interior edge, so plain truncation reproduces numpy.histogram here.
func valueBin(value int) int {
	position := int((float64(value) - 1.0) / 355.0 * float64(orderedBlockBins))
	if position < 0 {
		return 0
	}
	if position >= orderedBlockBins {
		return orderedBlockBins - 1
	}
	return position
}

func smoothedRoot(counts []float64) []float64 {
	var total float64
	for index := range counts {
		counts[index] += 0.5
		total += counts[index]
	}
	for index := range counts {
		counts[index] = math.Sqrt(counts[index] / total)
	}
	return counts
}

func orderedBlockFeature(numbers []int) []float64 {
	feature := make([]float64, 0, orderedFeatureDimension)
	for _, bounds := range splitSections(len(numbers), orderedBlockChunks) {
		histogram := make([]float64, orderedBlockBins)
		for _, value := range numbers[bounds[0]:bounds[1]] {
			histogram[valueBin(value)]++
		}
		feature = append(feature, smoothedRoot(histogram)...)
	}
	digits := make([]float64, lastDigitBins)
	for _, value := range numbers {
		digits[value%lastDigitBins]++
	}
	return append(feature, smoothedRoot(digits)...)
}

func dot(left, right []float64) float64 {
	var total float64
	for index, value := range left {
		total += value * right[index]
	}
	return total
}

func norm(values []float64) float64 {
	return math.Max(math.Sqrt(dot(values, values)), 1e-12)
}

func scaled(values []float64, divisor float64) []float64 {
	result := make([]float64, len(values))
	for index, value := range values {
		result[index] = value / divisor
	}
	return result
}

// removeNuisance projects out the shared environment subspace in place.
func removeNuisance(vector []float64, basis [][]float64) {
	if len(basis) == 0 {
		return
	}
	coefficients := make([]float64, len(basis))
	for index, row := range basis {
		coefficients[index] = dot(vector, row)
	}
	for column := range vector {
		var adjustment float64
		for index, row := range basis {
			adjustment += coefficients[index] * row[column]
		}
		vector[column] -= adjustment
	}
}

func standardizeFeature(feature, mean, scale []float64) []float64 {
	result := make([]float64, len(feature))
	for index, value := range feature {
		result[index] = (value - mean[index]) / scale[index]
	}
	return result
}

func centroidScores(vector []float64, centroids [][]float64) []float64 {
	scores := make([]float64, len(centroids))
	for index, centroid := range centroids {
		scores[index] = dot(vector, centroid)
	}
	return scores
}

// marginalScores scores the order-independent histogram against the bank.
func (b *Bank) marginalScores(counts []int) []float64 {
	artifact := b.Robust.Hellinger
	projected := standardizeFeature(hellingerFeature(counts), artifact.FeatureMean, artifact.FeatureScale)
	removeNuisance(projected, artifact.NuisanceBasis)
	nuisance := standardize(centroidScores(scaled(projected, norm(projected)), artifact.Centroids))
	return standardize(nuisance)
}

// orderedScores scores positional structure, taking the best-matching
// environment template and fusing it with the nuisance-projected score.
func (b *Bank) orderedScores(numbers []int) []float64 {
	artifact := b.Robust.OrderedBlocks
	feature := standardizeFeature(orderedBlockFeature(numbers), artifact.FeatureMean, artifact.FeatureScale)

	normalized := scaled(feature, norm(feature))
	template := make([]float64, len(artifact.Centroids))
	for index, environment := range artifact.EnvironmentCentroids {
		scores := centroidScores(normalized, environment)
		for model, score := range scores {
			if index == 0 || score > template[model] {
				template[model] = score
			}
		}
	}
	template = standardize(template)

	projected := make([]float64, len(feature))
	copy(projected, feature)
	removeNuisance(projected, artifact.NuisanceBasis)
	nuisance := standardize(centroidScores(scaled(projected, norm(projected)), artifact.Centroids))

	fused := make([]float64, len(template))
	for index := range fused {
		fused[index] = 0.5*template[index] + 0.5*nuisance[index]
	}
	return standardize(fused)
}

// sampleScores fuses the marginal and ordered-block views for one response.
func (b *Bank) sampleScores(numbers []int) []float64 {
	marginal := b.marginalScores(CountNumbers(numbers))
	weight := b.Robust.OrderedBlocks.Weight
	if weight == 0 || len(b.Robust.OrderedBlocks.Centroids) == 0 {
		return marginal
	}
	ordered := b.orderedScores(numbers)
	fused := make([]float64, len(marginal))
	for index := range fused {
		fused[index] = (1.0-weight)*marginal[index] + weight*ordered[index]
	}
	return fused
}

func softmax(values []float64) []float64 {
	maximum := math.Inf(-1)
	for _, value := range values {
		maximum = math.Max(maximum, value)
	}
	weights := make([]float64, len(values))
	var total float64
	for index, value := range values {
		weights[index] = math.Exp(value - maximum)
		total += weights[index]
	}
	for index := range weights {
		weights[index] /= total
	}
	return weights
}

// jsSimilarity reports 1 minus the Jensen-Shannon distance between the observed
// pooled counts and an enrolled profile.
func jsSimilarity(observed, reference []int) float64 {
	var observedTotal, referenceTotal float64
	for _, value := range observed {
		observedTotal += float64(value)
	}
	for _, value := range reference {
		referenceTotal += float64(value)
	}
	referenceTotal += alpha * float64(Dimension)
	if observedTotal == 0 || referenceTotal == 0 {
		return 0
	}
	var divergence float64
	for index := range observed {
		left := float64(observed[index]) / observedTotal
		right := (float64(reference[index]) + alpha) / referenceTotal
		middle := (left + right) / 2.0
		if left > 0 {
			divergence += left * math.Log(left/middle)
		}
		if right > 0 {
			divergence += right * math.Log(right/middle)
		}
	}
	divergence /= 2.0
	if divergence < 0 {
		divergence = 0
	}
	return 1.0 - math.Sqrt(divergence/math.Ln2)
}

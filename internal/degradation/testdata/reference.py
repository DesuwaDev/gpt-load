"""Pure-Python (no numpy) reference for ModelTrace's scoring pipeline.

Used once to mint internal/degradation/testdata/golden.json so the Go port can
be checked against the original math. Not part of the build.
"""
from __future__ import annotations

import json
import math
import random
import re
import sys
from pathlib import Path

VALUE_MIN, VALUE_MAX = 1, 355
DIMENSION = VALUE_MAX - VALUE_MIN + 1
ALPHA = 0.5


def parse_numbers(text):
    runs, current, previous_end = [], [], 0
    for match in re.finditer(r"[0-9]+", text):
        separator = text[previous_end:match.start()]
        value = int(match.group())
        if current and any(character.isalpha() for character in separator):
            runs.append(current)
            current = []
        if VALUE_MIN <= value <= VALUE_MAX:
            current.append(value)
        previous_end = match.end()
    if current:
        runs.append(current)
    return max(runs, key=len) if runs else []


def count_numbers(numbers):
    counts = [0] * DIMENSION
    for number in numbers:
        counts[number - VALUE_MIN] += 1
    return counts


def standardize(values):
    mean = sum(values) / len(values)
    variance = sum((value - mean) ** 2 for value in values) / len(values)
    scale = max(math.sqrt(variance), 1e-12)
    return [(value - mean) / scale for value in values]


def hellinger_feature(counts):
    values = [count + ALPHA for count in counts]
    total = sum(values)
    return [math.sqrt(value / total) for value in values]


def array_split(values, sections):
    size, extras = divmod(len(values), sections)
    chunks, start = [], 0
    for index in range(sections):
        length = size + (1 if index < extras else 0)
        chunks.append(values[start:start + length])
        start += length
    return chunks


def smoothed_root(counts):
    values = [count + 0.5 for count in counts]
    total = sum(values)
    return [math.sqrt(value / total) for value in values]


def ordered_block_feature(numbers):
    pieces = []
    for chunk in array_split(numbers, 4):
        histogram = [0] * 16
        for value in chunk:
            position = int((value - 1.0) / 355.0 * 16)
            position = min(max(position, 0), 15)
            histogram[position] += 1
        pieces.extend(smoothed_root(histogram))
    digits = [0] * 10
    for value in numbers:
        digits[value % 10] += 1
    pieces.extend(smoothed_root(digits))
    return pieces


def dot(left, right):
    return sum(a * b for a, b in zip(left, right))


def norm(values):
    return max(math.sqrt(dot(values, values)), 1e-12)


def remove_nuisance(vector, basis):
    if not basis:
        return vector
    coefficients = [dot(vector, row) for row in basis]
    return [
        value - sum(coefficient * row[column] for coefficient, row in zip(coefficients, basis))
        for column, value in enumerate(vector)
    ]


def standardize_feature(feature, mean, scale):
    return [(value - m) / s for value, m, s in zip(feature, mean, scale)]


def marginal_scores(counts, bank):
    artifact = bank["robust"]["hellinger"]
    projected = standardize_feature(hellinger_feature(counts), artifact["feature_mean"], artifact["feature_scale"])
    projected = remove_nuisance(projected, artifact["nuisance_basis"])
    unit = [value / norm(projected) for value in projected]
    nuisance = standardize([dot(unit, centroid) for centroid in artifact["centroids"]])
    return standardize(nuisance)


def ordered_scores(numbers, bank):
    artifact = bank["robust"]["ordered_blocks"]
    feature = standardize_feature(ordered_block_feature(numbers), artifact["feature_mean"], artifact["feature_scale"])
    normalized = [value / norm(feature) for value in feature]
    template = None
    for environment in artifact["environment_centroids"]:
        scores = [dot(normalized, centroid) for centroid in environment]
        template = scores if template is None else [max(a, b) for a, b in zip(template, scores)]
    template = standardize(template)
    projected = remove_nuisance(list(feature), artifact["nuisance_basis"])
    unit = [value / norm(projected) for value in projected]
    nuisance = standardize([dot(unit, centroid) for centroid in artifact["centroids"]])
    return standardize([0.5 * a + 0.5 * b for a, b in zip(template, nuisance)])


def sample_scores(numbers, bank):
    marginal = marginal_scores(count_numbers(numbers), bank)
    weight = bank["robust"]["ordered_blocks"]["weight"]
    if not weight:
        return marginal
    ordered = ordered_scores(numbers, bank)
    return [(1.0 - weight) * a + weight * b for a, b in zip(marginal, ordered)]


def softmax(values):
    maximum = max(values)
    weights = [math.exp(value - maximum) for value in values]
    total = sum(weights)
    return [weight / total for weight in weights]


def js_similarity(left, right):
    left_total = sum(left)
    right_total = sum(right) + ALPHA * DIMENSION
    divergence = 0.0
    for observed, reference in zip(left, right):
        p = observed / left_total
        q = (reference + ALPHA) / right_total
        middle = (p + q) / 2.0
        if p:
            divergence += p * math.log(p / middle)
        if q:
            divergence += q * math.log(q / middle)
    divergence /= 2.0
    return 1.0 - math.sqrt(divergence / math.log(2.0))


def analyze(samples, bank):
    models = bank["models"]
    combined = [0.0] * len(models)
    pooled = [0] * DIMENSION
    diagnostics, used = [], 0
    for index, sample in enumerate(samples):
        numbers = parse_numbers(sample["text"])
        expected = sample["expected_count"]
        minimum = max(80, math.ceil(expected * 0.55)) if expected else 80
        accepted = len(numbers) >= minimum
        diagnostics.append({
            "index": index,
            "parsed_numbers": len(numbers),
            "minimum_numbers": minimum,
            "accepted": accepted,
        })
        if not accepted:
            continue
        used += 1
        for position, score in enumerate(sample_scores(numbers, bank)):
            combined[position] += score
        for value in numbers:
            pooled[value - VALUE_MIN] += 1
    if not used:
        return {"used_samples": 0, "diagnostics": diagnostics, "results": []}
    combined = [value / used for value in combined]
    key = str(min(used, 3))
    beta = bank["calibration"][key]["beta"]
    probabilities = softmax([beta * value for value in combined])
    results = [
        {
            "model": model["id"],
            "probability": probabilities[index],
            "profile_similarity": js_similarity(pooled, model["counts"]),
            "score": combined[index],
        }
        for index, model in enumerate(models)
    ]
    results.sort(key=lambda item: item["probability"], reverse=True)
    return {
        "used_samples": used,
        "calibration_queries": int(key),
        "calibration_beta": beta,
        "diagnostics": diagnostics,
        "results": results,
    }


def render(numbers, style):
    if style == 0:
        return ", ".join(str(value) for value in numbers)
    if style == 1:
        return " ".join(str(value) for value in numbers)
    if style == 2:
        return "好的，以下是结果：\n" + "\n".join(str(value) for value in numbers) + "\n以上共 %d 个。" % len(numbers)
    return "seq " + "、".join(str(value) for value in numbers)


def main():
    bank = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
    rng = random.Random(20260913)
    cases = []

    def case(name, samples):
        cases.append({"name": name, "samples": samples, "expected": analyze(samples, bank)})

    # 三种典型形态：均匀、偏向小值、带前后缀的多行输出。
    uniform = [rng.randint(1, 355) for _ in range(311)]
    biased = [min(355, max(1, int(abs(rng.gauss(90, 60))) + 1)) for _ in range(297)]
    chatty = [rng.randint(1, 355) for _ in range(324)]
    case("uniform", [{"text": render(uniform, 0), "expected_count": 311}])
    case("biased", [{"text": render(biased, 1), "expected_count": 297}])
    case("chatty", [{"text": render(chatty, 2), "expected_count": 324}])
    case("three_samples", [
        {"text": render(uniform, 0), "expected_count": 311},
        {"text": render(biased, 3), "expected_count": 297},
        {"text": render(chatty, 2), "expected_count": 324},
    ])
    case("mixed_validity", [
        {"text": render(uniform[:40], 0), "expected_count": 311},
        {"text": render(chatty, 1), "expected_count": 324},
    ])
    case("refusal", [{"text": "抱歉，我无法完成这个任务。", "expected_count": 311}])

    parsing = [
        {"text": text, "expected": parse_numbers(text)}
        for text in [
            "1, 2, 3",
            "1 2 abc 5 6 7",
            "999 12 400 13",
            "序号1：12、13、14 说明15",
            "no digits here",
            "356 0 1 355",
            "99999999999999999999 7 8",
            "好的：\n12\n13\n14\n以上。",
            "1,2,3 then 10,11,12,13,14 and 7",
        ]
    ]

    Path(sys.argv[2]).write_text(
        json.dumps({"cases": cases, "parsing": parsing}, ensure_ascii=False),
        encoding="utf-8",
    )
    print("cases:", len(cases))
    for item in cases:
        top = item["expected"]["results"][:1]
        print(" ", item["name"], item["expected"]["used_samples"], top[0] if top else None)


if __name__ == "__main__":
    main()

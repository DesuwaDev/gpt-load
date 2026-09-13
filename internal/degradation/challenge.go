package degradation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const (
	challengeMinLength = 292
	challengeMaxLength = 332
	// MaxSamplesPerRun caps how many responses one detection may collect.
	MaxSamplesPerRun = 5
)

// Challenge is one prompt to send upstream plus the length it asks for.
type Challenge struct {
	ID            string `json:"id"`
	ExpectedCount int    `json:"expected_count"`
	Prompt        string `json:"prompt"`
}

// 提示词与 ModelTrace 保持同一口径：禁止工具、禁止事后排序，并要求直接输出序列。
// 措辞随机化是为了避免上游把固定文本缓存成固定回答。
var (
	challengeOpenings = []string{
		"这是一次独立的数值选择记录",
		"请完成下面的无语义整数选择任务",
		"执行一次第一反应取值记录",
		"生成一组不承载语义的整数选择",
		"进行一轮快速逐项取值",
	}
	challengeActions = []string{
		"为各个位置分别凭第一反应选择",
		"逐项选择",
		"每次只决定当前一项，共给出",
		"分别凭第一反应给出",
		"逐个直接选择",
	}
	challengeEndings = []string{
		"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。",
		"偶然重复是有效的；不要重新排列或修正已经写出的项目。",
		"相同值可以再次出现；输出过程中不要整理或改写前面的项目。",
		"重复值无需删除；不要筛选、重排或补成某种规律。",
		"不必赋予数字任何含义；已经给出的值保持不变。",
	}
	challengeSeparators = []string{
		"数字之间用逗号或空格分隔均可。",
		"使用一种一致的常见分隔符即可。",
		"可以用逗号、空格或换行分隔。",
		"只要每个整数边界清楚，格式可自行选择。",
	}
)

func randomIndex(limit int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0, fmt.Errorf("degradation: random source unavailable: %w", err)
	}
	return int(value.Int64()), nil
}

func choose(options []string) (string, error) {
	index, err := randomIndex(len(options))
	if err != nil {
		return "", err
	}
	return options[index], nil
}

// sampleLengths draws distinct run lengths, mirroring ModelTrace's sampling so
// two responses in the same run never ask for the same count.
func sampleLengths(count int) ([]int, error) {
	pool := make([]int, 0, challengeMaxLength-challengeMinLength+1)
	for length := challengeMinLength; length <= challengeMaxLength; length++ {
		pool = append(pool, length)
	}
	for index := len(pool) - 1; index > 0; index-- {
		swap, err := randomIndex(index + 1)
		if err != nil {
			return nil, err
		}
		pool[index], pool[swap] = pool[swap], pool[index]
	}
	return pool[:count], nil
}

// GenerateChallenges builds count independent prompts.
func GenerateChallenges(count int) ([]Challenge, error) {
	if count < 1 {
		count = 1
	}
	if count > MaxSamplesPerRun {
		count = MaxSamplesPerRun
	}
	lengths, err := sampleLengths(count)
	if err != nil {
		return nil, err
	}
	challenges := make([]Challenge, 0, count)
	for index, length := range lengths {
		opening, err := choose(challengeOpenings)
		if err != nil {
			return nil, err
		}
		action, err := choose(challengeActions)
		if err != nil {
			return nil, err
		}
		ending, err := choose(challengeEndings)
		if err != nil {
			return nil, err
		}
		separator, err := choose(challengeSeparators)
		if err != nil {
			return nil, err
		}
		suffix := make([]byte, 7)
		if _, err := rand.Read(suffix); err != nil {
			return nil, fmt.Errorf("degradation: random source unavailable: %w", err)
		}
		var prompt strings.Builder
		fmt.Fprintf(&prompt, "%s。%s %d 个 1 到 355（含端点）的整数。", opening, action, length)
		prompt.WriteString("每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。")
		prompt.WriteString("本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、")
		prompt.WriteString("计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。")
		prompt.WriteString(ending)
		prompt.WriteString(separator)
		prompt.WriteString("直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。")
		challenges = append(challenges, Challenge{
			ID:            fmt.Sprintf("probe-%d-%s", index+1, hex.EncodeToString(suffix)),
			ExpectedCount: length,
			Prompt:        prompt.String(),
		})
	}
	return challenges, nil
}

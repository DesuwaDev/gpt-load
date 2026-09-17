package gateway

import "testing"

// 模型名单是用来「缩小」注入范围的。转发路径上并非每次尝试都知道模型名——
// ResponsesRetrieve / Cancel / InputItems / Passthrough / ListModels 这些操作不
// 走 operationRequiresModel 的必填校验，metadata.Model 是 nil，没有改写时上游模型
// 名也是空。筛不动名单时必须照常注入，否则配了名单就会把这批请求静默漏掉。
func TestResolvedCodexTurnStateInjectsWhenTheModelIsUnknown(t *testing.T) {
	const injected = "turn-state-value"
	for _, test := range []struct {
		name          string
		turnState     string
		scope         string
		externalModel string
		upstreamModel string
		want          string
	}{
		{
			name: "no scope injects", turnState: injected,
			externalModel: "gpt-5", upstreamModel: "gpt-5", want: injected,
		},
		{
			name: "scope hit injects", turnState: injected, scope: "gpt-5",
			externalModel: "gpt-5", upstreamModel: "gpt-5", want: injected,
		},
		{
			name: "scope miss injects nothing", turnState: injected, scope: "gpt-5.1-codex",
			externalModel: "gpt-5", upstreamModel: "gpt-5",
		},
		{
			name: "upstream model alone still decides", turnState: injected, scope: "provider-model",
			upstreamModel: "provider-model", want: injected,
		},
		{
			name: "client model alone still decides", turnState: injected, scope: "gpt-5",
			externalModel: "gpt-5", want: injected,
		},
		{
			name: "unknown model falls back to injecting", turnState: injected,
			scope: "gpt-5.1-codex", want: injected,
		},
		{
			name: "unknown model with a blank-ish scope injects", turnState: injected,
			scope: " , ", want: injected,
		},
		{name: "unknown model without an override injects nothing", scope: "gpt-5.1-codex"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := ForwardInput{
				ExternalModel:        test.externalModel,
				UpstreamModelID:      test.upstreamModel,
				CodexTurnState:       test.turnState,
				CodexTurnStateModels: test.scope,
			}
			if got := input.ResolvedCodexTurnState(); got != test.want {
				t.Fatalf("ResolvedCodexTurnState() = %q, want %q", got, test.want)
			}
		})
	}
}

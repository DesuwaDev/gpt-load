package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// CommandCode 使用 Command Code 标准订阅的 /alpha/generate 接口。上游协议是 CLI
// 私有格式，执行层先把各客户端协议统一成 OpenAI Chat 再翻译，因此路由声明与 Cline 相同。
func CommandCode() spec.Module {
	return spec.Module{Definition: spec.Definition{
		ID: spec.CommandCode, Name: "Command Code", Mark: "CC", Icon: "commandcode",
		SearchTerms: []string{"commandcode", "command code", "commandcode.ai"},
		Description: "Managed API preset",
		Connection:  spec.Connection{Type: spec.ConnectionAPIKey, CredentialInput: "batch_text"},
		Params: []spec.Field{{
			Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
			Normalizer: spec.NormalizeBaseURL,
		}},
		Credentials: []spec.Field{{
			Key: "api_key", Label: "API Key", InputKind: spec.InputSecret,
			Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
		}},
		Provider: spec.ProviderBinding{
			ProviderKind:   spec.ProviderCommandCode,
			EndpointPolicy: spec.EndpointFixedWithOverride,
			FixedBaseURL:   "https://api.commandcode.ai",
		},
		Routes: []spec.Route{
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
			spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
			spec.NewRoute(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationProbe, execution.RouteConverted),
		},
	}}
}

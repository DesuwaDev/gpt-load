package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	GeminiCLISubscriptionDriver spec.SubscriptionDriverID = "gemini_cli"
	GeminiCLIModelDiscovery     spec.UtilityID            = "gemini_cli_models"
	GeminiCLIQuotaObservation   spec.UtilityID            = "gemini_cli_quota"
)

// GeminiCLI declares the Google Gemini CLI (Code Assist) subscription channel,
// covering personal Google accounts, Google One AI plans and GCP Code Assist.
func GeminiCLI() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.GeminiCLI,
			Name:        "Gemini CLI",
			Mark:        "GC",
			Icon:        "gemini",
			SearchTerms: []string{"subscription", "oauth", "google", "gemini", "cli", "code assist", "google one"},
			Description: "Google Gemini CLI subscription (Code Assist)",
			Connection: spec.Connection{
				Type:            spec.ConnectionSubscription,
				CredentialInput: "authorization",
				AuthorizationMethods: []spec.AuthorizationMethod{
					spec.AuthorizationBrowserOAuth,
					spec.AuthorizationOAuthFile,
				},
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "API root URL", InputKind: spec.InputURL,
				Normalizer: spec.NormalizeOptionalHTTPSBaseURL,
			}},
			Credentials: []spec.Field{},
			Provider: spec.ProviderBinding{
				ProviderKind:    spec.ProviderGeminiCLI,
				EndpointPolicy:  spec.EndpointSDKDefault,
				DefaultBaseURLs: []string{"https://cloudcode-pa.googleapis.com"},
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesEdit, execution.RouteConverted),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCompact, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteConverted),
			},
			Capabilities: spec.CapabilityBindings{
				SubscriptionDriver: GeminiCLISubscriptionDriver,
				ModelDiscovery:     GeminiCLIModelDiscovery,
				QuotaObservation:   GeminiCLIQuotaObservation,
			},
		},
	}
}

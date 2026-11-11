package embedded

import "context"

// Keep the selectable Code Assist catalog aligned with sub2api's
// backend/internal/pkg/geminicli.DefaultModels. This is a selection catalog,
// not evidence that an account is entitled to every listed model.
var geminiCLISelectableModelIDs = [...]string{
	"gemini-3.1-flash-image",
	"gemini-3.1-flash-image-preview",
	"gemini-3.1-flash-lite-image",
	"gemini-3-pro-image",
	"gemini-3-pro-image-preview",
	"gemini-3-flash",
	"gemini-3-flash-preview",
	"gemini-3.1-pro-preview",
	"gemini-3.1-pro-preview-customtools",
	"gemini-3.1-flash-lite",
	"gemini-3.1-flash-lite-preview",
	"gemini-3.5-flash",
	"gemini-3.5-flash-lite",
	"gemini-3.6-flash",
	"gemini-3.7-flash",
	"gemini-3.8-flash",
	"gemini-4-argon",
}

// DiscoverGeminiCLIModels returns the built-in selection catalog, like sub2api's
// OAuth account model selector. Code Assist has no supported live model-list
// endpoint; quota observation also uses the local tier policy and statistics.
func DiscoverGeminiCLIModels(ctx context.Context, credential GeminiCLICredential, _ GeminiCLIOptions) ([]string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if err := validateGeminiCLICredential(credential); err != nil {
		return nil, err
	}
	if credential.OAuthType == "google_one" {
		return []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.0-flash"}, nil
	}
	return append([]string(nil), geminiCLISelectableModelIDs[:]...), nil
}

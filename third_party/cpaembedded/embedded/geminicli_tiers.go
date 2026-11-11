package embedded

import "strings"

// Canonical aliases and per-OAuth defaults match sub2api's GeminiOAuthService.
func canonicalGeminiCLITier(oauthType, tier string) string {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if oauthType == "google_one" {
		switch tier {
		case "google_ai_pro", "ai_premium":
			return "google_ai_pro"
		case "google_ai_ultra", "google_one_unlimited":
			return "google_ai_ultra"
		default:
			return "google_one_free"
		}
	}
	switch tier {
	case "gcp_enterprise", "enterprise", "ultra", "ultra-tier":
		return "gcp_enterprise"
	default:
		return "gcp_standard"
	}
}

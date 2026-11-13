package bifrost

import (
	"maps"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
)

// mistralRenamedParams are the Mistral names that replace OpenAI parameters.
// They travel as extra params, which this path otherwise does not send.
var mistralRenamedParams = []string{"max_tokens", "random_seed"}

// sanitizeCompatibleChatForChannel applies upstream-specific history rules to a
// converted Chat request.
func sanitizeCompatibleChatForChannel(ctx *schemas.BifrostContext, chat *schemas.BifrostChatRequest, channelID string) {
	if channelID != string(channel.Mistral) {
		return
	}
	sanitizeMistralCompatibleChat(chat)
	if chat.Params == nil {
		return
	}
	renamed := make(map[string]any, len(mistralRenamedParams))
	for _, key := range mistralRenamedParams {
		if value, exists := chat.Params.ExtraParams[key]; exists {
			renamed[key] = value
		}
	}
	// Only the renamed parameters are passed through; other extra params were
	// never sent on this path and Mistral would reject unknown ones.
	chat.Params.ExtraParams = renamed
	if len(renamed) > 0 {
		ctx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	}
}

// sanitizeMistralCompatibleChat removes what Mistral rejects with 422
// extra_forbidden from a converted Chat request: reasoning replayed on earlier
// assistant turns, other providers' fields on messages and tool calls, and
// OpenAI-only parameters. The Chat request is built per attempt, but nested
// values may be shared with the source request, so they are copied first.
func sanitizeMistralCompatibleChat(chat *schemas.BifrostChatRequest) {
	if chat == nil {
		return
	}
	if chat.Params != nil {
		params := *chat.Params
		if params.MaxCompletionTokens != nil {
			setMistralExtraParam(&params, "max_tokens", *params.MaxCompletionTokens)
			params.MaxCompletionTokens = nil
		}
		if params.Seed != nil {
			setMistralExtraParam(&params, "random_seed", *params.Seed)
			params.Seed = nil
		}
		if params.Reasoning != nil && params.Reasoning.Effort != nil {
			// Mistral accepts only "none" and "high".
			effort := "high"
			if *params.Reasoning.Effort == "none" {
				effort = "none"
			}
			params.Reasoning = &schemas.ChatReasoning{Effort: schemas.Ptr(effort)}
		} else {
			params.Reasoning = nil
		}
		params.User = nil
		params.Store = nil
		params.LogitBias = nil
		params.Verbosity = nil
		params.WebSearchOptions = nil
		params.SafetyIdentifier = nil
		chat.Params = &params
	}
	messages := make([]schemas.ChatMessage, len(chat.Input))
	for index, message := range chat.Input {
		if message.Role != schemas.ChatMessageRoleTool {
			message.Name = nil
		}
		if message.Content != nil && len(message.Content.ContentBlocks) > 0 {
			content := *message.Content
			blocks := make([]schemas.ChatContentBlock, len(content.ContentBlocks))
			for blockIndex, block := range content.ContentBlocks {
				block.CacheControl = nil
				block.Citations = nil
				block.PromptCacheBreakpoint = nil
				block.CachePoint = nil
				blocks[blockIndex] = block
			}
			content.ContentBlocks = blocks
			message.Content = &content
		}
		if message.ChatAssistantMessage != nil {
			assistant := *message.ChatAssistantMessage
			assistant.Refusal = nil
			assistant.Audio = nil
			assistant.Images = nil
			assistant.Reasoning = nil
			assistant.ReasoningDetails = nil
			assistant.Annotations = nil
			if len(assistant.ToolCalls) > 0 {
				calls := make([]schemas.ChatAssistantMessageToolCall, len(assistant.ToolCalls))
				for callIndex, call := range assistant.ToolCalls {
					call.ExtraContent = nil
					call.CacheControl = nil
					calls[callIndex] = call
				}
				assistant.ToolCalls = calls
			}
			message.ChatAssistantMessage = &assistant
		}
		messages[index] = message
	}
	chat.Input = messages
}

// setMistralExtraParam sets a Mistral-named parameter unless the request
// already carries it; the map is copied because it may be shared.
func setMistralExtraParam(params *schemas.ChatParameters, key string, value any) {
	if _, exists := params.ExtraParams[key]; exists {
		return
	}
	extra := make(map[string]any, len(params.ExtraParams)+1)
	maps.Copy(extra, params.ExtraParams)
	extra[key] = value
	params.ExtraParams = extra
}

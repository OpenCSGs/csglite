package server

import (
	"net/http"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func (s *Server) recordAPIUsage(r *http.Request, model string, inputTokens, outputTokens int) {
	if s == nil || s.apiUsage == nil {
		return
	}
	key, ok := authenticatedAPIKey(r)
	if !ok {
		return
	}
	_ = s.apiUsage.Add(config.APIUsageEvent{
		APIKeyID:     key.ID,
		APIKeyName:   key.Name,
		Model:        model,
		InputTokens:  int64(inputTokens),
		OutputTokens: int64(outputTokens),
	})
}

func countMessageTokens(messages []api.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateAnthropicTokens(contentAsString(msg.Content))
		total += estimateAnthropicTokens(msg.ReasoningContent)
	}
	if total == 0 {
		return 1
	}
	return total
}

func openAIUsageTokens(resp api.OpenAIChatResponse) (int, int) {
	if resp.Usage.TotalTokens > 0 || resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	output := ""
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		output = contentAsString(resp.Choices[0].Message.Content)
	}
	return 0, estimateAnthropicTokens(output)
}

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	webSearchActionSkip      = "skip"
	webSearchRouteLLMTimeout = 4 * time.Second
	webSearchRouteMaxTokens  = 96
)

func webSearchRouteSystemPrompt(chinese bool, now time.Time) string {
	date := now.Format("2006-01-02")
	if chinese {
		return fmt.Sprintf(`联网搜索路由。日期：%s。只输出 JSON：{"action":"search"|"skip","query":"关键词","reason":"原因","confidence":0.0}。需要最新事实/天气/新闻/股价/赛程/版本/明确要求搜索时用 search；寒暄/代码/常识/改写/可凭上下文回答的追问用 skip。query 为 5-80 字搜索词，勿整句照搬。`, date)
	}
	return fmt.Sprintf(`Web search router. Date: %s. Output JSON only: {"action":"search"|"skip","query":"keywords","reason":"why","confidence":0.0}. Use search for fresh facts, weather, news, prices, scores, versions, or explicit lookup requests; use skip for greetings, coding, stable knowledge, rewriting, or follow-ups answerable from context. Query is a 5-80 char keyword phrase.`, date)
}

func webSearchRouteUserPrompt(userQuery, conversationContext string) string {
	var b strings.Builder
	b.WriteString("Latest user message:\n")
	b.WriteString(userQuery)
	if strings.TrimSpace(conversationContext) != "" {
		b.WriteString("\n\nRecent conversation:\n")
		b.WriteString(conversationContext)
	}
	return b.String()
}

func recentChatContextForRouting(messages []api.Message, maxTurns int) string {
	if maxTurns <= 0 {
		maxTurns = 2
	}
	var parts []string
	for i := len(messages) - 1; i >= 0 && len(parts) < maxTurns; i-- {
		role := strings.TrimSpace(messages[i].Role)
		if role != "user" && role != "assistant" {
			continue
		}
		text := strings.TrimSpace(responsesContentText(messages[i].Content))
		if text == "" {
			continue
		}
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		parts = append([]string{fmt.Sprintf("%s: %s", role, text)}, parts...)
	}
	return strings.Join(parts, "\n")
}

func parseWebSearchRouteResponse(raw, fallbackQuery string) webSearchRoute {
	fallback := webSearchRoute{
		Action:     webSearchActionSearch,
		Query:      strings.TrimSpace(fallbackQuery),
		Reason:     "fallback to search",
		Confidence: 0.5,
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "{"); idx >= 0 {
		raw = raw[idx:]
	}
	if end := strings.LastIndex(raw, "}"); end >= 0 {
		raw = raw[:end+1]
	}
	var route webSearchRoute
	if err := json.Unmarshal([]byte(raw), &route); err != nil {
		return fallback
	}
	route.Action = strings.ToLower(strings.TrimSpace(route.Action))
	route.Query = strings.TrimSpace(route.Query)
	route.Reason = strings.TrimSpace(route.Reason)
	if route.Action != webSearchActionSearch && route.Action != webSearchActionSkip {
		return fallback
	}
	if route.Action == webSearchActionSearch && route.Query == "" {
		route.Query = strings.TrimSpace(fallbackQuery)
	}
	if route.Reason == "" {
		if route.Action == webSearchActionSkip {
			route.Reason = "no web search needed"
		} else {
			route.Reason = "web search recommended"
		}
	}
	if route.Confidence < 0 {
		route.Confidence = 0
	}
	if route.Confidence > 1 {
		route.Confidence = 1
	}
	return route
}

func (s *Server) planWebSearchRoute(
	ctx context.Context,
	eng inference.Engine,
	req api.ChatRequest,
	userQuery string,
	emit chatSearchEventWriter,
) webSearchRoute {
	fallback := webSearchRoute{
		Action:     webSearchActionSearch,
		Query:      userQuery,
		Reason:     "web search enabled",
		Confidence: 1,
	}
	if eng == nil {
		return fallback
	}

	now := time.Now()
	if route, ok := tryFastWebSearchRoute(userQuery, now); ok {
		return route
	}

	if emit != nil {
		emit(map[string]string{"search_planning": userQuery})
	}

	chinese := isLikelyChineseText(userQuery)
	routeMessages := []inference.Message{
		{Role: "system", Content: webSearchRouteSystemPrompt(chinese, now)},
		{Role: "user", Content: webSearchRouteUserPrompt(userQuery, recentChatContextForRouting(req.Messages, 2))},
	}
	routeOpts := inference.DefaultOptions()
	routeOpts.Temperature = 0.1
	routeOpts.TopP = 0.9
	routeOpts.MaxTokens = webSearchRouteMaxTokens
	routeOpts.DisableThinking = true

	routeCtx, cancel := context.WithTimeout(ctx, webSearchRouteLLMTimeout)
	defer cancel()
	raw, err := eng.Chat(routeCtx, routeMessages, routeOpts, nil)
	if err != nil {
		return fallback
	}
	return parseWebSearchRouteResponse(raw, userQuery)
}

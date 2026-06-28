package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/websearch"
	"github.com/opencsgs/csglite/pkg/api"
)

type chatSearchEventWriter func(interface{})

var searchWeb = websearch.Search

var explicitYearPattern = regexp.MustCompile(`\b(19|20)\d{2}\b`)

const webSearchActionSearch = "search"

type webSearchRoute struct {
	Action     string  `json:"action"`
	Query      string  `json:"query,omitempty"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

func (s *Server) augmentChatMessagesWithWebSearch(ctx context.Context, req api.ChatRequest, messages []inference.Message, eng inference.Engine, emit chatSearchEventWriter) ([]inference.Message, string) {
	if req.WebSearch == nil || !req.WebSearch.Enabled {
		return messages, ""
	}
	cfg := config.NormalizeWebSearchConfig(s.cfg.WebSearch)
	if !cfg.Enabled {
		return messages, ""
	}

	query := strings.TrimSpace(req.WebSearch.Query)
	if query == "" {
		query = latestUserText(req.Messages)
	}
	if query == "" {
		return messages, ""
	}

	route := s.planWebSearchRoute(ctx, eng, req, query, emit)
	if emit != nil {
		emit(map[string]interface{}{"search_route": route})
	}
	if route.Action == webSearchActionSkip {
		if emit != nil {
			emit(map[string]string{"search_skipped": route.Reason})
		}
		return messages, ""
	}
	if strings.TrimSpace(route.Query) == "" {
		route.Query = query
	}

	searchQuery := enrichWebSearchQuery(route.Query, time.Now())
	if emit != nil {
		emit(map[string]string{"searching": searchQuery})
	}

	resp, err := searchWeb(ctx, websearch.Config{
		MaxResults:    cfg.MaxResults,
		Language:      cfg.Language,
		Providers:     cfg.Providers,
		SafeSearch:    cfg.SafeSearch,
		SafeSearchSet: true,
		Timeout:       time.Duration(cfg.TimeoutSeconds) * time.Second,
		Quick:         true,
	}, websearch.SearchRequest{Query: searchQuery})
	if err != nil {
		if emit != nil {
			emit(map[string]string{"search_error": webSearchUserError(err)})
		}
		return messages, ""
	}

	results := apiWebSearchResults(resp.Results)
	if len(results) == 0 {
		if emit != nil {
			emit(map[string]string{"search_error": "no web search results found"})
		}
		return messages, ""
	}
	if emit != nil {
		emit(map[string]interface{}{
			"search_query":   resp.Query,
			"search_results": results,
		})
	}

	contextText := webSearchContextMessage(query, resp.Query, results)
	return insertSystemMessage(messages, inference.Message{
		Role:    "system",
		Content: contextText,
	}), contextText
}

func latestUserText(messages []api.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if text := strings.TrimSpace(responsesContentText(messages[i].Content)); text != "" {
			return text
		}
	}
	return ""
}

func currentDateContextForQuery(query string, now time.Time) string {
	if isLikelyChineseText(query) {
		return fmt.Sprintf(
			"当前日期：%s。回答涉及“今天、今年、当前、最新、最近、本赛季”等相对时间时，必须按这个日期理解。\n\n%s",
			now.Format("2006-01-02"),
			assistantAnswerStyleContextForQuery(query),
		)
	}
	return fmt.Sprintf(
		"Current date: %s. Use this date to resolve relative time expressions such as today, this year, current, latest, recent, and this season.\n\n%s",
		now.Format("2006-01-02"),
		assistantAnswerStyleContextForQuery(query),
	)
}

func assistantAnswerStyleContextForQuery(query string) string {
	if isLikelyChineseText(query) {
		return "请使用与用户问题相同的语言回答。直接给出最终答案，不要暴露内部推理、草稿分析或思考过程。最终答案使用易读排版：短段落，列表或重点小节前留空行，引用编号紧跟对应结论。"
	}
	return "Use the same language as the user. Answer directly without exposing internal reasoning or draft analysis. Format the final answer as readable Markdown: use short paragraphs, put a blank line before lists or key-point sections, and keep inline citations attached to the claims they support."
}

func isLikelyChineseText(text string) bool {
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func enrichWebSearchQuery(query string, now time.Time) string {
	query = strings.TrimSpace(query)
	if query == "" || explicitYearPattern.MatchString(query) || !hasRelativeTimeTerm(query) {
		return query
	}
	return fmt.Sprintf("%s %d", query, now.Year())
}

func hasRelativeTimeTerm(query string) bool {
	lower := strings.ToLower(query)
	terms := []string{
		"今年", "当前", "最新", "最近", "现在", "今日", "今天", "本赛季", "这个赛季",
		"this year", "current", "latest", "recent", "today", "now", "this season",
	}
	for _, term := range terms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func apiWebSearchResults(results []websearch.Result) []api.WebSearchResult {
	out := make([]api.WebSearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, api.WebSearchResult{
			Title:       result.Title,
			URL:         result.URL,
			Snippet:     result.Snippet,
			Engine:      result.Engine,
			Category:    result.Category,
			Score:       result.Score,
			PublishedAt: result.PublishedAt,
		})
	}
	return out
}

func webSearchContextMessage(userQuery, searchQuery string, results []api.WebSearchResult) string {
	var b strings.Builder
	if isLikelyChineseText(userQuery) {
		fmt.Fprintf(&b, "当前日期：%s。\n", time.Now().Format("2006-01-02"))
		fmt.Fprintf(&b, "用户问题：%q。\n", userQuery)
		if strings.TrimSpace(searchQuery) != "" && strings.TrimSpace(searchQuery) != strings.TrimSpace(userQuery) {
			fmt.Fprintf(&b, "实际搜索词：%q。\n", searchQuery)
		}
		b.WriteString("以下是网页搜索结果。请把它们作为本轮回答的当前外部上下文。\n")
		b.WriteString("涉及最新事实、近期事件、网站、产品、人物、体育战绩或可能变化的信息时，优先依据这些搜索结果回答；如果结果冲突或不足，请明确说明，不要猜测。使用 [1]、[2] 等编号引用支持对应结论。\n")
		b.WriteString(assistantAnswerStyleContextForQuery(userQuery))
	} else {
		fmt.Fprintf(&b, "Current date: %s.\n", time.Now().Format("2006-01-02"))
		fmt.Fprintf(&b, "User query: %q.\n", userQuery)
		if strings.TrimSpace(searchQuery) != "" && strings.TrimSpace(searchQuery) != strings.TrimSpace(userQuery) {
			fmt.Fprintf(&b, "Search query used: %q.\n", searchQuery)
		}
		b.WriteString("Web search results are below. Treat them as current external context for this turn.\n")
		b.WriteString("Resolve relative dates like \"today\", \"recent\", or \"this year\" against the current date above. When the user asks about current facts, recent events, websites, products, people, sports records, or anything that may have changed, answer from these search results instead of relying only on model memory. Cite the used sources inline with [1], [2], etc. If the results conflict or are insufficient, say so rather than guessing.\n")
		b.WriteString(assistantAnswerStyleContextForQuery(userQuery))
	}
	b.WriteByte('\n')
	for i, result := range results {
		fmt.Fprintf(&b, "\n[%d] %s\nURL: %s", i+1, result.Title, result.URL)
		if result.Snippet != "" {
			fmt.Fprintf(&b, "\nSnippet: %s", result.Snippet)
		}
		if result.PublishedAt != "" {
			fmt.Fprintf(&b, "\nPublished: %s", result.PublishedAt)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func insertSystemMessage(messages []inference.Message, msg inference.Message) []inference.Message {
	msgText, ok := msg.Content.(string)
	if !ok || strings.TrimSpace(msgText) == "" {
		return append([]inference.Message{msg}, messages...)
	}
	for i, existing := range messages {
		if existing.Role != "system" {
			continue
		}
		if existingText, ok := existing.Content.(string); ok {
			out := append([]inference.Message{}, messages...)
			out[i].Content = strings.TrimSpace(existingText) + "\n\n" + strings.TrimSpace(msgText)
			return out
		}
	}
	out := make([]inference.Message, 0, len(messages)+1)
	out = append(out, msg)
	out = append(out, messages...)
	return out
}

func webSearchUserError(err error) string {
	return err.Error()
}

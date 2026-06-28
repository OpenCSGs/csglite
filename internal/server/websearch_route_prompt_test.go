package server

import (
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestParseWebSearchRouteResponse(t *testing.T) {
	raw := "```json\n{\"action\":\"search\",\"query\":\"湖人 2026 季后赛 战绩\",\"reason\":\"需要最新战绩\",\"confidence\":0.92}\n```"
	route := parseWebSearchRouteResponse(raw, "今年季后赛湖人战绩如何")
	if route.Action != webSearchActionSearch || route.Query != "湖人 2026 季后赛 战绩" {
		t.Fatalf("route = %#v", route)
	}
}

func TestParseWebSearchRouteResponseSkip(t *testing.T) {
	route := parseWebSearchRouteResponse(`{"action":"skip","reason":"寒暄","confidence":0.95}`, "你好")
	if route.Action != webSearchActionSkip || route.Reason != "寒暄" {
		t.Fatalf("route = %#v", route)
	}
}

func TestParseWebSearchRouteResponseFallback(t *testing.T) {
	route := parseWebSearchRouteResponse("not json", "fallback query")
	if route.Action != webSearchActionSearch || route.Query != "fallback query" {
		t.Fatalf("route = %#v", route)
	}
}

func TestWebSearchRouteSystemPromptChinese(t *testing.T) {
	prompt := webSearchRouteSystemPrompt(true, time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(prompt, "2026-05-16") || !strings.Contains(prompt, `"action":"search"`) {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestRecentChatContextForRouting(t *testing.T) {
	ctx := recentChatContextForRouting([]api.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
	}, 2)
	if !strings.Contains(ctx, "assistant: second") || !strings.Contains(ctx, "user: third") || strings.Contains(ctx, "first") {
		t.Fatalf("context = %q", ctx)
	}
}

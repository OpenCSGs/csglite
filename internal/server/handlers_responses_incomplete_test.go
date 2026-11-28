package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestResponsesReportsCutOffReplyAsIncomplete(t *testing.T) {
	for finishReason, wantReason := range map[string]string{
		"length":         "max_output_tokens",
		"content_filter": "content_filter",
		"stop":           "",
		"tool_calls":     "",
	} {
		t.Run(finishReason, func(t *testing.T) {
			reason := finishReason
			resp := api.OpenAIChatResponse{
				Choices: []api.OpenAIChoice{{
					Message:      &api.Message{Role: "assistant", Content: "partial answer"},
					FinishReason: &reason,
				}},
			}
			wantStatus, wantEvent := "completed", "response.completed"
			if wantReason != "" {
				wantStatus, wantEvent = "incomplete", "response.incomplete"
			}

			body := buildResponsesToolResponse("resp_1", "m", 1, resp, 3, "completed")
			if body["status"] != wantStatus {
				t.Fatalf("status = %v, want %s", body["status"], wantStatus)
			}
			details, _ := body["incomplete_details"].(map[string]interface{})
			if got := stringValue(details["reason"]); got != wantReason {
				t.Fatalf("incomplete_details.reason = %q, want %q", got, wantReason)
			}

			rec := httptest.NewRecorder()
			writeResponsesToolStream(rec, "resp_1", "m", 1, resp, 3)
			events := strings.Split(strings.TrimSpace(rec.Body.String()), "\n\n")
			last := events[len(events)-1]
			if !strings.HasPrefix(last, "event: "+wantEvent+"\n") || !strings.Contains(last, `"type":"`+wantEvent+`"`) {
				t.Fatalf("last event = %s, want %s", last, wantEvent)
			}
			if wantReason != "" && !strings.Contains(last, `"incomplete_details":{"reason":"`+wantReason+`"}`) {
				t.Fatalf("last event = %s, want incomplete_details", last)
			}
		})
	}
}

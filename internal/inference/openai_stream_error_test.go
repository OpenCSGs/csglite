package inference

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestOpenAIEngineStreamFailures(t *testing.T) {
	const partial = "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"error object", partial + "data: {\"error\":{\"message\":\"upstream overloaded\"}}\n\ndata: [DONE]\n\n", "upstream overloaded"},
		{"error string", "data: {\"error\":\"quota exceeded\"}\n\n", "quota exceeded"},
		{"cut connection", partial, io.ErrUnexpectedEOF.Error()},
		{"finish_reason without DONE", partial + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", ""},
		{"finish_reason without delta", partial + "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\n", ""},
		{"null error field", "data: {\"error\":null,\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\ndata: [DONE]\n\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &openAIEngine{}
			_, err := e.handleStream(strings.NewReader(tt.body), func(string) {})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if tt.name == "cut connection" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestOpenAIEngineStreamClosesThinkWithoutDone(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hm\"},\"finish_reason\":\"length\"}]}\n\n"
	got, err := (&openAIEngine{}).handleStream(strings.NewReader(body), func(string) {})
	if err != nil || got != "<think>hm</think>" {
		t.Fatalf("got %q, %v", got, err)
	}
}

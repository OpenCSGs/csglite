package chathistory

import (
	"testing"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestStoreSearchConversations(t *testing.T) {
	now := time.Now()
	store := NewStore(t.TempDir())

	mustSaveConversation(t, store, &api.Conversation{
		ID:        "older",
		Title:     "Deploy Notes",
		Model:     "Qwen/Qwen3",
		CreatedAt: now.Add(-3 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
		Messages: []api.Message{{
			Role:    "user",
			Content: "How should I ship the release?",
		}},
	})
	mustSaveConversation(t, store, &api.Conversation{
		ID:        "newer",
		Title:     "Database Plan",
		Model:     "local-model",
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-1 * time.Hour),
		Messages: []api.Message{{
			Role:    "assistant",
			Content: "Use a vector database migration window.",
		}},
	})
	mustSaveConversation(t, store, &api.Conversation{
		ID:        "multimodal",
		Title:     "Receipt",
		Model:     "vision-model",
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now,
		Messages: []api.Message{{
			Role: "user",
			Content: []map[string]interface{}{
				{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64,abc"}},
				{"type": "text", "text": "Find the invoice total."},
			},
		}},
	})

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "empty query returns all sorted by updated time", query: "  ", want: []string{"multimodal", "newer", "older"}},
		{name: "matches title", query: "deploy", want: []string{"older"}},
		{name: "matches message text", query: "database migration", want: []string{"newer"}},
		{name: "matches case-insensitively", query: "qWEN", want: []string{"older"}},
		{name: "matches multimodal text parts", query: "invoice total", want: []string{"multimodal"}},
		{name: "returns empty list for no match", query: "missing phrase", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Search(tt.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Search() returned %d conversations, want %d: %#v", len(got), len(tt.want), got)
			}
			for i, wantID := range tt.want {
				if got[i].ID != wantID {
					t.Fatalf("Search()[%d].ID = %q, want %q; full result=%#v", i, got[i].ID, wantID, got)
				}
			}
		})
	}
}

func mustSaveConversation(t *testing.T, store *Store, conv *api.Conversation) {
	t.Helper()
	if err := store.Save(conv); err != nil {
		t.Fatalf("Save(%q): %v", conv.ID, err)
	}
}

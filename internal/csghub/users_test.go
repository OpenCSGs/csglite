package csghub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTokenDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/token/test-token" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/v1/token/test-token")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(APIResponse[TokenDetail]{
			Msg: "OK",
			Data: TokenDetail{
				Token:       "test-token",
				TokenName:   "token-name",
				Application: "git",
				UserName:    "alice",
				UserUUID:    "user-1",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "ignored-by-token-detail")
	detail, err := client.GetTokenDetail(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("GetTokenDetail error: %v", err)
	}
	if detail.UserName != "alice" {
		t.Fatalf("UserName = %q, want %q", detail.UserName, "alice")
	}
	if detail.UserUUID != "user-1" {
		t.Fatalf("UserUUID = %q, want %q", detail.UserUUID, "user-1")
	}
}

func TestGetCurrentUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token/test-token":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("Authorization for token detail = %q, want empty", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(APIResponse[TokenDetail]{
				Msg: "OK",
				Data: TokenDetail{
					Token:       "test-token",
					TokenName:   "token-name",
					Application: "git",
					UserName:    "alice",
					UserUUID:    "user-1",
				},
			})
		case "/api/v1/user/alice":
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("Authorization for user = %q, want %q", got, "Bearer test-token")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(APIResponse[User]{
				Msg: "OK",
				Data: User{
					Username: "alice",
					Nickname: "Alice",
					Email:    "alice@example.com",
					UUID:     "user-1",
					Avatar:   "https://example.com/alice.png",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	user, err := client.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentUser error: %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("Username = %q, want %q", user.Username, "alice")
	}
	if user.Nickname != "Alice" {
		t.Fatalf("Nickname = %q, want %q", user.Nickname, "Alice")
	}
	if user.Email != "alice@example.com" {
		t.Fatalf("Email = %q, want %q", user.Email, "alice@example.com")
	}
}

func TestGetCurrentUserFallsBackToTokenOwner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token/test-token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(APIResponse[TokenDetail]{
				Msg: "OK",
				Data: TokenDetail{
					Token:    "test-token",
					UserName: "alice",
					UserUUID: "user-1",
				},
			})
		case "/api/v1/user/alice":
			http.Error(w, "boom", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	user, err := client.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentUser error: %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("Username = %q, want %q", user.Username, "alice")
	}
	if user.UUID != "user-1" {
		t.Fatalf("UUID = %q, want %q", user.UUID, "user-1")
	}
}

func TestGetBuiltinAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/user-1/apikeys/builtin" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/api/v1/namespaces/user-1/apikeys/builtin")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer test-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(APIResponse[map[string]any]{
			Msg: "OK",
			Data: map[string]any{
				"api_key": "builtin-key",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	apiKey, err := client.GetBuiltinAPIKey(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("GetBuiltinAPIKey error: %v", err)
	}
	if apiKey != "builtin-key" {
		t.Fatalf("apiKey = %q, want builtin-key", apiKey)
	}
}

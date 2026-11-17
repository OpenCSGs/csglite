package csghub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserLoginTokenVerifiesOwner(t *testing.T) {
	for _, scenario := range []string{"reuse", "create", "wrong_owner", "invalid_jwt", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			creates := 0
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/hf/api/whoami-v2":
					if r.Header.Get("Authorization") != "Bearer sso-jwt" {
						t.Error("missing SSO credential")
					}
					if scenario == "invalid_jwt" {
						w.WriteHeader(401)
						return
					}
					_, _ = w.Write([]byte(`{"name":"alice"}`))
				case r.URL.Path == "/api/v1/user/alice/tokens":
					tokens := []map[string]string{{"token": "account-token", "application": "csghub"}}
					if scenario == "create" {
						tokens = nil
					}
					if scenario == "expired" {
						tokens[0]["expire_at"] = "2020-01-01T00:00:00Z"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": tokens})
				case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/v1/token/csghub/csglite-"):
					creates++
					_, _ = w.Write([]byte(`{"data":{"token":"account-token"}}`))
				case r.URL.Path == "/api/v1/token/account-token":
					owner := "alice"
					if scenario == "wrong_owner" {
						owner = "bob"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"user_name": owner}})
				case strings.HasPrefix(r.URL.Path, "/api/v1/user/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"username": strings.TrimPrefix(r.URL.Path, "/api/v1/user/")}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer hub.Close()
			token, err := NewClient(hub.URL, "sso-jwt").BrowserLoginToken(t.Context())
			rejected := scenario == "wrong_owner" || scenario == "invalid_jwt"
			if rejected {
				if err == nil || token != "" {
					t.Fatal("accepted an unverified or mismatched identity")
				}
				return
			}
			if err != nil || token != "account-token" {
				t.Fatalf("login failed: %v", err)
			}
			wantCreates := 0
			if scenario == "create" || scenario == "expired" {
				wantCreates = 1
			}
			if creates != wantCreates {
				t.Fatalf("created %d credentials, want %d", creates, wantCreates)
			}
		})
	}
}

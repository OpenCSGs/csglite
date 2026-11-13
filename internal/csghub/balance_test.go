package csghub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAccountBalance(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want float64
		fail bool
	}{
		{"positive", `{"data":{"balance":12345.5}}`, 12345.5, false},
		{"zero", `{"data":{"balance":0}}`, 0, false},
		{"negative", `{"data":{"balance":-120}}`, -120, false},
		{"missing", `{"data":{}}`, 0, true},
		{"null", `{"data":{"balance":null}}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/accounting/credit/user-1/balance" {
					t.Errorf("unexpected balance request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer account-token" {
					t.Error("balance request did not use the current account token")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			balance, err := NewClient(upstream.URL, "account-token").GetAccountBalance(context.Background(), "user-1")
			if (err != nil) != tc.fail || balance != tc.want {
				t.Fatalf("balance = %v, error = %v; want %v, error: %v", balance, err, tc.want, tc.fail)
			}
		})
	}
}

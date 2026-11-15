package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestCloudAccountBalance(t *testing.T) {
	for _, tc := range []struct {
		name      string
		balance   string
		status    int
		available bool
	}{
		{"positive", "12345", http.StatusOK, true},
		{"zero", "0", http.StatusOK, true},
		{"billing disabled", "", http.StatusNotFound, false},
		{"billing failure", "", http.StatusBadGateway, false},
		{"missing balance", "null", http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			balanceRequests := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/token/current-token":
					_, _ = w.Write([]byte(`{"data":{"user_name":"alice","user_uuid":"user-1"}}`))
				case "/api/v1/user/alice":
					_, _ = w.Write([]byte(`{"data":{"username":"alice","uuid":"user-1"}}`))
				case "/api/v1/accounting/credit/user-1/balance":
					balanceRequests++
					if r.Header.Get("Authorization") != "Bearer current-token" {
						t.Error("balance used a credential other than the current access token")
					}
					w.WriteHeader(tc.status)
					if tc.status == http.StatusOK {
						_, _ = fmt.Fprintf(w, `{"data":{"balance":%s}}`, tc.balance)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			s := newTestServer(t)
			s.cfg.ServerURL, s.cfg.Token = upstream.URL, "current-token"
			s.cfg.OpenCSGAPIKey = "inference-key-not-for-billing"
			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cloud/balance", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var result cloudAccountBalance
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if (result.Balance != nil) != tc.available {
				t.Fatalf("unexpected balance availability: %+v", result)
			}
			if result.Balance != nil && (tc.balance == "0" && *result.Balance != 0 || tc.balance == "12345" && *result.Balance != 12345) {
				t.Fatal("balance unit was changed")
			}
			if result.RechargeURL != upstream.URL+"/settings/recharge-payment" {
				t.Fatalf("wrong community recharge URL: %q", result.RechargeURL)
			}
			if result.UserUUID != "user-1" {
				t.Fatal("balance was not tied to the authenticated account")
			}
			if rec.Header().Get("Cache-Control") != "no-store" || balanceRequests != 1 {
				t.Fatal("balance should be fetched once without caching")
			}
		})
	}
}

func TestCloudAccountBalanceRequiresLogin(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleCloudAccountBalance(rec, httptest.NewRequest(http.MethodGet, "/api/cloud/balance", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCloudAuthRechargeURLUsesCommunityWebsite(t *testing.T) {
	resetCommunityLoginCache()
	t.Cleanup(resetCommunityLoginCache)
	s := newTestServer(t)
	for _, tc := range []struct{ server, want string }{
		{config.DefaultServerURL, "https://opencsg.com/settings/recharge-payment"},
		{"https://sanxia.opencsg.com", "https://sanxia.opencsg.com/settings/recharge-payment"},
		{"https://opencsg.io", "https://opencsg.io/settings/recharge-payment"},
	} {
		s.cfg.ServerURL = tc.server
		storeCommunityLogin(tc.server, cachedCommunityLogin{url: tc.server + "/login"})
		// No upstream request is needed while checking the website link.
		status := s.cloudAuthStatus(t.Context())
		if status.RechargeURL != tc.want {
			t.Fatalf("recharge URL = %q, want %q", status.RechargeURL, tc.want)
		}
	}
}

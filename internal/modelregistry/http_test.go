package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

func TestHTTPStatusCode(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantOK     bool
	}{
		{
			name:       "csghub error",
			err:        &csghub.HTTPError{StatusCode: http.StatusUnauthorized, Body: "AUTH-ERR-5"},
			wantStatus: http.StatusUnauthorized,
			wantOK:     true,
		},
		{
			name: "local parse error",
			err: func() error {
				_, _, err := ParseRepoID("not-a-repo")
				return err
			}(),
			wantStatus: 0,
			wantOK:     false,
		},
		{
			name:       "nil error",
			err:        nil,
			wantStatus: 0,
			wantOK:     false,
		},
		{
			name:       "plain error",
			err:        errors.New("boom"),
			wantStatus: 0,
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, ok := HTTPStatusCode(tt.err)
			if status != tt.wantStatus || ok != tt.wantOK {
				t.Fatalf("HTTPStatusCode = (%d, %v), want (%d, %v)", status, ok, tt.wantStatus, tt.wantOK)
			}
		})
	}
}

func TestRegistryStatusErrorRateLimit(t *testing.T) {
	err := registryStatusError(http.StatusTooManyRequests, []byte(`{"error":"quota exceeded","retryAfter":22}`))
	var statusErr *registryHTTPError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *registryHTTPError", err)
	}
	if statusErr.statusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", statusErr.statusCode)
	}
	if statusErr.retryAfter != 22*time.Second {
		t.Fatalf("retryAfter = %v, want 22s", statusErr.retryAfter)
	}
	if !strings.Contains(err.Error(), "retry in about 22s") {
		t.Fatalf("message = %q, want a retry hint", err.Error())
	}
	if status, ok := RegistryStatus(err); !ok || status != http.StatusTooManyRequests {
		t.Fatalf("RegistryStatus = (%d, %v), want (429, true)", status, ok)
	}
	if wait, ok := RetryAfter(err); !ok || wait != 22*time.Second {
		t.Fatalf("RetryAfter = (%v, %v), want (22s, true)", wait, ok)
	}
}

func TestRegistryGetJSONRetriesShortRateLimit(t *testing.T) {
	var calls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"quota exceeded","retryAfter":0.05}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer apiServer.Close()

	client := newRegistryHTTPClient(apiServer.URL, "")
	var out map[string]string
	if _, err := client.getJSON(context.Background(), apiServer.URL+"/api/models/acme/demo", &out); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if out["ok"] != "yes" {
		t.Fatalf("payload = %#v, want the retried response", out)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("requests = %d, want 2 (one retry)", got)
	}
}

func TestRegistryGetJSONKeepsLongRateLimit(t *testing.T) {
	var calls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"quota exceeded","retryAfter":22}`))
	}))
	defer apiServer.Close()

	client := newRegistryHTTPClient(apiServer.URL, "")
	var out map[string]string
	_, err := client.getJSON(context.Background(), apiServer.URL+"/api/models/acme/demo", &out)
	if err == nil {
		t.Fatal("getJSON succeeded, want the rate limit error")
	}
	if status, ok := RegistryStatus(err); !ok || status != http.StatusTooManyRequests {
		t.Fatalf("RegistryStatus = (%d, %v), want (429, true)", status, ok)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("requests = %d, want 1 (a long quota window must not block)", got)
	}
}

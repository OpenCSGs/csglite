package csghub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLicenseStoreEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/license-store/checkout-sessions":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding body: %v", err)
			}
			if body["product"] != "CSGLite" || body["device_id"] != "dev-1" || body["client_version"] != "1.2.3" {
				t.Errorf("body = %v", body)
			}
			_, _ = w.Write([]byte(`{"msg":"OK","data":{"sid":"s-1","checkout_url":"https://hub.example/licenses/checkout?sid=s-1","expire_at":"2026-10-10T12:30:00Z"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/license-store/checkout-sessions/s-1":
			_, _ = w.Write([]byte(`{"msg":"OK","data":{"sid":"s-1","product":"CSGLite","status":"completed","owner_name":"alice","order_no":"o-1","expire_at":"2026-10-10T12:30:00Z","paid_by_other":true,"buyer_name":"b***b"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/license-store/current":
			if q := r.URL.Query(); q.Get("product") != "CSGLite" || q.Get("device_id") != "dev-1" {
				t.Errorf("query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"msg":"OK","data":{"status":"active","product":"CSGLite","license":"PEM","edition":"Enterprise","expire_time":"2027-10-10T00:00:00Z","device_count":3}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok")
	ctx := context.Background()

	session, err := c.CreateCheckoutSession(ctx, "CSGLite", "dev-1", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if session.SID != "s-1" || session.CheckoutURL == "" || session.ExpireAt.IsZero() {
		t.Fatalf("session = %+v", session)
	}

	status, err := c.GetCheckoutSession(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != CheckoutCompleted || status.Product != "CSGLite" || status.OwnerName != "alice" || status.OrderNo != "o-1" || !status.PaidByOther || status.BuyerName != "b***b" {
		t.Fatalf("status = %+v", status)
	}
	if _, err := c.GetCheckoutSession(ctx, " "); err == nil {
		t.Fatal("an empty session ID was accepted")
	}

	current, err := c.GetCurrentLicense(ctx, "CSGLite", "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "active" || current.Product != "CSGLite" || current.License != "PEM" || current.Edition != "Enterprise" || current.DeviceCount != 3 {
		t.Fatalf("current = %+v", current)
	}
}

func TestCreateCheckoutSessionRequiresURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"msg":"OK","data":{"sid":"s-1"}}`))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "tok").CreateCheckoutSession(context.Background(), "CSGLite", "d", "v"); err == nil {
		t.Fatal("a session without a checkout URL was accepted")
	}
}

func TestAPIErrorKeepsStatusAndMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"msg":"license store is closed"}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "tok").GetCurrentLicense(context.Background(), "CSGLite", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not an *APIError", err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Message() != "license store is closed" {
		t.Fatalf("status %d, message %q", apiErr.StatusCode, apiErr.Message())
	}
	if want := `API error 403: {"msg":"license store is closed"}`; apiErr.Error() != want {
		t.Fatalf("Error() = %q, want %q", apiErr.Error(), want)
	}
	if (&APIError{StatusCode: 502, Body: "  bad gateway "}).Message() != "bad gateway" {
		t.Fatal("non-JSON body was not used as the message")
	}
}

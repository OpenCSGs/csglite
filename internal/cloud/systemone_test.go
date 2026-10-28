package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSystemOnePostsModelAndToken(t *testing.T) {
	var path, token string
	var body map[string]any
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		token = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"answers":{},"usage":{"input_tokens":3}}`))
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)
	data, err := svc.SystemOne(context.Background(), "test-key", "jev-1.13", []byte(`{"state":"hello","questions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/systemone" {
		t.Fatalf("path = %q, want /v1/systemone", path)
	}
	if token != "Bearer test-key" {
		t.Fatalf("Authorization = %q", token)
	}
	if body["model"] != "jev-1.13" || body["state"] != "hello" {
		t.Fatalf("body = %#v", body)
	}
	if string(data) != `{"answers":{},"usage":{"input_tokens":3}}` {
		t.Fatalf("data = %q", data)
	}
}

func TestSystemOneReturnsStatusError(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)
	_, err := svc.SystemOne(context.Background(), "", "jev-1.13", []byte(`{"state":"x"}`))
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound || statusErr.Message() != "not found" {
		t.Fatalf("err = %v", err)
	}
}

func TestSystemOneRequiresBaseURL(t *testing.T) {
	svc := NewService("")
	if _, err := svc.SystemOne(context.Background(), "", "jev-1.13", []byte(`{}`)); err == nil {
		t.Fatal("expected an error without a gateway URL")
	}
}

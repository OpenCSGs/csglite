package csghub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListLocalModelFilterPostsRequestAndUnwrapsResponse(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotRequest LocalModelFilterRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(APIResponse[LocalModelFilterResponse]{
			Msg: "OK",
			Data: LocalModelFilterResponse{
				Data: []Model{{
					Name:          "demo",
					Path:          "acme/demo",
					DefaultBranch: "main",
				}},
				LocalCheck: []LocalModelFilterCandidate{{
					Model: Model{
						Name:          "candidate",
						Path:          "acme/candidate",
						DefaultBranch: "dev",
					},
					Reason: LocalModelFilterReasonMissingArchitecture,
				}},
				TotalConfirmed:      1,
				TotalExact:          true,
				HasMore:             false,
				CandidatesQueried:   true,
				LocalCheckExhausted: true,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret")
	response, err := client.ListLocalModelFilter(context.Background(), LocalModelFilterRequest{
		Filters: LocalModelFilterOptions{
			Sort: "trending",
			Tags: []TagFilter{{Category: "task", Name: "text-generation"}},
		},
		ExcludePaths: []string{"acme/seen"},
		Page:         2,
		Per:          16,
	})
	if err != nil {
		t.Fatalf("ListLocalModelFilter: %v", err)
	}
	if gotPath != "/api/v1/models/local_filter" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotRequest.Page != 2 || gotRequest.Per != 16 {
		t.Fatalf("page/per = %d/%d", gotRequest.Page, gotRequest.Per)
	}
	if len(gotRequest.ExcludePaths) != 1 || gotRequest.ExcludePaths[0] != "acme/seen" {
		t.Fatalf("exclude paths = %v", gotRequest.ExcludePaths)
	}
	if len(response.Data) != 1 || response.Data[0].Path != "acme/demo" {
		t.Fatalf("models = %#v", response.Data)
	}
	if response.Data[0].ArtifactSource != "opencsg" || response.Data[0].Revision != "main" {
		t.Fatalf("model source/revision = %q/%q", response.Data[0].ArtifactSource, response.Data[0].Revision)
	}
	if len(response.LocalCheck) != 1 {
		t.Fatalf("candidates = %#v", response.LocalCheck)
	}
	if candidate := response.LocalCheck[0].Model; candidate.ArtifactSource != "opencsg" || candidate.Revision != "dev" {
		t.Fatalf("candidate source/revision = %q/%q", candidate.ArtifactSource, candidate.Revision)
	}
	if !response.CandidatesQueried || !response.LocalCheckExhausted {
		t.Fatalf("response flags = %#v", response)
	}
}

func TestListLocalModelFilterReportsHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.ListLocalModelFilter(context.Background(), LocalModelFilterRequest{Per: 16})
	if err == nil {
		t.Fatal("want an error")
	}
	httpErr, ok := AsHTTPError(err)
	if !ok || httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %v, want a 429 HTTP error", err)
	}
}

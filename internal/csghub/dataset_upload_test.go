package csghub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDatasetAndUploadFile(t *testing.T) {
	var sawCreate, sawUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v1/datasets":
			sawCreate = true
			var request CreateDatasetRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Namespace != "alice" || request.Name != "trace-data" || !request.Private {
				t.Fatalf("create request = %+v", request)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"name":"trace-data","path":"alice/trace-data","private":true}}`)
		case "/api/v1/datasets/alice/trace-data/upload_file":
			sawUpload = true
			if err := r.ParseMultipartForm(1024 * 1024); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("file_path") != "data/train.jsonl" || r.FormValue("branch") != "main" {
				t.Fatalf("upload fields = path %q branch %q", r.FormValue("file_path"), r.FormValue("branch"))
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(file)
			_ = file.Close()
			if string(data) != `{"messages":[]}` {
				t.Fatalf("upload data = %q", data)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	dataset, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		Namespace: "alice", Name: "trace-data", Private: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Path != "alice/trace-data" {
		t.Fatalf("dataset = %+v", dataset)
	}
	localPath := filepath.Join(t.TempDir(), "train.jsonl")
	if err := os.WriteFile(localPath, []byte(`{"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.UploadDatasetFile(context.Background(), "alice", "trace-data", localPath, "data/train.jsonl", "main", "Upload data"); err != nil {
		t.Fatal(err)
	}
	if !sawCreate || !sawUpload {
		t.Fatalf("create=%v upload=%v", sawCreate, sawUpload)
	}
}

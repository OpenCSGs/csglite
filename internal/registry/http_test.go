package registry

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadOnceResumesOnlyValidPartialResponse(t *testing.T) {
	content := []byte("complete")
	sum := fmt.Sprintf("%x", sha256.Sum256(content))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=3-" {
			t.Errorf("Range = %q, want bytes=3-", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 3-7/8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content[3:])
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(dest+".part", content[:3], 0o644); err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.URL, "", "")
	if err := client.DownloadOnce(context.Background(), server.URL, dest, int64(len(content)), sum, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != string(content) {
		t.Fatalf("downloaded body = %q, %v", body, err)
	}
}

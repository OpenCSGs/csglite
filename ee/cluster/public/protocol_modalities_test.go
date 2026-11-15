// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTunnelForwardsDecisionImageAndAudioProtocols(t *testing.T) {
	g, server, dialer := newFixture(t, 50*time.Millisecond)
	fileBytes := []byte{0, 255, 1, 13, 10, 0, 128}
	startWorker(t, g, server, dialer, 1, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("client credentials reached worker")
		}
		if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			fields := map[string]string{}
			files := 0
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					return
				}
				data, err := io.ReadAll(part)
				if err != nil {
					t.Error(err)
					return
				}
				if part.FileName() != "" {
					files++
					if !bytes.Equal(data, fileBytes) {
						t.Error("file corruption")
					}
				} else {
					fields[part.FormName()] = string(data)
				}
			}
			if fields["model"] != "shared-model" || fields["source"] != "local" || files != 1 {
				t.Error("multipart lost target or file", fields, files)
			}
		} else {
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "shared-model" || body["source"] != "local" {
				t.Error("JSON lost target or local pin", body)
			}
		}
		if r.URL.Path == "/v1/audio/speech" {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(fileBytes)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	for _, path := range []string{"/v1/decisions", "/v1/images/generations", "/v1/images/edits", "/v1/audio/transcriptions", "/v1/audio/speech"} {
		t.Run(path, func(t *testing.T) {
			var body bytes.Buffer
			contentType := "application/json"
			if path == "/v1/images/edits" || path == "/v1/audio/transcriptions" {
				writer := multipart.NewWriter(&body)
				_ = writer.WriteField("model", "shared-model")
				field := "image"
				if path == "/v1/audio/transcriptions" {
					field = "file"
				}
				part, err := writer.CreateFormFile(field, "input.bin")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = part.Write(fileBytes)
				_ = writer.Close()
				contentType = writer.FormDataContentType()
			} else {
				body.WriteString(`{"model":"shared-model","input":"hello","prompt":"a cat"}`)
			}
			req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://unused.invalid"+path, &body)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("Authorization", "Bearer private")
			req.Header.Set("Cookie", "private=session")
			resp, err := g.RoundTrip("worker-1", req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("%d %s %v", resp.StatusCode, data, err)
			}
			if path == "/v1/audio/speech" && (!bytes.Equal(data, fileBytes) || resp.Header.Get("Content-Type") != "audio/wav") {
				t.Fatal("speech output corrupted")
			}
		})
	}
}

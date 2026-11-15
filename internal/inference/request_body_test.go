package inference

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

func TestModelRequestJSONKeepsProtocolFields(t *testing.T) {
	data := []byte(`{"model":"service","input":"你好","questions":[{"type":"predicate"}],"temperature":0.125}`)
	target, err := ParseModelRequest(data, "application/json; charset=utf-8")
	if err != nil || target.Model != "service" || target.Source != "" {
		t.Fatal(target, err)
	}
	out, contentType, err := RewriteModelRequest(data, "application/json", "deployment/one", "local")
	if err != nil || contentType != "application/json" {
		t.Fatal(contentType, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil || payload["model"] != "deployment/one" || payload["source"] != "local" || payload["input"] != "你好" || payload["temperature"] != 0.125 {
		t.Fatalf("rewritten payload: %s, %v", out, err)
	}
	for _, bad := range []string{`null`, `[]`, `"model"`, `{}`, `{"model":123}`, `{"model":"x","source":{}}`} {
		if _, err := ParseModelRequest([]byte(bad), "application/json"); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestModelRequestMultipartPreservesFilesAndReplacesTargets(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", "public-service")
	_ = w.WriteField("source", "pool:test")
	_ = w.WriteField("prompt", "给图片添加颜色")
	fileBytes := []byte{0, 255, 13, 10, 0, 128, 42}
	for _, name := range []string{"image", "mask", "file"} {
		part, err := w.CreateFormFile(name, name+".bin")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(fileBytes)
	}
	_ = w.Close()
	target, err := ParseModelRequest(buf.Bytes(), w.FormDataContentType())
	if err != nil || target.Model != "public-service" || target.Source != "pool:test" {
		t.Fatal(target, err)
	}
	data, contentType, err := RewriteModelRequest(buf.Bytes(), w.FormDataContentType(), "org/model", "local")
	if err != nil {
		t.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(contentType)
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	seen := map[string]int{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		seen[part.FormName()]++
		switch part.FormName() {
		case "model":
			if string(value) != "org/model" {
				t.Fatal(string(value))
			}
		case "source":
			if string(value) != "local" {
				t.Fatal(string(value))
			}
		case "prompt":
			if string(value) != "给图片添加颜色" {
				t.Fatal(string(value))
			}
		default:
			if !bytes.Equal(value, fileBytes) || part.FileName() != part.FormName()+".bin" {
				t.Fatal("file bytes or filename changed")
			}
		}
	}
	if len(seen) != 6 || seen["model"] != 1 || seen["source"] != 1 {
		t.Fatal(seen)
	}
}

func TestModelRequestMultipartRejectsAmbiguousTargets(t *testing.T) {
	for _, bad := range []string{"duplicate", "file", "oversized", "missing"} {
		t.Run(bad, func(t *testing.T) {
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			switch bad {
			case "duplicate":
				_ = w.WriteField("model", "first")
				_ = w.WriteField("model", "second")
			case "file":
				part, _ := w.CreateFormFile("model", "model.txt")
				_, _ = part.Write([]byte("hidden-model"))
			case "oversized":
				_ = w.WriteField("model", strings.Repeat("x", 1025))
			}
			_ = w.Close()
			if _, err := ParseModelRequest(buf.Bytes(), w.FormDataContentType()); err == nil {
				t.Fatal("accepted invalid target")
			}
		})
	}
}

package inference

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// ModelRequest identifies the target in either JSON or multipart inference
// bodies. Multipart file bytes remain opaque and never use temporary files.
type ModelRequest struct {
	Model  string
	Source string
}

func ParseModelRequest(data []byte, contentType string) (ModelRequest, error) {
	return ReadModelRequest(bytes.NewReader(data), contentType)
}

// ReadModelRequest can inspect a spooled legacy upload without reading its file
// parts into memory. JSON and multipart use the same target validation.
func ReadModelRequest(input io.Reader, contentType string) (ModelRequest, error) {
	var out ModelRequest
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return out, fmt.Errorf("invalid content type")
	}
	if media == "multipart/form-data" {
		reader := multipart.NewReader(input, params["boundary"])
		seen := map[string]bool{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return out, fmt.Errorf("invalid multipart request: %w", err)
			}
			name := part.FormName()
			if name != "model" && name != "source" {
				continue
			}
			if seen[name] || part.FileName() != "" {
				return out, fmt.Errorf("duplicate or invalid %s field", name)
			}
			seen[name] = true
			value, err := io.ReadAll(io.LimitReader(part, 1025))
			if err != nil || len(value) > 1024 {
				return out, fmt.Errorf("invalid %s field", name)
			}
			if name == "model" {
				out.Model = string(value)
			} else {
				out.Source = string(value)
			}
		}
	} else {
		var body struct {
			Model  string `json:"model"`
			Source string `json:"source"`
		}
		decoder := json.NewDecoder(input)
		var trailing interface{}
		if decoder.Decode(&body) != nil || decoder.Decode(&trailing) != io.EOF {
			return out, fmt.Errorf("a JSON object is required")
		}
		out.Model, out.Source = body.Model, body.Source
	}
	out.Model = strings.TrimSpace(out.Model)
	out.Source = strings.TrimSpace(out.Source)
	if out.Model == "" {
		return out, fmt.Errorf("model is required")
	}
	return out, nil
}

func RewriteModelRequest(data []byte, contentType, model, source string) ([]byte, string, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return nil, "", err
	}
	if media != "multipart/form-data" {
		var body map[string]json.RawMessage
		if json.Unmarshal(data, &body) != nil || body == nil {
			return nil, "", fmt.Errorf("a JSON object is required")
		}
		body["model"], _ = json.Marshal(model)
		body["source"], _ = json.Marshal(source)
		out, err := json.Marshal(body)
		return out, "application/json", err
	}
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}
		if part.FormName() == "model" || part.FormName() == "source" {
			continue
		}
		dst, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, "", err
		}
		if _, err := io.Copy(dst, part); err != nil {
			return nil, "", err
		}
	}
	if err := writer.WriteField("model", model); err != nil {
		return nil, "", err
	}
	if err := writer.WriteField("source", source); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return out.Bytes(), writer.FormDataContentType(), nil
}

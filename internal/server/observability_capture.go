package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

// Observe the bytes consumed by the handler without buffering uploads or
// changing its read errors. Multipart files are counted, never retained.
type observationRequestCapture struct {
	body      bytes.Buffer
	truncated bool
	pipe      *io.PipeWriter
	done      chan struct{}
}

type observationReadCloser struct {
	io.Reader
	io.Closer
}

func captureObservationRequest(r *http.Request) *observationRequestCapture {
	c := &observationRequestCapture{}
	var writer io.Writer = c
	media, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media == "multipart/form-data" && params["boundary"] == "" {
		writer = io.Discard
	} else if media == "multipart/form-data" {
		reader, pipe := io.Pipe()
		c.pipe, c.done = pipe, make(chan struct{})
		writer = observationMultipartTee{pipe}
		go func() {
			defer close(c.done)
			defer reader.Close()
			c.captureMultipart(multipart.NewReader(reader, params["boundary"]))
		}()
	} else if media != "" && media != "application/json" && !strings.HasSuffix(media, "+json") && !strings.HasPrefix(media, "text/") {
		writer = io.Discard
	} else if r.Body != nil {
		// Keep the existing JSON capture-before-routing behavior, bounded to
		// the display limit. Replay every byte and any read failure unchanged.
		data, err := io.ReadAll(io.LimitReader(r.Body, observabilityBodyLimit+1))
		_, _ = c.Write(data)
		rest := io.Reader(r.Body)
		if err != nil {
			rest = observationReadError{err}
		}
		r.Body = observationReadCloser{Reader: io.MultiReader(bytes.NewReader(data), rest), Closer: r.Body}
		return c
	}
	if r.Body != nil {
		r.Body = observationReadCloser{Reader: io.TeeReader(r.Body, writer), Closer: r.Body}
	}
	return c
}

type observationReadError struct{ err error }

func (r observationReadError) Read([]byte) (int, error) { return 0, r.err }

func (c *observationRequestCapture) Write(p []byte) (int, error) {
	remaining := observabilityBodyLimit - c.body.Len()
	if len(p) > remaining {
		c.truncated = true
	}
	if remaining > 0 {
		_, _ = c.body.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}

type observationMultipartTee struct{ pipe *io.PipeWriter }

func (w observationMultipartTee) Write(p []byte) (int, error) {
	// A malformed observation must not change what the inference handler reads.
	_, _ = w.pipe.Write(p)
	return len(p), nil
}

func (c *observationRequestCapture) finish() {
	if c.pipe != nil {
		_ = c.pipe.Close()
		<-c.done
		c.pipe = nil
	}
}

func (c *observationRequestCapture) captureMultipart(reader *multipart.Reader) {
	fields := map[string]any{}
	files := []map[string]any{}
	budget := observabilityBodyLimit / 2
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		name := part.FormName()
		if part.FileName() != "" {
			size, _ := io.Copy(io.Discard, part)
			if len(files) < 64 {
				files = append(files, map[string]any{"field": name, "filename": part.FileName(), "bytes": size, "content_type": part.Header.Get("Content-Type")})
			} else {
				c.truncated = true
			}
			continue
		}
		value, _ := io.ReadAll(io.LimitReader(part, int64(max(0, budget))+1))
		if len(value) > budget || len(fields) >= 128 {
			c.truncated = true
			continue
		}
		budget -= len(name) + len(value)
		fields[name] = string(value)
		if name == "stream" {
			fields[name], _ = strconv.ParseBool(strings.TrimSpace(string(value)))
		}
	}
	fields["_files"] = files
	data, _ := json.Marshal(fields)
	_, _ = c.Write(data)
}

func observationHasTextTokens(protocol string) bool {
	return protocol == "openai" || protocol == "responses" || protocol == "anthropic" || protocol == "lite"
}

func observationOmitsResponsePayload(protocol string) bool {
	switch protocol {
	case "embeddings", "images/generations", "images/edits", "audio/speech":
		return true
	}
	return false
}

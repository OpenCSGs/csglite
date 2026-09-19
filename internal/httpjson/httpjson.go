// Package httpjson holds the JSON response shape every CSGLite HTTP surface
// speaks: the operator API, the node-to-node cluster protocol, and whatever
// surface the next feature adds.
//
// It lives under the Apache-2.0 tree on purpose. Enterprise code may depend on
// Apache code, never the other way round, so putting the envelope here lets a
// feature under ee/ answer in the same shape without copying it, which is what
// happened when the cluster feature grew its own pair of writers beside the
// server's.
package httpjson

import (
	"encoding/json"
	"io"
	"net/http"
)

// Error is the error envelope. Error and ErrorCode are always present; the
// rest are set by the surfaces that have something more to say, such as a
// licence limit reporting the cap that was reached.
type Error struct {
	Error     string `json:"error"`
	ErrorCode int    `json:"errorCode"`
	// Code is a stable machine-readable reason such as "wrong_cluster", for
	// callers that must branch on the failure rather than show it.
	Code string `json:"code,omitempty"`
	// Limit and Current describe a quota that was reached.
	Limit   int `json:"limit,omitempty"`
	Current int `json:"current,omitempty"`
}

// Write encodes v as the response body. An encoding failure is deliberately
// ignored: the status line has already been written, so there is nothing left
// to tell the client.
func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError answers with the envelope, taking the machine code from the
// status alone.
func WriteError(w http.ResponseWriter, status int, msg string) {
	Write(w, status, Error{Error: msg, ErrorCode: status})
}

// WriteCodedError answers with the envelope plus a stable reason code.
func WriteCodedError(w http.ResponseWriter, status int, msg, code string) {
	Write(w, status, Error{Error: msg, ErrorCode: status, Code: code})
}

// ReadBody reads at most limit bytes of a request body, so a malformed or
// hostile caller cannot make the server allocate without bound.
func ReadBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r.Body, limit))
}

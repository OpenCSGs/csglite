package csghub

import (
	"encoding/json"
	"errors"
	"fmt"
)

// HTTPError is returned for any non-2xx response. Callers that need to branch
// on the status or on a CSGHub error code use errors.As; the message keeps the
// historical "API error <status>: <body>" wording so log output and callers
// that only compare error strings do not change.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// ErrorEnvelope is the CSGHub error body: {"code":"LICENSE-ERR-7","msg":"...","context":{...}}.
// Not every endpoint fills code or context.
type ErrorEnvelope struct {
	Code    string         `json:"code"`
	Msg     string         `json:"msg"`
	Context map[string]any `json:"context"`
}

// AsHTTPError returns the HTTP error behind err, if there is one.
func AsHTTPError(err error) (*HTTPError, bool) {
	var httpErr *HTTPError
	if err != nil && errors.As(err, &httpErr) {
		return httpErr, true
	}
	return nil, false
}

// ErrorEnvelopeOf parses the JSON body of an HTTP error. It returns false when
// err is not an HTTP error or the body is not a CSGHub error envelope, which
// is how callers tell a business 404 from a framework one.
func ErrorEnvelopeOf(err error) (ErrorEnvelope, bool) {
	httpErr, ok := AsHTTPError(err)
	if !ok || httpErr.Body == "" {
		return ErrorEnvelope{}, false
	}
	var envelope ErrorEnvelope
	if json.Unmarshal([]byte(httpErr.Body), &envelope) != nil {
		return ErrorEnvelope{}, false
	}
	if envelope.Code == "" && envelope.Msg == "" {
		return ErrorEnvelope{}, false
	}
	return envelope, true
}

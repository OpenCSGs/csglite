package correlation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	RequestIDHeader = "X-Request-ID"
	B3TraceIDHeader = "X-B3-TraceId"
	TraceIDHeader   = "X-CSGLite-Trace-ID"
	ThreadIDHeader  = "X-CSGLite-Thread-ID"

	maxIDLength = 128
)

type contextKey struct{}

// Values contains correlation identifiers for one inbound API request.
// TraceID is the logical csghub-lite trace, while B3TraceID identifies the
// distributed request path through trusted downstream inference services.
type Values struct {
	RequestID string
	TraceID   string
	B3TraceID string
	ThreadID  string
}

// FromHeaders accepts valid caller identifiers and fills every missing value.
func FromHeaders(headers http.Header) Values {
	requestID := NormalizeID(headers.Get(RequestIDHeader))
	if requestID == "" {
		requestID = newPrefixedID("request")
	}
	b3TraceID := NormalizeB3TraceID(headers.Get(B3TraceIDHeader))
	traceID := NormalizeID(headers.Get(TraceIDHeader))
	if traceID == "" {
		if b3TraceID != "" {
			traceID = b3TraceID
		} else {
			traceID = newTraceID()
		}
	}
	if b3TraceID == "" {
		if compatible := NormalizeB3TraceID(traceID); compatible != "" {
			b3TraceID = compatible
		} else {
			b3TraceID = newTraceID()
		}
	}
	threadID := NormalizeID(headers.Get(ThreadIDHeader))
	if threadID == "" {
		threadID = newPrefixedID("thread")
	}
	return Values{
		RequestID: requestID,
		TraceID:   traceID,
		B3TraceID: b3TraceID,
		ThreadID:  threadID,
	}
}

func WithContext(ctx context.Context, values Values) context.Context {
	return context.WithValue(ctx, contextKey{}, values)
}

func FromContext(ctx context.Context) (Values, bool) {
	if ctx == nil {
		return Values{}, false
	}
	values, ok := ctx.Value(contextKey{}).(Values)
	return values, ok
}

// ApplyRequestHeaders propagates correlation only to trusted downstreams.
func ApplyRequestHeaders(req *http.Request) {
	if req == nil {
		return
	}
	values, ok := FromContext(req.Context())
	if !ok {
		return
	}
	req.Header.Set(RequestIDHeader, values.RequestID)
	req.Header.Set(B3TraceIDHeader, values.B3TraceID)
	req.Header.Set(TraceIDHeader, values.TraceID)
	req.Header.Set(ThreadIDHeader, values.ThreadID)
}

func ApplyResponseHeaders(headers http.Header, values Values) {
	headers.Set(RequestIDHeader, values.RequestID)
	headers.Set(B3TraceIDHeader, values.B3TraceID)
	headers.Set(TraceIDHeader, values.TraceID)
	headers.Set(ThreadIDHeader, values.ThreadID)
}

// NormalizeID accepts printable correlation IDs without whitespace or header
// delimiters. This keeps logs single-line and prevents malformed propagation.
func NormalizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxIDLength {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
			continue
		}
		return ""
	}
	return value
}

// NormalizeB3TraceID enforces the B3 requirement of 64- or 128-bit non-zero
// lowercase hexadecimal trace identifiers.
func NormalizeB3TraceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 16 && len(value) != 32 {
		return ""
	}
	allZero := true
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return ""
		}
		if char != '0' {
			allZero = false
		}
	}
	if allZero {
		return ""
	}
	return value
}

func newTraceID() string {
	return randomHex(16)
}

func newPrefixedID(prefix string) string {
	return prefix + "-" + randomHex(16)
}

func randomHex(size int) string {
	random := make([]byte, size)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(random)
}

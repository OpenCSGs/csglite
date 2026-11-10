package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// systemOneMaxResponseBytes caps the gateway response we buffer. A System One
// answer is a small JSON object; anything larger is a proxy error page.
const systemOneMaxResponseBytes = 8 << 20

// systemOneClient is separate from s.client: the model-list client times out
// after 15s, which is too short for a decision request carrying a large state.
// The caller's context owns the deadline, matching the inference engines.
var systemOneClient = &http.Client{Timeout: 0}

// StatusError is a non-200 answer from the cloud gateway.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("systemone returned %d: %s", e.StatusCode, e.Message())
}

// Message returns the gateway's response body, or a status-only fallback when
// the body is empty.
func (e *StatusError) Message() string {
	if body := strings.TrimSpace(e.Body); body != "" {
		return body
	}
	return fmt.Sprintf("systemone returned %d", e.StatusCode)
}

// SystemOne posts a TypeSafe System One request body to the gateway's
// /v1/systemone and returns the raw response body. It patches the body's model
// field to model before sending.
func (s *Service) SystemOne(ctx context.Context, apiKey, model string, body []byte) ([]byte, error) {
	baseURL := s.BaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("cloud gateway URL is not configured")
	}
	payload, err := withSystemOneModel(body, model)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/systemone", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating systemone request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(apiKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := systemOneClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting systemone request: %w", err)
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, systemOneMaxResponseBytes))
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading systemone response: %w", readErr)
	}
	return data, nil
}

// withSystemOneModel patches the model field into a System One body. The
// translator that builds the body (decision.PrepareNativeOpenAI) omits model
// because llama-server does not need it, but the gateway requires it.
func withSystemOneModel(body []byte, model string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decoding systemone request: %w", err)
	}
	if model = strings.TrimSpace(model); model != "" {
		encoded, err := json.Marshal(model)
		if err != nil {
			return nil, err
		}
		payload["model"] = encoded
	}
	patched, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding systemone request: %w", err)
	}
	return patched, nil
}

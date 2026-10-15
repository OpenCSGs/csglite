package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/correlation"
)

// SystemOneAnswerer is implemented by engines that serve a native decision
// model (laya, openjev, kev, ...): llama-server answers typed questions in one
// forward pass at /v1/systemone, with the head and temperatures stored in the
// GGUF.
type SystemOneAnswerer interface {
	// NativeDecisionType is the model's "<arch>.decision.type", or "" when
	// the model is not a native decision model.
	NativeDecisionType() string
	// SystemOne posts a TypeSafe System One request body and returns the
	// response body.
	SystemOne(ctx context.Context, body []byte) ([]byte, error)
}

// DecisionOnlyType reports whether llama-server runs a native decision model
// of this type in embedding mode: such a model reads its answers from hidden
// states, computes no logits and so cannot generate text.
func DecisionOnlyType(decisionType string) bool {
	switch decisionType {
	case "laya", "kev", "clef", "lfm2-d1-omni":
		return true
	default:
		return false
	}
}

// decisionOnlyBatchArgs sizes the batch for an embedding-mode decision model.
// llama-server evaluates such a prompt in one ubatch and caps n_batch at
// n_ubatch, so the default 512-token ubatch would refuse any longer input.
func decisionOnlyBatchArgs(numCtx int) []string {
	n := fmt.Sprintf("%d", numCtx)
	return []string{"--batch-size", n, "--ubatch-size", n}
}

func (e *llamaEngine) NativeDecisionType() string { return e.decisionType }

// errDecisionOnly refuses text generation on a model that only answers
// decisions, instead of llama-server's "does not support logits" error.
func (e *llamaEngine) errDecisionOnly() error {
	if !DecisionOnlyType(e.decisionType) {
		return nil
	}
	return NewHTTPStatusError(http.StatusBadRequest, fmt.Sprintf(
		"model %q is a %s decision model: it answers typed questions only, through POST /v1/decisions; it cannot generate text",
		e.modelName, e.decisionType))
}

func (e *llamaEngine) SystemOne(ctx context.Context, body []byte) ([]byte, error) {
	if e.decisionType == "" {
		return nil, NewHTTPStatusError(http.StatusBadRequest, fmt.Sprintf("model %q is not a native decision model", e.modelName))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL()+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	correlation.ApplyRequestHeaders(req)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("decision request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("reading decision response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &inferenceHTTPError{status: resp.StatusCode, body: llamaErrorMessage(data)}
	}
	return data, nil
}

// llamaErrorMessage extracts the message of a llama-server error body.
func llamaErrorMessage(data []byte) string {
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &out) == nil && strings.TrimSpace(out.Error.Message) != "" {
		return out.Error.Message
	}
	if len(data) > 4096 {
		data = data[:4096]
	}
	return string(data)
}

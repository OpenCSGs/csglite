package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sync"

	"github.com/opencsgs/csglite/internal/correlation"
)

// DecisionScorer reads next-token log probabilities with a model's typed
// decision (System 1) adapter applied.
type DecisionScorer interface {
	// HasDecisionAdapter reports whether the engine was started with a
	// typed-decision adapter; without one ScoreDecisionTokens fails.
	HasDecisionAdapter() bool
	// ScoreDecisionTokens runs one prefill pass over prompt and returns the
	// log probability of each token ID, renormalized over tokenIDs, plus the
	// number of prompt tokens evaluated.
	ScoreDecisionTokens(ctx context.Context, prompt string, tokenIDs []int) (map[int]float64, int, error)
}

// DecisionLoRAResolver returns the LoRA adapter GGUF that serves a model's
// typed decisions, or "" when the model has none.
type DecisionLoRAResolver func(modelName string) (string, error)

var (
	decisionLoRAMu       sync.RWMutex
	decisionLoRAResolver DecisionLoRAResolver
)

// SetDecisionLoRAResolver registers the resolver llama engines consult at
// start-up to load a typed-decision adapter next to the base model.
func SetDecisionLoRAResolver(resolver DecisionLoRAResolver) {
	decisionLoRAMu.Lock()
	decisionLoRAResolver = resolver
	decisionLoRAMu.Unlock()
}

func resolveDecisionLoRA(modelName string) string {
	decisionLoRAMu.RLock()
	resolver := decisionLoRAResolver
	decisionLoRAMu.RUnlock()
	if resolver == nil || modelName == "" {
		return ""
	}
	path, err := resolver(modelName)
	if err != nil {
		log.Printf("LLAMA: typed decisions unavailable for model=%q: %v", modelName, err)
		return ""
	}
	return path
}

// decisionLoRAArgs loads the adapter without applying it. The flag alone
// still leaves the server-wide default scale at 1, so disableDecisionLoRA
// zeroes it once the server is ready.
func decisionLoRAArgs(path string) []string {
	return []string{"--lora", path, "--lora-init-without-apply"}
}

// disableDecisionLoRA sets the adapter's server-wide scale to 0 so requests
// that do not ask for it (all chat traffic) run the unmodified base model.
func (e *llamaEngine) disableDecisionLoRA() error {
	body, _ := json.Marshal([]map[string]any{{"id": 0, "scale": 0}})
	req, err := http.NewRequest(http.MethodPost, e.baseURL()+"/lora-adapters", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("POST /lora-adapters returned %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// decisionLogitBias lifts the option tokens far above every other token so
// the post-sampling distribution llama-server reports covers exactly them.
// A shared bias cancels in the renormalized distribution.
const decisionLogitBias = 100.0

var errNoDecisionAdapter = errors.New("model has no typed-decision adapter loaded")

func (e *llamaEngine) HasDecisionAdapter() bool { return e.decisionLoRA }

func (e *llamaEngine) ScoreDecisionTokens(ctx context.Context, prompt string, tokenIDs []int) (map[int]float64, int, error) {
	if !e.decisionLoRA {
		return nil, 0, errNoDecisionAdapter
	}
	if len(tokenIDs) == 0 {
		return nil, 0, fmt.Errorf("no option tokens to score")
	}
	bias := make([][2]float64, len(tokenIDs))
	for i, id := range tokenIDs {
		bias[i] = [2]float64{float64(id), decisionLogitBias}
	}
	reqBody := map[string]any{
		"prompt":              prompt,
		"n_predict":           1,
		"temperature":         1.0,
		"top_k":               0,
		"top_p":               1.0,
		"min_p":               0.0,
		"typical_p":           1.0,
		"repeat_penalty":      1.0,
		"presence_penalty":    0.0,
		"frequency_penalty":   0.0,
		"dry_multiplier":      0.0,
		"xtc_probability":     0.0,
		"n_probs":             len(tokenIDs),
		"post_sampling_probs": true,
		"logit_bias":          bias,
		"lora":                []map[string]any{{"id": 0, "scale": 1.0}},
		// llama-server's prompt cache does not key on the LoRA scale, so a
		// prefix cached by a base-model request would leak into the read-out.
		"cache_prompt": false,
	}
	slot, release := -1, func() {}
	if e.slots != nil {
		slot, release = e.slots.acquire(SlotAffinity(ctx))
	}
	defer release()
	if slot >= 0 {
		reqBody["id_slot"] = slot
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL()+"/completion", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	correlation.ApplyRequestHeaders(req)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("decision request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, 0, &inferenceHTTPError{status: resp.StatusCode, body: string(msg)}
	}
	var out struct {
		TokensEvaluated int `json:"tokens_evaluated"`
		Probabilities   []struct {
			TopProbs []struct {
				ID   int     `json:"id"`
				Prob float64 `json:"prob"`
			} `json:"top_probs"`
		} `json:"completion_probabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("decoding decision response: %w", err)
	}
	if len(out.Probabilities) == 0 {
		return nil, 0, fmt.Errorf("decision response has no token probabilities")
	}
	return decisionLogprobs(out.Probabilities[0].TopProbs, tokenIDs), out.TokensEvaluated, nil
}

// decisionLogprobs keeps the requested token IDs and renormalizes their
// probabilities. Options llama-server did not report get a negligible floor.
func decisionLogprobs(top []struct {
	ID   int     `json:"id"`
	Prob float64 `json:"prob"`
}, tokenIDs []int) map[int]float64 {
	const floor = 1e-30
	probs := make(map[int]float64, len(tokenIDs))
	for _, id := range tokenIDs {
		probs[id] = floor
	}
	total := 0.0
	for _, p := range top {
		if _, ok := probs[p.ID]; ok && p.Prob > floor {
			probs[p.ID] = p.Prob
		}
	}
	for _, p := range probs {
		total += p
	}
	logprobs := make(map[int]float64, len(probs))
	for id, p := range probs {
		logprobs[id] = math.Log(p / total)
	}
	return logprobs
}

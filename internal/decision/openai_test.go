package decision

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func decodeOpenAIRequest(t *testing.T, body string) OpenAIRequest {
	t.Helper()
	var req OpenAIRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestOpenAIDecisionsMapsQuestionTypesOntoTheHead(t *testing.T) {
	p := loadTestProfile(t)
	// Option i gets logprob -(i+1); with the test head bias and temperature
	// this gives a decreasing distribution, so the first option wins.
	scorer := &fakeScorer{logprob: func(_ string, i int) float64 { return -float64(i + 1) }}
	req := decodeOpenAIRequest(t, `{
	  "model": "m",
	  "input": [{"role": "user", "content": [{"type": "input_text", "text": "I was charged twice"}, {"type": "input_text", "text": "for my order."}]}],
	  "questions": [
	    {"type": "predicate", "name": "refund", "instructions": "Is the customer asking for a refund?"},
	    {"type": "choice", "name": "department", "instructions": "Which department?", "choices": [
	      {"value": "billing", "description": "Payments, invoices, and refunds."},
	      {"value": true},
	      {"value": "other"}
	    ]},
	    {"type": "score", "instructions": "How severe is this issue?", "levels": [
	      {"label": "Cosmetic", "description": "Appearance only."},
	      {"label": "Workaround available"},
	      {"label": "Fully blocked"}
	    ]}
	  ]
	}`)
	prepared, err := p.PrepareOpenAI(req)
	if err != nil {
		t.Fatalf("PrepareOpenAI: %v", err)
	}
	resp, err := prepared.Run(context.Background(), scorer, "org/JEV")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	state := "[state] I was charged twice\nfor my order.\n"
	wantPrompts := []string{
		"[kind] noul\n" + state + "[question] Is the customer asking for a refund?\n[options]\nfalse\ntrue\n[decision]:",
		"[kind] choice\n" + state + "[question] Which department?\n[options]\nA) billing: Payments, invoices, and refunds.\nB) true\nC) other\n[decision]:",
		"[kind] choice\n" + state + "[question] How severe is this issue?\n[options]\nA) Cosmetic: Appearance only.\nB) Workaround available\nC) Fully blocked\n[decision]:",
	}
	for i, want := range wantPrompts {
		if scorer.prompts[i] != want {
			t.Fatalf("prompt %d = %q, want %q", i, scorer.prompts[i], want)
		}
	}

	raw, _ := json.Marshal(resp)
	var got struct {
		Answers []map[string]any `json:"answers"`
		Model   string           `json:"model"`
		Usage   map[string]any   `json:"usage"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "org/JEV" || got.Usage["input_tokens"] != float64(30) || got.Usage["output_tokens"] != float64(0) || got.Usage["total_tokens"] != float64(30) {
		t.Fatalf("model/usage = %q %v", got.Model, got.Usage)
	}

	predicate := got.Answers[0]
	if predicate["type"] != "predicate" || predicate["name"] != "refund" {
		t.Fatalf("predicate answer = %v", predicate)
	}
	if p, _ := predicate["probability"].(float64); p <= 0 || p >= 0.5 {
		t.Fatalf("predicate probability = %v, want P(true) < 0.5", predicate["probability"])
	}

	choice := got.Answers[1]
	if choice["choice"] != "billing" || choice["name"] != "department" {
		t.Fatalf("choice answer = %v", choice)
	}
	probs := choice["probabilities"].([]any)
	if probs[1].(map[string]any)["value"] != true {
		t.Fatalf("boolean choice value lost its type: %v", probs[1])
	}
	p0, p1 := probs[0].(map[string]any)["probability"].(float64), probs[1].(map[string]any)["probability"].(float64)
	if math.Abs(choice["confidence"].(float64)-(p0-p1)) > 1e-12 {
		t.Fatalf("confidence = %v, want top-1 minus top-2 = %v", choice["confidence"], p0-p1)
	}

	score := got.Answers[2]
	if score["name"] != nil {
		t.Fatalf("unnamed question must answer with name null: %v", score)
	}
	levels := score["probabilities"].([]any)
	weighted := 0.0
	for i, level := range levels {
		l := level.(map[string]any)
		if l["value"] != float64(i) {
			t.Fatalf("level %d value = %v", i, l["value"])
		}
		weighted += float64(i) * l["probability"].(float64)
	}
	if levels[2].(map[string]any)["label"] != "Fully blocked" || math.Abs(score["score"].(float64)-weighted) > 1e-12 {
		t.Fatalf("score answer = %v", score)
	}
}

func TestOpenAIDecisionsUsesTournamentBeyondHeadSlots(t *testing.T) {
	p := loadTestProfile(t)
	choices := make([]string, 20)
	for i := range choices {
		choices[i] = `{"value":"opt` + string(rune('a'+i)) + `"}`
	}
	req := decodeOpenAIRequest(t, `{"model":"m","input":"x","questions":[{"type":"choice","instructions":"which?","choices":[`+strings.Join(choices, ",")+`]}]}`)
	scorer := &fakeScorer{logprob: func(string, int) float64 { return -1 }}
	prepared, err := p.PrepareOpenAI(req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := prepared.Run(context.Background(), scorer, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(scorer.prompts) != 3 || len(resp.Answers[0].Probabilities) != 20 {
		t.Fatalf("passes %d, probabilities %d", len(scorer.prompts), len(resp.Answers[0].Probabilities))
	}
}

func TestOpenAIDecisionsRejectsInvalidRequests(t *testing.T) {
	p := loadTestProfile(t)
	tests := map[string]string{
		"missing input":     `{"model":"m","questions":[{"type":"predicate","instructions":"q"}]}`,
		"no questions":      `{"model":"m","input":"x","questions":[]}`,
		"image input":       `{"model":"m","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"questions":[{"type":"predicate","instructions":"q"}]}`,
		"assistant message": `{"model":"m","input":[{"role":"assistant","content":"x"}],"questions":[{"type":"predicate","instructions":"q"}]}`,
		"unknown type":      `{"model":"m","input":"x","questions":[{"type":"rank","instructions":"q"}]}`,
		"no instructions":   `{"model":"m","input":"x","questions":[{"type":"predicate"}]}`,
		"one choice":        `{"model":"m","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":"a"}]}]}`,
		"duplicate choice":  `{"model":"m","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":"a"},{"value":"a"}]}]}`,
		"numeric choice":    `{"model":"m","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":1},{"value":2}]}]}`,
		"one level":         `{"model":"m","input":"x","questions":[{"type":"score","instructions":"q","levels":[{"label":"low"}]}]}`,
	}
	for name, body := range tests {
		_, err := p.PrepareOpenAI(decodeOpenAIRequest(t, body))
		var reqErr *RequestError
		if !errors.As(err, &reqErr) {
			t.Fatalf("%s: want RequestError, got %v", name, err)
		}
	}
	// A string and a boolean with the same text are distinct values.
	if _, err := p.PrepareOpenAI(decodeOpenAIRequest(t, `{"model":"m","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":"true"},{"value":true}]}]}`)); err != nil {
		t.Fatalf("typed values should be distinct: %v", err)
	}
}

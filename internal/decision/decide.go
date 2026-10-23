package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxOptions caps choice questions; a tournament over 16-option groups
// covers up to 16 groups in one round.
const MaxOptions = 256

// Request is the JEV /v1/decide request body.
type Request struct {
	Model     string          `json:"model"`
	Source    string          `json:"source,omitempty"`
	Kind      string          `json:"kind"`
	State     json.RawMessage `json:"state,omitempty"`
	Question  string          `json:"question"`
	Options   []string        `json:"options,omitempty"`
	Strategy  string          `json:"strategy,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Threshold *float64        `json:"threshold,omitempty"`
}

// Response mirrors the hosted JEV API's decision response.
type Response struct {
	Kind             string    `json:"kind"`
	EffectiveKind    string    `json:"effective_kind"`
	Options          []string  `json:"options"`
	Probabilities    []float64 `json:"probabilities"`
	ChoiceIndex      int       `json:"choice_index"`
	Choice           string    `json:"choice"`
	Adaptation       string    `json:"adaptation"`
	Protocol         string    `json:"protocol"`
	Model            string    `json:"model"`
	Usage            Usage     `json:"usage"`
	ElapsedSeconds   float64   `json:"elapsed_seconds"`
	NumModelRequests int       `json:"num_model_requests"`
}

// Usage counts tokens across all model passes of one decision.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Info describes a model's typed-decision support (GET /v1/decide/info).
type Info struct {
	Protocol            string             `json:"protocol"`
	Model               string             `json:"model"`
	MaxOptions          int                `json:"max_options"`
	NativeChoiceOptions int                `json:"native_choice_options"`
	Temperatures        map[string]float64 `json:"temperatures"`
	Defaults            map[string]any     `json:"defaults"`
	System2Available    bool               `json:"system2_available"`
}

// Scorer is the model-side read-out; inference.DecisionScorer satisfies it.
type Scorer interface {
	ScoreDecisionTokens(ctx context.Context, prompt string, tokenIDs []int) (map[int]float64, int, error)
}

// RequestError is a client error in a decision request.
type RequestError struct{ msg string }

func (e *RequestError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &RequestError{msg: fmt.Sprintf(format, args...)}
}

// Info reports the decision capabilities of the profile's model.
func (p *Profile) Info(modelID string) Info {
	return Info{
		Protocol:            p.Protocol(),
		Model:               modelID,
		MaxOptions:          MaxOptions,
		NativeChoiceOptions: p.NativeChoiceOptions(),
		Temperatures:        p.Temperatures,
		Defaults:            map[string]any{"strategy": "tournament", "thinking": "off"},
		System2Available:    false,
	}
}

type decideCtx struct {
	scorer       Scorer
	requests     int
	promptTokens int
}

// Decision is a validated typed question, ready to run against a model.
type Decision struct {
	profile    *Profile
	kind       string
	state      string
	question   string
	options    []string
	adaptation string
}

// Prepare validates a request without touching the model, so malformed
// questions are refused before a model is loaded.
func (p *Profile) Prepare(req Request) (*Decision, error) {
	d := &Decision{profile: p, kind: strings.ToLower(strings.TrimSpace(req.Kind)), question: req.Question, adaptation: "native"}
	switch d.kind {
	case "noul":
		d.options = []string{"false", "true"}
	case "score":
		d.options = []string{"0", "1", "2", "3", "4", "5"}
	case "choice":
		d.options = req.Options
		if len(d.options) < 2 || len(d.options) > MaxOptions {
			return nil, badRequest("choice needs 2-%d options, got %d", MaxOptions, len(d.options))
		}
	default:
		return nil, badRequest("kind must be noul, score or choice")
	}
	if strings.TrimSpace(req.Question) == "" {
		return nil, badRequest("question is required")
	}
	switch strings.ToLower(strings.TrimSpace(req.Thinking)) {
	case "", "default", "off":
	case "auto", "on":
		return nil, badRequest("thinking %q (System 2 escalation) is not supported; use thinking \"off\" or ask the same model through /v1/chat/completions", req.Thinking)
	default:
		return nil, badRequest("thinking must be off, auto or on")
	}
	state, err := stateText(req.State)
	if err != nil {
		return nil, err
	}
	d.state = state

	native := p.NativeChoiceOptions()
	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	switch strategy {
	case "", "auto", "single", "tournament", "permute":
	default:
		return nil, badRequest("strategy must be auto, single, tournament or permute")
	}
	if d.kind == "choice" && len(d.options) > native {
		if strategy == "single" || strategy == "permute" {
			return nil, badRequest("strategy %q needs at most %d options on this model; use tournament", strategy, native)
		}
		d.adaptation = "tournament"
	}
	return d, nil
}

// newDecision builds a validated question, choosing a tournament when there
// are more choice options than head slots.
func (p *Profile) newDecision(kind, state, question string, options []string) *Decision {
	d := &Decision{profile: p, kind: kind, state: state, question: question, options: options, adaptation: "native"}
	if kind == "choice" && len(options) > p.NativeChoiceOptions() {
		d.adaptation = "tournament"
	}
	return d
}

// Decide validates and answers one typed question with the profile's System 1 head.
func (p *Profile) Decide(ctx context.Context, scorer Scorer, modelID string, req Request) (*Response, error) {
	d, err := p.Prepare(req)
	if err != nil {
		return nil, err
	}
	return d.Run(ctx, scorer, modelID)
}

// Run answers the question with one read-out, or a tournament of read-outs
// when there are more options than head slots.
func (d *Decision) Run(ctx context.Context, scorer Scorer, modelID string) (*Response, error) {
	start := time.Now()
	p := d.profile
	dc := &decideCtx{scorer: scorer}
	var probs []float64
	var err error
	if d.adaptation == "tournament" {
		probs, err = p.tournament(ctx, dc, d.state, d.question, d.options)
	} else {
		probs, err = p.pass(ctx, dc, d.kind, d.state, d.question, d.options)
	}
	if err != nil {
		return nil, err
	}

	best := 0
	for i, v := range probs {
		if v > probs[best] {
			best = i
		}
	}
	return &Response{
		Kind:             d.kind,
		EffectiveKind:    d.kind,
		Options:          d.options,
		Probabilities:    probs,
		ChoiceIndex:      best,
		Choice:           d.options[best],
		Adaptation:       d.adaptation,
		Protocol:         p.Protocol(),
		Model:            modelID,
		Usage:            Usage{PromptTokens: dc.promptTokens, CompletionTokens: dc.requests, TotalTokens: dc.promptTokens + dc.requests},
		ElapsedSeconds:   time.Since(start).Seconds(),
		NumModelRequests: dc.requests,
	}, nil
}

// pass runs one System 1 read-out over at most NativeChoiceOptions options.
func (p *Profile) pass(ctx context.Context, dc *decideCtx, kind, state, question string, opts []string) ([]float64, error) {
	lo, hi := p.slotRange(kind)
	if len(opts) > hi-lo {
		return nil, fmt.Errorf("%d options exceed the %d %s slots", len(opts), hi-lo, kind)
	}
	ids := p.Head.VerbalizerIDs[lo : lo+len(opts)]
	bias := p.Head.Bias[lo : lo+len(opts)]
	lines := opts
	if kind == "choice" {
		labels := p.Head.Slots.Verbalizers[lo:hi]
		lines = make([]string, len(opts))
		for i, o := range opts {
			lines[i] = labels[i] + ") " + o
		}
	}
	prompt := "[kind] " + kind + "\n[state] " + state + "\n[question] " + question +
		"\n[options]\n" + strings.Join(lines, "\n") + "\n[decision]:"
	lp, promptTokens, err := dc.scorer.ScoreDecisionTokens(ctx, prompt, ids)
	if err != nil {
		return nil, err
	}
	dc.requests++
	dc.promptTokens += promptTokens
	t := p.Temperatures[kind]
	if t <= 0 {
		t = 1
	}
	z := make([]float64, len(ids))
	for i, id := range ids {
		z[i] = (lp[id] + bias[i]) / t
	}
	return softmax(z), nil
}

// tournament answers more options than the head has slots: groups of at most
// 16 options, then a final over the group winners topped up with the best
// runners-up, folded back into one distribution (as the reference server).
func (p *Profile) tournament(ctx context.Context, dc *decideCtx, state, question string, opts []string) ([]float64, error) {
	native := p.NativeChoiceOptions()
	groups := splitGroups(len(opts), native)
	inGroup := make([]float64, len(opts))
	groupOf := make([]int, len(opts))
	var chosen []int
	for gi, g := range groups {
		sub := make([]string, len(g))
		for i, o := range g {
			sub[i] = opts[o]
			groupOf[o] = gi
		}
		probs, err := p.pass(ctx, dc, "choice", state, question, sub)
		if err != nil {
			return nil, err
		}
		best := g[0]
		for i, o := range g {
			inGroup[o] = probs[i]
			if probs[i] > inGroup[best] {
				best = o
			}
		}
		chosen = append(chosen, best)
	}
	isChosen := map[int]bool{}
	for _, o := range chosen {
		isChosen[o] = true
	}
	var rest []int
	for o := range opts {
		if !isChosen[o] {
			rest = append(rest, o)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return inGroup[rest[i]] > inGroup[rest[j]] })
	final := append([]int(nil), chosen...)
	if extra := native - len(chosen); extra > 0 && extra <= len(rest) {
		final = append(final, rest[:extra]...)
	} else if extra > 0 {
		final = append(final, rest...)
	}
	sort.Ints(final)
	sub := make([]string, len(final))
	for i, o := range final {
		sub[i] = opts[o]
	}
	finalProbs, err := p.pass(ctx, dc, "choice", state, question, sub)
	if err != nil {
		return nil, err
	}
	inFinal := map[int]float64{}
	share := make([]float64, len(groups))
	capacity := make([]float64, len(groups))
	for i, o := range final {
		inFinal[o] = finalProbs[i]
		share[groupOf[o]] += finalProbs[i]
		capacity[groupOf[o]] += inGroup[o]
	}
	among := 0.0
	for gi := range groups {
		among += share[gi] * capacity[gi]
	}
	out := make([]float64, len(opts))
	total := 0.0
	for o := range opts {
		if v, ok := inFinal[o]; ok {
			out[o] = v * among
		} else {
			out[o] = share[groupOf[o]] * inGroup[o]
		}
		total += out[o]
	}
	for o := range out {
		out[o] /= total
	}
	return out, nil
}

// splitGroups splits n options, in order, into the fewest groups of at most
// k with sizes differing by at most one.
func splitGroups(n, k int) [][]int {
	g := (n + k - 1) / k
	base, extra := n/g, n%g
	groups := make([][]int, 0, g)
	next := 0
	for j := 0; j < g; j++ {
		size := base
		if j < extra {
			size++
		}
		group := make([]int, size)
		for i := range group {
			group[i] = next
			next++
		}
		groups = append(groups, group)
	}
	return groups
}

func softmax(z []float64) []float64 {
	m := math.Inf(-1)
	for _, v := range z {
		m = math.Max(m, v)
	}
	out := make([]float64, len(z))
	sum := 0.0
	for i, v := range z {
		out[i] = math.Exp(v - m)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

// stateText renders the state the way the head was trained: strings as-is,
// JSON objects with Python's json.dumps(ensure_ascii=False) spacing, and
// lists of text parts concatenated. Images are refused.
func stateText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", badRequest("invalid state: %v", err)
		}
		return s, nil
	case '{':
		return pythonJSON(raw)
	case '[':
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err != nil {
			return "", badRequest("invalid state: %v", err)
		}
		var b strings.Builder
		for _, part := range parts {
			text, err := statePart(part)
			if err != nil {
				return "", err
			}
			b.WriteString(text)
		}
		return b.String(), nil
	default:
		return "", badRequest("state must be a string, an object, or a list of text parts")
	}
}

func statePart(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", badRequest("invalid state part: %v", err)
		}
		return s, nil
	}
	var obj map[string]json.RawMessage
	if len(raw) > 0 && raw[0] == '{' && json.Unmarshal(raw, &obj) == nil {
		if _, ok := obj["image"]; ok {
			return "", badRequest("image state parts are not supported by this model")
		}
		var typ string
		_ = json.Unmarshal(obj["type"], &typ)
		switch typ {
		case "image_url":
			return "", badRequest("image state parts are not supported by this model")
		case "text":
			var text string
			if err := json.Unmarshal(obj["text"], &text); err != nil {
				return "", badRequest("invalid text state part: %v", err)
			}
			return text, nil
		}
	}
	return pythonJSON(raw)
}

// pythonJSON re-encodes JSON like Python's json.dumps(v, ensure_ascii=False):
// key order preserved, ", " and ": " separators, non-ASCII kept.
func pythonJSON(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	if err := writePythonJSON(dec, &b); err != nil {
		return "", badRequest("invalid state: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", badRequest("invalid state: trailing data")
	}
	return b.String(), nil
}

func writePythonJSON(dec *json.Decoder, b *strings.Builder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		closing := map[json.Delim]json.Delim{'{': '}', '[': ']'}[v]
		b.WriteRune(rune(v))
		for i := 0; dec.More(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if v == '{' {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				writePythonString(b, key.(string))
				b.WriteString(": ")
			}
			if err := writePythonJSON(dec, b); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		b.WriteRune(rune(closing))
	case string:
		writePythonString(b, v)
	case json.Number:
		b.WriteString(pythonNumber(v))
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case nil:
		b.WriteString("null")
	}
	return nil
}

func pythonNumber(n json.Number) string {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		return s
	}
	f, err := n.Float64()
	if err != nil {
		return s
	}
	// Python prints floats with repr: shortest round-trip, ".0" for integers.
	out := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(out, ".eE") {
		out += ".0"
	}
	return out
}

func writePythonString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

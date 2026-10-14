package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// OpenAI Decisions API (POST /v1/decisions) limits.
const (
	maxOpenAIQuestions = 200
	maxOpenAIChoices   = 255
)

// OpenAIRequest is the OpenAI Decisions API request body.
type OpenAIRequest struct {
	Model            string           `json:"model"`
	Source           string           `json:"source,omitempty"`
	Input            json.RawMessage  `json:"input"`
	Questions        []OpenAIQuestion `json:"questions"`
	SafetyIdentifier string           `json:"safety_identifier,omitempty"`
}

// OpenAIQuestion is one predicate, choice or score question.
type OpenAIQuestion struct {
	Type         string            `json:"type"`
	Name         *string           `json:"name,omitempty"`
	Instructions string            `json:"instructions"`
	Choices      []OpenAIChoiceDef `json:"choices,omitempty"`
	Levels       []OpenAILevelDef  `json:"levels,omitempty"`
}

// OpenAIChoiceDef is a choice option; its value is a string or a boolean.
type OpenAIChoiceDef struct {
	Value       json.RawMessage `json:"value"`
	Description *string         `json:"description,omitempty"`
}

// OpenAILevelDef is one score level, listed from lowest to highest.
type OpenAILevelDef struct {
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`
}

// OpenAIDecision is the OpenAI Decisions API response.
type OpenAIDecision struct {
	Answers []OpenAIAnswer `json:"answers"`
	Model   string         `json:"model"`
	Usage   OpenAIUsage    `json:"usage"`
}

// OpenAIAnswer is a predicate, choice or score answer. Name is null for an
// unnamed question.
type OpenAIAnswer struct {
	Type          string              `json:"type"`
	Name          *string             `json:"name"`
	Probability   *float64            `json:"probability,omitempty"`
	Choice        json.RawMessage     `json:"choice,omitempty"`
	Score         *float64            `json:"score,omitempty"`
	Probabilities []OpenAIProbability `json:"probabilities,omitempty"`
	Confidence    *float64            `json:"confidence,omitempty"`
}

// OpenAIProbability is one option's probability. Choice answers carry the
// typed value; score answers carry the level index and label.
type OpenAIProbability struct {
	Value       json.RawMessage `json:"value"`
	Label       *string         `json:"label,omitempty"`
	Probability float64         `json:"probability"`
}

// OpenAIUsage mirrors the Decisions API usage object. Decisions read one
// next-token distribution per pass, so all tokens are input tokens.
type OpenAIUsage struct {
	InputTokens        int `json:"input_tokens"`
	InputTokensDetails struct {
		CacheWriteTokens int `json:"cache_write_tokens"`
		CachedTokens     int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens int `json:"total_tokens"`
}

// OpenAIDecisions is a validated OpenAI Decisions request.
type OpenAIDecisions struct {
	questions []openAIPrepared
}

type openAIPrepared struct {
	q        OpenAIQuestion
	decision *Decision
	values   []json.RawMessage // choice values, compacted
}

// PrepareOpenAI validates an OpenAI Decisions request and maps each question
// onto the System 1 head: predicate -> noul, choice -> choice, score -> a
// choice over the level labels whose probability-weighted index is the score.
func (p *Profile) PrepareOpenAI(req OpenAIRequest) (*OpenAIDecisions, error) {
	state, err := openAIInputText(req.Input)
	if err != nil {
		return nil, err
	}
	if len(req.Questions) == 0 || len(req.Questions) > maxOpenAIQuestions {
		return nil, badRequest("questions must contain 1-%d questions, got %d", maxOpenAIQuestions, len(req.Questions))
	}
	out := &OpenAIDecisions{}
	for i, q := range req.Questions {
		prepared, err := p.prepareOpenAIQuestion(state, q)
		if err != nil {
			return nil, badRequest("questions[%d]: %v", i, err)
		}
		out.questions = append(out.questions, prepared)
	}
	return out, nil
}

func (p *Profile) prepareOpenAIQuestion(state string, q OpenAIQuestion) (openAIPrepared, error) {
	instructions := strings.TrimSpace(q.Instructions)
	if instructions == "" {
		return openAIPrepared{}, badRequest("instructions is required")
	}
	switch q.Type {
	case "predicate":
		return openAIPrepared{q: q, decision: p.newDecision("noul", state, instructions, []string{"false", "true"})}, nil
	case "choice":
		if len(q.Choices) < 2 || len(q.Choices) > maxOpenAIChoices {
			return openAIPrepared{}, badRequest("choices must contain 2-%d options, got %d", maxOpenAIChoices, len(q.Choices))
		}
		seen := map[string]bool{}
		options := make([]string, len(q.Choices))
		values := make([]json.RawMessage, len(q.Choices))
		for i, c := range q.Choices {
			value, text, err := choiceValue(c.Value)
			if err != nil {
				return openAIPrepared{}, err
			}
			if seen[string(value)] {
				return openAIPrepared{}, badRequest("choice value %s is not unique", value)
			}
			seen[string(value)] = true
			values[i] = value
			options[i] = optionText(text, c.Description)
		}
		return openAIPrepared{q: q, decision: p.newDecision("choice", state, instructions, options), values: values}, nil
	case "score":
		if len(q.Levels) < 2 || len(q.Levels) > maxOpenAIChoices {
			return openAIPrepared{}, badRequest("levels must contain 2-%d levels, got %d", maxOpenAIChoices, len(q.Levels))
		}
		options := make([]string, len(q.Levels))
		for i, level := range q.Levels {
			if strings.TrimSpace(level.Label) == "" {
				return openAIPrepared{}, badRequest("levels[%d].label is required", i)
			}
			options[i] = optionText(level.Label, level.Description)
		}
		return openAIPrepared{q: q, decision: p.newDecision("choice", state, instructions, options)}, nil
	default:
		return openAIPrepared{}, badRequest("type must be predicate, choice or score")
	}
}

// Run answers every question in order.
func (d *OpenAIDecisions) Run(ctx context.Context, scorer Scorer, modelID string) (*OpenAIDecision, error) {
	resp := &OpenAIDecision{Answers: make([]OpenAIAnswer, 0, len(d.questions)), Model: modelID}
	for _, prepared := range d.questions {
		result, err := prepared.decision.Run(ctx, scorer, modelID)
		if err != nil {
			return nil, err
		}
		resp.Usage.InputTokens += result.Usage.PromptTokens
		resp.Answers = append(resp.Answers, prepared.answer(result.Probabilities))
	}
	resp.Usage.TotalTokens = resp.Usage.InputTokens
	return resp, nil
}

func (prepared openAIPrepared) answer(probs []float64) OpenAIAnswer {
	q := prepared.q
	a := OpenAIAnswer{Type: q.Type, Name: q.Name}
	switch q.Type {
	case "predicate":
		a.Probability = float64Ptr(probs[1])
	case "choice":
		best := argmax(probs)
		a.Choice = prepared.values[best]
		for i, v := range prepared.values {
			a.Probabilities = append(a.Probabilities, OpenAIProbability{Value: v, Probability: probs[i]})
		}
		a.Confidence = float64Ptr(margin(probs))
	case "score":
		score := 0.0
		for i, level := range q.Levels {
			score += float64(i) * probs[i]
			label := level.Label
			index, _ := json.Marshal(i)
			a.Probabilities = append(a.Probabilities, OpenAIProbability{Value: index, Label: &label, Probability: probs[i]})
		}
		a.Score = float64Ptr(score)
		a.Confidence = float64Ptr(margin(probs))
	}
	return a
}

// openAIInputText flattens the Decisions input (a string, or user messages of
// text parts) into the state the head reads. Images are refused: the head
// was trained on text.
func openAIInputText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", badRequest("input is required")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", badRequest("invalid input: %v", err)
		}
		return s, nil
	}
	var messages []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return "", badRequest("input must be a string or a list of user messages")
	}
	var texts []string
	for i, m := range messages {
		if m.Role != "user" || (m.Type != "" && m.Type != "message") {
			return "", badRequest("input[%d] must be a user message", i)
		}
		content := bytes.TrimSpace(m.Content)
		if len(content) > 0 && content[0] == '"' {
			var s string
			if err := json.Unmarshal(content, &s); err != nil {
				return "", badRequest("invalid input[%d].content: %v", i, err)
			}
			texts = append(texts, s)
			continue
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(content, &parts); err != nil {
			return "", badRequest("input[%d].content must be a string or a list of parts", i)
		}
		var partTexts []string
		for _, part := range parts {
			switch part.Type {
			case "input_text":
				partTexts = append(partTexts, part.Text)
			case "input_image":
				return "", badRequest("input_image parts are not supported by this model; send text input")
			default:
				return "", badRequest("unsupported input part type %q", part.Type)
			}
		}
		texts = append(texts, strings.Join(partTexts, "\n"))
	}
	return strings.Join(texts, "\n\n"), nil
}

// choiceValue validates a typed choice value (string or boolean) and returns
// it compacted with its display text.
func choiceValue(raw json.RawMessage) (json.RawMessage, string, error) {
	raw = bytes.TrimSpace(raw)
	var s string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return nil, "", badRequest("choice value must not be empty")
		}
		compact, _ := json.Marshal(s)
		return compact, s, nil
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		compact, _ := json.Marshal(b)
		return compact, string(compact), nil
	}
	return nil, "", badRequest("choice value must be a string or a boolean")
}

func optionText(text string, description *string) string {
	text = strings.TrimSpace(text)
	if description != nil && strings.TrimSpace(*description) != "" {
		return text + ": " + strings.TrimSpace(*description)
	}
	return text
}

// margin is the confidence of a distribution: the gap between its two most
// likely options.
func margin(probs []float64) float64 {
	sorted := append([]float64(nil), probs...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	if len(sorted) < 2 {
		return 1
	}
	return sorted[0] - sorted[1]
}

func argmax(probs []float64) int {
	best := 0
	for i, v := range probs {
		if v > probs[best] {
			best = i
		}
	}
	return best
}

func float64Ptr(v float64) *float64 { return &v }

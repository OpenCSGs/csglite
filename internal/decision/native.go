package decision

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/ggufmeta"
)

// TypeSafe System One limits for native decision models. llama-server
// answers a score question over 2-10 levels.
const maxNativeScoreLevels = 10

// NativeType returns the native decision type of the model in dir: the
// "<arch>.decision.type" of its GGUF, or "laya" for a ModernBERT decision
// checkpoint that is not converted yet. It returns "" for other models.
func NativeType(dir string) string {
	if convert.IsDecisionCheckpoint(dir) {
		return "laya"
	}
	found := ""
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if found != "" {
			return fs.SkipAll
		}
		if err != nil || entry.IsDir() {
			if err == nil && path == dir {
				return nil
			}
			rel, _ := filepath.Rel(dir, path)
			if err != nil || strings.HasPrefix(entry.Name(), ".") || strings.Count(rel, string(filepath.Separator)) >= 1 {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".gguf") && !strings.Contains(name, "mmproj") {
			found = ggufmeta.DecisionType(path)
		}
		return nil
	})
	return found
}

// NativeDecisions is an OpenAI Decisions request translated into the TypeSafe
// System One request llama-server answers for native decision models.
type NativeDecisions struct {
	questions []nativeQuestion
	body      []byte
}

type nativeQuestion struct {
	q      OpenAIQuestion
	id     string
	keys   []string          // System One option keys, in request order
	values []json.RawMessage // choice values, in request order
}

type systemOneQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// PrepareNativeOpenAI validates an OpenAI Decisions request for a native
// decision model: predicate -> noul, choice -> choice (options keyed by
// value, described by their description), score -> score over the levels.
func PrepareNativeOpenAI(req OpenAIRequest) (*NativeDecisions, error) {
	state, err := openAIInputText(req.Input)
	if err != nil {
		return nil, err
	}
	if len(req.Questions) == 0 || len(req.Questions) > maxOpenAIQuestions {
		return nil, badRequest("questions must contain 1-%d questions, got %d", maxOpenAIQuestions, len(req.Questions))
	}
	out := &NativeDecisions{}
	questions := make(map[string]systemOneQuestion, len(req.Questions))
	for i, q := range req.Questions {
		// System One keys questions by id and llama-server walks them in key
		// order, so zero-padded ids keep the request order.
		prepared, body, err := prepareNativeQuestion(fmt.Sprintf("q%03d", i), q)
		if err != nil {
			return nil, badRequest("questions[%d]: %v", i, err)
		}
		out.questions = append(out.questions, prepared)
		questions[prepared.id] = body
	}
	out.body, err = json.Marshal(map[string]any{"state": state, "questions": questions})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func prepareNativeQuestion(id string, q OpenAIQuestion) (nativeQuestion, systemOneQuestion, error) {
	instructions := strings.TrimSpace(q.Instructions)
	if instructions == "" {
		return nativeQuestion{}, systemOneQuestion{}, badRequest("instructions is required")
	}
	prepared := nativeQuestion{q: q, id: id}
	switch q.Type {
	case "predicate":
		return prepared, systemOneQuestion{Type: "noul", Instructions: instructions}, nil
	case "choice":
		if len(q.Choices) < 2 || len(q.Choices) > maxOpenAIChoices {
			return nativeQuestion{}, systemOneQuestion{}, badRequest("choices must contain 2-%d options, got %d", maxOpenAIChoices, len(q.Choices))
		}
		criteria := make(map[string]*string, len(q.Choices))
		for _, c := range q.Choices {
			value, key, err := choiceValue(c.Value)
			if err != nil {
				return nativeQuestion{}, systemOneQuestion{}, err
			}
			// A string "true" and the boolean true share a System One key.
			if _, dup := criteria[key]; dup {
				return nativeQuestion{}, systemOneQuestion{}, badRequest("choice value %s is not unique", value)
			}
			var description *string
			if c.Description != nil && strings.TrimSpace(*c.Description) != "" {
				text := strings.TrimSpace(*c.Description)
				description = &text
			}
			criteria[key] = description
			prepared.keys = append(prepared.keys, key)
			prepared.values = append(prepared.values, value)
		}
		return prepared, systemOneQuestion{Type: "choice", Instructions: instructions, Criteria: criteria}, nil
	case "score":
		if len(q.Levels) < 2 || len(q.Levels) > maxNativeScoreLevels {
			return nativeQuestion{}, systemOneQuestion{}, badRequest("levels must contain 2-%d levels for this model, got %d", maxNativeScoreLevels, len(q.Levels))
		}
		levels := make([]string, len(q.Levels))
		for i, level := range q.Levels {
			if strings.TrimSpace(level.Label) == "" {
				return nativeQuestion{}, systemOneQuestion{}, badRequest("levels[%d].label is required", i)
			}
			levels[i] = optionText(level.Label, level.Description)
			prepared.keys = append(prepared.keys, fmt.Sprint(i))
		}
		return prepared, systemOneQuestion{Type: "score", Instructions: instructions, Criteria: levels}, nil
	default:
		return nativeQuestion{}, systemOneQuestion{}, badRequest("type must be predicate, choice or score")
	}
}

// Body is the System One request body.
func (d *NativeDecisions) Body() []byte { return d.body }

// Answer maps a System One response back to the OpenAI Decisions response.
// Confidence is recomputed as the gap between the two most likely options,
// as for JEV models, rather than System One's own measure.
func (d *NativeDecisions) Answer(data []byte, modelID string) (*OpenAIDecision, error) {
	var out struct {
		Answers map[string]struct {
			Noul          *float64           `json:"noul"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding decision response: %w", err)
	}
	resp := &OpenAIDecision{Answers: make([]OpenAIAnswer, 0, len(d.questions)), Model: modelID}
	for _, q := range d.questions {
		answer, ok := out.Answers[q.id]
		if !ok {
			return nil, fmt.Errorf("decision response has no answer for question %s", q.id)
		}
		if q.q.Type == "predicate" {
			if answer.Noul == nil {
				return nil, fmt.Errorf("decision response for question %s has no noul probability", q.id)
			}
			resp.Answers = append(resp.Answers, OpenAIAnswer{Type: "predicate", Name: q.q.Name, Probability: float64Ptr(*answer.Noul)})
			continue
		}
		probs := make([]float64, len(q.keys))
		for i, key := range q.keys {
			p, ok := answer.Probabilities[key]
			if !ok {
				return nil, fmt.Errorf("decision response for question %s has no probability for option %q", q.id, key)
			}
			probs[i] = p
		}
		prepared := openAIPrepared{q: q.q, values: q.values}
		resp.Answers = append(resp.Answers, prepared.answer(probs))
	}
	resp.Usage.InputTokens = out.Usage.InputTokens
	resp.Usage.TotalTokens = out.Usage.InputTokens
	return resp, nil
}

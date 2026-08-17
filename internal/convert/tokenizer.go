package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GGUF token types.
const (
	tokenTypeNormal      int32 = 1
	tokenTypeUnknown     int32 = 2
	tokenTypeControl     int32 = 3
	tokenTypeUserDefined int32 = 4
	tokenTypeUnused      int32 = 5
	tokenTypeByte        int32 = 6
)

type parsedTokenizer struct {
	Model        string   // "gpt2", "llama"
	Pre          string   // pre-tokenizer type: "default", "qwen2", "llama-bpe"
	Tokens       []string // ordered by ID
	Scores       []float32
	Types        []int32
	Merges       []string
	BosID        int32
	EosID        int32
	PadID        int32
	UnkID        int32
	AddBosToken  bool
	ChatTemplate string
}

type tokenizerJSON struct {
	Model struct {
		Type   string            `json:"type"`
		Vocab  map[string]int    `json:"vocab"`
		Merges []json.RawMessage `json:"merges"`
	} `json:"model"`
	AddedTokens []addedToken `json:"added_tokens"`
}

type addedToken struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
	Special bool   `json:"special"`
}

type tokenizerConfigJSON struct {
	BosToken     tokenOrString `json:"bos_token"`
	EosToken     tokenOrString `json:"eos_token"`
	PadToken     tokenOrString `json:"pad_token"`
	UnkToken     tokenOrString `json:"unk_token"`
	ChatTemplate interface{}   `json:"chat_template"`
}

// tokenOrString handles both {"content": "..."} and "..." formats in tokenizer_config.json.
type tokenOrString struct {
	Value string
}

func (t *tokenOrString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.Value = s
		return nil
	}
	var obj struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		t.Value = obj.Content
		return nil
	}
	return nil
}

func parseTokenizer(modelDir, architecture string) (*parsedTokenizer, error) {
	tokPath := filepath.Join(modelDir, "tokenizer.json")
	configPath := filepath.Join(modelDir, "tokenizer_config.json")

	data, err := os.ReadFile(tokPath)
	if err != nil {
		return nil, fmt.Errorf("reading tokenizer.json: %w", err)
	}

	var tj tokenizerJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("parsing tokenizer.json: %w", err)
	}

	if len(tj.Model.Vocab) == 0 {
		return nil, fmt.Errorf("tokenizer.json has no vocabulary")
	}

	// Determine vocabulary size (max ID + 1).
	maxID := 0
	for _, id := range tj.Model.Vocab {
		if id > maxID {
			maxID = id
		}
	}
	for _, at := range tj.AddedTokens {
		if at.ID > maxID {
			maxID = at.ID
		}
	}
	vocabSize := maxID + 1

	tokens := make([]string, vocabSize)
	scores := make([]float32, vocabSize)
	types := make([]int32, vocabSize)

	// Fill all with defaults.
	for i := range tokens {
		tokens[i] = fmt.Sprintf("[PAD%d]", i)
		types[i] = tokenTypeUnused
	}

	// Populate from main vocab.
	for token, id := range tj.Model.Vocab {
		if id >= 0 && id < vocabSize {
			tokens[id] = token
			types[id] = tokenTypeNormal
		}
	}

	// Populate added tokens (may override).
	specialTokens := make(map[string]int)
	for _, at := range tj.AddedTokens {
		if at.ID >= 0 && at.ID < vocabSize {
			tokens[at.ID] = at.Content
			if at.Special {
				types[at.ID] = tokenTypeControl
				specialTokens[at.Content] = at.ID
			} else {
				types[at.ID] = tokenTypeUserDefined
			}
		}
	}

	// Parse merges: can be either ["a b", ...] or [["a", "b"], ...].
	merges, err := parseMerges(tj.Model.Merges)
	if err != nil {
		return nil, fmt.Errorf("parsing merges: %w", err)
	}

	// Determine tokenizer model type.
	tokModel := "gpt2" // BPE default
	switch tj.Model.Type {
	case "BPE":
		tokModel = "gpt2"
	case "Unigram":
		tokModel = "llama"
	case "WordPiece":
		tokModel = "bert"
	}

	result := &parsedTokenizer{
		Model:       tokModel,
		Pre:         preTokenizerForArch(architecture),
		Tokens:      tokens,
		Scores:      scores,
		Types:       types,
		Merges:      merges,
		BosID:       -1,
		EosID:       -1,
		PadID:       -1,
		UnkID:       -1,
		AddBosToken: false,
	}

	if cfgData, err := os.ReadFile(configPath); err == nil {
		var cfg tokenizerConfigJSON
		if err := json.Unmarshal(cfgData, &cfg); err == nil {
			if cfg.BosToken.Value != "" {
				if id, ok := findTokenID(cfg.BosToken.Value, specialTokens, tj.Model.Vocab); ok {
					result.BosID = int32(id)
					result.AddBosToken = true
				}
			}
			if id, ok := findTokenID(cfg.EosToken.Value, specialTokens, tj.Model.Vocab); ok {
				result.EosID = int32(id)
			}
			if id, ok := findTokenID(cfg.PadToken.Value, specialTokens, tj.Model.Vocab); ok {
				result.PadID = int32(id)
			}
			if id, ok := findTokenID(cfg.UnkToken.Value, specialTokens, tj.Model.Vocab); ok {
				result.UnkID = int32(id)
			}
			result.ChatTemplate = extractChatTemplate(cfg.ChatTemplate)
		}
	}

	return result, nil
}

func findTokenID(token string, special map[string]int, vocab map[string]int) (int, bool) {
	if token == "" {
		return 0, false
	}
	if id, ok := special[token]; ok {
		return id, true
	}
	if id, ok := vocab[token]; ok {
		return id, true
	}
	return 0, false
}

func extractChatTemplate(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []interface{}:
		// Some models provide multiple templates; pick the first "default" one.
		for _, item := range val {
			if m, ok := item.(map[string]interface{}); ok {
				if name, _ := m["name"].(string); name == "default" {
					if tpl, ok := m["template"].(string); ok {
						return tpl
					}
				}
			}
		}
		// Fall back to first template.
		if len(val) > 0 {
			if m, ok := val[0].(map[string]interface{}); ok {
				if tpl, ok := m["template"].(string); ok {
					return tpl
				}
			}
		}
	}
	return ""
}

// parseMerges handles both string merges ("a b") and array merges (["a", "b"]).
func parseMerges(raw []json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	merges := make([]string, 0, len(raw))
	for _, r := range raw {
		// Try as string first: "a b"
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			merges = append(merges, s)
			continue
		}

		// Try as array: ["a", "b"]
		var pair []string
		if err := json.Unmarshal(r, &pair); err == nil {
			merges = append(merges, strings.Join(pair, " "))
			continue
		}

		return nil, fmt.Errorf("unexpected merge format: %s", string(r))
	}

	return merges, nil
}

func preTokenizerForArch(arch string) string {
	m := map[string]string{
		// Qwen family → qwen2 pre-tokenizer
		"QWenLMHeadModel":                        "qwen2",
		"Qwen2ForCausalLM":                       "qwen2",
		"Qwen2Model":                             "qwen2",
		"Qwen2MoeForCausalLM":                    "qwen2",
		"Qwen2AudioForConditionalGeneration":     "qwen2",
		"KORMoForCausalLM":                       "qwen2",
		"AudioFlamingo3ForConditionalGeneration": "qwen2",
		"Qwen3ForCausalLM":                       "qwen2",
		"Qwen3MoeForCausalLM":                    "qwen2",
		"Qwen3VLForConditionalGeneration":        "qwen2",
		"Qwen3VLMoeForConditionalGeneration":     "qwen2",
		"Qwen3NextForCausalLM":                   "qwen2",
		"Qwen3_5ForConditionalGeneration":        "qwen2",
		"Qwen3_5ForCausalLM":                     "qwen2",
		"Qwen3_5MoeForConditionalGeneration":     "qwen2",
		"Qwen3_5MoeForCausalLM":                  "qwen2",

		// DeepSeek → deepseek-llm
		"DeepseekV2ForCausalLM": "deepseek-llm",
		"DeepseekV3ForCausalLM": "deepseek-llm",

		// ChatGLM → chatglm-bpe
		"ChatGLMModel":                    "chatglm-bpe",
		"ChatGLMForConditionalGeneration": "chatglm-bpe",
		"GlmForCausalLM":                  "chatglm-bpe",

		// RWKV → rwkv-world
		"Rwkv6ForCausalLM":      "rwkv-world",
		"Rwkv7ForCausalLM":      "rwkv-world",
		"RWKV7ForCausalLM":      "rwkv-world",
		"RwkvHybridForCausalLM": "rwkv-world",

		// RWKV6Qwen2 uses qwen2 tokenizer
		"RWKV6Qwen2ForCausalLM": "qwen2",

		// Kimi-Linear uses qwen2 tokenizer
		"KimiLinearForCausalLM": "qwen2",
		"KimiLinearModel":       "qwen2",

		// GPT2
		"GPT2LMHeadModel": "gpt2",
	}
	if pre, ok := m[arch]; ok {
		return pre
	}
	return "default"
}

// writeTokenizerKV adds tokenizer metadata to the GGUF writer.
func writeTokenizerKV(w *ggufWriter, tok *parsedTokenizer, cfg *modelConfig) {
	w.addKV("tokenizer.ggml.model", tok.Model)

	if tok.Pre != "" {
		w.addKV("tokenizer.ggml.pre", tok.Pre)
	}

	w.addKV("tokenizer.ggml.tokens", tok.Tokens)
	w.addKV("tokenizer.ggml.scores", tok.Scores)
	w.addKV("tokenizer.ggml.token_type", tok.Types)

	if len(tok.Merges) > 0 {
		w.addKV("tokenizer.ggml.merges", tok.Merges)
	}

	bosID := tok.BosID
	if bosID < 0 && cfg != nil && cfg.BosTokenID != nil {
		bosID = int32(*cfg.BosTokenID)
	}
	if bosID >= 0 {
		w.addKV("tokenizer.ggml.bos_token_id", uint32(bosID))
	}
	w.addKV("tokenizer.ggml.add_bos_token", tok.AddBosToken)

	eosID := tok.EosID
	if eosID < 0 && cfg != nil && cfg.EosTokenID != nil {
		eosID = int32(*cfg.EosTokenID)
	}
	if eosID >= 0 {
		w.addKV("tokenizer.ggml.eos_token_id", uint32(eosID))
	}

	if tok.PadID >= 0 {
		w.addKV("tokenizer.ggml.padding_token_id", uint32(tok.PadID))
	}
	if tok.UnkID >= 0 {
		w.addKV("tokenizer.ggml.unknown_token_id", uint32(tok.UnkID))
	}
	if tok.ChatTemplate != "" {
		w.addKV("tokenizer.chat_template", tok.ChatTemplate)
	}
}

// sortedVocab returns tokens sorted by ID for debugging.
func sortedVocab(vocab map[string]int) []string {
	type entry struct {
		token string
		id    int
	}
	entries := make([]entry, 0, len(vocab))
	for t, id := range vocab {
		entries = append(entries, entry{t, id})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].id < entries[j].id
	})
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.token
	}
	return result
}

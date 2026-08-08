package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Supported HuggingFace architecture names and their GGUF mapping.
// Synced from llama.cpp convert_hf_to_gguf.py and src/llama-arch.cpp.
var archMapping = map[string]string{
	// Llama family
	"LlamaForCausalLM":               "llama",
	"LLaMAForCausalLM":               "llama",
	"MistralForCausalLM":             "llama",
	"MixtralForCausalLM":             "llama",
	"InternLM3ForCausalLM":           "llama",
	"Llama4ForCausalLM":              "llama4",
	"Llama4ForConditionalGeneration": "llama4",
	"LlamaBidirectionalModel":        "llama",
	"DeciLMForCausalLM":              "deci",
	"ArceeForCausalLM":               "arcee",
	"AfmoeForCausalLM":               "afmoe",
	"SmolLM3ForCausalLM":             "smollm3",
	"ApertusForCausalLM":             "apertus",
	"CogVLMForCausalLM":              "cogvlm",

	// Qwen family
	"QWenLMHeadModel":                        "qwen2",
	"Qwen2ForCausalLM":                       "qwen2",
	"Qwen2Model":                             "qwen2",
	"Qwen2MoeForCausalLM":                    "qwen2moe",
	"Qwen2VLForConditionalGeneration":        "qwen2vl",
	"Qwen2_5_VLForConditionalGeneration":     "qwen2vl",
	"Qwen2AudioForConditionalGeneration":     "qwen2",
	"KORMoForCausalLM":                       "qwen2",
	"AudioFlamingo3ForConditionalGeneration": "qwen2",
	"Qwen3ForCausalLM":                       "qwen3",
	"Qwen3MoeForCausalLM":                    "qwen3moe",
	"Qwen3VLForConditionalGeneration":        "qwen3",
	"Qwen3VLMoeForConditionalGeneration":     "qwen3moe",
	"Qwen3NextForCausalLM":                   "qwen3next",
	"Qwen3_5ForConditionalGeneration":        "qwen35",
	"Qwen3_5ForCausalLM":                     "qwen35",
	"Qwen3_5MoeForConditionalGeneration":     "qwen35moe",
	"Qwen3_5MoeForCausalLM":                  "qwen35moe",

	// Gemma family
	"GemmaForCausalLM":                "gemma",
	"Gemma2ForCausalLM":               "gemma2",
	"Gemma3ForCausalLM":               "gemma3",
	"Gemma3ForConditionalGeneration":  "gemma3",
	"Gemma3nForCausalLM":              "gemma3n",
	"Gemma3nForConditionalGeneration": "gemma3n",

	// Phi family
	"PhiForCausalLM":    "phi2",
	"Phi3ForCausalLM":   "phi3",
	"Phi4ForCausalLMV":  "phi3",
	"PhiMoEForCausalLM": "phimoe",

	// GLM / ChatGLM family
	"GlmForCausalLM":                   "chatglm",
	"ChatGLMModel":                     "chatglm",
	"ChatGLMForConditionalGeneration":  "chatglm",
	"Glm4ForCausalLM":                  "glm4",
	"Glm4vForConditionalGeneration":    "glm4",
	"GlmOcrForConditionalGeneration":   "glm4",
	"Glm4MoeForCausalLM":               "glm4moe",
	"Glm4vMoeForConditionalGeneration": "glm4moe",
	"GlmMoeDsaForCausalLM":             "glm-dsa",
	"Glm4MoeLiteForCausalLM":           "deepseek2",
	"SolarOpenForCausalLM":             "glm4moe",

	// Deepseek family
	"DeepseekForCausalLM":   "deepseek",
	"DeepseekV2ForCausalLM": "deepseek2",
	"DeepseekV3ForCausalLM": "deepseek2",

	// InternLM
	"InternLM2ForCausalLM": "internlm2",

	// Baichuan
	"BaichuanForCausalLM": "baichuan",
	"BaiChuanForCausalLM": "baichuan",

	// Cohere / Command-R
	"CohereForCausalLM":  "command-r",
	"Cohere2ForCausalLM": "cohere2",

	// Olmo family
	"OlmoForCausalLM":  "olmo",
	"OLMoForCausalLM":  "olmo",
	"Olmo2ForCausalLM": "olmo2",
	"Olmo3ForCausalLM": "olmo2",
	"OlmoeForCausalLM": "olmoe",

	// MiniCPM
	"MiniCPMForCausalLM":  "minicpm",
	"MiniCPM3ForCausalLM": "minicpm3",

	// Falcon family
	"FalconForCausalLM":   "falcon",
	"RWForCausalLM":       "falcon",
	"FalconH1ForCausalLM": "falcon-h1",

	// Mamba / SSM family
	"MambaForCausalLM":       "mamba",
	"MambaLMHeadModel":       "mamba",
	"FalconMambaForCausalLM": "mamba",
	"Mamba2ForCausalLM":      "mamba2",
	"JambaForCausalLM":       "jamba",

	// RWKV family
	"Rwkv6ForCausalLM":      "rwkv6",
	"RWKV6Qwen2ForCausalLM": "rwkv6qwen2",
	"Rwkv7ForCausalLM":      "rwkv7",
	"RWKV7ForCausalLM":      "rwkv7",
	"RwkvHybridForCausalLM": "arwkv7",

	// Granite family
	"GraniteForCausalLM":          "granite",
	"GraniteMoeForCausalLM":       "granitemoe",
	"GraniteMoeSharedForCausalLM": "granitemoe",
	"GraniteMoeHybridForCausalLM": "granitehybrid",
	"BambaForCausalLM":            "granitehybrid",

	// Nemotron
	"NemotronForCausalLM":  "nemotron",
	"NemotronHForCausalLM": "nemotron_h",

	// Exaone
	"ExaoneForCausalLM":    "exaone",
	"Exaone4ForCausalLM":   "exaone4",
	"ExaoneMoEForCausalLM": "exaone-moe",

	// StarCoder
	"GPTBigCodeForCausalLM": "starcoder",
	"Starcoder2ForCausalLM": "starcoder2",

	// Mistral (standalone)
	"Ministral3ForCausalLM":            "mistral3",
	"Mistral3ForConditionalGeneration": "mistral3",
	"VoxtralForConditionalGeneration":  "mistral3",
	"Mistral4ForCausalLM":              "mistral4",

	// HunyYuan
	"HunYuanMoEV1ForCausalLM":   "hunyuan-moe",
	"HunYuanDenseV1ForCausalLM": "hunyuan-dense",

	// Ernie
	"Ernie4_5_ForCausalLM":    "ernie4_5",
	"Ernie4_5ForCausalLM":     "ernie4_5",
	"Ernie4_5_MoeForCausalLM": "ernie4_5-moe",

	// GPT family
	"GPT2LMHeadModel":      "gpt2",
	"GPTNeoXForCausalLM":   "gptneox",
	"GPTRefactForCausalLM": "refact",

	// Bloom
	"BloomForCausalLM": "bloom",
	"BloomModel":       "bloom",

	// StableLM
	"StableLmForCausalLM":           "stablelm",
	"StableLMEpochForCausalLM":      "stablelm",
	"LlavaStableLMEpochForCausalLM": "stablelm",

	// T5 encoder-decoder
	"T5WithLMHeadModel":            "t5",
	"T5ForConditionalGeneration":   "t5",
	"MT5ForConditionalGeneration":  "t5",
	"UMT5ForConditionalGeneration": "t5",
	"UMT5Model":                    "t5",
	"T5EncoderModel":               "t5encoder",

	// JAIS
	"JaisModel":        "jais",
	"JAISLMHeadModel":  "jais",
	"Jais2ForCausalLM": "jais2",

	// PLaMo family
	"PlamoForCausalLM":  "plamo",
	"Plamo2ForCausalLM": "plamo2",
	"PLaMo2ForCausalLM": "plamo2",
	"Plamo3ForCausalLM": "plamo3",
	"PLaMo3ForCausalLM": "plamo3",

	// Diffusion models
	"DreamModel":      "dream",
	"LLaDAModelLM":    "llada",
	"LLaDAMoEModel":   "llada-moe",
	"LLaDAMoEModelLM": "llada-moe",
	"RND1":            "rnd1",

	// Hybrid architectures
	"KimiLinearForCausalLM": "kimi-linear",
	"KimiLinearModel":       "kimi-linear",

	// LFM2 family
	"Lfm2ForCausalLM":     "lfm2",
	"LFM2ForCausalLM":     "lfm2",
	"Lfm2Model":           "lfm2",
	"Lfm25AudioTokenizer": "lfm2",
	"Lfm2MoeForCausalLM":  "lfm2moe",

	// Other architectures
	"MPTForCausalLM":                         "mpt",
	"OrionForCausalLM":                       "orion",
	"XverseForCausalLM":                      "xverse",
	"CodeShellForCausalLM":                   "codeshell",
	"DbrxForCausalLM":                        "dbrx",
	"OpenELMForCausalLM":                     "openelm",
	"ArcticForCausalLM":                      "arctic",
	"BitnetForCausalLM":                      "bitnet",
	"GrokForCausalLM":                        "grok",
	"Grok1ForCausalLM":                       "grok",
	"ChameleonForCausalLM":                   "chameleon",
	"ChameleonForConditionalGeneration":      "chameleon",
	"PLMForCausalLM":                         "plm",
	"Dots1ForCausalLM":                       "dots1",
	"BailingMoeForCausalLM":                  "bailingmoe",
	"BailingMoeV2ForCausalLM":                "bailingmoe2",
	"GroveMoeForCausalLM":                    "grovemoe",
	"modeling_grove_moe.GroveMoeForCausalLM": "grovemoe",
	"SmallThinkerForCausalLM":                "smallthinker",
	"GptOssForCausalLM":                      "gpt-oss",
	"SeedOssForCausalLM":                     "seed_oss",
	"MiniMaxM2ForCausalLM":                   "minimax-m2",
	"MiMoV2FlashForCausalLM":                 "mimo2",
	"Step3p5ForCausalLM":                     "step35",
	"MaincoderForCausalLM":                   "maincoder",
	"PanguEmbeddedForCausalLM":               "pangu-embedded",
	"WavTokenizerDec":                        "wavtokenizer-dec",
	"PaddleOCRVLForConditionalGeneration":    "paddleocr",
}

// ropeParameters holds rope_parameters / rope_scaling fields from config.json.
type ropeParameters struct {
	MropeSection []int   `json:"mrope_section"`
	RopeTheta    float64 `json:"rope_theta"`
	RopeType     string  `json:"rope_type"`
}

// modelConfig holds config.json fields common to transformer models.
type modelConfig struct {
	Architectures         []string `json:"architectures"`
	HiddenSize            int      `json:"hidden_size"`
	IntermediateSize      int      `json:"intermediate_size"`
	MaxPositionEmbeddings int      `json:"max_position_embeddings"`
	NumAttentionHeads     int      `json:"num_attention_heads"`
	NumHiddenLayers       int      `json:"num_hidden_layers"`
	NumKeyValueHeads      int      `json:"num_key_value_heads"`
	RmsNormEps            float64  `json:"rms_norm_eps"`
	RopeTheta             float64  `json:"rope_theta"`
	VocabSize             int      `json:"vocab_size"`
	HeadDim               int      `json:"head_dim"`
	TieWordEmbeddings     bool     `json:"tie_word_embeddings"`
	ModelType             string   `json:"model_type"`
	BosTokenID            *int     `json:"bos_token_id"`
	EosTokenID            *int     `json:"eos_token_id"`

	RopeParams  *ropeParameters `json:"rope_parameters"`
	RopeScaling *ropeParameters `json:"rope_scaling"`

	// SSM / linear-attention fields (Qwen3.5, Mamba, etc.)
	LinearConvKernelDim   int     `json:"linear_conv_kernel_dim"`
	LinearKeyHeadDim      int     `json:"linear_key_head_dim"`
	LinearValueHeadDim    int     `json:"linear_value_head_dim"`
	LinearNumKeyHeads     int     `json:"linear_num_key_heads"`
	LinearNumValueHeads   int     `json:"linear_num_value_heads"`
	FullAttentionInterval int     `json:"full_attention_interval"`
	PartialRotaryFactor   float64 `json:"partial_rotary_factor"`

	// Mamba SSM fields (alternative naming in different models)
	ConvKernel   int `json:"conv_kernel"`
	DConv        int `json:"d_conv"`
	StateSize    int `json:"state_size"`
	DState       int `json:"d_state"`
	TimeStepRank int `json:"time_step_rank"`
	DtRank       int `json:"dt_rank"`
	DModel       int `json:"d_model"`
	DInner       int `json:"d_inner"`
	NGroups      int `json:"n_groups"`
	MambaDSSM    int `json:"mamba_d_ssm"`

	// RWKV fields
	WkvHeadSize      int     `json:"head_size"`
	RescaleEvery     int     `json:"rescale_every"`
	LayerNormEpsilon float64 `json:"layer_norm_epsilon"`

	// MoE fields
	NumLocalExperts              int `json:"num_local_experts"`
	NumExpertsPerTok             int `json:"num_experts_per_tok"`
	MoeIntermediateSize          int `json:"moe_intermediate_size"`
	SharedExpertIntermediateSize int `json:"shared_expert_intermediate_size"`

	// VL (vision-language) models nest text model params under text_config.
	TextConfig *modelConfig `json:"text_config"`
}

func loadModelConfig(modelDir string) (*modelConfig, error) {
	cfg, err := readModelConfig(modelDir)
	if err != nil {
		return nil, err
	}

	if len(cfg.Architectures) == 0 {
		return nil, fmt.Errorf("config.json: no architectures specified")
	}

	normalizeModelConfig(cfg)
	return cfg, nil
}

// ModelMaxPositionEmbeddings returns the text model's declared maximum context
// from config.json, including vision-language configs that nest text params
// under text_config.
func ModelMaxPositionEmbeddings(modelDir string) (int, error) {
	cfg, err := readModelConfig(modelDir)
	if err != nil {
		return 0, err
	}
	return cfg.MaxPositionEmbeddings, nil
}

func readModelConfig(modelDir string) (*modelConfig, error) {
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("reading config.json: %w", err)
	}

	var cfg modelConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config.json: %w", err)
	}

	promoteTextModelConfig(&cfg)
	return &cfg, nil
}

func promoteTextModelConfig(cfg *modelConfig) {
	// VL models store text model params under text_config; promote them.
	if cfg.HiddenSize == 0 && cfg.TextConfig != nil {
		tc := cfg.TextConfig
		cfg.HiddenSize = tc.HiddenSize
		cfg.IntermediateSize = tc.IntermediateSize
		cfg.MaxPositionEmbeddings = tc.MaxPositionEmbeddings
		cfg.NumAttentionHeads = tc.NumAttentionHeads
		cfg.NumHiddenLayers = tc.NumHiddenLayers
		cfg.NumKeyValueHeads = tc.NumKeyValueHeads
		cfg.VocabSize = tc.VocabSize
		cfg.HeadDim = tc.HeadDim
		if tc.RmsNormEps != 0 {
			cfg.RmsNormEps = tc.RmsNormEps
		}
		if tc.RopeTheta != 0 {
			cfg.RopeTheta = tc.RopeTheta
		}
		if tc.BosTokenID != nil {
			cfg.BosTokenID = tc.BosTokenID
		}
		if tc.EosTokenID != nil {
			cfg.EosTokenID = tc.EosTokenID
		}
		cfg.TieWordEmbeddings = tc.TieWordEmbeddings
		if cfg.RopeParams == nil {
			cfg.RopeParams = tc.RopeParams
		}
		if cfg.RopeScaling == nil {
			cfg.RopeScaling = tc.RopeScaling
		}
		if cfg.LinearConvKernelDim == 0 {
			cfg.LinearConvKernelDim = tc.LinearConvKernelDim
		}
		if cfg.LinearKeyHeadDim == 0 {
			cfg.LinearKeyHeadDim = tc.LinearKeyHeadDim
		}
		if cfg.LinearValueHeadDim == 0 {
			cfg.LinearValueHeadDim = tc.LinearValueHeadDim
		}
		if cfg.LinearNumKeyHeads == 0 {
			cfg.LinearNumKeyHeads = tc.LinearNumKeyHeads
		}
		if cfg.LinearNumValueHeads == 0 {
			cfg.LinearNumValueHeads = tc.LinearNumValueHeads
		}
		if cfg.FullAttentionInterval == 0 {
			cfg.FullAttentionInterval = tc.FullAttentionInterval
		}
		if cfg.PartialRotaryFactor == 0 {
			cfg.PartialRotaryFactor = tc.PartialRotaryFactor
		}
	}
}

func normalizeModelConfig(cfg *modelConfig) {
	// Use rope_theta from rope_parameters if top-level is unset.
	rp := cfg.RopeParams
	if rp == nil {
		rp = cfg.RopeScaling
	}
	if rp != nil && rp.RopeTheta != 0 && cfg.RopeTheta == 0 {
		cfg.RopeTheta = rp.RopeTheta
	}

	// Merge Mamba alternative field names.
	if cfg.ConvKernel == 0 && cfg.DConv > 0 {
		cfg.ConvKernel = cfg.DConv
	}
	if cfg.StateSize == 0 && cfg.DState > 0 {
		cfg.StateSize = cfg.DState
	}
	if cfg.TimeStepRank == 0 && cfg.DtRank > 0 {
		cfg.TimeStepRank = cfg.DtRank
	}
	if cfg.HiddenSize == 0 && cfg.DModel > 0 {
		cfg.HiddenSize = cfg.DModel
	}
	if cfg.IntermediateSize == 0 && cfg.DInner > 0 {
		cfg.IntermediateSize = cfg.DInner
	}
	if cfg.RmsNormEps == 0 && cfg.LayerNormEpsilon > 0 {
		cfg.RmsNormEps = cfg.LayerNormEpsilon
	}

	// Defaults.
	if cfg.NumKeyValueHeads == 0 {
		cfg.NumKeyValueHeads = cfg.NumAttentionHeads
	}
	if cfg.RopeTheta == 0 {
		cfg.RopeTheta = 10000.0
	}
	if cfg.RmsNormEps == 0 {
		cfg.RmsNormEps = 1e-5
	}
}

// detectGGUFArch returns the GGUF architecture name for a HuggingFace architecture.
// Returns ("", false) if the architecture is unknown.
func detectGGUFArch(hfArch string) (ggufArch string, ok bool) {
	arch, found := archMapping[hfArch]
	return arch, found
}

// writeModelKV writes architecture-specific metadata to the GGUF writer.
func writeModelKV(w *ggufWriter, cfg *modelConfig, ggufArch string) {
	prefix := ggufArch

	w.addKV("general.architecture", ggufArch)
	w.addKV("general.name", cfg.ModelType)
	w.addKV("general.file_type", uint32(1)) // F16

	w.addKV(prefix+".block_count", uint32(cfg.NumHiddenLayers))
	w.addKV(prefix+".context_length", uint32(cfg.MaxPositionEmbeddings))
	w.addKV(prefix+".embedding_length", uint32(cfg.HiddenSize))
	w.addKV(prefix+".feed_forward_length", uint32(cfg.IntermediateSize))
	w.addKV(prefix+".attention.head_count", uint32(cfg.NumAttentionHeads))
	w.addKV(prefix+".attention.head_count_kv", uint32(cfg.NumKeyValueHeads))
	w.addKV(prefix+".attention.layer_norm_rms_epsilon", float32(cfg.RmsNormEps))
	w.addKV(prefix+".rope.freq_base", float32(cfg.RopeTheta))

	if cfg.HeadDim > 0 {
		w.addKV(prefix+".attention.key_length", uint32(cfg.HeadDim))
		w.addKV(prefix+".attention.value_length", uint32(cfg.HeadDim))
	}

	// M-RoPE dimension sections (required by Qwen2VL, Qwen3.5, etc.)
	rp := cfg.RopeParams
	if rp == nil {
		rp = cfg.RopeScaling
	}
	if rp != nil && len(rp.MropeSection) > 0 {
		sections := make([]uint32, 4)
		for i := 0; i < len(rp.MropeSection) && i < 4; i++ {
			sections[i] = uint32(rp.MropeSection[i])
		}
		w.addKV(prefix+".rope.dimension_sections", sections)
	}

	// SSM / linear-attention metadata (Qwen3.5, Qwen3Next, Mamba, etc.)
	if cfg.LinearConvKernelDim > 0 {
		w.addKV(prefix+".ssm.conv_kernel", uint32(cfg.LinearConvKernelDim))
		w.addKV(prefix+".ssm.state_size", uint32(cfg.LinearKeyHeadDim))
		w.addKV(prefix+".ssm.group_count", uint32(cfg.LinearNumKeyHeads))
		w.addKV(prefix+".ssm.time_step_rank", uint32(cfg.LinearNumValueHeads))
		w.addKV(prefix+".ssm.inner_size", uint32(cfg.LinearValueHeadDim*cfg.LinearNumValueHeads))
	}
	if cfg.FullAttentionInterval > 0 {
		w.addKV(prefix+".attention.full_attention_interval", uint32(cfg.FullAttentionInterval))
	}

	// Partial rotary dimension count
	if cfg.PartialRotaryFactor > 0 {
		headDim := cfg.HeadDim
		if headDim == 0 && cfg.NumAttentionHeads > 0 {
			headDim = cfg.HiddenSize / cfg.NumAttentionHeads
		}
		ropeDimCount := int(float64(headDim) * cfg.PartialRotaryFactor)
		w.addKV(prefix+".rope.dimension_count", uint32(ropeDimCount))
	}
}

// tensorNameReplacer returns a mapping function for HuggingFace → GGUF tensor names.
// This covers Llama, Qwen, Mistral, DeepSeek, and other architectures that follow
// the standard transformer decoder layout.
func tensorNameMapper(ggufArch string) func(string) string {
	// Architecture-specific overrides go first so they take priority
	// over the standard replacements in strings.NewReplacer.
	var overrides []string

	switch ggufArch {
	case "phi3":
		overrides = append(overrides,
			".mlp.gate_up_proj", ".ffn_gate_up",
		)
	case "qwen35", "qwen35moe", "qwen3next":
		overrides = append(overrides,
			".post_attention_layernorm", ".post_attention_norm",
			".linear_attn.in_proj_qkv", ".attn_qkv",
			".linear_attn.in_proj_z", ".attn_gate",
			".linear_attn.in_proj_a", ".ssm_beta",
			".linear_attn.in_proj_b", ".ssm_alpha",
			".linear_attn.A_log", ".ssm_a",
			".linear_attn.conv1d", ".ssm_conv1d",
			".linear_attn.dt_bias", ".ssm_dt.bias",
			".linear_attn.norm", ".ssm_norm",
			".linear_attn.out_proj", ".ssm_out",
		)
	case "kimi-linear":
		overrides = append(overrides,
			".linear_attn.in_proj_qkv", ".attn_qkv",
			".linear_attn.in_proj_z", ".attn_gate",
			".linear_attn.in_proj_a", ".ssm_beta",
			".linear_attn.in_proj_b", ".ssm_alpha",
			".linear_attn.A_log", ".ssm_a",
			".linear_attn.conv1d", ".ssm_conv1d",
			".linear_attn.dt_bias", ".ssm_dt.bias",
			".linear_attn.norm", ".ssm_norm",
			".linear_attn.out_proj", ".ssm_out",
		)
	case "falcon-h1", "granitehybrid", "nemotron_h":
		overrides = append(overrides,
			".mamba.in_proj", ".ssm_in",
			".mamba.conv1d", ".ssm_conv1d",
			".mamba.dt_proj", ".ssm_dt",
			".mamba.dt_bias", ".ssm_dt.bias",
			".mamba.A_log", ".ssm_a",
			".mamba.D", ".ssm_d",
			".mamba.norm", ".ssm_norm",
			".mamba.out_proj", ".ssm_out",
			".pre_mamba_layernorm", ".attn_norm",
		)
	case "plamo2":
		overrides = append(overrides,
			".mamba2.in_proj", ".ssm_in",
			".mamba2.conv1d", ".ssm_conv1d",
			".mamba2.dt_bias", ".ssm_dt.bias",
			".mamba2.A_log", ".ssm_a",
			".mamba2.D", ".ssm_d",
			".mamba2.norm", ".ssm_norm",
			".mamba2.out_proj", ".ssm_out",
		)
	}

	replacements := append(overrides, []string{
		"model.embed_tokens", "token_embd",
		"model.layers.", "blk.",
		".self_attn.q_proj", ".attn_q",
		".self_attn.k_proj", ".attn_k",
		".self_attn.v_proj", ".attn_v",
		".self_attn.o_proj", ".attn_output",
		".self_attn.q_norm", ".attn_q_norm",
		".self_attn.k_norm", ".attn_k_norm",
		".mlp.gate_proj", ".ffn_gate",
		".mlp.up_proj", ".ffn_up",
		".mlp.down_proj", ".ffn_down",
		".input_layernorm", ".attn_norm",
		".post_attention_layernorm", ".ffn_norm",
		"model.norm", "output_norm",
		"lm_head", "output",
	}...)

	r := strings.NewReplacer(replacements...)
	return func(hfName string) string {
		// VL models use "model.language_model.*" prefix; strip it first.
		hfName = strings.TrimPrefix(hfName, "language_model.")
		hfName = strings.TrimPrefix(hfName, "model.language_model.")
		if !strings.HasPrefix(hfName, "model.") && !strings.HasPrefix(hfName, "lm_head") {
			hfName = "model." + hfName
		}
		return r.Replace(hfName)
	}
}

// shouldIncludeTensor returns true if the tensor should be included in GGUF output.
// Filters out rotary embeddings (regenerated by llama.cpp) and multimodal
// encoder tensors (visual/audio) that belong in a separate mmproj file.
func shouldIncludeTensor(name string) bool {
	if strings.HasSuffix(name, "rotary_emb.inv_freq") {
		return false
	}
	skipPrefixes := []string{
		"model.visual.",
		"visual_encoder.",
		"visual.",
		"vision_tower.",
		"vision_model.",
		"model.vision_tower.",
		"model.mm_projector.",
		"multi_modal_projector.",
		"audio_tower.",
		"model.audio.",
		"mtp.",
	}
	for _, p := range skipPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// handleTiedEmbeddings manages the lm_head.weight tensor based on tie_word_embeddings config.
// When tie_word_embeddings is true, llama.cpp reuses token_embd.weight internally,
// so we strip lm_head.weight to avoid a redundant tensor.
// When tie_word_embeddings is false AND lm_head.weight is missing, we duplicate
// from embed_tokens as a fallback.
func handleTiedEmbeddings(sources []tensorSource, cfg *modelConfig) []tensorSource {
	if cfg.TieWordEmbeddings {
		var filtered []tensorSource
		for _, t := range sources {
			if t.name != "lm_head.weight" {
				filtered = append(filtered, t)
			}
		}
		return filtered
	}

	hasLmHead := false
	for _, t := range sources {
		if t.name == "lm_head.weight" {
			hasLmHead = true
			break
		}
	}
	if hasLmHead {
		return sources
	}

	for _, t := range sources {
		if t.name == "model.embed_tokens.weight" {
			tied := tensorSource{
				name:  "lm_head.weight",
				shape: t.shape,
				dtype: t.dtype,
				file:  t.file,
			}
			return append(sources, tied)
		}
	}

	return sources
}

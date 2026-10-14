package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// loraArchitectures lists the GGUF architectures whose tensors convert
// without the Python converter's per-tensor rewrites (Llama-style Q/K
// permutes, expert stacking, ...), so a LoRA pair maps 1:1 onto the base
// tensor name. Qwen3.5's linear-attention V-head reorder is applied below.
var loraArchitectures = map[string]bool{
	"qwen2":  true,
	"qwen3":  true,
	"qwen35": true,
}

type loraAdapterConfig struct {
	PeftType     string                 `json:"peft_type"`
	R            int                    `json:"r"`
	LoraAlpha    float64                `json:"lora_alpha"`
	UseDoRA      bool                   `json:"use_dora"`
	RankPattern  map[string]interface{} `json:"rank_pattern"`
	AlphaPattern map[string]interface{} `json:"alpha_pattern"`
}

type loraPair struct {
	a, b *tensorSource
}

// ConvertLoRA converts a PEFT LoRA adapter directory (adapter_config.json and
// adapter_model.safetensors) into a llama.cpp LoRA adapter GGUF that can be
// loaded with llama-server --lora. baseConfigDir holds the base model's
// config.json, which decides tensor names and layout transforms.
func ConvertLoRA(adapterDir, baseConfigDir, outPath string) error {
	adapterCfg, err := readLoRAAdapterConfig(adapterDir)
	if err != nil {
		return err
	}
	cfg, err := loadModelConfig(baseConfigDir)
	if err != nil {
		return fmt.Errorf("base model: %w", err)
	}
	arch, ok := detectGGUFArch(cfg.Architectures[0])
	if !ok || !loraArchitectures[arch] {
		return fmt.Errorf("LoRA conversion does not support architecture %s", cfg.Architectures[0])
	}

	sf, err := parseSafeTensorsHeader(filepath.Join(adapterDir, "adapter_model.safetensors"))
	if err != nil {
		return fmt.Errorf("reading adapter weights: %w", err)
	}
	pairs, err := collectLoRAPairs(collectTensors([]*safeTensorsFile{sf}))
	if err != nil {
		return err
	}

	w := newGGUFWriter()
	w.addKV("general.architecture", arch)
	w.addKV("general.type", "adapter")
	w.addKV("adapter.type", "lora")
	w.addKV("adapter.lora.alpha", float32(adapterCfg.LoraAlpha))

	mapName := tensorNameMapper(arch)
	baseNames := make([]string, 0, len(pairs))
	for name := range pairs {
		baseNames = append(baseNames, name)
	}
	sort.Strings(baseNames)
	for _, baseName := range baseNames {
		pair := pairs[baseName]
		if len(pair.a.shape) != 2 || len(pair.b.shape) != 2 || pair.a.shape[0] != pair.b.shape[1] {
			return fmt.Errorf("LoRA tensor %s: lora_A %v and lora_B %v do not form a rank-r product", baseName, pair.a.shape, pair.b.shape)
		}
		ggufName := mapName(baseName)
		// W = B @ A: a row permutation of W permutes B's rows, a column
		// permutation permutes A's columns. out_proj is the only Qwen3.5
		// weight reordered along its input (column) axis.
		reorderA := arch == "qwen35" && strings.Contains(baseName, ".linear_attn.out_proj.")
		reorderB := arch == "qwen35" && !reorderA
		if err := addLoRATensor(w, ggufName+".lora_a", baseName, pair.a, reorderA, cfg); err != nil {
			return err
		}
		if err := addLoRATensor(w, ggufName+".lora_b", baseName, pair.b, reorderB, cfg); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	tmp := outPath + ".tmp"
	if err := w.writeTo(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing LoRA GGUF: %w", err)
	}
	return os.Rename(tmp, outPath)
}

func readLoRAAdapterConfig(adapterDir string) (*loraAdapterConfig, error) {
	data, err := os.ReadFile(filepath.Join(adapterDir, "adapter_config.json"))
	if err != nil {
		return nil, fmt.Errorf("reading adapter_config.json: %w", err)
	}
	var cfg loraAdapterConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing adapter_config.json: %w", err)
	}
	switch {
	case !strings.EqualFold(cfg.PeftType, "LORA"):
		return nil, fmt.Errorf("adapter peft_type %q is not LORA", cfg.PeftType)
	case cfg.UseDoRA:
		return nil, fmt.Errorf("DoRA adapters are not supported")
	case len(cfg.RankPattern) > 0 || len(cfg.AlphaPattern) > 0:
		return nil, fmt.Errorf("adapters with per-module rank_pattern or alpha_pattern are not supported")
	case cfg.R <= 0 || cfg.LoraAlpha <= 0:
		return nil, fmt.Errorf("adapter_config.json needs positive r and lora_alpha")
	}
	return &cfg, nil
}

// collectLoRAPairs groups PEFT tensors such as
// base_model.model.model.layers.0.mlp.up_proj.lora_A.weight by the base
// weight name they adapt (model.layers.0.mlp.up_proj.weight).
func collectLoRAPairs(sources []tensorSource) (map[string]*loraPair, error) {
	pairs := map[string]*loraPair{}
	for i := range sources {
		src := &sources[i]
		name := strings.TrimPrefix(src.name, "base_model.model.")
		var isA bool
		switch {
		case strings.HasSuffix(name, ".lora_A.weight"):
			isA = true
			name = strings.TrimSuffix(name, ".lora_A.weight") + ".weight"
		case strings.HasSuffix(name, ".lora_B.weight"):
			name = strings.TrimSuffix(name, ".lora_B.weight") + ".weight"
		default:
			return nil, fmt.Errorf("unexpected adapter tensor %q: only lora_A/lora_B weights are supported", src.name)
		}
		pair := pairs[name]
		if pair == nil {
			pair = &loraPair{}
			pairs[name] = pair
		}
		if isA {
			pair.a = src
		} else {
			pair.b = src
		}
	}
	for name, pair := range pairs {
		if pair.a == nil || pair.b == nil {
			return nil, fmt.Errorf("adapter tensor %s is missing its lora_A or lora_B half", name)
		}
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("adapter has no LoRA tensors")
	}
	return pairs, nil
}

func addLoRATensor(w *ggufWriter, ggufName, baseName string, src *tensorSource, reorder bool, cfg *modelConfig) error {
	inType, err := stDTypeToGGML(src.dtype)
	if err != nil {
		return fmt.Errorf("tensor %q: %w", src.name, err)
	}
	outType := inType
	if inType == GGMLTypeBF16 {
		outType = GGMLTypeF16
	}
	w.addTensor(ggufName, reverseShape(src.shape), outType, func() ([]byte, error) {
		data, err := src.readData()
		if err != nil {
			return nil, err
		}
		if reorder {
			// The reorder rules key on the base weight name; the factor's
			// own shape picks the permuted axis.
			data = qwen35ReorderLinearAttention(data, src.shape, baseName, inType, cfg)
		}
		return convertDtype(data, inType, outType), nil
	})
	return nil
}

package inference

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var supportedSpeculativeTypes = map[string]bool{
	"draft-simple":  true,
	"draft-eagle3":  true,
	"draft-dflash":  true,
	"draft-dspark":  true,
	"draft-mtp":     true,
	"ngram-cache":   true,
	"ngram-simple":  true,
	"ngram-map-k":   true,
	"ngram-map-k4v": true,
	"ngram-mod":     true,
}

var draftSpeculativeTypes = map[string]bool{
	"draft-simple": true,
	"draft-eagle3": true,
	"draft-dflash": true,
	"draft-dspark": true,
	"draft-mtp":    true,
}

type SpeculativeConfig struct {
	Types      []string
	DraftModel string
	DraftNMax  int
	DraftNMin  int
	DraftPMin  *float64
}

func NormalizeSpeculativeConfig(config SpeculativeConfig) (SpeculativeConfig, error) {
	seen := make(map[string]bool, len(config.Types))
	types := make([]string, 0, len(config.Types))
	draftCount := 0
	for _, value := range config.Types {
		for _, part := range strings.Split(value, ",") {
			specType := strings.ToLower(strings.TrimSpace(part))
			if specType == "" || specType == "none" {
				continue
			}
			if !supportedSpeculativeTypes[specType] {
				return SpeculativeConfig{}, fmt.Errorf("unsupported speculative decoding type %q", specType)
			}
			if seen[specType] {
				continue
			}
			seen[specType] = true
			types = append(types, specType)
			if draftSpeculativeTypes[specType] {
				draftCount++
			}
		}
	}
	if draftCount > 1 {
		return SpeculativeConfig{}, fmt.Errorf("only one draft speculative decoding type can be enabled")
	}
	sort.SliceStable(types, func(i, j int) bool {
		return speculativeTypeRank(types[i]) < speculativeTypeRank(types[j])
	})
	config.Types = types
	config.DraftModel = strings.TrimSpace(config.DraftModel)
	if len(types) == 0 {
		if config.DraftModel != "" || config.DraftNMax != 0 || config.DraftNMin != 0 || config.DraftPMin != nil {
			return SpeculativeConfig{}, fmt.Errorf("speculative decoding parameters require at least one type")
		}
		return SpeculativeConfig{}, nil
	}
	if draftCount == 0 && (config.DraftModel != "" || config.DraftNMax != 0 || config.DraftNMin != 0 || config.DraftPMin != nil) {
		return SpeculativeConfig{}, fmt.Errorf("draft parameters require a draft-* speculative decoding type")
	}
	hasExternalDraftType := false
	for _, specType := range types {
		if draftSpeculativeTypes[specType] && specType != "draft-mtp" {
			hasExternalDraftType = true
		}
	}
	if hasExternalDraftType && config.DraftModel == "" {
		return SpeculativeConfig{}, fmt.Errorf("draft_model is required for %s", strings.Join(types, ","))
	}
	if config.DraftNMax < 0 || config.DraftNMax > 256 {
		return SpeculativeConfig{}, fmt.Errorf("draft_n_max must be between 1 and 256")
	}
	if config.DraftNMin < 0 || config.DraftNMin > 256 {
		return SpeculativeConfig{}, fmt.Errorf("draft_n_min must be between 1 and 256")
	}
	if config.DraftNMax > 0 && config.DraftNMin > config.DraftNMax {
		return SpeculativeConfig{}, fmt.Errorf("draft_n_min cannot exceed draft_n_max")
	}
	if config.DraftPMin != nil && (*config.DraftPMin < 0 || *config.DraftPMin > 1) {
		return SpeculativeConfig{}, fmt.Errorf("draft_p_min must be between 0 and 1")
	}
	return config, nil
}

func speculativeTypeRank(specType string) int {
	if draftSpeculativeTypes[specType] {
		return 0
	}
	return 1
}

func (config SpeculativeConfig) Enabled() bool {
	return len(config.Types) > 0
}

func (config SpeculativeConfig) Key() string {
	if !config.Enabled() {
		return ""
	}
	pMin := ""
	if config.DraftPMin != nil {
		pMin = strconv.FormatFloat(*config.DraftPMin, 'g', -1, 64)
	}
	return strings.Join(config.Types, ",") + "|" + config.DraftModel + "|" +
		strconv.Itoa(config.DraftNMax) + "|" + strconv.Itoa(config.DraftNMin) + "|" + pMin
}

func (config SpeculativeConfig) llamaArgs() []string {
	if !config.Enabled() {
		return nil
	}
	args := []string{"--spec-type", strings.Join(config.Types, ",")}
	if config.DraftModel != "" {
		args = append(args, "--spec-draft-model", config.DraftModel)
	}
	if config.DraftNMax > 0 {
		args = append(args, "--spec-draft-n-max", strconv.Itoa(config.DraftNMax))
	}
	if config.DraftNMin > 0 {
		args = append(args, "--spec-draft-n-min", strconv.Itoa(config.DraftNMin))
	}
	if config.DraftPMin != nil {
		args = append(args, "--spec-draft-p-min", strconv.FormatFloat(*config.DraftPMin, 'g', -1, 64))
	}
	return args
}

var llamaSpecCapabilities sync.Map

func validateLlamaSpecCapabilities(binary string, config SpeculativeConfig) error {
	if !config.Enabled() {
		return nil
	}
	raw, ok := llamaSpecCapabilities.Load(binary)
	if !ok {
		output, err := exec.Command(binary, "--help").CombinedOutput()
		if err != nil {
			return fmt.Errorf("checking llama-server speculative decoding support: %w", err)
		}
		raw = strings.ToLower(string(output))
		llamaSpecCapabilities.Store(binary, raw)
	}
	help := raw.(string)
	if !strings.Contains(help, "--spec-type") {
		return fmt.Errorf("installed llama-server does not support speculative decoding")
	}
	for _, specType := range config.Types {
		if !strings.Contains(help, specType) {
			return fmt.Errorf("installed llama-server does not support speculative type %q; upgrade llama.cpp", specType)
		}
	}
	return nil
}

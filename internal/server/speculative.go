package server

import (
	"fmt"
	"strings"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

func (s *Server) resolveSpeculativeConfig(modelID string, options *api.SpeculativeOptions) (inference.SpeculativeConfig, error) {
	if options == nil {
		return inference.SpeculativeConfig{}, nil
	}
	config := inference.SpeculativeConfig{
		Types:     append([]string(nil), options.Types...),
		DraftNMax: options.DraftNMax,
		DraftNMin: options.DraftNMin,
		DraftPMin: options.DraftPMin,
	}
	draftModelID := strings.TrimSpace(options.DraftModel)
	if draftModelID != "" {
		storageID := s.resolveLocalModelStorageID(draftModelID)
		draftDir, err := s.manager.ModelPath(storageID)
		if err != nil {
			return inference.SpeculativeConfig{}, fmt.Errorf("draft model %q not found locally", draftModelID)
		}
		draftPath, err := resolveDraftGGUFPath(draftDir, draftModelID)
		if err != nil {
			return inference.SpeculativeConfig{}, err
		}
		config.DraftModel = draftPath
	}

	// MTP models may carry the draft head in the target GGUF or in a companion
	// file. If a companion exists, pass it explicitly; otherwise llama.cpp can
	// still discover an integrated MTP head from the main model.
	hasMTP := false
	for _, specType := range config.Types {
		for _, part := range strings.Split(specType, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "draft-mtp") {
				hasMTP = true
			}
		}
	}
	if hasMTP && config.DraftModel == "" {
		targetDir, pathErr := s.manager.ModelPath(s.resolveLocalModelStorageID(modelID))
		if pathErr == nil {
			if mtpFiles, findErr := model.FindMTPFiles(targetDir); findErr == nil && len(mtpFiles) > 0 {
				config.DraftModel = mtpFiles[0]
			}
		}
	}
	return inference.NormalizeSpeculativeConfig(config)
}

// resolveDraftGGUFPath returns a GGUF path for speculative draft weights.
// Existing GGUF is preferred; otherwise convertible HF weights are auto-converted
// the same way target SafeTensors models are handled on load.
func resolveDraftGGUFPath(draftDir, draftModelID string) (string, error) {
	if path, ok, err := convert.FindGGUFForDType(draftDir, ""); err != nil {
		return "", err
	} else if ok {
		return path, nil
	}
	if !convert.HasConvertibleHFWeights(draftDir) {
		return "", fmt.Errorf("draft model %q must contain GGUF weights or convertible SafeTensors/PyTorch weights", draftModelID)
	}
	ggufPath, err := convert.Convert(draftDir, nil, "")
	if err != nil {
		return "", fmt.Errorf("auto-converting draft model %q to GGUF: %w", draftModelID, err)
	}
	return ggufPath, nil
}

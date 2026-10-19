package convert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sourceWeightIndexName is the shard index that names the SafeTensors files
// llama.cpp reads; it is only useful alongside those shards.
const sourceWeightIndexName = "model.safetensors.index.json"

// ConvertedSourceWeightFiles returns the top-level SafeTensors files the GGUF
// converter consumed in modelDir, plus their shard index. llama.cpp's
// convert_hf_to_gguf.py reads every "model*.safetensors" file at the top
// level, so companions such as head.safetensors or adapter directories are
// not listed and stay in place.
func ConvertedSourceWeightFiles(modelDir string) ([]string, error) {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if lower == sourceWeightIndexName || (strings.HasPrefix(lower, "model") && strings.HasSuffix(lower, ".safetensors")) {
			names = append(names, name)
		}
	}
	return names, nil
}

// RemoveConvertedSourceWeights deletes the SafeTensors weights in modelDir
// once a converted GGUF serves the model, so the checkpoint is not stored
// twice. It keeps them when no weight GGUF exists, or when a vision model is
// still missing its mmproj, since a later load converts the projector from
// the same weights. It returns the removed file names and their total size.
func RemoveConvertedSourceWeights(modelDir string) ([]string, int64, error) {
	if _, ok, err := FindGGUFForDType(modelDir, ""); err != nil || !ok {
		return nil, 0, err
	}
	if hasVisionConfig(modelDir) {
		if _, ok, err := FindMMProjForDType(modelDir, ""); err != nil || !ok {
			return nil, 0, err
		}
	}
	names, err := ConvertedSourceWeightFiles(modelDir)
	if err != nil {
		return nil, 0, err
	}
	var (
		removed []string
		freed   int64
		errs    []error
	)
	for _, name := range names {
		path := filepath.Join(modelDir, name)
		info, statErr := os.Stat(path)
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("removing %s: %w", name, err))
			}
			continue
		}
		removed = append(removed, name)
		if statErr == nil {
			freed += info.Size()
		}
	}
	return removed, freed, errors.Join(errs...)
}

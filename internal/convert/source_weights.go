package convert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/model"
)

// ErrSourceWeightsRemoved reports that a model's SafeTensors were deleted
// after an earlier conversion, so no other dtype can be converted from it.
var ErrSourceWeightsRemoved = errors.New("SafeTensors source weights were removed after an earlier GGUF conversion; pull the model again to convert another dtype")

// sourceWeightLocks serializes deleting a model's SafeTensors with
// conversions reading them. Every load runs in the server process, so an
// in-process lock per model directory is enough.
var sourceWeightLocks sync.Map

func sourceWeightLock(modelDir string) *sync.RWMutex {
	key := filepath.Clean(modelDir)
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	lock, _ := sourceWeightLocks.LoadOrStore(key, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

// SourceWeightsRemoved reports whether the manifest records SafeTensors that
// were deleted after conversion and at least one of them is still missing.
// A re-pull that restores every file makes the model convertible again.
func SourceWeightsRemoved(modelDir string) bool {
	lm, err := model.LoadManifestInDir(modelDir)
	if err != nil {
		return false
	}
	for _, name := range lm.RemovedSourceWeights {
		if _, err := os.Stat(filepath.Join(modelDir, name)); err != nil {
			return true
		}
	}
	return false
}

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
// the same weights. The files are recorded in the manifest before they are
// deleted, and conversions of the same directory are waited out first. It
// returns the removed file names and their total size.
func RemoveConvertedSourceWeights(modelDir string) ([]string, int64, error) {
	lock := sourceWeightLock(modelDir)
	lock.Lock()
	defer lock.Unlock()

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
	// An index without shards next to it is not what the converter read.
	if !containsSafeTensors(names) {
		return nil, 0, nil
	}
	if err := model.RecordRemovedSourceWeights(modelDir, names); err != nil {
		return nil, 0, fmt.Errorf("recording removed SafeTensors: %w", err)
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

func containsSafeTensors(names []string) bool {
	for _, name := range names {
		if strings.HasSuffix(strings.ToLower(name), ".safetensors") {
			return true
		}
	}
	return false
}

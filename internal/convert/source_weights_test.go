package convert

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/model"
)

const sourceWeightManifest = `{"namespace":"demo","name":"demo","format":"safetensors"}`

func writeSourceWeightFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func remainingFiles(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(names)
	return names
}

func TestRemoveConvertedSourceWeightsKeepsCompanions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "JEV-9B")
	writeSourceWeightFixture(t, dir, map[string]string{
		"config.json":                       `{"architectures":["Qwen3ForCausalLM"]}`,
		"manifest.json":                     sourceWeightManifest,
		"model-00001-of-00002.safetensors":  "aaaa",
		"model-00002-of-00002.safetensors":  "bb",
		"model.safetensors.index.json":      "{}",
		"head.safetensors":                  "head",
		"adapter/adapter_model.safetensors": "lora",
		"JEV-9B-q8_0.gguf":                  "gguf",
	})

	removed, freed, err := RemoveConvertedSourceWeights(dir)
	if err != nil {
		t.Fatalf("RemoveConvertedSourceWeights: %v", err)
	}
	wantRemoved := []string{"model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors", "model.safetensors.index.json"}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Fatalf("removed = %v, want %v", removed, wantRemoved)
	}
	if freed != 8 {
		t.Fatalf("freed = %d, want 8", freed)
	}
	want := []string{"JEV-9B-q8_0.gguf", "adapter/adapter_model.safetensors", "config.json", "head.safetensors", "manifest.json"}
	if got := remainingFiles(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining = %v, want %v", got, want)
	}
}

func TestRemoveConvertedSourceWeightsKeepsWeightsWithoutGGUF(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	writeSourceWeightFixture(t, dir, map[string]string{
		"config.json":       `{"architectures":["LlamaForCausalLM"]}`,
		"model.safetensors": "weights",
	})

	removed, _, err := RemoveConvertedSourceWeights(dir)
	if err != nil {
		t.Fatalf("RemoveConvertedSourceWeights: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
}

func TestRemoveConvertedSourceWeightsKeepsVisionWeightsUntilMMProjExists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vlm")
	writeSourceWeightFixture(t, dir, map[string]string{
		"config.json":       `{"architectures":["Qwen2VLForConditionalGeneration"],"vision_config":{}}`,
		"manifest.json":     sourceWeightManifest,
		"model.safetensors": "weights",
		"vlm-f16.gguf":      "gguf",
	})

	removed, _, err := RemoveConvertedSourceWeights(dir)
	if err != nil {
		t.Fatalf("RemoveConvertedSourceWeights: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v before mmproj exists, want none", removed)
	}

	writeSourceWeightFixture(t, dir, map[string]string{"mmproj-vlm-f16.gguf": "mmproj"})
	removed, _, err = RemoveConvertedSourceWeights(dir)
	if err != nil {
		t.Fatalf("RemoveConvertedSourceWeights: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"model.safetensors"}) {
		t.Fatalf("removed = %v after mmproj exists, want [model.safetensors]", removed)
	}
}

func TestRemovedSourceWeightsAreNotConvertible(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "JEV-9B")
	writeSourceWeightFixture(t, dir, map[string]string{
		"config.json":                      `{"architectures":["Qwen3ForCausalLM"]}`,
		"manifest.json":                    sourceWeightManifest,
		"model-00001-of-00002.safetensors": "aaaa",
		"model-00002-of-00002.safetensors": "bb",
		"head.safetensors":                 "head",
		"JEV-9B-q8_0.gguf":                 "gguf",
	})
	if !HasConvertibleHFWeights(dir) {
		t.Fatal("HasConvertibleHFWeights = false before removal, want true")
	}

	if _, _, err := RemoveConvertedSourceWeights(dir); err != nil {
		t.Fatalf("RemoveConvertedSourceWeights: %v", err)
	}
	lm, err := model.LoadManifestInDir(dir)
	if err != nil {
		t.Fatalf("LoadManifestInDir: %v", err)
	}
	wantRecorded := []string{"model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors"}
	if !reflect.DeepEqual(lm.RemovedSourceWeights, wantRecorded) {
		t.Fatalf("RemovedSourceWeights = %v, want %v", lm.RemovedSourceWeights, wantRecorded)
	}
	// head.safetensors stays behind but is not something the converter reads.
	if !SourceWeightsRemoved(dir) {
		t.Fatal("SourceWeightsRemoved = false after removal, want true")
	}
	if HasConvertibleHFWeights(dir) {
		t.Fatal("HasConvertibleHFWeights = true with only head.safetensors left, want false")
	}
	if needs, err := NeedsConversionForDType(dir, "f16"); err != nil || needs {
		t.Fatalf("NeedsConversionForDType(f16) = %v, %v; want false, nil", needs, err)
	}
	if _, err := Convert(dir, nil, "f16"); !errors.Is(err, ErrSourceWeightsRemoved) {
		t.Fatalf("Convert(f16) error = %v, want ErrSourceWeightsRemoved", err)
	}

	// A re-pull that restores every shard makes the model convertible again.
	writeSourceWeightFixture(t, dir, map[string]string{
		"model-00001-of-00002.safetensors": "aaaa",
		"model-00002-of-00002.safetensors": "bb",
	})
	if SourceWeightsRemoved(dir) || !HasConvertibleHFWeights(dir) {
		t.Fatal("model still reported as removed after its shards were restored")
	}
}

func TestRemoveConvertedSourceWeightsKeepsWeightsWithoutManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	writeSourceWeightFixture(t, dir, map[string]string{
		"config.json":       `{"architectures":["LlamaForCausalLM"]}`,
		"model.safetensors": "weights",
		"demo-f16.gguf":     "gguf",
	})

	removed, _, err := RemoveConvertedSourceWeights(dir)
	if err == nil {
		t.Fatal("RemoveConvertedSourceWeights succeeded without a manifest to record the removal")
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "model.safetensors")); statErr != nil {
		t.Fatalf("model.safetensors was deleted: %v", statErr)
	}
}

func TestRemoveConvertedSourceWeightsWaitsForConversion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	writeSourceWeightFixture(t, dir, map[string]string{
		"config.json":       `{"architectures":["LlamaForCausalLM"]}`,
		"manifest.json":     sourceWeightManifest,
		"model.safetensors": "weights",
		"demo-f16.gguf":     "gguf",
	})

	// Hold the lock a running conversion takes.
	lock := sourceWeightLock(dir)
	lock.RLock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := RemoveConvertedSourceWeights(dir); err != nil {
			t.Errorf("RemoveConvertedSourceWeights: %v", err)
		}
	}()

	select {
	case <-done:
		lock.RUnlock()
		t.Fatal("RemoveConvertedSourceWeights ran while a conversion held the source weights")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(dir, "model.safetensors")); err != nil {
		t.Fatalf("model.safetensors deleted during conversion: %v", err)
	}

	lock.RUnlock()
	<-done
	if _, err := os.Stat(filepath.Join(dir, "model.safetensors")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("model.safetensors still present after conversion finished: %v", err)
	}
}

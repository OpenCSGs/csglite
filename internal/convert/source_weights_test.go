package convert

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

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
	want := []string{"JEV-9B-q8_0.gguf", "adapter/adapter_model.safetensors", "config.json", "head.safetensors"}
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

package server

import "testing"

func TestStampClusterManifestRecordsInstallSource(t *testing.T) {
	manifest := map[string]any{
		"namespace":       "Qwen",
		"name":            "Qwen3-ASR-0.6B",
		"repository":      "Qwen/Qwen3-ASR-0.6B",
		"artifact_source": "opencsg",
	}
	stampClusterManifest(manifest, "modelscope", "Qwen", "Qwen3-ASR-0.6B")
	if manifest["artifact_source"] != "modelscope" {
		t.Fatalf("artifact_source = %v", manifest["artifact_source"])
	}
	if manifest["repository"] != "Qwen/Qwen3-ASR-0.6B" {
		t.Fatalf("repository = %v", manifest["repository"])
	}
}

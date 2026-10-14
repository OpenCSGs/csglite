package convert

import (
	"path/filepath"
	"testing"
)

func TestDeclaresMissingMTPLayers(t *testing.T) {
	write := func(cfg map[string]any, tensors map[string]testTensor) string {
		dir := t.TempDir()
		writeTestJSON(t, filepath.Join(dir, "config.json"), cfg)
		writeTestSafeTensors(t, filepath.Join(dir, "model.safetensors"), tensors)
		return dir
	}
	textOnly := map[string]testTensor{"model.layers.0.mlp.up_proj.weight": seqTensor(2, 2)}
	withMTP := map[string]testTensor{
		"model.layers.0.mlp.up_proj.weight": seqTensor(2, 2),
		"mtp.layers.0.mlp.up_proj.weight":   seqTensor(2, 2),
	}

	if !declaresMissingMTPLayers(write(map[string]any{"mtp_num_hidden_layers": 1}, textOnly)) {
		t.Fatal("declared MTP layers without mtp.* weights should be detected")
	}
	if !declaresMissingMTPLayers(write(map[string]any{"text_config": map[string]any{"mtp_num_hidden_layers": 1}}, textOnly)) {
		t.Fatal("text_config MTP layers without weights should be detected")
	}
	if declaresMissingMTPLayers(write(map[string]any{"mtp_num_hidden_layers": 1}, withMTP)) {
		t.Fatal("checkpoints that ship MTP weights must keep them")
	}
	if declaresMissingMTPLayers(write(map[string]any{"num_hidden_layers": 1}, textOnly)) {
		t.Fatal("configs without MTP layers need no flag")
	}
}

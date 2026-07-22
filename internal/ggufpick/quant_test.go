package ggufpick

import (
	"reflect"
	"strings"
	"testing"
)

func TestQuantRank(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"Qwen3-0.6B-Q8_0.gguf", quantRanks["q8_0"]},
		{"model-Q4_K_M.gguf", quantRanks["q4_k_m"]},
		{"Llama-3-8B-Q4_K_M-00001-of-00003.gguf", quantRanks["q4_k_m"]},
		{"weights-f16.gguf", quantRanks["f16"]},
		{"x-bf16.gguf", quantRanks["bf16"]},
		{"x-f32.gguf", quantRanks["f32"]},
		// Dot-separated quant tokens (issue #75).
		{"Qwen.Qwen3-VL-Embedding-2B.Q5_K_M.gguf", quantRanks["q5_k_m"]},
		{"Qwen.Qwen3-VL-Embedding-2B.f16.gguf", quantRanks["f16"]},
		{"unknown.gguf", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if g := QuantRank(tt.name); g != tt.want {
				t.Errorf("QuantRank(%q) = %d, want %d", tt.name, g, tt.want)
			}
		})
	}
}

func TestFilterWeightGGUFFiles(t *testing.T) {
	entries := []FileEntry{
		{Path: "a/Q4_0.gguf", Name: "Q4_0.gguf", Size: 100},
		{Path: "a/Q8_0.gguf", Name: "Q8_0.gguf", Size: 200},
		{Path: "a/Q4_K_M.gguf", Name: "Q4_K_M.gguf", Size: 150},
	}
	got := FilterWeightGGUFFiles(entries)
	if len(got) != 1 || got[0].Name != "Q8_0.gguf" {
		t.Errorf("FilterWeightGGUFFiles = %#v, want single Q8_0", got)
	}

	sharded := []FileEntry{
		{Path: "M-Q4_0-00001-of-00002.gguf", Name: "M-Q4_0-00001-of-00002.gguf"},
		{Path: "M-Q4_0-00002-of-00002.gguf", Name: "M-Q4_0-00002-of-00002.gguf"},
		{Path: "M-Q8_0-00001-of-00002.gguf", Name: "M-Q8_0-00001-of-00002.gguf"},
		{Path: "M-Q8_0-00002-of-00002.gguf", Name: "M-Q8_0-00002-of-00002.gguf"},
	}
	got2 := FilterWeightGGUFFiles(sharded)
	wantPaths := map[string]bool{
		"M-Q8_0-00001-of-00002.gguf": true,
		"M-Q8_0-00002-of-00002.gguf": true,
	}
	if len(got2) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got2), got2)
	}
	for _, e := range got2 {
		if !wantPaths[e.Path] {
			t.Errorf("unexpected path %q", e.Path)
		}
	}
}

func TestFilterWeightGGUFFiles_unknownOnlyNoOp(t *testing.T) {
	entries := []FileEntry{
		{Path: "a.gguf", Name: "a.gguf"},
		{Path: "b.gguf", Name: "b.gguf"},
	}
	got := FilterWeightGGUFFiles(entries)
	if !reflect.DeepEqual(got, entries) {
		t.Errorf("expected unchanged, got %#v", got)
	}
}

func TestBestWeightGGUFName(t *testing.T) {
	names := []string{"x-Q4_0.gguf", "x-Q8_0.gguf", "x-Q4_K_M.gguf"}
	if g := BestWeightGGUFName(names); g != "x-Q8_0.gguf" {
		t.Errorf("got %q, want x-Q8_0.gguf", g)
	}
}

func TestQuantRankFromRepoPath(t *testing.T) {
	if g := QuantRankFromRepoPath("Q8_0/model.gguf"); g != quantRanks["q8_0"] {
		t.Errorf("Q8_0/model.gguf = %d", g)
	}
	if g := QuantRankFromRepoPath(`weights\Q4_K_M\model.gguf`); g != quantRanks["q4_k_m"] {
		t.Errorf("windows path = %d", g)
	}
	// Filename wins when it encodes a different quant than the parent folder.
	if g := QuantRankFromRepoPath("Q8_0/legacy-Q4_0.gguf"); g != quantRanks["q4_0"] {
		t.Errorf("filename should win: got %d", g)
	}
}

func TestQuantLabel(t *testing.T) {
	if g := QuantLabel("Qwen3-0.6B-Q8_0.gguf"); g != "Q8_0" {
		t.Fatalf("QuantLabel = %q, want Q8_0", g)
	}
	if g := QuantLabel("Qwen.Qwen3-VL-Embedding-2B.Q5_K_M.gguf"); g != "Q5_K_M" {
		t.Fatalf("QuantLabel(dot separated) = %q, want Q5_K_M", g)
	}
	if g := QuantLabel("model.gguf"); g != "" {
		t.Fatalf("QuantLabel = %q, want empty", g)
	}
}

func TestQuantLabelFromRepoPath(t *testing.T) {
	if g := QuantLabelFromRepoPath("Q4_K_M/model.gguf"); g != "Q4_K_M" {
		t.Fatalf("QuantLabelFromRepoPath(dir) = %q, want Q4_K_M", g)
	}
	if g := QuantLabelFromRepoPath("Q8_0/legacy-Q4_0.gguf"); g != "Q4_0" {
		t.Fatalf("QuantLabelFromRepoPath(filename wins) = %q, want Q4_0", g)
	}
	if g := QuantLabelFromRepoPath("weights/model.gguf"); g != "" {
		t.Fatalf("QuantLabelFromRepoPath = %q, want empty", g)
	}
}

func TestFilterWeightGGUFFiles_nestedDirs(t *testing.T) {
	entries := []FileEntry{
		{Path: "Q4_0/model.gguf", Name: "model.gguf"},
		{Path: "Q8_0/model.gguf", Name: "model.gguf"},
	}
	got := FilterWeightGGUFFiles(entries)
	if len(got) != 1 || got[0].Path != "Q8_0/model.gguf" {
		t.Errorf("got %#v, want Q8_0/model.gguf only", got)
	}
}

func TestBestWeightGGUFRelPath(t *testing.T) {
	paths := []string{"Q4_0/a.gguf", "Q8_0/b.gguf"}
	if g := BestWeightGGUFRelPath(paths); g != "Q8_0/b.gguf" {
		t.Errorf("got %q", g)
	}
}

// HF-style: quant folder + long basename with dots/sizes + -00001-of-00003 shards.
func TestQuantRankFromRepoPath_Qwen35ShardInQuantFolder(t *testing.T) {
	p := "Q3_K_M/Qwen3.5-122B-A10B-Q3_K_M-00001-of-00003.gguf"
	if g := QuantRankFromRepoPath(p); g != quantRanks["q3_k_m"] {
		t.Fatalf("QuantRankFromRepoPath(%q) = %d, want %d (q3_k_m)", p, g, quantRanks["q3_k_m"])
	}
}

func TestFilterWeightGGUFFiles_ShardedPerQuantFolder(t *testing.T) {
	entries := []FileEntry{
		{Path: "Q4_K_M/Qwen3.5-122B-A10B-Q4_K_M-00001-of-00002.gguf"},
		{Path: "Q4_K_M/Qwen3.5-122B-A10B-Q4_K_M-00002-of-00002.gguf"},
		{Path: "Q3_K_M/Qwen3.5-122B-A10B-Q3_K_M-00001-of-00003.gguf"},
		{Path: "Q3_K_M/Qwen3.5-122B-A10B-Q3_K_M-00002-of-00003.gguf"},
		{Path: "Q3_K_M/Qwen3.5-122B-A10B-Q3_K_M-00003-of-00003.gguf"},
	}
	got := FilterWeightGGUFFiles(entries)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 shards of Q4_K_M only: %#v", len(got), got)
	}
	for _, e := range got {
		if !strings.HasPrefix(e.Path, "Q4_K_M/") {
			t.Errorf("unexpected path %q", e.Path)
		}
	}
}

func TestFilterWeightGGUFFilesByQuant(t *testing.T) {
	entries := []FileEntry{
		{Path: "Q4_K_M/Qwen3.5-122B-A10B-Q4_K_M-00001-of-00002.gguf"},
		{Path: "Q4_K_M/Qwen3.5-122B-A10B-Q4_K_M-00002-of-00002.gguf"},
		{Path: "Q3_K_M/Qwen3.5-122B-A10B-Q3_K_M-00001-of-00003.gguf"},
	}
	got := FilterWeightGGUFFilesByQuant(entries, "Q4_K_M")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if empty := FilterWeightGGUFFilesByQuant(entries, "IQ4_XS"); len(empty) != 0 {
		t.Fatalf("want empty, got %#v", empty)
	}
}

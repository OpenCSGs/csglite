package csghub

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestFilterGGUFMultiQuantDownload(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "README.md", Name: "README.md"},
		{Type: "file", Path: "Q8_0.gguf", Name: "Q8_0.gguf", LFS: true},
		{Type: "file", Path: "Q4_0.gguf", Name: "Q4_0.gguf", LFS: true},
	}
	got, err := filterGGUFMultiQuantDownload(files, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"README.md", "Q8_0.gguf"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestFilterGGUFMultiQuantDownload_singleGGUF(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "Q4_0.gguf", Name: "Q4_0.gguf"},
	}
	got, err := filterGGUFMultiQuantDownload(files, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestFilterGGUFMultiQuantDownload_nestedQuantDirs(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "README.md", Name: "README.md"},
		{Type: "file", Path: "Q4_0/model.gguf", Name: "model.gguf", LFS: true},
		{Type: "file", Path: "Q8_0/model.gguf", Name: "model.gguf", LFS: true},
	}
	got, err := filterGGUFMultiQuantDownload(files, "")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := []string{"README.md", "Q8_0/model.gguf"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("got %v, want %v", paths, want)
	}
}

func TestFilterGGUFMultiQuantDownload_explicitQuant(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "README.md", Name: "README.md"},
		{Type: "file", Path: "Q8_0.gguf", Name: "Q8_0.gguf", LFS: true},
		{Type: "file", Path: "Q4_0.gguf", Name: "Q4_0.gguf", LFS: true},
	}
	got, err := filterGGUFMultiQuantDownload(files, "Q4_0")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"README.md", "Q4_0.gguf"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestFilterGGUFMultiQuantDownload_unknownQuantReturnsError(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "Q8_0.gguf", Name: "Q8_0.gguf", LFS: true},
		{Type: "file", Path: "Q4_0.gguf", Name: "Q4_0.gguf", LFS: true},
	}
	_, err := filterGGUFMultiQuantDownload(files, "IQ4_XS")
	if err == nil {
		t.Fatal("expected error for unknown quantization")
	}
}

func TestFilterTransformersWeightDownloadPrefersSafeTensors(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "config.json", Name: "config.json"},
		{Type: "file", Path: "model.safetensors", Name: "model.safetensors", LFS: true},
		{Type: "file", Path: "model.fp32-00001-of-00002.safetensors", Name: "model.fp32-00001-of-00002.safetensors", LFS: true},
		{Type: "file", Path: "pytorch_model.bin", Name: "pytorch_model.bin", LFS: true},
		{Type: "file", Path: "weights/extra.bin", Name: "extra.bin", LFS: true},
		{Type: "file", Path: "flax_model.msgpack", Name: "flax_model.msgpack", LFS: true},
	}
	got := filterTransformersWeightDownload(files)
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"config.json", "model.safetensors"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestFilterTransformersWeightDownloadSkipsRedundantPyTorchWeights(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "config.json", Name: "config.json"},
		{Type: "file", Path: "pytorch_model.bin", Name: "pytorch_model.bin", LFS: true},
		{Type: "file", Path: "pytorch_model.fp32-00001-of-00002.bin", Name: "pytorch_model.fp32-00001-of-00002.bin", LFS: true},
		{Type: "file", Path: "flax_model.msgpack", Name: "flax_model.msgpack", LFS: true},
	}
	got := filterTransformersWeightDownload(files)
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"config.json", "pytorch_model.bin"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestFilterTransformersWeightDownloadSkipsStandaloneFP32PyTorchShards(t *testing.T) {
	files := []RepoFile{
		{Type: "file", Path: "config.json", Name: "config.json"},
		{Type: "file", Path: "pytorch_model.fp32-00001-of-00002.bin", Name: "pytorch_model.fp32-00001-of-00002.bin", LFS: true},
		{Type: "file", Path: "pytorch_model.fp32-00002-of-00002.bin", Name: "pytorch_model.fp32-00002-of-00002.bin", LFS: true},
		{Type: "file", Path: "subdir/pytorch.fp32-00001-of-00001.bin", Name: "pytorch.fp32-00001-of-00001.bin", LFS: true},
		{Type: "file", Path: "tokenizer.json", Name: "tokenizer.json"},
	}
	got := filterTransformersWeightDownload(files)
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"config.json", "tokenizer.json"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestDownloadLFSFileRemovesOversizedPartial(t *testing.T) {
	dest := t.TempDir() + "/model.safetensors"
	if err := os.WriteFile(dest, []byte("too-large"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := NewClient("http://127.0.0.1:1", "")
	err := client.downloadLFSFile(context.Background(), "models", "ns", "name", "model.safetensors", dest, 3, "", nil)
	if err == nil {
		t.Fatal("expected download error after oversized file is removed")
	}
	if info, statErr := os.Stat(dest); statErr == nil && info.Size() > 3 {
		t.Fatalf("oversized partial file was not removed, size=%d", info.Size())
	}
}

func TestSnapshotProgressTrackerAggregatesBytes(t *testing.T) {
	tracker := newSnapshotProgressTracker([]RepoFile{
		{Type: "file", Path: "config.json", Name: "config.json", Size: 10},
		{Type: "file", Path: "weights/model.safetensors", Name: "model.safetensors", Size: 30},
	})

	completed, total := tracker.update(1, 15, 30)
	if completed != 15 || total != 40 {
		t.Fatalf("after partial update completed=%d total=%d, want 15/40", completed, total)
	}

	completed, total = tracker.update(0, 10, 10)
	if completed != 25 || total != 40 {
		t.Fatalf("after second file update completed=%d total=%d, want 25/40", completed, total)
	}

	completed, total = tracker.update(1, 30, 30)
	if completed != 40 || total != 40 {
		t.Fatalf("after complete update completed=%d total=%d, want 40/40", completed, total)
	}
}

func TestParseModelID(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNS    string
		wantName  string
		wantError bool
	}{
		{
			name:     "valid model ID",
			input:    "OpenCSG/csg-wukong-1B",
			wantNS:   "OpenCSG",
			wantName: "csg-wukong-1B",
		},
		{
			name:     "valid with dots and hyphens",
			input:    "my-org/my.model-v2",
			wantNS:   "my-org",
			wantName: "my.model-v2",
		},
		{
			name:      "no slash",
			input:     "justname",
			wantError: true,
		},
		{
			name:      "empty namespace",
			input:     "/name",
			wantError: true,
		},
		{
			name:      "empty name",
			input:     "namespace/",
			wantError: true,
		},
		{
			name:      "empty string",
			input:     "",
			wantError: true,
		},
		{
			name:     "multiple slashes (takes first)",
			input:    "ns/name/extra",
			wantNS:   "ns",
			wantName: "name/extra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, name, err := ParseModelID(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf("ParseModelID(%q) = (%q, %q, nil), want error", tt.input, ns, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseModelID(%q) error: %v", tt.input, err)
			}
			if ns != tt.wantNS {
				t.Errorf("namespace = %q, want %q", ns, tt.wantNS)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

package model

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestManagerImport_Directory(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "weights"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "weights", "model.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config.json"), []byte(`{"architectures":["Qwen2VLForConditionalGeneration"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm, err := mgr.Import(ImportOptions{
		ModelID: "local/demo",
		Source:  source,
		Kind:    ImportSourceDirectory,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if lm.FullName() != "local/demo" {
		t.Fatalf("FullName = %q, want local/demo", lm.FullName())
	}
	if lm.Format != FormatGGUF {
		t.Fatalf("format = %q, want gguf", lm.Format)
	}
	if lm.Size <= 0 {
		t.Fatalf("size = %d, want > 0", lm.Size)
	}
	if lm.PipelineTag != "image-text-to-text" {
		t.Fatalf("pipeline_tag = %q, want image-text-to-text", lm.PipelineTag)
	}
	if lm.Origin != LocalModelOriginUpload {
		t.Fatalf("origin = %q, want upload", lm.Origin)
	}
	if _, err := os.Stat(filepath.Join(mgr.cfg.ModelDir, "local", "demo", "weights", "model.gguf")); err != nil {
		t.Fatalf("imported model file: %v", err)
	}
}

func TestManagerImport_ZipStripsSingleTopLevelDirectory(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "model.zip")
	writeZip(t, archivePath, map[string]string{
		"wrapped/model.safetensors": "weights",
		"wrapped/config.json":       "{}",
	})

	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm, err := mgr.Import(ImportOptions{
		ModelID: "local/zipped",
		Source:  archivePath,
		Kind:    ImportSourceArchive,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if lm.Format != FormatSafeTensors {
		t.Fatalf("format = %q, want safetensors", lm.Format)
	}
	if _, err := os.Stat(filepath.Join(mgr.cfg.ModelDir, "local", "zipped", "model.safetensors")); err != nil {
		t.Fatalf("single top-level directory was not stripped: %v", err)
	}
}

func TestManagerImport_TarGz(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "model.tar.gz")
	writeTarGz(t, archivePath, map[string]string{
		"model/pytorch_model.bin": "weights",
	})

	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm, err := mgr.Import(ImportOptions{
		ModelID: "local/tarred",
		Source:  archivePath,
		Kind:    ImportSourceArchive,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if lm.Format != FormatPyTorch {
		t.Fatalf("format = %q, want pytorch", lm.Format)
	}
}

func TestManagerImport_ModelScopeFunASRPT(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "model.pt"), []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "configuration.json"), []byte(`{
  "framework": "pytorch",
  "task": "auto-speech-recognition",
  "model": {"type": "funasr"},
  "pipeline": {"type": "funasr-pipeline"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm, err := mgr.Import(ImportOptions{
		ModelID: "local/funasr",
		Source:  source,
		Kind:    ImportSourceDirectory,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if lm.Format != FormatPyTorch {
		t.Fatalf("format = %q, want pytorch", lm.Format)
	}
	if lm.PipelineTag != "automatic-speech-recognition" {
		t.Fatalf("pipeline_tag = %q, want automatic-speech-recognition", lm.PipelineTag)
	}
}

func TestManagerImport_RejectsUnsafeArchivePath(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "bad.zip")
	writeZip(t, archivePath, map[string]string{
		"../escape.gguf": "bad",
	})

	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	if _, err := mgr.Import(ImportOptions{ModelID: "local/bad", Source: archivePath, Kind: ImportSourceArchive}); err == nil {
		t.Fatal("Import succeeded, want invalid archive path error")
	}
}

func TestManagerImport_OverwriteConflict(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "model.gguf"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelDir := t.TempDir()
	mgr := NewManager(&config.Config{ModelDir: modelDir})
	if err := SaveManifest(modelDir, &LocalModel{Namespace: "local", Name: "demo", Format: FormatGGUF}); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Import(ImportOptions{ModelID: "local/demo", Source: source, Kind: ImportSourceDirectory}); err == nil {
		t.Fatal("Import succeeded, want conflict")
	}
	if _, err := mgr.Import(ImportOptions{ModelID: "local/demo", Source: source, Kind: ImportSourceDirectory, Overwrite: true}); err != nil {
		t.Fatalf("overwrite Import: %v", err)
	}
}

func TestManagerImport_RejectsInvalidModelID(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "model.gguf"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	if _, err := mgr.Import(ImportOptions{ModelID: "../bad/model", Source: source, Kind: ImportSourceDirectory}); err == nil {
		t.Fatal("Import succeeded, want invalid model ID error")
	}
}

func writeZip(t *testing.T, archivePath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
}

func writeTarGz(t *testing.T, archivePath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()
	for name, body := range files {
		data := []byte(body)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
}

package model

import (
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
)

func TestResolveLocalModelByFullID(t *testing.T) {
	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm := &LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       FormatGGUF,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	if err := SaveManifest(mgr.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}

	got, err := mgr.ResolveLocalModel("Qwen/Qwen3-0.6B-GGUF")
	if err != nil {
		t.Fatalf("ResolveLocalModel() error = %v", err)
	}
	if got.FullName() != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("FullName() = %q, want Qwen/Qwen3-0.6B-GGUF", got.FullName())
	}
}

func TestResolveLocalModelByShortName(t *testing.T) {
	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm := &LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       FormatGGUF,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	if err := SaveManifest(mgr.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}

	got, err := mgr.ResolveLocalModel("Qwen3-0.6B-GGUF")
	if err != nil {
		t.Fatalf("ResolveLocalModel() error = %v", err)
	}
	if got.FullName() != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("FullName() = %q, want Qwen/Qwen3-0.6B-GGUF", got.FullName())
	}
}

func TestResolveLocalModelCollidingShortNamesUseStableSuffix(t *testing.T) {
	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	items := []*LocalModel{
		{
			Namespace:    "Qwen",
			Name:         "shared-name",
			Format:       FormatGGUF,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(100, 0),
		},
		{
			Namespace:    "Acme",
			Name:         "shared-name",
			Format:       FormatGGUF,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(200, 0),
		},
	}
	for _, item := range items {
		if err := SaveManifest(mgr.cfg.ModelDir, item); err != nil {
			t.Fatalf("SaveManifest() error = %v", err)
		}
	}

	first, err := mgr.ResolveLocalModel("shared-name")
	if err != nil {
		t.Fatalf("ResolveLocalModel() error = %v", err)
	}
	if first.FullName() != "Qwen/shared-name" {
		t.Fatalf("first model = %q, want Qwen/shared-name", first.FullName())
	}

	secondPublicID, err := mgr.PublicModelID("Acme/shared-name")
	if err != nil {
		t.Fatalf("PublicModelID() error = %v", err)
	}
	if secondPublicID == "shared-name" || !strings.HasPrefix(secondPublicID, "shared-name-") {
		t.Fatalf("second public ID = %q, want stable suffixed ID", secondPublicID)
	}
	second, err := mgr.ResolveLocalModel(secondPublicID)
	if err != nil {
		t.Fatalf("ResolveLocalModel(%q) error = %v", secondPublicID, err)
	}
	if second.FullName() != "Acme/shared-name" {
		t.Fatalf("second model = %q, want Acme/shared-name", second.FullName())
	}
}

func TestResolveLocalModelMissingFullIDDoesNotFallbackToShortName(t *testing.T) {
	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	lm := &LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       FormatGGUF,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	if err := SaveManifest(mgr.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}

	_, err := mgr.ResolveLocalModel("Qwen/missing-model")
	if err == nil {
		t.Fatal("ResolveLocalModel() returned nil error, want not found")
	}
	if !strings.Contains(err.Error(), `model "Qwen/missing-model" not found locally`) {
		t.Fatalf("error = %q, want not found for explicit full ID", err.Error())
	}
}

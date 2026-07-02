package convert

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDTypeAcceptsAllowedValues(t *testing.T) {
	got, err := NormalizeDType("Q8_0")
	if err != nil {
		t.Fatalf("NormalizeDType returned error: %v", err)
	}
	if got != "q8_0" {
		t.Fatalf("NormalizeDType = %q, want q8_0", got)
	}
}

func TestNormalizeDTypeRejectsUnknownValues(t *testing.T) {
	if _, err := NormalizeDType("q4_k_m"); err == nil {
		t.Fatal("expected invalid dtype error")
	}
}

func TestNormalizeRuntimeDTypeAcceptsGGUFQuant(t *testing.T) {
	got, err := NormalizeRuntimeDType("Q4_K_M")
	if err != nil {
		t.Fatalf("NormalizeRuntimeDType returned error: %v", err)
	}
	if got != "q4_k_m" {
		t.Fatalf("NormalizeRuntimeDType = %q, want q4_k_m", got)
	}
}

func TestResolveDTypeUsesDefaultWhenUnset(t *testing.T) {
	got, err := ResolveDType("")
	if err != nil {
		t.Fatalf("ResolveDType returned error: %v", err)
	}
	if got != "f16" {
		t.Fatalf("ResolveDType = %q, want f16", got)
	}
}

func TestGenerateOutputNameUsesRequestedDType(t *testing.T) {
	if got := generateOutputName("/tmp/model", "q8_0"); got != "model-q8_0.gguf" {
		t.Fatalf("generateOutputName = %q, want model-q8_0.gguf", got)
	}
	if got := generateOutputName("/tmp/model", "auto"); got != "model-{ftype}.gguf" {
		t.Fatalf("generateOutputName = %q, want model-{ftype}.gguf", got)
	}
}

func TestFindGGUFForDTypeMatchesRequestedQuant(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model-f16.gguf"), []byte("f16"), 0o644); err != nil {
		t.Fatal(err)
	}
	q4Dir := filepath.Join(dir, "Q4_K_M")
	if err := os.MkdirAll(q4Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q4Dir, "model.gguf"), []byte("q4"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok, err := FindGGUFForDType(dir, "q4_k_m")
	if err != nil {
		t.Fatalf("FindGGUFForDType returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected q4_k_m GGUF match")
	}
	if filepath.ToSlash(got) != filepath.ToSlash(filepath.Join(dir, "Q4_K_M", "model.gguf")) {
		t.Fatalf("FindGGUFForDType = %q, want Q4_K_M/model.gguf", got)
	}
}

func TestNeedsConversionForDTypeChecksRequestedGGUF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("safetensors"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model-f16.gguf"), []byte("f16"), 0o644); err != nil {
		t.Fatal(err)
	}

	needs, err := NeedsConversionForDType(dir, "")
	if err != nil {
		t.Fatalf("NeedsConversionForDType returned error: %v", err)
	}
	if needs {
		t.Fatal("unset dtype should accept existing GGUF")
	}

	needs, err = NeedsConversionForDType(dir, "q8_0")
	if err != nil {
		t.Fatalf("NeedsConversionForDType returned error: %v", err)
	}
	if !needs {
		t.Fatal("q8_0 should need conversion when only f16 GGUF exists")
	}

	if err := os.WriteFile(filepath.Join(dir, "model-q8_0.gguf"), []byte("q8"), 0o644); err != nil {
		t.Fatal(err)
	}
	needs, err = NeedsConversionForDType(dir, "q8_0")
	if err != nil {
		t.Fatalf("NeedsConversionForDType returned error: %v", err)
	}
	if needs {
		t.Fatal("q8_0 should not need conversion when matching GGUF exists")
	}
}

func TestNeedsConversionForDTypeRejectsRuntimeOnlyQuantForConversion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("safetensors"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NeedsConversionForDType(dir, "q4_k_m"); err == nil {
		t.Fatal("expected conversion dtype error for runtime-only GGUF quant")
	}
}

func TestFindMMProjForDTypeMatchesRequestedQuant(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mmproj-model-f16.gguf"), []byte("f16"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mmproj-model-q8_0.gguf"), []byte("q8"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok, err := FindMMProjForDType(dir, "q8_0")
	if err != nil {
		t.Fatalf("FindMMProjForDType returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected q8_0 mmproj match")
	}
	if filepath.Base(got) != "mmproj-model-q8_0.gguf" {
		t.Fatalf("FindMMProjForDType = %q, want mmproj-model-q8_0.gguf", got)
	}
}

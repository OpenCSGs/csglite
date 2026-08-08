package convert

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBundledConverterPyPresent(t *testing.T) {
	if len(bundledConverterPy) < 10_000 {
		t.Fatalf("embedded convert_hf_to_gguf.py looks missing or truncated (got %d bytes)", len(bundledConverterPy))
	}
}

func TestBundledConverterIncludesMiniMaxPatchedTokenizerHash(t *testing.T) {
	const patchedHash = "a77756c3cc91392f442c5b99e414be8020d53ae31460de90754b4fcf5cc84a2d"
	if !strings.Contains(string(bundledConverterPy), patchedHash) {
		t.Fatalf("bundled converter is missing patched MiniMax tokenizer hash %q", patchedHash)
	}
}

func TestBundledGGUFPyPresent(t *testing.T) {
	const initPath = bundledGGUFPyRoot + "/gguf/__init__.py"
	if _, err := fs.Stat(bundledGGUFPy, initPath); err != nil {
		t.Fatalf("embedded gguf-py package is missing %s: %v", initPath, err)
	}
}

func TestMaterializeBundledGGUFPy(t *testing.T) {
	dst := t.TempDir()
	if err := materializeBundledGGUFPy(dst); err != nil {
		t.Fatalf("materializeBundledGGUFPy() error = %v", err)
	}
	initPath := filepath.Join(dst, "gguf", "__init__.py")
	if data, err := os.ReadFile(initPath); err != nil || len(data) == 0 {
		t.Fatalf("materialized gguf-py package is invalid: path=%s bytes=%d err=%v", initPath, len(data), err)
	}
}

func TestBundledGGUFPyInitializationLockSerializesWriters(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "gguf-py")
	unlock, err := acquireBundledGGUFPyLock(dst)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer unlock()

	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		unlock()
		close(released)
	}()

	start := time.Now()
	secondUnlock, err := acquireBundledGGUFPyLock(dst)
	if err != nil {
		t.Fatalf("acquire second lock: %v", err)
	}
	defer secondUnlock()
	<-released
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("second lock did not wait for first lock: %s", elapsed)
	}
}

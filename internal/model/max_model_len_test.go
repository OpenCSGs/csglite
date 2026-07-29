package model

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestMaxModelLenSafeTensorsReadsConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"max_position_embeddings":32768}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := MaxModelLen(dir, FormatSafeTensors)
	if err != nil {
		t.Fatalf("MaxModelLen() error = %v", err)
	}
	if got != 32768 {
		t.Fatalf("MaxModelLen() = %d, want 32768", got)
	}
}

func TestMaxModelLenSafeTensorsReadsQwen35TextConfig(t *testing.T) {
	dir := t.TempDir()
	config := `{
		"model_type": "qwen3_5",
		"text_config": {
			"model_type": "qwen3_5_text",
			"max_position_embeddings": 262144
		},
		"vision_config": {
			"num_position_embeddings": 2304
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := MaxModelLen(dir, FormatSafeTensors)
	if err != nil {
		t.Fatalf("MaxModelLen() error = %v", err)
	}
	if got != 262144 {
		t.Fatalf("MaxModelLen() = %d, want 262144", got)
	}
}

func TestMaxModelLenGGUFReadsArchitectureContextLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	writeTestGGUF(t, path, []testGGUFMetadata{
		{key: "general.architecture", valueType: ggufMetadataString, value: "qwen3"},
		{key: "tokenizer.ggml.tokens", valueType: ggufMetadataArray, value: []string{"a", "b"}},
		{key: "clip.context_length", valueType: ggufMetadataUint32, value: uint32(1024)},
		{key: "qwen3.context_length", valueType: ggufMetadataUint32, value: uint32(40960)},
	})

	got, err := MaxModelLen(dir, FormatGGUF)
	if err != nil {
		t.Fatalf("MaxModelLen() error = %v", err)
	}
	if got != 40960 {
		t.Fatalf("MaxModelLen() = %d, want 40960", got)
	}
}

func TestMaxModelLenGGUFRejectsInvalidHeader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("not-gguf"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MaxModelLen(dir, FormatGGUF); err == nil {
		t.Fatal("MaxModelLen() error = nil, want invalid-header error")
	}
}

type testGGUFMetadata struct {
	key       string
	valueType uint32
	value     any
}

func writeTestGGUF(t *testing.T, path string, metadata []testGGUFMetadata) {
	t.Helper()
	var buf bytes.Buffer
	write := func(value any) {
		t.Helper()
		if err := binary.Write(&buf, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	writeString := func(value string) {
		write(uint64(len(value)))
		if _, err := buf.WriteString(value); err != nil {
			t.Fatal(err)
		}
	}

	write(uint32(ggufHeaderMagic))
	write(uint32(3))
	write(uint64(0))
	write(uint64(len(metadata)))
	for _, item := range metadata {
		writeString(item.key)
		write(item.valueType)
		switch item.valueType {
		case ggufMetadataString:
			writeString(item.value.(string))
		case ggufMetadataUint32:
			write(item.value.(uint32))
		case ggufMetadataArray:
			values := item.value.([]string)
			write(uint32(ggufMetadataString))
			write(uint64(len(values)))
			for _, value := range values {
				writeString(value)
			}
		default:
			t.Fatalf("unsupported test metadata type %d", item.valueType)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestReadScalarMetadata(t *testing.T) {
	data := buildTestGGUF(t, []testMetadata{
		{key: "general.architecture", valueType: typeString, value: "clip"},
		{key: "clip.has_vision_encoder", valueType: typeBool, value: true},
		{key: "qwen3_5.context_length", valueType: typeUint32, value: uint32(262144)},
		{key: "tokenizer.ggml.tokens", valueType: typeArray, value: []string{"a", "b"}},
	})

	metadata, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if architecture, ok := metadata.String("general.architecture"); !ok || architecture != "clip" {
		t.Fatalf("architecture = %q, %t", architecture, ok)
	}
	if !metadata.IsVisionProjector() {
		t.Fatal("IsVisionProjector = false, want true")
	}
	if contextLength, ok := metadata.PositiveIntWithSuffix(".context_length"); !ok || contextLength != 262144 {
		t.Fatalf("context length = %d, %t", contextLength, ok)
	}
}

func TestReadRejectsInvalidAndOversizedMetadata(t *testing.T) {
	if _, err := Read(strings.NewReader("not-a-gguf")); err == nil {
		t.Fatal("Read invalid magic succeeded")
	}

	var data bytes.Buffer
	data.WriteString("GGUF")
	writeTestValue(t, &data, uint32(3))
	writeTestValue(t, &data, uint64(0))
	writeTestValue(t, &data, uint64(maxMetadataCount+1))
	if _, err := Read(bytes.NewReader(data.Bytes())); err == nil {
		t.Fatal("Read oversized metadata count succeeded")
	}
}

type testMetadata struct {
	key       string
	valueType uint32
	value     any
}

func buildTestGGUF(t *testing.T, entries []testMetadata) []byte {
	t.Helper()
	var data bytes.Buffer
	data.WriteString("GGUF")
	writeTestValue(t, &data, uint32(3))
	writeTestValue(t, &data, uint64(0))
	writeTestValue(t, &data, uint64(len(entries)))
	for _, entry := range entries {
		writeTestString(t, &data, entry.key)
		writeTestValue(t, &data, entry.valueType)
		switch entry.valueType {
		case typeString:
			writeTestString(t, &data, entry.value.(string))
		case typeBool:
			if entry.value.(bool) {
				writeTestValue(t, &data, uint8(1))
			} else {
				writeTestValue(t, &data, uint8(0))
			}
		case typeUint32:
			writeTestValue(t, &data, entry.value.(uint32))
		case typeArray:
			values := entry.value.([]string)
			writeTestValue(t, &data, uint32(typeString))
			writeTestValue(t, &data, uint64(len(values)))
			for _, value := range values {
				writeTestString(t, &data, value)
			}
		default:
			t.Fatalf("unsupported test metadata type %d", entry.valueType)
		}
	}
	return data.Bytes()
}

func writeTestString(t *testing.T, data *bytes.Buffer, value string) {
	t.Helper()
	writeTestValue(t, data, uint64(len(value)))
	data.WriteString(value)
}

func writeTestValue(t *testing.T, data *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(data, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}

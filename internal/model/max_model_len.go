package model

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	ggufHeaderMagic       = 0x46554747
	maxGGUFMetadataCount  = 1_000_000
	maxGGUFMetadataString = 1 << 20
	maxGGUFStringArray    = 10_000_000
)

const (
	ggufMetadataUint8 uint32 = iota
	ggufMetadataInt8
	ggufMetadataUint16
	ggufMetadataInt16
	ggufMetadataUint32
	ggufMetadataInt32
	ggufMetadataFloat32
	ggufMetadataBool
	ggufMetadataString
	ggufMetadataArray
	ggufMetadataUint64
	ggufMetadataInt64
	ggufMetadataFloat64
)

// MaxModelLen returns the model's native maximum context length. GGUF models
// carry this value in their metadata header; SafeTensors models use the
// Hugging Face config.json stored alongside the weights.
func MaxModelLen(modelDir string, format Format) (int64, error) {
	switch format {
	case FormatGGUF:
		modelPath, detectedFormat, err := FindModelFile(modelDir)
		if err != nil {
			return 0, err
		}
		if detectedFormat != FormatGGUF {
			return 0, fmt.Errorf("GGUF model file not found in %s", modelDir)
		}
		return maxModelLenFromGGUF(modelPath)
	case FormatSafeTensors:
		return maxModelLenFromConfig(filepath.Join(modelDir, "config.json"))
	default:
		return 0, nil
	}
}

func maxModelLenFromConfig(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var cfg struct {
		MaxPositionEmbeddings int64 `json:"max_position_embeddings"`
		TextConfig            struct {
			MaxPositionEmbeddings int64 `json:"max_position_embeddings"`
		} `json:"text_config"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("parsing %s: %w", path, err)
	}
	if cfg.MaxPositionEmbeddings > 0 {
		return cfg.MaxPositionEmbeddings, nil
	}
	if cfg.TextConfig.MaxPositionEmbeddings > 0 {
		return cfg.TextConfig.MaxPositionEmbeddings, nil
	}
	return 0, nil
}

func maxModelLenFromGGUF(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	r := &ggufMetadataReader{file: f, size: info.Size()}

	magic, err := r.u32()
	if err != nil {
		return 0, fmt.Errorf("reading GGUF header %s: %w", path, err)
	}
	if magic != ggufHeaderMagic {
		return 0, fmt.Errorf("%s is not a GGUF file", path)
	}
	version, err := r.u32()
	if err != nil {
		return 0, fmt.Errorf("reading GGUF version %s: %w", path, err)
	}
	if version < 2 || version > 3 {
		return 0, fmt.Errorf("unsupported GGUF version %d in %s", version, path)
	}
	if _, err := r.u64(); err != nil { // tensor count
		return 0, fmt.Errorf("reading GGUF tensor count %s: %w", path, err)
	}
	metadataCount, err := r.u64()
	if err != nil {
		return 0, fmt.Errorf("reading GGUF metadata count %s: %w", path, err)
	}
	if metadataCount > maxGGUFMetadataCount {
		return 0, fmt.Errorf("GGUF metadata count %d is too large", metadataCount)
	}

	architecture := ""
	contextLengths := make(map[string]int64)
	for range metadataCount {
		key, err := r.string(maxGGUFMetadataString)
		if err != nil {
			return 0, fmt.Errorf("reading GGUF metadata key: %w", err)
		}
		valueType, err := r.u32()
		if err != nil {
			return 0, fmt.Errorf("reading GGUF metadata type for %q: %w", key, err)
		}

		switch {
		case key == "general.architecture" && valueType == ggufMetadataString:
			architecture, err = r.string(maxGGUFMetadataString)
		case strings.HasSuffix(key, ".context_length"):
			var value int64
			value, err = r.positiveInteger(valueType)
			if value > 0 {
				contextLengths[key] = value
			}
		default:
			err = r.skipValue(valueType)
		}
		if err != nil {
			return 0, fmt.Errorf("reading GGUF metadata %q: %w", key, err)
		}
	}

	if architecture != "" {
		if value := contextLengths[architecture+".context_length"]; value > 0 {
			return value, nil
		}
	}
	var maxLen int64
	for _, value := range contextLengths {
		if value > maxLen {
			maxLen = value
		}
	}
	return maxLen, nil
}

type ggufMetadataReader struct {
	file *os.File
	size int64
	pos  int64
}

func (r *ggufMetadataReader) read(p []byte) error {
	if int64(len(p)) > r.size-r.pos {
		return io.ErrUnexpectedEOF
	}
	if _, err := io.ReadFull(r.file, p); err != nil {
		return err
	}
	r.pos += int64(len(p))
	return nil
}

func (r *ggufMetadataReader) skip(n uint64) error {
	if n > math.MaxInt64 || int64(n) > r.size-r.pos {
		return io.ErrUnexpectedEOF
	}
	if _, err := r.file.Seek(int64(n), io.SeekCurrent); err != nil {
		return err
	}
	r.pos += int64(n)
	return nil
}

func (r *ggufMetadataReader) u32() (uint32, error) {
	var buf [4]byte
	if err := r.read(buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

func (r *ggufMetadataReader) u64() (uint64, error) {
	var buf [8]byte
	if err := r.read(buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

func (r *ggufMetadataReader) string(maxLen uint64) (string, error) {
	length, err := r.u64()
	if err != nil {
		return "", err
	}
	if length > maxLen {
		return "", fmt.Errorf("GGUF string length %d exceeds limit %d", length, maxLen)
	}
	data := make([]byte, int(length))
	if err := r.read(data); err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *ggufMetadataReader) positiveInteger(valueType uint32) (int64, error) {
	var buf [8]byte
	switch valueType {
	case ggufMetadataUint8:
		if err := r.read(buf[:1]); err != nil {
			return 0, err
		}
		return int64(buf[0]), nil
	case ggufMetadataInt8:
		if err := r.read(buf[:1]); err != nil {
			return 0, err
		}
		return max(int64(int8(buf[0])), 0), nil
	case ggufMetadataUint16, ggufMetadataInt16:
		if err := r.read(buf[:2]); err != nil {
			return 0, err
		}
		value := binary.LittleEndian.Uint16(buf[:2])
		if valueType == ggufMetadataInt16 {
			return max(int64(int16(value)), 0), nil
		}
		return int64(value), nil
	case ggufMetadataUint32, ggufMetadataInt32:
		if err := r.read(buf[:4]); err != nil {
			return 0, err
		}
		value := binary.LittleEndian.Uint32(buf[:4])
		if valueType == ggufMetadataInt32 {
			return max(int64(int32(value)), 0), nil
		}
		return int64(value), nil
	case ggufMetadataUint64, ggufMetadataInt64:
		if err := r.read(buf[:8]); err != nil {
			return 0, err
		}
		value := binary.LittleEndian.Uint64(buf[:8])
		if valueType == ggufMetadataInt64 {
			return max(int64(value), 0), nil
		}
		if value > math.MaxInt64 {
			return 0, nil
		}
		return int64(value), nil
	default:
		if err := r.skipValue(valueType); err != nil {
			return 0, err
		}
		return 0, nil
	}
}

func (r *ggufMetadataReader) skipValue(valueType uint32) error {
	if size, ok := ggufFixedMetadataSize(valueType); ok {
		return r.skip(size)
	}
	switch valueType {
	case ggufMetadataString:
		length, err := r.u64()
		if err != nil {
			return err
		}
		return r.skip(length)
	case ggufMetadataArray:
		elementType, err := r.u32()
		if err != nil {
			return err
		}
		count, err := r.u64()
		if err != nil {
			return err
		}
		if size, ok := ggufFixedMetadataSize(elementType); ok {
			if count > math.MaxUint64/size {
				return io.ErrUnexpectedEOF
			}
			return r.skip(count * size)
		}
		if elementType != ggufMetadataString {
			return fmt.Errorf("unsupported GGUF array element type %d", elementType)
		}
		if count > maxGGUFStringArray {
			return fmt.Errorf("GGUF string array count %d is too large", count)
		}
		for range count {
			length, err := r.u64()
			if err != nil {
				return err
			}
			if err := r.skip(length); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported GGUF metadata type %d", valueType)
	}
}

func ggufFixedMetadataSize(valueType uint32) (uint64, bool) {
	switch valueType {
	case ggufMetadataUint8, ggufMetadataInt8, ggufMetadataBool:
		return 1, true
	case ggufMetadataUint16, ggufMetadataInt16:
		return 2, true
	case ggufMetadataUint32, ggufMetadataInt32, ggufMetadataFloat32:
		return 4, true
	case ggufMetadataUint64, ggufMetadataInt64, ggufMetadataFloat64:
		return 8, true
	default:
		return 0, false
	}
}

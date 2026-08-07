package ggufmeta

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	maxHeaderBytes   = 64 << 20
	maxMetadataCount = 1 << 16
	maxStringBytes   = 16 << 20
	maxArrayElements = 1 << 24
)

const (
	typeUint8   = 0
	typeInt8    = 1
	typeUint16  = 2
	typeInt16   = 3
	typeUint32  = 4
	typeInt32   = 5
	typeFloat32 = 6
	typeBool    = 7
	typeString  = 8
	typeArray   = 9
	typeUint64  = 10
	typeInt64   = 11
	typeFloat64 = 12
)

type value struct {
	stringValue string
	intValue    int64
	uintValue   uint64
	kind        uint32
}

// Metadata contains scalar key/value pairs from a GGUF header.
type Metadata struct {
	values map[string]value
}

// ReadFile reads bounded scalar metadata from a GGUF file without reading tensors.
func ReadFile(path string) (*Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Read(file)
}

// Read reads bounded scalar metadata from a GGUF stream without reading tensors.
func Read(reader io.Reader) (*Metadata, error) {
	limited := &io.LimitedReader{R: reader, N: maxHeaderBytes}
	metadataCount, err := readMetadataCount(limited)
	if err != nil {
		return nil, err
	}

	metadata := &Metadata{values: make(map[string]value, metadataCount)}
	for i := uint64(0); i < metadataCount; i++ {
		key, err := readString(limited)
		if err != nil {
			return nil, fmt.Errorf("reading GGUF metadata key %d: %w", i, err)
		}
		var valueType uint32
		if err := binary.Read(limited, binary.LittleEndian, &valueType); err != nil {
			return nil, fmt.Errorf("reading GGUF metadata type for %q: %w", key, err)
		}
		item, scalar, err := readValue(limited, valueType)
		if err != nil {
			return nil, fmt.Errorf("reading GGUF metadata value for %q: %w", key, err)
		}
		if scalar {
			metadata.values[key] = item
		}
	}
	return metadata, nil
}

// IsVisionProjectorFile checks projector metadata and stops as soon as the
// GGUF architecture provides a definitive answer.
func IsVisionProjectorFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maxHeaderBytes}
	metadataCount, err := readMetadataCount(limited)
	if err != nil {
		return false, err
	}
	for i := uint64(0); i < metadataCount; i++ {
		key, err := readString(limited)
		if err != nil {
			return false, err
		}
		var valueType uint32
		if err := binary.Read(limited, binary.LittleEndian, &valueType); err != nil {
			return false, err
		}
		switch key {
		case "general.architecture":
			item, scalar, err := readValue(limited, valueType)
			if err != nil {
				return false, err
			}
			if scalar && item.kind == typeString {
				return strings.EqualFold(strings.TrimSpace(item.stringValue), "clip"), nil
			}
		case "clip.has_vision_encoder", "clip.vision.has_vision_encoder":
			item, scalar, err := readValue(limited, valueType)
			if err != nil {
				return false, err
			}
			if scalar && item.kind == typeBool && item.uintValue != 0 {
				return true, nil
			}
		case "clip.projector_type", "clip.vision.projector_type":
			item, scalar, err := readValue(limited, valueType)
			if err != nil {
				return false, err
			}
			if scalar && item.kind == typeString && strings.TrimSpace(item.stringValue) != "" {
				return true, nil
			}
		default:
			if err := skipValue(limited, valueType); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

func readMetadataCount(reader io.Reader) (uint64, error) {
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return 0, err
	}
	if string(magic[:]) != "GGUF" {
		return 0, fmt.Errorf("invalid GGUF magic")
	}
	var version uint32
	var tensorCount uint64
	var metadataCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &version); err != nil {
		return 0, err
	}
	if version == 0 {
		return 0, fmt.Errorf("unsupported GGUF version %d", version)
	}
	if err := binary.Read(reader, binary.LittleEndian, &tensorCount); err != nil {
		return 0, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &metadataCount); err != nil {
		return 0, err
	}
	if metadataCount > maxMetadataCount {
		return 0, fmt.Errorf("GGUF metadata count too large: %d", metadataCount)
	}
	return metadataCount, nil
}

func (m *Metadata) String(key string) (string, bool) {
	if m == nil {
		return "", false
	}
	item, ok := m.values[key]
	return item.stringValue, ok && item.kind == typeString
}

func (m *Metadata) Bool(key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	item, ok := m.values[key]
	return item.uintValue != 0, ok && item.kind == typeBool
}

func (m *Metadata) PositiveInt(key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	item, ok := m.values[key]
	if !ok {
		return 0, false
	}
	var number uint64
	switch item.kind {
	case typeUint8, typeUint16, typeUint32, typeUint64:
		number = item.uintValue
	case typeInt8, typeInt16, typeInt32, typeInt64:
		if item.intValue <= 0 {
			return 0, false
		}
		number = uint64(item.intValue)
	default:
		return 0, false
	}
	maxInt := uint64(^uint(0) >> 1)
	if number == 0 || number > maxInt {
		return 0, false
	}
	return int(number), true
}

func (m *Metadata) PositiveIntWithSuffix(suffix string) (int, bool) {
	if m == nil {
		return 0, false
	}
	for key := range m.values {
		if strings.HasSuffix(key, suffix) {
			if value, ok := m.PositiveInt(key); ok {
				return value, true
			}
		}
	}
	return 0, false
}

// IsVisionProjector reports whether GGUF metadata describes a vision projector.
func (m *Metadata) IsVisionProjector() bool {
	for _, key := range []string{"clip.has_vision_encoder", "clip.vision.has_vision_encoder"} {
		if enabled, ok := m.Bool(key); ok && enabled {
			return true
		}
	}
	for _, key := range []string{"clip.projector_type", "clip.vision.projector_type"} {
		if projectorType, ok := m.String(key); ok && strings.TrimSpace(projectorType) != "" {
			return true
		}
	}
	if architecture, ok := m.String("general.architecture"); ok && strings.EqualFold(strings.TrimSpace(architecture), "clip") {
		return true
	}
	return false
}

func readValue(reader io.Reader, valueType uint32) (value, bool, error) {
	var item value
	item.kind = valueType
	switch valueType {
	case typeUint8, typeBool:
		var number uint8
		err := binary.Read(reader, binary.LittleEndian, &number)
		item.uintValue = uint64(number)
		return item, true, err
	case typeInt8:
		var number int8
		err := binary.Read(reader, binary.LittleEndian, &number)
		item.intValue = int64(number)
		return item, true, err
	case typeUint16:
		var number uint16
		err := binary.Read(reader, binary.LittleEndian, &number)
		item.uintValue = uint64(number)
		return item, true, err
	case typeInt16:
		var number int16
		err := binary.Read(reader, binary.LittleEndian, &number)
		item.intValue = int64(number)
		return item, true, err
	case typeUint32:
		var number uint32
		err := binary.Read(reader, binary.LittleEndian, &number)
		item.uintValue = uint64(number)
		return item, true, err
	case typeInt32:
		var number int32
		err := binary.Read(reader, binary.LittleEndian, &number)
		item.intValue = int64(number)
		return item, true, err
	case typeUint64:
		err := binary.Read(reader, binary.LittleEndian, &item.uintValue)
		return item, true, err
	case typeInt64:
		err := binary.Read(reader, binary.LittleEndian, &item.intValue)
		return item, true, err
	case typeString:
		text, err := readString(reader)
		item.stringValue = text
		return item, true, err
	case typeFloat32:
		return item, false, discard(reader, 4)
	case typeFloat64:
		return item, false, discard(reader, 8)
	case typeArray:
		var elementType uint32
		var count uint64
		if err := binary.Read(reader, binary.LittleEndian, &elementType); err != nil {
			return item, false, err
		}
		if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
			return item, false, err
		}
		if count > maxArrayElements {
			return item, false, fmt.Errorf("GGUF metadata array too large: %d", count)
		}
		for i := uint64(0); i < count; i++ {
			if err := skipValue(reader, elementType); err != nil {
				return item, false, err
			}
		}
		return item, false, nil
	default:
		return item, false, fmt.Errorf("unsupported GGUF metadata type %d", valueType)
	}
}

func skipValue(reader io.Reader, valueType uint32) error {
	switch valueType {
	case typeUint8, typeInt8, typeBool:
		return discard(reader, 1)
	case typeUint16, typeInt16:
		return discard(reader, 2)
	case typeUint32, typeInt32, typeFloat32:
		return discard(reader, 4)
	case typeUint64, typeInt64, typeFloat64:
		return discard(reader, 8)
	case typeString:
		var length uint64
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			return err
		}
		if length > maxStringBytes {
			return fmt.Errorf("GGUF string too large: %d", length)
		}
		return discard(reader, int64(length))
	case typeArray:
		var elementType uint32
		var count uint64
		if err := binary.Read(reader, binary.LittleEndian, &elementType); err != nil {
			return err
		}
		if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
			return err
		}
		if count > maxArrayElements {
			return fmt.Errorf("GGUF metadata array too large: %d", count)
		}
		for i := uint64(0); i < count; i++ {
			if err := skipValue(reader, elementType); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported GGUF metadata type %d", valueType)
	}
}

func readString(reader io.Reader) (string, error) {
	var length uint64
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	if length > maxStringBytes {
		return "", fmt.Errorf("GGUF string too large: %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func discard(reader io.Reader, count int64) error {
	_, err := io.CopyN(io.Discard, reader, count)
	return err
}

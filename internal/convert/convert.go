package convert

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/ggufpick"
	"github.com/opencsgs/csglite/internal/model"
)

// ProgressFunc reports conversion progress.
type ProgressFunc func(step string, current, total int)

const defaultConvertDType = "f16"

var allowedConvertDTypes = []string{"f32", "f16", "bf16", "q8_0", "tq1_0", "tq2_0", "auto"}

var allowedConvertDTypeSet = func() map[string]struct{} {
	allowed := make(map[string]struct{}, len(allowedConvertDTypes))
	for _, v := range allowedConvertDTypes {
		allowed[v] = struct{}{}
	}
	return allowed
}()

// Convert converts HuggingFace model files in modelDir to a GGUF file.
// It runs the llama.cpp convert_hf_to_gguf.py bundled in the binary (or from
// CSGHUB_LITE_CONVERTER_URL when set); see convert_python.go.
func Convert(modelDir string, progress ProgressFunc, dtype string) (string, error) {
	if progress == nil {
		progress = func(string, int, int) {}
	}

	return ConvertPython(modelDir, progress, dtype)
}

// AllowedDTypes returns the converter output dtypes accepted by csghub-lite.
func AllowedDTypes() []string {
	return append([]string(nil), allowedConvertDTypes...)
}

// NormalizeDType returns a lower-case converter dtype or "" when unset.
func NormalizeDType(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", nil
	}
	if _, ok := allowedConvertDTypeSet[normalized]; ok {
		return normalized, nil
	}
	return "", fmt.Errorf("unsupported dtype %q (allowed: %s)", value, strings.Join(allowedConvertDTypes, ", "))
}

// ResolveDType returns the effective converter dtype, falling back to the
// project default when the flag is unset.
func ResolveDType(value string) (string, error) {
	normalized, err := NormalizeDType(value)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return defaultConvertDType, nil
	}
	return normalized, nil
}

func resolveDType(value string) (string, error) {
	return ResolveDType(value)
}

// HasGGUF checks if a GGUF file already exists in the model directory.
func HasGGUF(modelDir string) (string, bool) {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		if !e.IsDir() && strings.HasSuffix(lower, ".gguf") && !strings.Contains(lower, "mmproj") {
			return filepath.Join(modelDir, e.Name()), true
		}
	}
	return "", false
}

// HasSafeTensors reports whether modelDir contains SafeTensors files.
func HasSafeTensors(modelDir string) bool {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".safetensors") {
			return true
		}
	}
	return false
}

// HasPyTorchWeights reports whether modelDir contains legacy PyTorch .bin
// weights that llama.cpp's HuggingFace converter can read.
func HasPyTorchWeights(modelDir string) bool {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".bin") {
			return true
		}
	}
	return false
}

func HasConvertibleHFWeights(modelDir string) bool {
	return HasSafeTensors(modelDir) || HasPyTorchWeights(modelDir)
}

func mmprojGGUFNames(modelDir string) ([]string, error) {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		if !e.IsDir() && strings.Contains(lower, "mmproj") && strings.HasSuffix(lower, ".gguf") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// NeedsConversion checks if the model directory contains HuggingFace weights
// but no GGUF files.
func NeedsConversion(modelDir string) bool {
	_, hasGGUF := HasGGUF(modelDir)
	if hasGGUF {
		return false
	}

	return HasConvertibleHFWeights(modelDir)
}

// NeedsConversionForDType reports whether SafeTensors conversion is needed for
// the requested output dtype. When dtype is unset, it preserves the legacy
// behavior of accepting any existing GGUF.
func NeedsConversionForDType(modelDir, dtype string) (bool, error) {
	normalized, err := NormalizeDType(dtype)
	if err != nil {
		return false, err
	}
	if normalized == "" {
		return NeedsConversion(modelDir), nil
	}
	if _, ok, err := FindGGUFForDType(modelDir, normalized); err != nil {
		return false, err
	} else if ok {
		return false, nil
	}
	return HasConvertibleHFWeights(modelDir), nil
}

func reverseShape(shape []int64) []uint64 {
	n := len(shape)
	dims := make([]uint64, n)
	for i, s := range shape {
		dims[n-1-i] = uint64(s)
	}
	return dims
}

func generateOutputName(modelDir string, dtype string) string {
	base := filepath.Base(modelDir)
	if base == "." || base == "" {
		base = "model"
	}
	switch strings.ToLower(strings.TrimSpace(dtype)) {
	case "", defaultConvertDType:
		return base + "-f16.gguf"
	case "auto":
		return base + "-{ftype}.gguf"
	default:
		return base + "-" + strings.ToLower(strings.TrimSpace(dtype)) + ".gguf"
	}
}

// FindGGUFForDType returns an existing GGUF path that matches the requested dtype.
// When dtype is empty or auto, it returns the highest precision GGUF chosen by model.FindModelFile.
func FindGGUFForDType(modelDir, dtype string) (string, bool, error) {
	normalized, err := NormalizeDType(dtype)
	if err != nil {
		return "", false, err
	}
	if normalized == "" || normalized == "auto" {
		path, format, err := model.FindModelFile(modelDir)
		if err != nil || format != model.FormatGGUF {
			return "", false, nil
		}
		return path, true, nil
	}

	relPaths, err := ggufpick.CollectWeightGGUFRelPaths(modelDir)
	if err != nil {
		return "", false, err
	}
	for _, relPath := range relPaths {
		if strings.EqualFold(ggufpick.QuantLabelFromRepoPath(relPath), normalized) {
			return filepath.Join(modelDir, relPath), true, nil
		}
	}
	return "", false, nil
}

// FindMMProjForDType returns an existing mmproj GGUF path that matches the requested dtype.
// When dtype is empty or auto, it returns the highest precision mmproj GGUF if present.
func FindMMProjForDType(modelDir, dtype string) (string, bool, error) {
	normalized, err := NormalizeDType(dtype)
	if err != nil {
		return "", false, err
	}
	names, err := mmprojGGUFNames(modelDir)
	if err != nil {
		return "", false, err
	}
	if len(names) == 0 {
		return "", false, nil
	}
	if normalized == "" || normalized == "auto" {
		bestName := ""
		bestRank := -2
		for _, name := range names {
			rank := ggufpick.QuantRank(name)
			if bestName == "" || rank > bestRank {
				bestName = name
				bestRank = rank
			}
		}
		return filepath.Join(modelDir, bestName), true, nil
	}
	for _, name := range names {
		if strings.EqualFold(ggufpick.QuantLabel(name), normalized) {
			return filepath.Join(modelDir, name), true, nil
		}
	}
	return "", false, nil
}

func findKVIndex(kvs []ggufKV, key string) int {
	for i, kv := range kvs {
		if kv.key == key {
			return i
		}
	}
	return -1
}

// transformFloats applies fn to each float value in data (F16 or F32 encoded).
func transformFloats(data []byte, dtype GGMLType, fn func(float32) float32) []byte {
	switch dtype {
	case GGMLTypeF32:
		out := make([]byte, len(data))
		for i := 0; i+3 < len(data); i += 4 {
			v := math.Float32frombits(binary.LittleEndian.Uint32(data[i:]))
			binary.LittleEndian.PutUint32(out[i:], math.Float32bits(fn(v)))
		}
		return out
	case GGMLTypeF16:
		out := make([]byte, len(data))
		for i := 0; i+1 < len(data); i += 2 {
			f16 := binary.LittleEndian.Uint16(data[i:])
			v := float16ToFloat32(f16)
			binary.LittleEndian.PutUint16(out[i:], float32ToFloat16(fn(v)))
		}
		return out
	}
	return data
}

func float16ToFloat32(h uint16) float32 {
	sign := uint32(h>>15) & 1
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h) & 0x3ff
	if exp == 0 {
		if mant == 0 {
			return math.Float32frombits(sign << 31)
		}
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		exp++
		mant &= 0x3ff
	} else if exp == 31 {
		return math.Float32frombits(sign<<31 | 0x7f800000 | mant<<13)
	}
	return math.Float32frombits(sign<<31 | (exp+112)<<23 | mant<<13)
}

func float32ToFloat16(f float32) uint16 {
	bits := math.Float32bits(f)
	return float32BitsToFloat16(bits)
}

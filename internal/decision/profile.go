// Package decision serves JEV-style typed decisions ("System 1"): noul
// (yes/no), score (0-5) and choice questions answered in one prefill pass with
// a calibrated probability distribution over the options.
//
// A decision model ships a "System 1" block next to its base weights: a LoRA
// adapter whose lm_head rows for the option tokens act as the decision head,
// decision_head.json (per-slot bias and verbalizer token IDs) and
// calibration.json (per-kind temperatures). csghub-lite converts the adapter
// to a llama.cpp LoRA GGUF, loads it into the model's llama-server with scale
// 0 so ordinary chat stays the unmodified base model, and enables it per
// decision request.
package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/model"
)

// Model card tags that mark a typed-decision model.
var decisionTags = []string{"jev", "typed-decisions"}

const (
	adapterSubdir   = "adapter_vllm"
	headFile        = "decision_head.json"
	calibrationFile = "calibration.json"
)

var kindOrder = []string{"noul", "score", "choice"}

// ErrNoSystem1 reports a model tagged for typed decisions whose System 1
// block (adapter, head, calibration) is not available locally.
var ErrNoSystem1 = errors.New("typed-decision adapter not found")

// IsDecisionModel reports whether the model card in modelDir tags the model
// as a typed-decision (JEV-style) model.
func IsDecisionModel(modelDir string) bool {
	for _, tag := range model.ModelCardTags(modelDir) {
		for _, want := range decisionTags {
			if strings.EqualFold(strings.TrimSpace(tag), want) {
				return true
			}
		}
	}
	return false
}

// Head is the System 1 decision head: one slot per option token, grouped by
// decision kind.
type Head struct {
	Bias          []float64 `json:"bias"`
	VerbalizerIDs []int     `json:"verbalizer_ids"`
	Slots         struct {
		Ranges          map[string][2]int `json:"ranges"`
		Verbalizers     []string          `json:"verbalizers"`
		TemplateVersion string            `json:"template_version"`
	} `json:"slots"`
}

// Profile is everything needed to answer typed decisions for one model.
type Profile struct {
	// AssetsDir is the repository directory holding the System 1 block; it
	// is the model itself or a locally downloaded base_model repository.
	AssetsDir    string
	AdapterDir   string
	Head         Head
	Temperatures map[string]float64
}

// slotRange returns the [lo, hi) head slots of a decision kind.
func (p *Profile) slotRange(kind string) (int, int) {
	r := p.Head.Slots.Ranges[kind]
	return r[0], r[1]
}

// NativeChoiceOptions is the number of choice options the head answers in a
// single pass.
func (p *Profile) NativeChoiceOptions() int {
	lo, hi := p.slotRange("choice")
	return hi - lo
}

// Protocol identifies the prompt template the head was trained on.
func (p *Profile) Protocol() string {
	if v := strings.TrimSpace(p.Head.Slots.TemplateVersion); v != "" {
		return "jev-" + v
	}
	return "jev-bare-v1"
}

// LocateRepo finds a locally downloaded repository by its namespace/name.
type LocateRepo func(repo string) (dir string, ok bool)

// FindProfile resolves the System 1 block for the model in modelDir. It looks
// in the model directory first, then in each base_model repository named by
// the model card that is downloaded locally (a GGUF quantization usually
// ships only the base weights). It returns ErrNoSystem1 when the model is a
// decision model but none of those directories hold the block.
func FindProfile(modelDir string, locate LocateRepo) (*Profile, error) {
	candidates := []string{modelDir}
	if locate != nil {
		for _, repo := range model.ModelCardBaseModels(modelDir) {
			if dir, ok := locate(repo); ok {
				candidates = append(candidates, dir)
			}
		}
	}
	for _, dir := range candidates {
		if !fileExists(filepath.Join(dir, adapterSubdir, headFile)) {
			continue
		}
		return loadProfile(dir)
	}
	bases := model.ModelCardBaseModels(modelDir)
	if len(bases) > 0 {
		return nil, fmt.Errorf("%w: download %s, which ships %s/", ErrNoSystem1, strings.Join(bases, " or "), adapterSubdir)
	}
	return nil, fmt.Errorf("%w: the model has no %s/ directory", ErrNoSystem1, adapterSubdir)
}

func loadProfile(dir string) (*Profile, error) {
	p := &Profile{AssetsDir: dir, AdapterDir: filepath.Join(dir, adapterSubdir)}
	data, err := os.ReadFile(filepath.Join(p.AdapterDir, headFile))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &p.Head); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", headFile, err)
	}
	if err := p.validateHead(); err != nil {
		return nil, fmt.Errorf("%s: %w", headFile, err)
	}
	p.Temperatures = map[string]float64{"noul": 1, "score": 1, "choice": 1}
	if data, err := os.ReadFile(filepath.Join(dir, calibrationFile)); err == nil {
		var calib struct {
			PerKind map[string]float64 `json:"per_kind"`
		}
		if err := json.Unmarshal(data, &calib); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", calibrationFile, err)
		}
		for kind, t := range calib.PerKind {
			if t > 0 {
				p.Temperatures[kind] = t
			}
		}
	}
	return p, nil
}

func (p *Profile) validateHead() error {
	n := len(p.Head.VerbalizerIDs)
	if n == 0 || len(p.Head.Bias) != n {
		return fmt.Errorf("bias and verbalizer_ids must be non-empty and the same length")
	}
	want := map[string]int{"noul": 2, "score": 6}
	for _, kind := range kindOrder {
		lo, hi := p.slotRange(kind)
		if lo < 0 || hi > n || hi <= lo {
			return fmt.Errorf("invalid slot range for %s", kind)
		}
		if size, ok := want[kind]; ok && hi-lo != size {
			return fmt.Errorf("%s needs %d slots, has %d", kind, size, hi-lo)
		}
	}
	if p.NativeChoiceOptions() < 2 {
		return fmt.Errorf("choice needs at least 2 slots")
	}
	return nil
}

var conversionMu sync.Mutex

// EnsureLoRA converts the profile's adapter to a llama.cpp LoRA GGUF under
// cacheDir once and returns its path. The cache key covers the adapter
// weights' path, size and modification time, so a re-downloaded adapter is
// converted again.
func (p *Profile) EnsureLoRA(cacheDir string) (string, error) {
	weights := filepath.Join(p.AdapterDir, "adapter_model.safetensors")
	info, err := os.Stat(weights)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoSystem1, err)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", weights, info.Size(), info.ModTime().UnixNano())))
	out := filepath.Join(cacheDir, hex.EncodeToString(sum[:8])+".gguf")

	conversionMu.Lock()
	defer conversionMu.Unlock()
	if fileExists(out) {
		return out, nil
	}
	if err := convert.ConvertLoRA(p.AdapterDir, p.AssetsDir, out); err != nil {
		return "", fmt.Errorf("converting decision adapter: %w", err)
	}
	return out, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

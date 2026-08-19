package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/model"
)

var ErrUnsupportedFormat = errors.New("unsupported model format for inference")

// Engine is the interface for model inference backends.
type Engine interface {
	// Generate produces text from a prompt, calling onToken for each generated token.
	Generate(ctx context.Context, prompt string, opts Options, onToken TokenCallback) (string, error)

	// Chat produces a response from a conversation history.
	Chat(ctx context.Context, messages []Message, opts Options, onToken TokenCallback) (string, error)

	// Close releases the model resources.
	Close() error

	// ModelName returns the loaded model identifier.
	ModelName() string
}

// ChatCompletionProxier exposes direct access to the underlying
// OpenAI-compatible /v1/chat/completions API for advanced use cases
// such as native Ollama tool-calling compatibility.
type ChatCompletionProxier interface {
	ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error)
}

// AnthropicMessagesProxier exposes direct access to an Anthropic-compatible
// /v1/messages API. Request headers are supplied separately so protocol
// version and beta feature headers can be preserved without forwarding local
// authentication credentials.
type AnthropicMessagesProxier interface {
	AnthropicMessages(ctx context.Context, reqBody map[string]interface{}, headers http.Header) (*http.Response, error)
}

type nativeAnthropicMessagesPreferer interface {
	PrefersNativeAnthropicMessages() bool
}

// PrefersNativeAnthropicMessages reports whether an engine represents a
// third-party compatible API where an incoming Messages request should be
// preserved before falling back to Chat Completions.
func PrefersNativeAnthropicMessages(eng Engine) bool {
	preferer, ok := eng.(nativeAnthropicMessagesPreferer)
	return ok && preferer.PrefersNativeAnthropicMessages()
}

// EmbeddingsProxier exposes direct access to the underlying
// OpenAI-compatible /v1/embeddings API.
type EmbeddingsProxier interface {
	Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error)
}

// NativeToolStreamer marks engines whose backend natively emits
// OpenAI-compatible streaming tool-call deltas, so tool requests can be
// proxied with stream enabled instead of being aggregated and normalized
// locally after the full completion.
type NativeToolStreamer interface {
	SupportsNativeToolStreaming() bool
}

// SupportsNativeToolStreaming reports whether eng can stream tool-call
// responses directly from its backend.
func SupportsNativeToolStreaming(eng Engine) bool {
	streamer, ok := eng.(NativeToolStreamer)
	return ok && streamer.SupportsNativeToolStreaming()
}

// ConvertProgressFunc receives conversion progress updates.
// If nil, conversion progress is not reported.
type ConvertProgressFunc func(step string, current, total int)

// LoadEngine loads a model and returns an Engine for inference.
// If the model is SafeTensors, it auto-converts to GGUF first.
// By default, llama-server output is not mirrored to stderr, but it is still
// captured for diagnostics and appended to the llama-server log file.
func LoadEngine(modelDir string, lm *model.LocalModel) (Engine, error) {
	return LoadEngineWithProgress(modelDir, lm, nil, false, 0, 0, -1, "", "", "")
}

// LoadEngineWithProgress is like LoadEngine but accepts a progress callback
// for SafeTensors → GGUF conversion. When verbose is true, llama-server
// output is printed to stderr.
func LoadEngineWithProgress(modelDir string, lm *model.LocalModel, progress ConvertProgressFunc, verbose bool, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (Engine, error) {
	return LoadEngineWithSpeculativeProgress(modelDir, lm, progress, verbose, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, SpeculativeConfig{})
}

func LoadEngineWithSpeculativeProgress(modelDir string, lm *model.LocalModel, progress ConvertProgressFunc, verbose bool, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string, speculative SpeculativeConfig) (Engine, error) {
	return loadEngineWithProgressMode(modelDir, lm, progress, verbose, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, false, speculative)
}

// LoadEmbeddingEngineWithProgress is like LoadEngineWithProgress but starts
// llama-server in embedding mode for OpenAI-compatible /v1/embeddings.
func LoadEmbeddingEngineWithProgress(modelDir string, lm *model.LocalModel, progress ConvertProgressFunc, verbose bool, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (Engine, error) {
	return loadEngineWithProgressMode(modelDir, lm, progress, verbose, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, true, SpeculativeConfig{})
}

func loadEngineWithProgressMode(modelDir string, lm *model.LocalModel, progress ConvertProgressFunc, verbose bool, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string, embedding bool, speculative SpeculativeConfig) (Engine, error) {
	modelName := ""
	if lm != nil {
		modelName = lm.FullName()
	}
	log.Printf("INFERENCE %s: resolving model engine in %s dtype=%q", modelName, modelDir, dtype)
	normalizedDType, err := convert.NormalizeRuntimeDType(dtype)
	if err != nil {
		return nil, err
	}

	resolveMMProj := func() (string, error) {
		if path, ok, err := convert.FindMMProjForDType(modelDir, normalizedDType); err != nil {
			return "", err
		} else if ok {
			return path, nil
		}
		if path, ok, err := convert.FindMMProjForDType(modelDir, ""); err != nil {
			return "", err
		} else if ok {
			return path, nil
		}
		return "", nil
	}

	if normalizedDType != "" {
		if ggufPath, ok, err := convert.FindGGUFForDType(modelDir, normalizedDType); err != nil {
			return nil, err
		} else if ok {
			log.Printf("INFERENCE %s: using GGUF %s for dtype=%q", modelName, ggufPath, normalizedDType)
			mmproj, err := resolveMMProj()
			if err != nil {
				return nil, err
			}
			if embedding {
				return newLlamaEmbeddingEngine(ggufPath, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, mmproj)
			}
			return newLlamaSpeculativeEngine(ggufPath, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, speculative, mmproj)
		}
		if convert.HasConvertibleHFWeights(modelDir) {
			if err := ensureConvertibleHFArchitecture(modelDir); err != nil {
				return nil, err
			}
			log.Printf("INFERENCE %s: HuggingFace weights detected; converting to GGUF dtype=%q", modelName, normalizedDType)
			ggufPath, err := convertSafeTensors(modelDir, progress, normalizedDType)
			if err != nil {
				return nil, fmt.Errorf("auto-converting SafeTensors to GGUF: %w", err)
			}
			mmproj, err := resolveMMProj()
			if err != nil {
				return nil, err
			}
			if embedding {
				eng, err := newLlamaEmbeddingEngine(ggufPath, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, mmproj)
				if err != nil {
					removeConvertedGGUFIfInvalid(ggufPath, err)
					return nil, err
				}
				return eng, nil
			}
			eng, err := newLlamaSpeculativeEngine(ggufPath, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, speculative, mmproj)
			if err != nil {
				removeConvertedGGUFIfInvalid(ggufPath, err)
				return nil, err
			}
			return eng, nil
		}
	}

	modelFile, format, err := model.FindModelFile(modelDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("finding model file: %w", err)
		}
		if !convert.NeedsConversion(modelDir) {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
		}
		format = model.FormatSafeTensors
	}

	switch format {
	case model.FormatGGUF:
		log.Printf("INFERENCE %s: using GGUF %s", modelName, modelFile)
		mmproj, err := resolveMMProj()
		if err != nil {
			return nil, err
		}
		if embedding {
			return newLlamaEmbeddingEngine(modelFile, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, mmproj)
		}
		return newLlamaSpeculativeEngine(modelFile, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, speculative, mmproj)

	case model.FormatSafeTensors, model.FormatPyTorch:
		if err := ensureConvertibleHFArchitecture(modelDir); err != nil {
			return nil, err
		}
		log.Printf("INFERENCE %s: HuggingFace weights detected; converting to GGUF dtype=%q", modelName, normalizedDType)
		ggufPath, err := convertSafeTensors(modelDir, progress, normalizedDType)
		if err != nil {
			return nil, fmt.Errorf("auto-converting SafeTensors to GGUF: %w", err)
		}
		mmproj, err := resolveMMProj()
		if err != nil {
			return nil, err
		}
		if embedding {
			eng, err := newLlamaEmbeddingEngine(ggufPath, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, mmproj)
			if err != nil {
				removeConvertedGGUFIfInvalid(ggufPath, err)
				return nil, err
			}
			return eng, nil
		}
		eng, err := newLlamaSpeculativeEngine(ggufPath, lm.FullName(), verbose, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, speculative, mmproj)
		if err != nil {
			removeConvertedGGUFIfInvalid(ggufPath, err)
			return nil, err
		}
		return eng, nil

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

func ensureConvertibleHFArchitecture(modelDir string) error {
	arch, ok, err := readHFArchitecture(modelDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, supported := convert.SupportedHFArchitecture(arch); supported {
		return nil
	}
	log.Printf("CONVERT: unsupported HuggingFace architecture %q in %s", arch, modelDir)
	return fmt.Errorf("%w: HuggingFace architecture %q is not supported by bundled llama.cpp GGUF converter", ErrUnsupportedFormat, arch)
}

func readHFArchitecture(modelDir string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading HuggingFace config: %w", err)
	}
	var cfg struct {
		Architectures []string `json:"architectures"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", false, fmt.Errorf("parsing HuggingFace config: %w", err)
	}
	for _, arch := range cfg.Architectures {
		if arch = strings.TrimSpace(arch); arch != "" {
			return arch, true, nil
		}
	}
	return "", false, nil
}

func convertSafeTensors(modelDir string, progress ConvertProgressFunc, dtype string) (string, error) {
	if ggufPath, ok, err := convert.FindGGUFForDType(modelDir, dtype); err != nil {
		return "", err
	} else if ok {
		log.Printf("CONVERT: reusing existing GGUF %s", ggufPath)
		return ggufPath, nil
	}

	var progressFn convert.ProgressFunc
	if progress != nil {
		progressFn = convert.ProgressFunc(progress)
	}

	log.Printf("CONVERT: starting SafeTensors to GGUF model_dir=%s dtype=%q", modelDir, dtype)
	path, err := convert.Convert(modelDir, progressFn, dtype)
	if err != nil {
		log.Printf("CONVERT: failed model_dir=%s dtype=%q: %v", modelDir, dtype, err)
		return "", err
	}
	log.Printf("CONVERT: complete output=%s", path)
	return path, nil
}

func removeConvertedGGUFIfInvalid(ggufPath string, err error) {
	if !shouldRemoveConvertedGGUF(err) {
		log.Printf("keeping converted GGUF after llama-server load failure: %s", ggufPath)
		return
	}
	log.Printf("removing invalid converted GGUF: %s", ggufPath)
	if removeErr := os.Remove(ggufPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		log.Printf("warning: could not remove invalid converted GGUF %s: %v", ggufPath, removeErr)
	}
}

func shouldRemoveConvertedGGUF(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())

	// Runtime/resource failures should keep the converted file so retries do not
	// pay the conversion cost again.
	keepMarkers := []string{
		"out of memory",
		"cudaMalloc failed",
		"hipmalloc failed",
		"failed to fit params to free device memory",
		"unable to allocate",
		"no such device",
		"device busy",
		"insufficient memory",
		"timeout waiting for llama-server",
		"address already in use",
	}
	for _, marker := range keepMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return false
		}
	}

	// Only clean up when the failure looks like the GGUF itself is invalid or
	// incomplete, so the next attempt can reconvert a fresh copy.
	removeMarkers := []string{
		"invalid magic characters",
		"invalid gguf",
		"failed to read magic",
		"failed to load model",
		"unknown model architecture",
		"unknown model arch",
		"unknown tensor type",
		"tensor data is not within file bounds",
		"failed to open gguf",
		"gguf file is",
		"not a gguf file",
		"corrupt",
		"truncated",
	}
	for _, marker := range removeMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}

	return false
}

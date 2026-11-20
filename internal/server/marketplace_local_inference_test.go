package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/modelregistry"
	"github.com/opencsgs/csglite/pkg/api"
)

type fakeLocalInferenceRegistry struct {
	source    modelregistry.Source
	pages     map[int][]csghub.Model
	total     int
	listCalls int
	listErrs  map[int]error
	listDelay time.Duration
	readFile  func(ctx context.Context, repoID, revision, filePath string) (string, error)
	readErrs  map[string]error

	sizes     map[string]int64
	params    map[string]float64
	sizeErr   error
	sizeCalls int

	fileSizes      map[string]int64
	fileSizeErr    error
	fileSizeCalls  int
	fileSizeDelays map[string]time.Duration
	fileSizeMax    int64
	sizeDelay      time.Duration
	fileSizeDelay  time.Duration

	mu            sync.Mutex
	readCalls     int
	readRepos     []string
	readRevisions []string
	listOpts      []modelregistry.ListOptions
}

func (r *fakeLocalInferenceRegistry) Source() modelregistry.Source { return r.source }

func (r *fakeLocalInferenceRegistry) DefaultRevision() string { return "main" }

func (r *fakeLocalInferenceRegistry) ListModels(_ context.Context, opts modelregistry.ListOptions) ([]csghub.Model, int, error) {
	r.listCalls++
	r.mu.Lock()
	r.listOpts = append(r.listOpts, opts)
	r.mu.Unlock()
	if r.listDelay > 0 {
		time.Sleep(r.listDelay)
	}
	if err := r.listErrs[opts.Page]; err != nil {
		return nil, 0, err
	}
	return r.pages[opts.Page], r.total, nil
}

func (r *fakeLocalInferenceRegistry) GetModel(context.Context, string, string) (*csghub.Model, error) {
	return nil, errors.New("not implemented")
}

func (r *fakeLocalInferenceRegistry) ListFiles(context.Context, string, string) ([]csghub.RepoFile, string, error) {
	return nil, "", nil
}

func (r *fakeLocalInferenceRegistry) ReadFile(ctx context.Context, repoID, revision, filePath string) (string, error) {
	r.mu.Lock()
	r.readCalls++
	r.readRepos = append(r.readRepos, repoID)
	r.readRevisions = append(r.readRevisions, revision)
	r.mu.Unlock()
	if r.readFile != nil {
		return r.readFile(ctx, repoID, revision, filePath)
	}
	if err := r.readErrs[repoID]; err != nil {
		return "", err
	}
	return "", &csghub.HTTPError{StatusCode: http.StatusNotFound, Body: "not found"}
}

func (r *fakeLocalInferenceRegistry) DownloadSnapshot(context.Context, string, string, string, []string, csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	return nil, "", nil
}

func (r *fakeLocalInferenceRegistry) FillRepoSizes(ctx context.Context, models []csghub.Model) error {
	r.mu.Lock()
	r.sizeCalls++
	delay := r.sizeDelay
	r.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.sizeErr != nil {
		return r.sizeErr
	}
	for i := range models {
		if size, ok := r.sizes[models[i].Path]; ok {
			models[i].RepoSize = size
		}
		if params, ok := r.params[models[i].Path]; ok {
			models[i].Metadata.ModelParams = params
		}
	}
	return nil
}

func (r *fakeLocalInferenceRegistry) FillFileSizes(ctx context.Context, models []csghub.Model, maxSize int64) error {
	r.mu.Lock()
	r.fileSizeCalls++
	r.fileSizeMax = maxSize
	delay := r.fileSizeDelay
	r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.fileSizeErr != nil {
		return r.fileSizeErr
	}
	for i := range models {
		if delay, ok := r.fileSizeDelays[models[i].Path]; ok && delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if size, ok := r.fileSizes[models[i].Path]; ok {
			models[i].RepoSize = size
		}
	}
	return nil
}

func (r *fakeLocalInferenceRegistry) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readCalls
}

func (r *fakeLocalInferenceRegistry) readRepoSet() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.readRepos...)
	sort.Strings(out)
	return out
}

func newTestLocalInferenceFilter(reg modelregistry.Registry, clock *time.Time) *marketplaceLocalInferenceFilter {
	clearMarketplaceLocalInferenceCache()
	filter := newMarketplaceLocalInferenceFilter(reg)
	filter.now = func() time.Time { return *clock }
	return filter
}

func localInferenceTag(name, category string) csghub.Tag {
	return csghub.Tag{Name: name, Category: category}
}

func localInferenceModel(repoID string, tags ...csghub.Tag) csghub.Model {
	name := repoID
	for i := len(repoID) - 1; i >= 0; i-- {
		if repoID[i] == '/' {
			name = repoID[i+1:]
			break
		}
	}
	return csghub.Model{Name: name, Path: repoID, Tags: tags}
}

func TestMarketplaceModelInferenceFormat(t *testing.T) {
	tests := []struct {
		name string
		tags []csghub.Tag
		want string
	}{
		{
			name: "cross category prefers gguf",
			tags: []csghub.Tag{
				localInferenceTag("safetensors", "framework"),
				localInferenceTag("gguf", "runtime_framework"),
			},
			want: "gguf",
		},
		{
			name: "case insensitive",
			tags: []csghub.Tag{localInferenceTag("GGUF", "framework")},
			want: "gguf",
		},
		{
			name: "pytorch from runtime framework",
			tags: []csghub.Tag{localInferenceTag("pytorch", "runtime_framework")},
			want: "pytorch",
		},
		{
			name: "safetensors beats pytorch",
			tags: []csghub.Tag{
				localInferenceTag("pytorch", "framework"),
				localInferenceTag("safetensors", "runtime_framework"),
			},
			want: "safetensors",
		},
		{
			name: "unknown values ignored",
			tags: []csghub.Tag{localInferenceTag("mlx", "runtime_framework")},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marketplaceModelInferenceFormat(tt.tags); got != tt.want {
				t.Fatalf("marketplaceModelInferenceFormat = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarketplaceModelListFormat(t *testing.T) {
	tests := []struct {
		name string
		tags []csghub.Tag
		want string
	}{
		{
			name: "framework tag wins",
			tags: []csghub.Tag{
				localInferenceTag("safetensors", "framework"),
				localInferenceTag("gguf", "task"),
			},
			want: "safetensors",
		},
		{
			name: "task gguf fills the missing framework tag",
			tags: []csghub.Tag{
				localInferenceTag("gguf", "task"),
				localInferenceTag("text-to-image", "task"),
			},
			want: "gguf",
		},
		{
			name: "case insensitive task gguf",
			tags: []csghub.Tag{localInferenceTag("GGUF", "task")},
			want: "gguf",
		},
		{
			name: "no format hint",
			tags: []csghub.Tag{localInferenceTag("text-to-image", "task")},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marketplaceModelListFormat(tt.tags); got != tt.want {
				t.Fatalf("marketplaceModelListFormat = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarketplaceModelHasUnsupportedFormat(t *testing.T) {
	tests := []struct {
		name string
		tags []csghub.Tag
		want bool
	}{
		{name: "framework onnx", tags: []csghub.Tag{localInferenceTag("onnx", "framework")}, want: true},
		{name: "framework jax", tags: []csghub.Tag{localInferenceTag("jax", "framework")}, want: true},
		{name: "runtime onnx", tags: []csghub.Tag{localInferenceTag("onnx", "runtime_framework")}, want: true},
		{name: "runtime mlx is not a format", tags: []csghub.Tag{localInferenceTag("mlx", "runtime_framework")}, want: false},
		{name: "unknown framework", tags: []csghub.Tag{localInferenceTag("coreml", "framework")}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marketplaceModelHasUnsupportedFormat(tt.tags); got != tt.want {
				t.Fatalf("marketplaceModelHasUnsupportedFormat = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMarketplaceRoutedTaskTag(t *testing.T) {
	tests := []struct {
		name string
		tags []csghub.Tag
		want string
	}{
		{
			name: "image after unrelated first task",
			tags: []csghub.Tag{
				localInferenceTag("stable-diffusion", "task"),
				localInferenceTag("text-to-image", "task"),
			},
			want: "text-to-image",
		},
		{
			name: "legacy stable-diffusion tag maps to text-to-image",
			tags: []csghub.Tag{localInferenceTag("stable-diffusion", "task")},
			want: "text-to-image",
		},
		{
			name: "video wins regardless of order",
			tags: []csghub.Tag{
				localInferenceTag("text-to-image", "task"),
				localInferenceTag("text-to-video", "task"),
			},
			want: "text-to-video",
		},
		{
			name: "case normalized",
			tags: []csghub.Tag{localInferenceTag("Text-to-Image", "task")},
			want: "text-to-image",
		},
		{
			name: "falls back to first task",
			tags: []csghub.Tag{
				localInferenceTag("text-classification", "task"),
				localInferenceTag("text-generation", "task"),
			},
			want: "text-classification",
		},
		{name: "no task", tags: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marketplaceRoutedTaskTag(tt.tags); got != tt.want {
				t.Fatalf("marketplaceRoutedTaskTag = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarketplaceLooksLikeNonTextGGUF(t *testing.T) {
	for _, repo := range []string{
		"Z-Image-Turbo",
		"zimage-turbo",
		"GLM-Image",
		"glmimage",
		"OVIS-Image",
		"ovisimage",
		"stable-cascade-prior",
		"LTX-Video",
		"ltxv-13b",
		"hunyuan-video",
		"hunyuanvideo",
		"Wan2.1-T2V-1.3B",
		"latent-consistency",
		"latentconsistency",
		"FLUX.1-dev",
	} {
		if !marketplaceLooksLikeNonTextGGUF(repo) {
			t.Fatalf("marketplaceLooksLikeNonTextGGUF(%q) = false, want true", repo)
		}
	}
	for _, repo := range []string{"qwen3-8b", "llama-3.1", ""} {
		if marketplaceLooksLikeNonTextGGUF(repo) {
			t.Fatalf("marketplaceLooksLikeNonTextGGUF(%q) = true, want false", repo)
		}
	}
}

func TestMarketplaceLocalInferenceFilterKeepsStableDiffusionModels(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/sd-xl", localInferenceTag("safetensors", "framework"), localInferenceTag("stable-diffusion", "task")),
		localInferenceModel("acme/sd-gguf", localInferenceTag("gguf", "framework"), localInferenceTag("stable-diffusion", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].Path != "acme/sd-xl" {
		t.Fatalf("models = %v, want only the safetensors diffusion model", result.Models)
	}
	if reg.readCount() != 0 {
		t.Fatalf("config reads = %d, want 0 (an image task tag must not trigger a text probe)", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterKeepsRunnableModels(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework")),
		localInferenceModel("acme/whisper", localInferenceTag("safetensors", "framework"), localInferenceTag("automatic-speech-recognition", "task")),
		localInferenceModel("acme/diffusion", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task")),
		localInferenceModel("acme/video", localInferenceTag("gguf", "framework"), localInferenceTag("text-to-video", "task")),
		localInferenceModel("acme/unknown", localInferenceTag("text-generation", "task")),
		localInferenceModel("acme/paddle", localInferenceTag("paddlepaddle", "framework"), localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/flux-turbo", localInferenceTag("gguf", "framework")),
		localInferenceModel("acme/mixed", localInferenceTag("safetensors", "framework"), localInferenceTag("onnx", "runtime_framework"), localInferenceTag("text-generation", "task")),
	}
	models[3].Metadata.Architecture = "LlamaForCausalLM"
	models[8].Metadata.Architecture = "LlamaForCausalLM"

	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		got = append(got, m.Path)
	}
	want := []string{"acme/qwen-gguf", "acme/whisper", "acme/diffusion", "acme/llama", "acme/mixed"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %v, want %v", got, want)
		}
	}
	if reg.readCount() != 0 {
		t.Fatalf("config reads = %d, want 0", reg.readCount())
	}
	if !result.TotalExact || result.Total != len(want) || result.Partial || result.Degraded {
		t.Fatalf("result = %#v, want exact total=%d", result, len(want))
	}
}

func TestMarketplaceLocalInferenceFilterExcludesQuantizedWeights(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	textTags := func() []csghub.Tag {
		return []csghub.Tag{
			localInferenceTag("safetensors", "framework"),
			localInferenceTag("text-generation", "task"),
		}
	}
	models := []csghub.Model{
		localInferenceModel("Qwen/Qwen2.5-72B-Instruct-AWQ", textTags()...),
		localInferenceModel("TheBloke/Llama-2-7B-GPTQ", textTags()...),
		localInferenceModel("nvidia/DeepSeek-R1-0528-FP4", textTags()...),
		localInferenceModel("mlx-community/GLM-5.2-mxfp4", textTags()...),
		localInferenceModel("acme/model-int4", textTags()...),
		localInferenceModel("acme/model-4bit", textTags()...),
		localInferenceModel("acme/model-4bits", textTags()...),
		localInferenceModel("acme/model-8bits", textTags()...),
		localInferenceModel("acme/model-w4a16", textTags()...),
		localInferenceModel("acme/qwen-awq-gguf", localInferenceTag("gguf", "framework"), localInferenceTag("text-generation", "task")),
		// Not quantized: "print4" contains "int4" and "24bit" contains "4bit",
		// so these must survive the whole-token match.
		localInferenceModel("acme/print4-model", textTags()...),
		localInferenceModel("acme/24bit-weights", textTags()...),
		localInferenceModel("acme/plain", textTags()...),
	}
	for i := range models {
		switch models[i].Path {
		case "acme/print4-model", "acme/24bit-weights", "acme/plain":
			models[i].Metadata.Architecture = "LlamaForCausalLM"
		}
	}

	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		got = append(got, m.Path)
	}
	want := []string{"acme/qwen-awq-gguf", "acme/print4-model", "acme/24bit-weights", "acme/plain"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %v, want %v", got, want)
		}
	}
	if reg.readCount() != 0 {
		t.Fatalf("config reads = %d, want 0 (quantized models must not be probed)", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterProbesTTSArchitecture(t *testing.T) {
	for _, format := range []string{"safetensors", "gguf"} {
		t.Run(format, func(t *testing.T) {
			clock := time.Unix(1700000000, 0)
			reg := &fakeLocalInferenceRegistry{
				source: modelregistry.SourceOpenCSG,
				pages: map[int][]csghub.Model{1: {
					localInferenceModel("acme/tts", localInferenceTag(format, "framework"), localInferenceTag("text-to-speech", "task")),
				}},
				total: 1,
				readFile: func(context.Context, string, string, string) (string, error) {
					return `{"architectures":["VitsModel"]}`, nil
				},
			}
			filter := newTestLocalInferenceFilter(reg, &clock)
			result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(result.Models) != 1 {
				t.Fatalf("models = %v, want the TTS model", result.Models)
			}
			if reg.readCount() != 1 {
				t.Fatalf("config reads = %d, want 1", reg.readCount())
			}
			if result.Degraded || result.Partial {
				t.Fatalf("result = %#v, want complete", result)
			}
		})
	}
}

func TestMarketplaceLocalInferenceFilterReusesArchitectureCache(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	for i := 0; i < 2; i++ {
		result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
		if len(result.Models) != 1 {
			t.Fatalf("list %d models = %v, want one", i, result.Models)
		}
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads = %d, want 1", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterTransientCacheTTL(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readErrs: map[string]error{
			"acme/llama": &csghub.HTTPError{StatusCode: http.StatusTooManyRequests},
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !result.Degraded || !result.Partial {
		t.Fatalf("result = %#v, want degraded partial", result)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads = %d, want 1", reg.readCount())
	}

	clock = clock.Add(20 * time.Second)
	result, err = filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list within TTL: %v", err)
	}
	if !result.Degraded {
		t.Fatalf("result = %#v, want degraded on cached transient entry", result)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads within TTL = %d, want 1", reg.readCount())
	}

	clock = clock.Add(11 * time.Second)
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list after TTL: %v", err)
	}
	if reg.readCount() != 2 {
		t.Fatalf("config reads after TTL = %d, want 2", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterResetsStateBetweenScans(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return "", &csghub.HTTPError{StatusCode: http.StatusTooManyRequests}
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	first, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	if !first.Degraded {
		t.Fatalf("first result = %#v, want degraded", first)
	}

	clearMarketplaceLocalInferenceCache()
	reg.readFile = func(context.Context, string, string, string) (string, error) {
		return `{"architectures":["LlamaForCausalLM"]}`, nil
	}
	second, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if second.Degraded {
		t.Fatalf("second result = %#v, degraded flag leaked between scans", second)
	}
	if len(second.Models) != 1 {
		t.Fatalf("second models = %v, want one", second.Models)
	}
}

func TestMarketplaceLocalInferenceFilterResetsBudgetBetweenScans(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/one", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/two", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/three", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/four", localInferenceTag("safetensors", "framework")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	const budget = 2
	filter.budgetLimit = budget

	for i := 1; i <= 2; i++ {
		result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
		if err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
		if !result.Degraded {
			t.Fatalf("scan %d not degraded: budgetLimit was not applied to this scan", i)
		}
		if len(result.Models) != budget {
			t.Fatalf("scan %d models = %d, want %d", i, len(result.Models), budget)
		}
		if reg.readCount() != i*budget {
			t.Fatalf("scan %d config reads = %d, want %d", i, reg.readCount(), i*budget)
		}
		clearMarketplaceLocalInferenceCache()
	}
}

func TestMarketplaceLocalInferenceFilterMissCacheTTL(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readErrs: map[string]error{
			"acme/llama": &csghub.HTTPError{StatusCode: http.StatusNotFound},
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Degraded || result.Partial {
		t.Fatalf("result = %#v, want deterministic empty result", result)
	}

	clock = clock.Add(31 * time.Second)
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list within TTL: %v", err)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads within TTL = %d, want 1", reg.readCount())
	}

	clock = clock.Add(5 * time.Minute)
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list after TTL: %v", err)
	}
	if reg.readCount() != 2 {
		t.Fatalf("config reads after TTL = %d, want 2", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterCachesForbiddenAsMiss(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readErrs: map[string]error{
			"acme/llama": &csghub.HTTPError{StatusCode: http.StatusForbidden},
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Degraded || result.Partial {
		t.Fatalf("result = %#v, want deterministic empty result", result)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads = %d, want 1", reg.readCount())
	}

	clock = clock.Add(31 * time.Second)
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list within TTL: %v", err)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads within TTL = %d, want 1 (403 is cached as a miss)", reg.readCount())
	}

	clock = clock.Add(5 * time.Minute)
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list after TTL: %v", err)
	}
	if reg.readCount() != 2 {
		t.Fatalf("config reads after TTL = %d, want 2", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterProbesPytorchOnlyModels(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("pytorch", "runtime_framework")),
		}},
		total: 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].Path != "acme/llama" {
		t.Fatalf("models = %v, want the pytorch model", result.Models)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads = %d, want 1 (pytorch-only models must be probed)", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterBudgetIsDeterministic(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := make([]csghub.Model, 0, 5)
	for i := 0; i < 5; i++ {
		models = append(models, localInferenceModel("acme/model-"+strconv.Itoa(i), localInferenceTag("safetensors", "framework")))
	}
	run := func() []string {
		reg := &fakeLocalInferenceRegistry{
			source: modelregistry.SourceOpenCSG,
			pages:  map[int][]csghub.Model{1: models},
			total:  len(models),
		}
		filter := newTestLocalInferenceFilter(reg, &clock)
		filter.budgetLimit = 2
		if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
			t.Fatalf("list: %v", err)
		}
		if reg.readCount() != 2 {
			t.Fatalf("config reads = %d, want 2", reg.readCount())
		}
		return reg.readRepoSet()
	}
	first := run()
	second := run()
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("probed = %v / %v, want two models each", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("probed sets differ: %v vs %v", first, second)
		}
	}
}

func TestMarketplaceLocalInferenceFilterBudgetOnlyCountsCacheMisses(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/one", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/two", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/three", localInferenceTag("safetensors", "framework")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.budgetLimit = 1
	for _, m := range models[:2] {
		filter.cache.set(marketplaceLocalInferenceCacheKey(reg.Source(), m), marketplaceArchEntry{
			architecture: "LlamaForCausalLM",
			writtenAt:    clock,
			status:       marketplaceArchStatusHit,
		})
	}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 3 {
		t.Fatalf("models = %v, want all three", result.Models)
	}
	if reg.readCount() != 1 {
		t.Fatalf("config reads = %d, want 1", reg.readCount())
	}
	if repos := reg.readRepoSet(); len(repos) != 1 || repos[0] != "acme/three" {
		t.Fatalf("probed = %v, want acme/three", repos)
	}
}

func TestMarketplaceLocalInferenceFilterProbeTimeout(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/one", localInferenceTag("safetensors", "framework")),
			localInferenceModel("acme/two", localInferenceTag("safetensors", "framework")),
		}},
		total: 2,
		readFile: func(ctx context.Context, _ string, _ string, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.probeTimeout = 50 * time.Millisecond

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !result.Degraded || !result.Partial || result.TotalExact {
		t.Fatalf("result = %#v, want degraded partial lower bound", result)
	}
	if reg.readCount() != 2 {
		t.Fatalf("config reads = %d, want 2", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterAuthFailureLimit(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := make([]csghub.Model, 0, 3)
	for i := 0; i < 3; i++ {
		models = append(models, localInferenceModel("acme/model-"+strconv.Itoa(i), localInferenceTag("safetensors", "framework")))
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		readFile: func(context.Context, string, string, string) (string, error) {
			return "", &csghub.HTTPError{StatusCode: http.StatusUnauthorized, Body: "AUTH-ERR-5"}
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !result.Degraded {
		t.Fatalf("result = %#v, want degraded", result)
	}
	if got := filter.authFailures.Load(); got != marketplaceLocalInferenceAuthFailureLimit {
		t.Fatalf("auth failures = %d, want %d", got, marketplaceLocalInferenceAuthFailureLimit)
	}
}

func TestMarketplaceLocalInferenceFilterAuthFailureKeepsScanningReadableRepos(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	page1 := []csghub.Model{
		localInferenceModel("acme/private-1", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/private-2", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/private-3", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/public-1", localInferenceTag("safetensors", "framework")),
	}
	page2 := []csghub.Model{
		localInferenceModel("acme/public-2", localInferenceTag("safetensors", "framework")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: page1, 2: page2},
		total:  len(page1) + len(page2),
		readFile: func(_ context.Context, repoID, _, _ string) (string, error) {
			if strings.HasPrefix(repoID, "acme/private-") {
				return "", &csghub.HTTPError{StatusCode: http.StatusUnauthorized}
			}
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.listCalls != 2 {
		t.Fatalf("list calls = %d, want 2 (readable repositories must keep the scan going)", reg.listCalls)
	}
	if len(result.Models) != 2 || result.Models[0].Path != "acme/public-1" || result.Models[1].Path != "acme/public-2" {
		t.Fatalf("models = %v, want the two readable models", result.Models)
	}
	if !result.Degraded {
		t.Fatalf("result = %#v, want degraded because some repositories were unreadable", result)
	}
}

func TestMarketplaceLocalInferenceFilterDoesNotCacheCanceledReads(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return "", context.Canceled
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	for i := 0; i < 2; i++ {
		result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
		if !result.Degraded {
			t.Fatalf("list %d result = %#v, want degraded", i, result)
		}
	}
	if reg.readCount() != 2 {
		t.Fatalf("config reads = %d, want 2 (a canceled read must not be cached)", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterKeepsModelsWithoutFrameworkTag(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/whisper-large-v3", localInferenceTag("automatic-speech-recognition", "task")),
		localInferenceModel("acme/image-pipeline", localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/plain-text", localInferenceTag("text-generation", "task")),
		localInferenceModel("acme/paddle-image", localInferenceTag("paddlepaddle", "framework"), localInferenceTag("text-to-image", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"acme/whisper-large-v3", "acme/image-pipeline"}
	if len(result.Models) != len(want) {
		t.Fatalf("models = %v, want %v", result.Models, want)
	}
	for i := range want {
		if result.Models[i].Path != want[i] {
			t.Fatalf("models = %v, want %v", result.Models, want)
		}
	}
	if reg.readCount() != 0 {
		t.Fatalf("config reads = %d, want 0", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterTreatsTaskGGUFAsFormat(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		// The list payload only tags GGUF under task; the detail payload and the
		// file tree still resolve it to GGUF, where image models are excluded.
		localInferenceModel("acme/Qwen-Image-2.1-Anime-Fusal-GGUF",
			localInferenceTag("gguf", "task"), localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/text-gguf",
			localInferenceTag("gguf", "task"), localInferenceTag("text-generation", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].Path != "acme/text-gguf" {
		t.Fatalf("models = %v, want only the text GGUF", result.Models)
	}
	if reg.readCount() != 0 {
		t.Fatalf("config reads = %d, want 0 (GGUF needs no architecture probe)", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterDoesNotTreatShortPagesAsExhausted(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	makePage := func(prefix string) []csghub.Model {
		page := make([]csghub.Model, 0, 50)
		for i := 0; i < 50; i++ {
			page = append(page, localInferenceModel(prefix+"-"+strconv.Itoa(i), localInferenceTag("safetensors", "framework")))
		}
		return page
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: makePage("acme/one"), 2: makePage("acme/two")},
		total:  0,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.scanPageLimit = 2

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.listCalls != 2 {
		t.Fatalf("list calls = %d, want 2 (a 50-model page must not be mistaken for the last page)", reg.listCalls)
	}
	if result.TotalExact {
		t.Fatalf("total_exact = true, want false: the scan stopped before the upstream ran out")
	}
	if !result.Partial {
		t.Fatalf("result = %#v, want partial", result)
	}
}

func TestMarketplaceLocalInferenceFilterStopsAtScanTimeout(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	page := make([]csghub.Model, 0, marketplaceLocalInferenceMinScanPer)
	for i := 0; i < marketplaceLocalInferenceMinScanPer; i++ {
		page = append(page, localInferenceModel(
			"acme/video-"+strconv.Itoa(i),
			localInferenceTag("gguf", "framework"),
			localInferenceTag("text-to-video", "task"),
		))
	}
	reg := &fakeLocalInferenceRegistry{
		source:    modelregistry.SourceOpenCSG,
		pages:     map[int][]csghub.Model{1: page, 2: page, 3: page},
		total:     1000,
		listDelay: 100 * time.Millisecond,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.scanTimeout = 50 * time.Millisecond

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1: the first page already outlived the scan budget", reg.listCalls)
	}
	if result.TotalExact {
		t.Fatalf("total_exact = true, want false: the scan stopped before the upstream ran out")
	}
	if !result.Partial {
		t.Fatalf("result = %#v, want partial", result)
	}
}

func TestMarketplaceLocalInferenceFilterSlowSizePhaseIsNotAProbeTimeout(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
		sizeDelay: 100 * time.Millisecond,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.probeTimeout = 50 * time.Millisecond
	filter.limits = marketplaceLocalInferenceLimits{
		maxDownloadBytes: 100 << 30,
		maxMemoryBytes:   100 << 30,
	}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("models = %v, want the convertible model", result.Models)
	}
	// The size lookup outlives the probe timeout, but the probe phase itself
	// finished in time; that must not be reported as a partial scan.
	if result.Degraded || result.Partial {
		t.Fatalf("result = %#v, want a complete scan", result)
	}
	if !result.TotalExact {
		t.Fatalf("result = %#v, want total_exact", result)
	}
}

func TestMarketplaceLocalInferenceFilterSizeDeadlineKeepsModelsAndCachesMiss(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
		fileSizeDelay: time.Second,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.sizeTimeout = 50 * time.Millisecond
	filter.limits = marketplaceLocalInferenceLimits{
		maxDownloadBytes: 100 << 30,
		maxMemoryBytes:   100 << 30,
	}

	for i := 0; i < 2; i++ {
		result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
		if len(result.Models) != 1 {
			t.Fatalf("list %d models = %v, want the unknown-size model to be kept", i, result.Models)
		}
		if result.Degraded {
			t.Fatalf("list %d result = %#v, want no degradation", i, result)
		}
	}
	if reg.fileSizeCalls != 1 {
		t.Fatalf("file size lookups = %d, want 1 (a timed-out lookup must be cached as unknown)", reg.fileSizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterSizeLookupSurvivesSlowFileTreeOnEarlierPage(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	pageOne := make([]csghub.Model, 0, marketplaceLocalInferenceMinScanPer)
	pageOne = append(pageOne, localInferenceModel("acme/slow-tree", localInferenceTag("safetensors", "framework")))
	for i := 1; i < marketplaceLocalInferenceMinScanPer; i++ {
		pageOne = append(pageOne, localInferenceModel("acme/onnx-"+strconv.Itoa(i), localInferenceTag("onnx", "framework")))
	}
	huge := localInferenceModel("acme/huge", localInferenceTag("safetensors", "framework"))
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{
			1: pageOne,
			2: {huge},
		},
		total: marketplaceLocalInferenceMinScanPer + 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
		// The tree fallback on the first page outlives the size deadline, which
		// used to starve the batch lookup on the second page.
		fileSizeDelay: 200 * time.Millisecond,
		sizes:         map[string]int64{huge.Path: 64 << 30},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.sizeTimeout = 50 * time.Millisecond
	filter.limits = marketplaceLocalInferenceLimits{
		maxDownloadBytes: 200 << 30,
		maxMemoryBytes:   16 << 30,
	}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.sizeCalls < 2 {
		t.Fatalf("size calls = %d, want a batch lookup on both pages", reg.sizeCalls)
	}
	for _, m := range result.Models {
		if m.Path == huge.Path {
			t.Fatalf("models = %v, want the oversized model excluded", result.Models)
		}
	}
}

func TestMarketplaceLocalInferenceFilterFileSizesGetFreshBudgetPerPage(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	pageOne := make([]csghub.Model, 0, marketplaceLocalInferenceMinScanPer)
	pageOne = append(pageOne, localInferenceModel("acme/slow-tree", localInferenceTag("safetensors", "framework")))
	for i := 1; i < marketplaceLocalInferenceMinScanPer; i++ {
		pageOne = append(pageOne, localInferenceModel("acme/onnx-"+strconv.Itoa(i), localInferenceTag("onnx", "framework")))
	}
	huge := localInferenceModel("acme/huge", localInferenceTag("safetensors", "framework"))
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{
			1: pageOne,
			2: {huge},
		},
		total: marketplaceLocalInferenceMinScanPer + 1,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
		// The first page spends its whole tree deadline on one repository with
		// many directories; the second page must still get its own deadline.
		fileSizeDelays: map[string]time.Duration{"acme/slow-tree": 200 * time.Millisecond},
		fileSizes:      map[string]int64{huge.Path: 64 << 30},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.sizeTimeout = 50 * time.Millisecond
	filter.limits = marketplaceLocalInferenceLimits{
		maxDownloadBytes: 200 << 30,
		maxMemoryBytes:   16 << 30,
	}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.fileSizeCalls < 2 {
		t.Fatalf("file size calls = %d, want a tree lookup on both pages", reg.fileSizeCalls)
	}
	for _, m := range result.Models {
		if m.Path == huge.Path {
			t.Fatalf("models = %v, want the oversized model excluded", result.Models)
		}
	}
}

func TestMarketplaceLocalInferenceErrorStatus(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  marketplaceArchStatus
		wantAuthErr bool
	}{
		{name: "401", err: &csghub.HTTPError{StatusCode: http.StatusUnauthorized}, wantStatus: marketplaceArchStatusTransient, wantAuthErr: true},
		{name: "408", err: &csghub.HTTPError{StatusCode: http.StatusRequestTimeout}, wantStatus: marketplaceArchStatusTransient},
		{name: "429", err: &csghub.HTTPError{StatusCode: http.StatusTooManyRequests}, wantStatus: marketplaceArchStatusTransient},
		{name: "500", err: &csghub.HTTPError{StatusCode: http.StatusInternalServerError}, wantStatus: marketplaceArchStatusTransient},
		{name: "400", err: &csghub.HTTPError{StatusCode: http.StatusBadRequest}, wantStatus: marketplaceArchStatusMiss},
		{name: "403", err: &csghub.HTTPError{StatusCode: http.StatusForbidden}, wantStatus: marketplaceArchStatusMiss},
		{name: "404", err: &csghub.HTTPError{StatusCode: http.StatusNotFound}, wantStatus: marketplaceArchStatusMiss},
		{name: "410", err: &csghub.HTTPError{StatusCode: http.StatusGone}, wantStatus: marketplaceArchStatusMiss},
		{name: "deadline", err: context.DeadlineExceeded, wantStatus: marketplaceArchStatusTransient},
		{name: "canceled", err: context.Canceled, wantStatus: marketplaceArchStatusTransient},
		{name: "net error", err: &net.DNSError{Err: "no such host", Name: "hub.example"}, wantStatus: marketplaceArchStatusTransient},
		{name: "local error", err: errors.New("invalid JSON"), wantStatus: marketplaceArchStatusMiss},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, isAuth := marketplaceLocalInferenceErrorStatus(tt.err)
			if status != tt.wantStatus || isAuth != tt.wantAuthErr {
				t.Fatalf("status = (%v, %v), want (%v, %v)", status, isAuth, tt.wantStatus, tt.wantAuthErr)
			}
		})
	}
}

func TestMarketplaceLocalInferenceFilterAppliesFrameworkFilter(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/safetensors", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/gguf", localInferenceTag("gguf", "framework")),
		localInferenceModel("acme/runtime-gguf", localInferenceTag("gguf", "runtime_framework")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.budgetLimit = 1

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16, Framework: "gguf"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 2 || result.Models[0].Path != "acme/gguf" || result.Models[1].Path != "acme/runtime-gguf" {
		t.Fatalf("models = %v, want acme/gguf and acme/runtime-gguf", result.Models)
	}
	if reg.readCount() != 0 {
		t.Fatalf("config reads = %d, want 0 (filtered model must not consume budget)", reg.readCount())
	}
}

func TestMarketplaceLocalInferenceFilterPagination(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := make([]csghub.Model, 0, 40)
	for i := 0; i < 40; i++ {
		models = append(models, localInferenceModel("acme/model-"+strconv.Itoa(i), localInferenceTag("gguf", "framework")))
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 2, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 16 || result.Models[0].Path != "acme/model-16" || result.Models[15].Path != "acme/model-31" {
		t.Fatalf("models = %v, want models 16..31", result.Models)
	}
	if !result.TotalExact || result.Total != 40 || result.Partial {
		t.Fatalf("result = %#v, want exact total 40", result)
	}
}

func TestMarketplaceLocalInferenceFilterHandlesClampedUpstreamPages(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	pageOne := make([]csghub.Model, 0, 50)
	for i := 0; i < 50; i++ {
		pageOne = append(pageOne, localInferenceModel("acme/video-"+strconv.Itoa(i), localInferenceTag("gguf", "framework"), localInferenceTag("text-to-video", "task")))
	}
	pageTwo := []csghub.Model{
		localInferenceModel("acme/video-last", localInferenceTag("gguf", "framework"), localInferenceTag("text-to-video", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: pageOne, 2: pageTwo},
		total:  0,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.listCalls != 2 {
		t.Fatalf("list calls = %d, want 2 (first page must not look exhausted)", reg.listCalls)
	}
}

func TestMarketplaceLocalInferenceFilterUpstreamExhausted(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := make([]csghub.Model, 0, 10)
	for i := 0; i < 10; i++ {
		models = append(models, localInferenceModel("acme/model-"+strconv.Itoa(i), localInferenceTag("gguf", "framework")))
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !result.TotalExact || result.Total != 10 || result.Partial {
		t.Fatalf("result = %#v, want exact total 10", result)
	}
}

func TestMarketplaceLocalInferenceFilterTotalPastLastPage(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := make([]csghub.Model, 0, 10)
	for i := 0; i < 10; i++ {
		models = append(models, localInferenceModel("acme/model-"+strconv.Itoa(i), localInferenceTag("gguf", "framework")))
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 2, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 0 {
		t.Fatalf("models = %v, want an empty page", result.Models)
	}
	if !result.TotalExact || result.Total != 10 || result.Partial {
		t.Fatalf("result = %#v, want exact total 10", result)
	}
}

func TestMarketplaceLocalInferenceFilterSecondPageError(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	pageOne := make([]csghub.Model, 0, 16)
	for i := 0; i < 16; i++ {
		pageOne = append(pageOne, localInferenceModel("acme/video-"+strconv.Itoa(i), localInferenceTag("gguf", "framework"), localInferenceTag("text-to-video", "task")))
	}
	reg := &fakeLocalInferenceRegistry{
		source:   modelregistry.SourceOpenCSG,
		pages:    map[int][]csghub.Model{1: pageOne},
		total:    100,
		listErrs: map[int]error{2: &csghub.HTTPError{StatusCode: http.StatusInternalServerError}},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !result.Degraded || !result.Partial || result.TotalExact {
		t.Fatalf("result = %#v, want degraded partial lower bound", result)
	}
}

func TestMarketplaceLocalInferenceFilterFirstPageError(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source:   modelregistry.SourceOpenCSG,
		listErrs: map[int]error{1: &csghub.HTTPError{StatusCode: http.StatusInternalServerError}},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err == nil {
		t.Fatal("list error = nil, want first-page error")
	}
}

func TestMarketplaceLocalInferenceFilterReadsConfigWithoutRevision(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages: map[int][]csghub.Model{1: {
			localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework")),
		}},
		total: 1,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list: %v", err)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if len(reg.readRevisions) != 1 || reg.readRevisions[0] != "" {
		t.Fatalf("revisions = %v, want one empty revision", reg.readRevisions)
	}
}

func TestMarketplaceLocalInferenceFilterRepoNameFallback(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		{Path: "acme/flux-turbo", Tags: []csghub.Tag{localInferenceTag("gguf", "framework")}},
		localInferenceModel("flux/plain-model", localInferenceTag("gguf", "framework")),
		{
			Name: "flux/plain-gguf",
			Path: "acme/plain-gguf",
			Tags: []csghub.Tag{localInferenceTag("gguf", "framework")},
		},
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"flux/plain-model", "acme/plain-gguf"}
	if len(result.Models) != len(want) {
		t.Fatalf("models = %v, want %v", result.Models, want)
	}
	for i := range want {
		if result.Models[i].Path != want[i] {
			t.Fatalf("models = %v, want %v", result.Models, want)
		}
	}
}

func TestMarketplaceLocalInferenceEnabled(t *testing.T) {
	tests := map[string]bool{
		"true": true, "TRUE": true, "1": true, " true ": true,
		"0": false, "false": false, "": false, "yes": false,
	}
	for value, want := range tests {
		if got := marketplaceLocalInferenceEnabled(value); got != want {
			t.Fatalf("marketplaceLocalInferenceEnabled(%q) = %v, want %v", value, got, want)
		}
	}
}

type marketplaceLocalInferenceListResponse struct {
	Data       []csghub.Model `json:"data"`
	Total      int            `json:"total"`
	HasMore    bool           `json:"has_more"`
	TotalExact bool           `json:"total_exact"`
	Partial    *bool          `json:"partial"`
}

func newLocalInferenceUpstream(t *testing.T, models []csghub.Model, raw func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *int) {
	t.Helper()
	listCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/models/local_filter":
			// These tests exercise the scan fallback.
			http.Error(w, "not found", http.StatusNotFound)
		case r.URL.Path == "/api/v1/models":
			listCalls++
			_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
				Msg:   "OK",
				Data:  models,
				Total: len(models),
			})
		case raw != nil:
			raw(w, r)
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server, &listCalls
}

func TestHandleMarketplaceModelsLocalInferenceParam(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	models := []csghub.Model{localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework"))}
	server, _ := newLocalInferenceUpstream(t, models, nil)
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	tests := []struct {
		query       string
		wantPartial bool
	}{
		{query: "local_inference=true", wantPartial: true},
		{query: "local_inference=1", wantPartial: true},
		{query: "local_inference=0", wantPartial: false},
		{query: "local_inference=false", wantPartial: false},
		{query: "", wantPartial: false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			clearMarketplaceCache()
			req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?"+tt.query, nil)
			w := httptest.NewRecorder()
			s.handleMarketplaceModels(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
			}
			var resp marketplaceLocalInferenceListResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if (resp.Partial != nil) != tt.wantPartial {
				t.Fatalf("partial present = %v, want %v", resp.Partial != nil, tt.wantPartial)
			}
			if len(resp.Data) != 1 || !resp.TotalExact || resp.Total != 1 || resp.HasMore {
				t.Fatalf("response = %#v, want one exact model", resp)
			}
		})
	}
}

func TestHandleMarketplaceModelsLocalInferenceCombinesFrameworkFilter(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	// The fake upstream ignores the framework filter, like OpenCSG sometimes
	// does. The local filter must still drop the safetensors model without
	// probing it.
	models := []csghub.Model{
		localInferenceModel("acme/safe", localInferenceTag("safetensors", "framework")),
		localInferenceModel("acme/gguf", localInferenceTag("gguf", "framework")),
	}
	server, _ := newLocalInferenceUpstream(t, models, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected config read for %s", r.URL.Path)
	})
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&framework=gguf", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	var resp marketplaceLocalInferenceListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Path != "acme/gguf" {
		t.Fatalf("models = %#v, want only acme/gguf", resp.Data)
	}
}

func TestHandleMarketplaceModelsLocalInferenceRejectsLargePer(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	models := []csghub.Model{localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework"))}
	server, _ := newLocalInferenceUpstream(t, models, nil)
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	tests := []struct {
		per      string
		wantCode int
	}{
		{per: "50", wantCode: http.StatusOK},
		{per: "51", wantCode: http.StatusBadRequest},
		{per: "100", wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run("per="+tt.per, func(t *testing.T) {
			clearMarketplaceCache()
			req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&per="+tt.per, nil)
			w := httptest.NewRecorder()
			s.handleMarketplaceModels(w, req)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d body=%s", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

func TestHandleMarketplaceModelsLocalInferenceSkipsResponseCacheWhenDegraded(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	models := []csghub.Model{localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework"))}
	server, listCalls := newLocalInferenceUpstream(t, models, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
		w := httptest.NewRecorder()
		s.handleMarketplaceModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 body=%s", i, w.Code, w.Body.String())
		}
		if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != "miss" {
			t.Fatalf("request %d cache header = %q, want miss", i, got)
		}
		var resp marketplaceLocalInferenceListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Partial == nil || !*resp.Partial {
			t.Fatalf("request %d partial = %v, want true", i, resp.Partial)
		}
	}
	if *listCalls != 2 {
		t.Fatalf("list calls = %d, want 2 (degraded result must not be cached)", *listCalls)
	}
}

func TestHandleMarketplaceModelsLocalInferenceReportsPartialWhenBudgetRunsOut(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	models := make([]csghub.Model, 0, 130)
	for i := 0; i < 130; i++ {
		models = append(models, localInferenceModel("acme/model-"+strconv.Itoa(i),
			localInferenceTag("safetensors", "framework"),
			localInferenceTag("text-generation", "task")))
	}
	server, _ := newLocalInferenceUpstream(t, models, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
		w := httptest.NewRecorder()
		s.handleMarketplaceModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 body=%s", i, w.Code, w.Body.String())
		}
		var resp marketplaceLocalInferenceListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != "miss" {
			t.Fatalf("request %d cache header = %q, want miss (a degraded result must not be cached)", i, got)
		}
		if i > 0 {
			// The second request reads the negative arch cache entries, so it no
			// longer runs out of budget; only the first request is partial.
			continue
		}
		if resp.TotalExact {
			t.Fatalf("total_exact = true, want false when the probe budget ran out")
		}
		if resp.HasMore {
			t.Fatalf("has_more = true, want false for an empty partial page")
		}
		if resp.Partial == nil || !*resp.Partial {
			t.Fatalf("partial = %v, want true", resp.Partial)
		}
	}
}

func TestHandleMarketplaceModelsLocalInferenceCachesCompleteResponse(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	models := []csghub.Model{localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework"))}
	server, listCalls := newLocalInferenceUpstream(t, models, nil)
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
		w := httptest.NewRecorder()
		s.handleMarketplaceModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 body=%s", i, w.Code, w.Body.String())
		}
		wantCache := "miss"
		if i > 0 {
			wantCache = "fresh"
		}
		if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != wantCache {
			t.Fatalf("request %d cache header = %q, want %q", i, got, wantCache)
		}
	}
	if *listCalls != 1 {
		t.Fatalf("list calls = %d, want 1 (complete result is cached)", *listCalls)
	}
}

func TestHandleMarketplaceModelsLocalInferencePassesFiltersUpstream(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  []csghub.Model{},
			Total: 0,
		})
	}))
	defer server.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&search=llama&sort=most_download&framework=gguf&task=text-generation&model_params_min=6&model_params_max=7&source=opencsg", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}

	values, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse upstream query: %v", err)
	}
	for key, want := range map[string]string{
		"search":           "llama",
		"sort":             "most_download",
		"tag_category":     "task",
		"tag_name":         "text-generation",
		"model_params_min": "6",
		"model_params_max": "7",
		"source":           "opencsg",
		"per":              "64",
	} {
		if got := values.Get(key); got != want {
			t.Fatalf("upstream %s = %q, want %q", key, got, want)
		}
	}
}

func TestHandleMarketplaceModelsLocalInferencePagesInMemory(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	models := make([]csghub.Model, 0, 32)
	for i := 0; i < 32; i++ {
		models = append(models, localInferenceModel("acme/gguf-"+strconv.Itoa(i), localInferenceTag("gguf", "framework")))
	}
	server, _ := newLocalInferenceUpstream(t, models, nil)
	s := newTestServer(t)
	s.cfg.ServerURL = server.URL

	tests := []struct {
		page      string
		wantFirst string
		wantLast  string
		wantMore  bool
	}{
		{page: "1", wantFirst: "acme/gguf-0", wantLast: "acme/gguf-15", wantMore: true},
		{page: "2", wantFirst: "acme/gguf-16", wantLast: "acme/gguf-31", wantMore: false},
	}
	for _, tt := range tests {
		t.Run("page="+tt.page, func(t *testing.T) {
			clearMarketplaceCache()
			req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&page="+tt.page+"&per=16", nil)
			w := httptest.NewRecorder()
			s.handleMarketplaceModels(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
			}
			var resp marketplaceLocalInferenceListResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(resp.Data) != 16 {
				t.Fatalf("models = %d, want 16", len(resp.Data))
			}
			first, last := resp.Data[0].Path, resp.Data[len(resp.Data)-1].Path
			if first != tt.wantFirst || last != tt.wantLast {
				t.Fatalf("models = %s..%s, want %s..%s", first, last, tt.wantFirst, tt.wantLast)
			}
			if resp.Total != 32 || !resp.TotalExact || resp.HasMore != tt.wantMore {
				t.Fatalf("response = %#v, want total=32 exact has_more=%v", resp, tt.wantMore)
			}
			if resp.Partial == nil || *resp.Partial {
				t.Fatalf("partial = %v, want false", resp.Partial)
			}
		})
	}
}

func TestMarketplaceLocalInferenceListAndDetailAgree(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	byPath := map[string]csghub.Model{
		"acme/mixed": localInferenceModel("acme/mixed",
			localInferenceTag("safetensors", "framework"),
			localInferenceTag("gguf", "framework"),
			localInferenceTag("image-text-to-text", "task")),
		"acme/gguf": localInferenceModel("acme/gguf", localInferenceTag("gguf", "framework")),
		"acme/convertible": localInferenceModel("acme/convertible",
			localInferenceTag("safetensors", "framework"),
			localInferenceTag("text-generation", "task")),
		"acme/video": localInferenceModel("acme/video",
			localInferenceTag("gguf", "framework"),
			localInferenceTag("text-to-video", "task")),
		"acme/unknown": localInferenceModel("acme/unknown", localInferenceTag("text-generation", "task")),
		"acme/flux":    localInferenceModel("acme/flux", localInferenceTag("gguf", "framework")),
	}
	convertible := byPath["acme/convertible"]
	convertible.Metadata.Architecture = "LlamaForCausalLM"
	byPath["acme/convertible"] = convertible

	list := []csghub.Model{
		byPath["acme/mixed"], byPath["acme/gguf"], byPath["acme/convertible"],
		byPath["acme/video"], byPath["acme/unknown"], byPath["acme/flux"],
	}

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/models/local_filter":
			// This test exercises the scan fallback.
			http.Error(w, "not found", http.StatusNotFound)
		case r.URL.Path == "/api/v1/models":
			_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{Msg: "OK", Data: list, Total: len(list)})
		case strings.HasSuffix(r.URL.Path, "/tree"):
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{Msg: "OK"})
		case strings.HasPrefix(r.URL.Path, "/api/v1/models/"):
			model, ok := byPath[strings.TrimPrefix(r.URL.Path, "/api/v1/models/")]
			if !ok {
				t.Fatalf("unexpected model detail path: %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{Msg: "OK", Data: model})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&per=16", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	var listResp marketplaceLocalInferenceListResponse
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data) != 3 {
		t.Fatalf("models = %#v, want mixed/gguf/convertible", listResp.Data)
	}

	for _, model := range listResp.Data {
		namespace, name, _ := strings.Cut(model.Path, "/")
		detailReq := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/"+model.Path, nil)
		detailReq.SetPathValue("namespace", namespace)
		detailReq.SetPathValue("name", name)
		detailW := httptest.NewRecorder()
		s.handleMarketplaceModelDetail(detailW, detailReq)
		if detailW.Code != http.StatusOK {
			t.Fatalf("%s detail status = %d, want 200 body=%s", model.Path, detailW.Code, detailW.Body.String())
		}
		var detail marketplaceModelDetailResponse
		if err := json.NewDecoder(detailW.Body).Decode(&detail); err != nil {
			t.Fatalf("%s decode detail: %v", model.Path, err)
		}
		if !detail.LocalInference.Supported {
			t.Fatalf("%s kept by the list but the detail says %#v", model.Path, detail.LocalInference)
		}
	}
}

func TestHandleMarketplaceModelsLocalInferenceOpenCSGScanLimitIsPartial(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	const upstreamTotal = 100000
	pages := make(map[string][]csghub.Model, marketplaceLocalInferenceScanPageLimit)
	for page := 1; page <= marketplaceLocalInferenceScanPageLimit; page++ {
		batch := make([]csghub.Model, 0, marketplaceLocalInferenceMinScanPer)
		for i := 0; i < marketplaceLocalInferenceMinScanPer; i++ {
			batch = append(batch, localInferenceModel(
				"acme/video-"+strconv.Itoa(page)+"-"+strconv.Itoa(i),
				localInferenceTag("gguf", "framework"),
				localInferenceTag("text-to-video", "task"),
			))
		}
		pages[strconv.Itoa(page)] = batch
	}

	listCalls := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models/local_filter" {
			// This test exercises the scan fallback.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		listCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  pages[r.URL.Query().Get("page")],
			Total: upstreamTotal,
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&per=16", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}

	var resp marketplaceLocalInferenceListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if listCalls != marketplaceLocalInferenceScanPageLimit {
		t.Fatalf("upstream list calls = %d, want %d (scan must stop at the page limit, not early)", listCalls, marketplaceLocalInferenceScanPageLimit)
	}
	if len(resp.Data) != 0 || resp.Total != 0 {
		t.Fatalf("response = %#v, want no matches with a lower-bound total", resp)
	}
	if resp.TotalExact || resp.HasMore {
		t.Fatalf("response = %#v, want total_exact=false and has_more=false", resp)
	}
	if resp.Partial == nil || !*resp.Partial {
		t.Fatalf("partial = %v, want true: only the first %d of %d upstream models were scanned", resp.Partial, marketplaceLocalInferenceScanPageLimit*marketplaceLocalInferenceMinScanPer, upstreamTotal)
	}
}

func TestMarketplaceLocalInferenceCacheKeyScopes(t *testing.T) {
	updated := time.Unix(1700000000, 0)
	model := csghub.Model{Path: "/acme/model/", UpdatedAt: updated}

	key := marketplaceLocalInferenceCacheKey(modelregistry.SourceOpenCSG, model)
	if want := "opencsg|acme/model|1700000000"; key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
	if other := marketplaceLocalInferenceCacheKey(modelregistry.SourceModelScope, model); other == key {
		t.Fatalf("key = %q, want a source-scoped key", other)
	}
	bumped := model
	bumped.UpdatedAt = updated.Add(time.Hour)
	if other := marketplaceLocalInferenceCacheKey(modelregistry.SourceOpenCSG, bumped); other == key {
		t.Fatalf("key = %q, want an updated_at-scoped key", other)
	}
	if empty := marketplaceLocalInferenceCacheKey(modelregistry.SourceOpenCSG, csghub.Model{}); empty != "" {
		t.Fatalf("key = %q, want empty for a model without a path", empty)
	}
}

func TestMarketplaceArchCacheEvictsOldestEntry(t *testing.T) {
	cache := newMarketplaceArchCache()
	base := time.Unix(1700000000, 0)
	for i := 0; i < marketplaceLocalInferenceCacheLimit; i++ {
		cache.set(strconv.Itoa(i), marketplaceArchEntry{
			architecture: "arch-" + strconv.Itoa(i),
			writtenAt:    base.Add(time.Duration(i) * time.Second),
			status:       marketplaceArchStatusHit,
		})
	}
	cache.set("newest", marketplaceArchEntry{
		architecture: "newest",
		writtenAt:    base.Add(time.Hour),
		status:       marketplaceArchStatusHit,
	})

	if _, ok := cache.get("0", base.Add(time.Minute)); ok {
		t.Fatalf("oldest entry survived eviction")
	}
	if entry, ok := cache.get("1", base.Add(time.Minute)); !ok || entry.architecture != "arch-1" {
		t.Fatalf("entry 1 = %#v, ok=%v; want the second-oldest entry to stay", entry, ok)
	}
	if entry, ok := cache.get("newest", base.Add(time.Hour)); !ok || entry.architecture != "newest" {
		t.Fatalf("newest entry = %#v, ok=%v", entry, ok)
	}
}

func TestHandleMarketplaceModelsLocalInferenceServesStaleCacheOnUpstreamFailure(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	listCalls := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models/local_filter" {
			// This test exercises the scan fallback.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		listCalls++
		if listCalls > 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  []csghub.Model{localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework"))},
			Total: 1,
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&per=16", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200 body=%s", w.Code, w.Body.String())
	}

	// Age the cached payload past the fresh TTL so the next request reaches the
	// upstream and has to fall back to the stale copy.
	cacheKey := marketplaceCacheKey("models", req.URL.RawQuery+marketplaceHardwareFingerprint(s.marketplaceResourceLimits()))
	marketplaceListCache.Lock()
	entry := marketplaceListCache.entries[cacheKey]
	entry.updatedAt = time.Now().Add(-marketplaceListCacheTTL - time.Minute)
	marketplaceListCache.entries[cacheKey] = entry
	marketplaceListCache.Unlock()

	req = httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&per=16", nil)
	w = httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second status = %d, want the stale cache body, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != "stale" {
		t.Fatalf("cache header = %q, want stale", got)
	}
	var resp marketplaceLocalInferenceListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode stale response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Path != "acme/qwen-gguf" || resp.Partial == nil {
		t.Fatalf("stale response = %#v, want the cached local-inference payload", resp)
	}
}

func TestMarketplaceLocalInferenceSizeThreshold(t *testing.T) {
	tests := []struct {
		name   string
		limits marketplaceLocalInferenceLimits
		mode   string
		want   int64
	}{
		{name: "convert disk halves", limits: marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30}, mode: "convert", want: 100 << 30},
		{name: "image disk is one copy", limits: marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30}, mode: "image", want: 200 << 30},
		{name: "memory wins", limits: marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 8 << 30}, mode: "convert", want: 8 << 30},
		{name: "memory only", limits: marketplaceLocalInferenceLimits{maxMemoryBytes: 8 << 30}, mode: "image", want: 8 << 30},
		{name: "disk only", limits: marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30}, mode: "asr", want: 200 << 30},
		{name: "no limits", mode: "convert", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marketplaceLocalInferenceSizeThreshold(tt.limits, tt.mode); got != tt.want {
				t.Fatalf("threshold = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMarketplaceLocalInferenceSizeAllowed(t *testing.T) {
	const gib = int64(1 << 30)
	base := marketplaceLocalInferenceLimits{maxDownloadBytes: 200 * gib, maxMemoryBytes: 64 * gib}
	roomy := marketplaceLocalInferenceLimits{maxDownloadBytes: 200 * gib, maxMemoryBytes: 256 * gib}
	tests := []struct {
		name        string
		support     api.LocalInferenceSupport
		size        int64
		params      float64
		paramsValid *bool
		limits      marketplaceLocalInferenceLimits
		want        bool
	}{
		{name: "unsupported", support: api.LocalInferenceSupport{Mode: "none"}, limits: base},
		{
			name:    "gguf direct ignores limits",
			support: api.LocalInferenceSupport{Supported: true, Mode: "direct"},
			size:    5000 * gib,
			limits:  base,
			want:    true,
		},
		{name: "convert fits", support: api.LocalInferenceSupport{Supported: true, Mode: "convert"}, size: 40 * gib, limits: base, want: true},
		{name: "convert over memory", support: api.LocalInferenceSupport{Supported: true, Mode: "convert"}, size: 65 * gib, limits: base},
		{name: "convert over disk", support: api.LocalInferenceSupport{Supported: true, Mode: "convert"}, size: 120 * gib, limits: roomy},
		{name: "convert at disk boundary", support: api.LocalInferenceSupport{Supported: true, Mode: "convert"}, size: 100 * gib, limits: roomy, want: true},
		{name: "image downloads once", support: api.LocalInferenceSupport{Supported: true, Mode: "image"}, size: 180 * gib, limits: roomy, want: true},
		{name: "image over disk", support: api.LocalInferenceSupport{Supported: true, Mode: "image"}, size: 220 * gib, limits: roomy},
		{name: "unknown size stays", support: api.LocalInferenceSupport{Supported: true, Mode: "convert"}, limits: base, want: true},
		{name: "no limits stays", support: api.LocalInferenceSupport{Supported: true, Mode: "convert"}, size: 5000 * gib, want: true},
		{
			name:    "params fallback over memory",
			support: api.LocalInferenceSupport{Supported: true, Mode: "convert"},
			params:  100,
			limits:  base,
		},
		{
			name:    "params fallback fits",
			support: api.LocalInferenceSupport{Supported: true, Mode: "convert"},
			params:  7,
			limits:  base,
			want:    true,
		},
		{
			name:        "invalid params are ignored",
			support:     api.LocalInferenceSupport{Supported: true, Mode: "convert"},
			params:      2446.18,
			paramsValid: boolPointer(false),
			limits:      base,
			want:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := csghub.Model{RepoSize: tt.size}
			m.Metadata.ModelParams = tt.params
			m.Metadata.ModelParamsValid = tt.paramsValid
			if got := marketplaceLocalInferenceSizeAllowed(tt.support, m, tt.limits); got != tt.want {
				t.Fatalf("size allowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func boolPointer(v bool) *bool {
	return &v
}

func TestMarketplaceLocalInferenceModelBytesIgnoresInvalidParams(t *testing.T) {
	const gib = int64(1 << 30)
	tests := []struct {
		name        string
		size        int64
		params      float64
		paramsValid *bool
		want        int64
	}{
		{name: "repo size wins", size: 3 * gib, params: 100, want: 3 * gib},
		{name: "trusted params", params: 7, paramsValid: boolPointer(true), want: 14_000_000_000},
		{name: "missing flag keeps old payloads working", params: 7, want: 14_000_000_000},
		{name: "invalid params are ignored", params: 2446.18, paramsValid: boolPointer(false), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := csghub.Model{RepoSize: tt.size}
			m.Metadata.ModelParams = tt.params
			m.Metadata.ModelParamsValid = tt.paramsValid
			if got := marketplaceLocalInferenceModelBytes(m); got != tt.want {
				t.Fatalf("model bytes = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMarketplaceLocalInferenceFilterSkipsOversizedConvertModels(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	const oversized = int64(4_892_388_876_361) // 4.4 TiB snapshot
	models := []csghub.Model{
		localInferenceModel("acme/huge-convert", localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task")),
		localInferenceModel("acme/small-convert", localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task")),
		localInferenceModel("acme/huge-gguf", localInferenceTag("gguf", "framework"), localInferenceTag("text-generation", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		sizes: map[string]int64{
			"acme/huge-convert":  oversized,
			"acme/small-convert": 40 << 30,
			"acme/huge-gguf":     oversized,
		},
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["Qwen3ForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 64 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		got = append(got, m.Path)
	}
	want := []string{"acme/small-convert", "acme/huge-gguf"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %v, want %v", got, want)
		}
	}
	if reg.sizeCalls != 1 {
		t.Fatalf("size calls = %d, want 1 (one batch call, GGUF skipped)", reg.sizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterKeepsModelsWhenSizesFail(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/huge-convert", localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source:  modelregistry.SourceOpenCSG,
		pages:   map[int][]csghub.Model{1: models},
		total:   len(models),
		sizeErr: errors.New("extras unavailable"),
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["Qwen3ForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 64 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].Path != "acme/huge-convert" {
		t.Fatalf("models = %v, want the model kept when sizes are unavailable", result.Models)
	}
	if result.Degraded {
		t.Fatal("degraded = true, want false (an unknown size is not a partial scan)")
	}
}

func TestMarketplaceLocalInferenceFilterUsesFileSizes(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/huge-image", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/small-image", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		fileSizes: map[string]int64{
			"acme/huge-image":  40 << 30,
			"acme/small-image": 2 << 30,
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 8 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].Path != "acme/small-image" {
		t.Fatalf("models = %v, want only the model that fits", result.Models)
	}
	if reg.fileSizeCalls != 1 {
		t.Fatalf("file size calls = %d, want 1", reg.fileSizeCalls)
	}

	// A second scan reuses the cached sizes instead of walking the tree again.
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.fileSizeCalls != 1 {
		t.Fatalf("file size calls = %d, want 1 (cached)", reg.fileSizeCalls)
	}

	// The cache key carries updated_at and the entry expires after six hours.
	clock = clock.Add(7 * time.Hour)
	if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.fileSizeCalls != 2 {
		t.Fatalf("file size calls = %d, want 2 (expired cache)", reg.fileSizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterFileSizeBudget(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/huge-one", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/huge-two", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		fileSizes: map[string]int64{
			"acme/huge-one": 40 << 30,
			"acme/huge-two": 40 << 30,
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 8 << 30}
	filter.fileSizeBudgetLimit = 1

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// The first model is sized and rejected; the second keeps an unknown size
	// and stays allowed, which is the documented fallback when the budget runs out.
	if len(result.Models) != 1 || result.Models[0].Path != "acme/huge-two" {
		t.Fatalf("models = %v, want the unsized model kept", result.Models)
	}
	if reg.fileSizeCalls != 1 {
		t.Fatalf("file size calls = %d, want 1", reg.fileSizeCalls)
	}
}

func TestMarketplaceResourceLimitsFor(t *testing.T) {
	probes := 0
	probe := func() marketplaceLocalInferenceLimits {
		probes++
		return marketplaceLocalInferenceLimits{maxDownloadBytes: 100, maxMemoryBytes: 200}
	}

	if got := marketplaceResourceLimitsFor(true, probe); got.maxDownloadBytes != 0 || got.maxMemoryBytes != 0 || len(got.gpus) != 0 || got.ramBytes != 0 {
		t.Fatalf("cluster limits = %#v, want zero", got)
	}
	if probes != 0 {
		t.Fatalf("hardware probes = %d, want 0 in cluster mode", probes)
	}

	got := marketplaceResourceLimitsFor(false, probe)
	if got.maxDownloadBytes != 100 || got.maxMemoryBytes != 200 {
		t.Fatalf("limits = %#v, want the probed limits", got)
	}
	if probes != 1 {
		t.Fatalf("hardware probes = %d, want 1", probes)
	}
}

func TestMarketplaceLocalInferenceFilterSkipsOversizedImageModels(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	const oversized = int64(204_016_544_771) // MiniMax-H3-Image repository size
	models := []csghub.Model{
		localInferenceModel("acme/huge-image", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
		localInferenceModel("acme/small-image", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		sizes: map[string]int64{
			"acme/huge-image":  oversized,
			"acme/small-image": 10 << 30,
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 64 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].Path != "acme/small-image" {
		t.Fatalf("models = %v, want only the image model that fits", result.Models)
	}
	if reg.sizeCalls != 1 {
		t.Fatalf("size calls = %d, want 1", reg.sizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterCachesRepoSizes(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/image", localInferenceTag("safetensors", "framework"), localInferenceTag("text-to-image", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		sizes:  map[string]int64{"acme/image": 10 << 30},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 64 << 30}

	for i := 0; i < 2; i++ {
		if _, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16}); err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
	}
	if reg.sizeCalls != 1 {
		t.Fatalf("size calls = %d, want 1 (the second scan hits the size cache)", reg.sizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterUsesRepoSizeOverParameterEstimate(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	// 5.5B parameters estimate ~11 GiB, but the repository ships several
	// variants and is 43 GiB on disk.
	model := localInferenceModel("acme/flux-image", localInferenceTag("safetensors", "framework"), localInferenceTag("image-to-image", "task"))
	model.Metadata.ModelParams = 5.548681984
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: {model}},
		total:  1,
		sizes:  map[string]int64{"acme/flux-image": 43_317_651_057},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 16 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 0 {
		t.Fatalf("models = %v, want the 43 GiB repository excluded", result.Models)
	}
	if reg.sizeCalls != 1 {
		t.Fatalf("size calls = %d, want 1 (the parameter estimate is not enough)", reg.sizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterUsesFilledParameterEstimate(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	models := []csghub.Model{
		localInferenceModel("acme/huge-text", localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task")),
	}
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: models},
		total:  len(models),
		params: map[string]float64{"acme/huge-text": 684.53},
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["DeepseekV3ForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 2000 << 30, maxMemoryBytes: 16 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Models) != 0 {
		t.Fatalf("models = %v, want the 684B-parameter model excluded", result.Models)
	}
	if reg.sizeCalls != 1 {
		t.Fatalf("size calls = %d, want 1", reg.sizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterSkipsSizeFillForListedSizes(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	model := localInferenceModel("acme/llama", localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task"))
	model.Metadata.Architecture = "LlamaForCausalLM"
	model.RepoSize = 4 << 30
	reg := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		pages:  map[int][]csghub.Model{1: {model}},
		total:  1,
		sizes:  map[string]int64{"acme/llama": 8 << 30},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 100 << 30, maxMemoryBytes: 64 << 30}

	result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if reg.sizeCalls != 0 {
		t.Fatalf("size calls = %d, want 0 when the list already reports the size", reg.sizeCalls)
	}
	if len(result.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(result.Models))
	}
}

func TestMarketplaceLocalInferenceFilterMarksUnresolvedSizesUnknown(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	valid := true
	withParams := localInferenceModel("acme/params", localInferenceTag("safetensors", "framework"))
	withParams.Metadata.ModelParams = 7
	withParams.Metadata.ModelParamsValid = &valid
	withoutParams := localInferenceModel("acme/none", localInferenceTag("safetensors", "framework"))

	reg := &fakeLocalInferenceRegistry{
		source:    modelregistry.SourceOpenCSG,
		pages:     map[int][]csghub.Model{1: {withParams, withoutParams}},
		total:     2,
		sizeDelay: 200 * time.Millisecond,
		readFile: func(context.Context, string, string, string) (string, error) {
			return `{"architectures":["LlamaForCausalLM"]}`, nil
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.sizeTimeout = 50 * time.Millisecond
	filter.limits = marketplaceLocalInferenceLimits{maxDownloadBytes: 100 << 30, maxMemoryBytes: 100 << 30}

	for i := 0; i < 2; i++ {
		result, err := filter.list(context.Background(), modelregistry.ListOptions{Page: 1, PerPage: 16})
		if err != nil {
			t.Fatalf("list %d: %v", i, err)
		}
		if len(result.Models) != 2 {
			t.Fatalf("list %d models = %v, want both unknown-size models kept", i, result.Models)
		}
		if result.Degraded {
			t.Fatalf("list %d result = %#v, want no degradation", i, result)
		}
	}
	// The second scan must reuse both cache entries: the unresolved size is
	// remembered as unknown, and the parameter estimate is remembered as such.
	if reg.sizeCalls != 1 {
		t.Fatalf("size calls = %d, want 1 (unresolved sizes must not be re-fetched)", reg.sizeCalls)
	}

	unknownKey := marketplaceLocalInferenceCacheKey(reg.Source(), withoutParams)
	entry, ok := marketplaceLocalInferenceRepoSizeCache.get(unknownKey, clock)
	if !ok || !entry.unknown {
		t.Fatalf("cache entry = %#v (ok=%v), want an unknown marker for the parameter-less model", entry, ok)
	}
	paramsKey := marketplaceLocalInferenceCacheKey(reg.Source(), withParams)
	entry, ok = marketplaceLocalInferenceRepoSizeCache.get(paramsKey, clock)
	if !ok || entry.unknown || entry.modelParams != 7 {
		t.Fatalf("cache entry = %#v (ok=%v), want the parameter estimate instead of an unknown marker", entry, ok)
	}
}

func TestMarketplaceLocalInferenceRepoSizeCacheStates(t *testing.T) {
	clearMarketplaceLocalInferenceCache()
	now := time.Unix(1700000000, 0)
	cache := marketplaceLocalInferenceRepoSizeCache

	cache.set("acme/hit", marketplaceRepoSizeEntry{size: 4 << 30}, now)
	if entry, ok := cache.get("acme/hit", now.Add(time.Hour)); !ok || entry.size != 4<<30 {
		t.Fatalf("entry = %#v (ok=%v), want the cached size", entry, ok)
	}
	if _, ok := cache.get("acme/hit", now.Add(marketplaceLocalInferenceCacheHitTTL+time.Minute)); ok {
		t.Fatal("size entry must expire after the hit TTL")
	}

	cache.setUnknown("acme/unknown", now)
	if entry, ok := cache.get("acme/unknown", now.Add(time.Minute)); !ok || !entry.unknown {
		t.Fatalf("entry = %#v (ok=%v), want an unknown marker", entry, ok)
	}
	if _, ok := cache.get("acme/unknown", now.Add(marketplaceLocalInferenceSizeUnknownTTL+time.Minute)); ok {
		t.Fatal("unknown marker must expire after the short TTL")
	}

	cache.set("", marketplaceRepoSizeEntry{size: 1}, now)
	cache.set("acme/empty", marketplaceRepoSizeEntry{}, now)
	if _, ok := cache.get("acme/empty", now); ok {
		t.Fatal("an empty entry must not be stored")
	}
	cache.setUnknown("", now)
	if _, ok := cache.get("", now); ok {
		t.Fatal("an empty key must not be stored")
	}
}

func TestMarketplaceLocalInferenceRepoSizeCacheEvictsOldestEntry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cache := newMarketplaceRepoSizeCache()
	for i := 0; i < marketplaceLocalInferenceCacheLimit; i++ {
		cache.set("key-"+strconv.Itoa(i), marketplaceRepoSizeEntry{size: int64(i + 1)}, now.Add(time.Duration(i)*time.Millisecond))
	}
	cache.set("key-new", marketplaceRepoSizeEntry{size: 1}, now.Add(time.Hour))

	if _, ok := cache.get("key-0", now); ok {
		t.Fatal("the oldest entry must be evicted")
	}
	for _, key := range []string{"key-1", "key-new"} {
		if _, ok := cache.get(key, now); !ok {
			t.Fatalf("%s must be kept", key)
		}
	}
}

// plainLocalInferenceRegistry only implements modelregistry.Registry, so it
// hides the optional RepoSizeFiller of the wrapped registry.
type plainLocalInferenceRegistry struct {
	modelregistry.Registry
}

func TestMarketplaceLocalInferenceSizeFillSkipsUnsupportedPaths(t *testing.T) {
	inner := &fakeLocalInferenceRegistry{
		source: modelregistry.SourceOpenCSG,
		sizes:  map[string]int64{"acme/convert": 4 << 30},
	}
	filter := newMarketplaceLocalInferenceFilter(plainLocalInferenceRegistry{Registry: inner})
	if err := filter.fillRepoSizes(context.Background(), []csghub.Model{localInferenceModel("acme/convert")}); err != nil {
		t.Fatalf("fillRepoSizes: %v", err)
	}
	if err := filter.fillRepoSizes(context.Background(), nil); err != nil {
		t.Fatalf("fillRepoSizes empty: %v", err)
	}
	if inner.sizeCalls != 0 {
		t.Fatalf("size calls = %d, want 0 without a RepoSizeFiller", inner.sizeCalls)
	}

	// Zero limits mean the sizes are never used (cluster mode), so the scan
	// must not spend upstream calls on them.
	reg := &fakeLocalInferenceRegistry{source: modelregistry.SourceOpenCSG}
	clusterFilter := newMarketplaceLocalInferenceFilter(reg)
	candidates := []marketplaceLocalInferenceCandidate{{
		model:   localInferenceModel("acme/convert"),
		support: api.LocalInferenceSupport{Supported: true, Mode: "convert"},
	}}
	if err := clusterFilter.fillCandidateSizes(context.Background(), context.Background(), candidates); err != nil {
		t.Fatalf("fillCandidateSizes: %v", err)
	}
	if reg.sizeCalls != 0 {
		t.Fatalf("size calls = %d, want 0 with zero limits", reg.sizeCalls)
	}
}

func TestMarketplaceLocalInferenceFilterNormalizesDefaults(t *testing.T) {
	filter := &marketplaceLocalInferenceFilter{
		budgetLimit:         -1,
		sizeBudgetLimit:     -1,
		fileSizeBudgetLimit: -1,
	}
	filter.normalize()

	if filter.budgetLimit != 0 || filter.sizeBudgetLimit != 0 || filter.fileSizeBudgetLimit != 0 {
		t.Fatalf("negative budgets = %d/%d/%d, want 0", filter.budgetLimit, filter.sizeBudgetLimit, filter.fileSizeBudgetLimit)
	}
	if filter.cache != marketplaceLocalInferenceArchCache {
		t.Fatal("normalize must fall back to the shared arch cache")
	}
	if filter.now == nil {
		t.Fatal("normalize must set a clock")
	}
	if filter.concurrency != marketplaceLocalInferenceProbeConcurrency {
		t.Fatalf("concurrency = %d, want %d", filter.concurrency, marketplaceLocalInferenceProbeConcurrency)
	}
	if filter.probeTimeout != marketplaceLocalInferenceProbeTimeout {
		t.Fatalf("probeTimeout = %v, want %v", filter.probeTimeout, marketplaceLocalInferenceProbeTimeout)
	}
	if filter.scanPageLimit != marketplaceLocalInferenceScanPageLimit {
		t.Fatalf("scanPageLimit = %d, want %d", filter.scanPageLimit, marketplaceLocalInferenceScanPageLimit)
	}
	if filter.scanTimeout != marketplaceLocalInferenceScanTimeout {
		t.Fatalf("scanTimeout = %v, want %v", filter.scanTimeout, marketplaceLocalInferenceScanTimeout)
	}
	if filter.sizeTimeout != marketplaceLocalInferenceSizeTimeout {
		t.Fatalf("sizeTimeout = %v, want %v", filter.sizeTimeout, marketplaceLocalInferenceSizeTimeout)
	}
}

func TestCachedLocalInferenceLimitsFollowsProbedDirectory(t *testing.T) {
	resetLimits := func() {
		marketplaceLocalInferenceLimitsMu.Lock()
		defer marketplaceLocalInferenceLimitsMu.Unlock()
		marketplaceLocalInferenceLimitsAt = time.Time{}
		marketplaceLocalInferenceLimitsDir = ""
		marketplaceLocalInferenceLimitsCached = marketplaceLocalInferenceLimits{}
	}
	resetLimits()
	t.Cleanup(resetLimits)

	dir := t.TempDir()
	if got := cachedLocalInferenceLimits(dir); got.maxDownloadBytes <= 0 {
		t.Fatalf("disk limit = %d, want a positive value from the real probe", got.maxDownloadBytes)
	}

	// Poison the cache: a hit within the TTL must return the stored value
	// instead of probing again.
	marketplaceLocalInferenceLimitsMu.Lock()
	marketplaceLocalInferenceLimitsCached = marketplaceLocalInferenceLimits{maxDownloadBytes: 42}
	marketplaceLocalInferenceLimitsMu.Unlock()
	if got := cachedLocalInferenceLimits(dir); got.maxDownloadBytes != 42 {
		t.Fatalf("disk limit = %d, want the cached 42", got.maxDownloadBytes)
	}

	// A different directory (ModelDir changed at runtime) must not reuse the
	// limits of the old volume.
	if got := cachedLocalInferenceLimits(t.TempDir()); got.maxDownloadBytes == 42 {
		t.Fatal("cache must follow the probed directory")
	} else if got.maxDownloadBytes <= 0 {
		t.Fatalf("disk limit = %d, want a positive value after re-probing", got.maxDownloadBytes)
	}
}

func TestMarketplaceHardwareFingerprint(t *testing.T) {
	a := marketplaceLocalInferenceLimits{
		maxDownloadBytes: 10,
		maxMemoryBytes:   20,
		gpus: []marketplaceLocalInferenceGPU{
			{vendor: "nvidia", vramBytes: 8},
			{vendor: "amd", vramBytes: 16},
		},
	}
	b := marketplaceLocalInferenceLimits{
		maxDownloadBytes: 10,
		maxMemoryBytes:   20,
		gpus: []marketplaceLocalInferenceGPU{
			{vendor: "amd", vramBytes: 16},
			{vendor: "nvidia", vramBytes: 8},
		},
	}
	if marketplaceHardwareFingerprint(a) != marketplaceHardwareFingerprint(b) {
		t.Fatal("fingerprint must not depend on probe order")
	}
	c := a
	c.maxDownloadBytes++
	if marketplaceHardwareFingerprint(a) == marketplaceHardwareFingerprint(c) {
		t.Fatal("fingerprint must change with the disk budget")
	}
	d := a
	d.gpus = append([]marketplaceLocalInferenceGPU(nil), a.gpus...)
	d.gpus[0].vramBytes *= 2
	if marketplaceHardwareFingerprint(a) == marketplaceHardwareFingerprint(d) {
		t.Fatal("fingerprint must change with the GPU memory")
	}
}

func TestHandleMarketplaceModelsLocalInferenceDoesNotCacheCandidateTimeout(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/local_filter" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"msg": "OK",
			"data": csghub.LocalModelFilterResponse{
				Data:              []csghub.Model{localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework"))},
				TotalConfirmed:    1,
				LocalCheckTimeout: true,
			},
		})
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = upstream.URL

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
		w := httptest.NewRecorder()
		s.handleMarketplaceModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 body=%s", i, w.Code, w.Body.String())
		}
		var resp marketplaceLocalInferenceListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("request %d decode: %v", i, err)
		}
		if resp.Partial != nil && *resp.Partial {
			t.Fatalf("request %d partial = %v, want false: a candidate timeout leaves the page intact", i, resp.Partial)
		}
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2: a candidate timeout must not be cached", calls)
	}
}

func TestHandleMarketplaceModelsLocalInferenceStaleCacheSurvivesHardwareChange(t *testing.T) {
	clearMarketplaceCache()
	clearMarketplaceLocalInferenceCache()

	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/local_filter" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		calls++
		if calls > 1 {
			// 429 is surfaced instead of falling back to the scan path, so the
			// stale cache is the only way to serve this request.
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"msg": "OK",
			"data": csghub.LocalModelFilterResponse{
				Data:           []csghub.Model{localInferenceModel("acme/qwen-gguf", localInferenceTag("gguf", "framework"))},
				TotalConfirmed: 1,
				TotalExact:     true,
			},
		})
	}))
	defer upstream.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = upstream.URL
	s.localInferenceLimits = func() marketplaceLocalInferenceLimits {
		return marketplaceLocalInferenceLimits{maxDownloadBytes: 100 << 30, maxMemoryBytes: 32 << 30}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200 body=%s", w.Code, w.Body.String())
	}

	// A re-probe reports a different disk, so the exact cache key changes. The
	// stale fallback must still find the response written under the old
	// fingerprint instead of surfacing the upstream failure.
	s.localInferenceLimits = func() marketplaceLocalInferenceLimits {
		return marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 32 << 30}
	}
	req = httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
	w = httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second status = %d, want the stale cache body, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != "stale" {
		t.Fatalf("cache header = %q, want stale", got)
	}
}

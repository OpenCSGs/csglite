package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/modelregistry"
)

// fakeLocalModelFilterRegistry implements both the scan registry and the optional
// local-model-filter endpoint so a test can drive either path.
type fakeLocalModelFilterRegistry struct {
	*fakeLocalInferenceRegistry

	response     *csghub.LocalModelFilterResponse
	err          error
	anonResponse *csghub.LocalModelFilterResponse
	anonErr      error

	calls     int
	anonCalls int
	lastReq   csghub.LocalModelFilterRequest
	anonReq   csghub.LocalModelFilterRequest
}

func newFakeLocalModelFilterRegistry() *fakeLocalModelFilterRegistry {
	return &fakeLocalModelFilterRegistry{
		fakeLocalInferenceRegistry: &fakeLocalInferenceRegistry{source: modelregistry.SourceOpenCSG},
	}
}

func (r *fakeLocalModelFilterRegistry) ListLocalModelFilter(_ context.Context, req csghub.LocalModelFilterRequest) (*csghub.LocalModelFilterResponse, error) {
	r.calls++
	r.lastReq = req
	if r.err != nil {
		return nil, r.err
	}
	return r.response, nil
}

func (r *fakeLocalModelFilterRegistry) ListLocalModelFilterAnonymous(_ context.Context, req csghub.LocalModelFilterRequest) (*csghub.LocalModelFilterResponse, error) {
	r.anonCalls++
	r.anonReq = req
	if r.anonErr != nil {
		return nil, r.anonErr
	}
	return r.anonResponse, nil
}

func localModelFilterCandidate(path string, tags ...csghub.Tag) csghub.LocalModelFilterCandidate {
	model := localInferenceModel(path, tags...)
	return csghub.LocalModelFilterCandidate{Model: model, Reason: csghub.LocalModelFilterReasonMissingArchitecture}
}

func supportedTextCandidate(path string) csghub.LocalModelFilterCandidate {
	candidate := localModelFilterCandidate(path,
		localInferenceTag("safetensors", "framework"),
		localInferenceTag("text-generation", "task"),
	)
	candidate.Model.Metadata.Architecture = "LlamaForCausalLM"
	candidate.Model.RepoSize = 1 << 30
	return candidate
}

func TestMarketplaceLocalModelFilterSupplementsPage(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data: []csghub.Model{
			localInferenceModel("acme/data-one", localInferenceTag("gguf", "framework"), localInferenceTag("text-generation", "task")),
		},
		TotalConfirmed:    1,
		TotalExact:        true,
		HasMore:           true,
		CandidatesQueried: true,
		LocalCheck: []csghub.LocalModelFilterCandidate{
			// Rejected locally: no shipped runtime serves video.
			localModelFilterCandidate("acme/video", localInferenceTag("safetensors", "framework"), localInferenceTag("image-to-video", "task")),
			supportedTextCandidate("acme/supported"),
			// Rejected locally: the architecture is not convertible.
			func() csghub.LocalModelFilterCandidate {
				candidate := localModelFilterCandidate("acme/unknown-arch",
					localInferenceTag("safetensors", "framework"),
					localInferenceTag("text-generation", "task"))
				candidate.Model.Metadata.Architecture = "MysteryForCausalLM"
				return candidate
			}(),
			supportedTextCandidate("acme/left-over"),
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{
		Per:          2,
		ExcludePaths: []string{"acme/already-seen"},
	})
	if err != nil {
		t.Fatalf("listLocalModelFilter: %v", err)
	}
	if fallback {
		t.Fatal("fallback = true, want the endpoint result")
	}
	var paths []string
	for _, model := range result.Models {
		paths = append(paths, model.Path)
	}
	wantPaths := []string{"acme/data-one", "acme/supported"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("models = %v, want %v", paths, wantPaths)
	}
	wantExclude := map[string]bool{"acme/already-seen": true, "acme/video": true, "acme/supported": true}
	for _, path := range result.ExcludePaths {
		delete(wantExclude, path)
	}
	if len(wantExclude) != 0 {
		t.Fatalf("exclude paths missing %v, got %v", wantExclude, result.ExcludePaths)
	}
	// Candidates the page never reached must not be consumed: they come back
	// with the next page instead of disappearing.
	for _, path := range result.ExcludePaths {
		if path == "acme/left-over" || path == "acme/unknown-arch" {
			t.Fatalf("unevaluated candidate was consumed: %v", result.ExcludePaths)
		}
	}
	if !result.HasMore {
		t.Fatalf("has_more = %v, want true", result.HasMore)
	}
}

func TestMarketplaceLocalModelFilterSkipsCandidatesWhenPageIsFull(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data: []csghub.Model{
			localInferenceModel("acme/one", localInferenceTag("gguf", "framework")),
			localInferenceModel("acme/two", localInferenceTag("gguf", "framework")),
		},
		TotalConfirmed:    2,
		HasMore:           true,
		CandidatesQueried: true,
		LocalCheck:        []csghub.LocalModelFilterCandidate{supportedTextCandidate("acme/supported")},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 2})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	if len(result.Models) != 2 || len(result.ExcludePaths) != 0 {
		t.Fatalf("models = %d, exclude = %v; want an untouched full page", len(result.Models), result.ExcludePaths)
	}
}

func TestMarketplaceLocalModelFilterTimeoutSemantics(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	t.Run("query timeout surfaces a retryable error", func(t *testing.T) {
		reg := newFakeLocalModelFilterRegistry()
		reg.response = &csghub.LocalModelFilterResponse{QueryTimeout: true}
		filter := newTestLocalInferenceFilter(reg, &clock)
		if _, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16}); fallback || !errors.Is(err, errMarketplaceLocalModelFilterUnavailable) {
			t.Fatalf("fallback=%v err=%v, want the unavailable error", fallback, err)
		}
	})
	t.Run("candidate timeout keeps the page", func(t *testing.T) {
		reg := newFakeLocalModelFilterRegistry()
		reg.response = &csghub.LocalModelFilterResponse{
			Data:              []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
			TotalConfirmed:    1,
			HasMore:           false,
			LocalCheckTimeout: true,
			LocalCheck:        []csghub.LocalModelFilterCandidate{supportedTextCandidate("acme/supported")},
		}
		filter := newTestLocalInferenceFilter(reg, &clock)
		result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
		if err != nil || fallback {
			t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
		}
		if len(result.Models) != 1 || len(result.ExcludePaths) != 0 {
			t.Fatalf("models = %v, exclude = %v; want data only", result.Models, result.ExcludePaths)
		}
		if result.Partial {
			t.Fatal("partial = true, want false: a candidate timeout leaves the confirmed page intact")
		}
		if !result.Uncacheable {
			t.Fatal("uncacheable = false, want true: a timed-out candidate list must not be cached")
		}
	})
	t.Run("candidates not queried keeps the page", func(t *testing.T) {
		reg := newFakeLocalModelFilterRegistry()
		reg.response = &csghub.LocalModelFilterResponse{
			Data:              []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
			TotalConfirmed:    1,
			CandidatesQueried: false,
			LocalCheck:        []csghub.LocalModelFilterCandidate{supportedTextCandidate("acme/supported")},
		}
		filter := newTestLocalInferenceFilter(reg, &clock)
		result, _, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
		if err != nil {
			t.Fatalf("listLocalModelFilter: %v", err)
		}
		if len(result.Models) != 1 {
			t.Fatalf("models = %v, want data only", result.Models)
		}
	})
}

func TestMarketplaceLocalModelFilterFallbacks(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	tests := []struct {
		name     string
		err      error
		fallback bool
	}{
		{"not found", &csghub.HTTPError{StatusCode: http.StatusNotFound}, true},
		{"bad request", &csghub.HTTPError{StatusCode: http.StatusBadRequest}, true},
		{"server error", &csghub.HTTPError{StatusCode: http.StatusInternalServerError}, true},
		{"network", errors.New("connection refused"), true},
		{"rate limited", &csghub.HTTPError{StatusCode: http.StatusTooManyRequests}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newFakeLocalModelFilterRegistry()
			reg.err = tt.err
			filter := newTestLocalInferenceFilter(reg, &clock)
			_, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
			if fallback != tt.fallback {
				t.Fatalf("fallback = %v, want %v (err %v)", fallback, tt.fallback, err)
			}
			if !tt.fallback && err == nil {
				t.Fatal("want the rate limit error surfaced")
			}
		})
	}
}

func TestMarketplaceLocalModelFilterRetriesUnauthorizedAnonymously(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.err = &csghub.HTTPError{StatusCode: http.StatusUnauthorized}
	reg.anonResponse = &csghub.LocalModelFilterResponse{
		Data:           []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
		TotalConfirmed: 1,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want the anonymous result", fallback, err)
	}
	if reg.anonCalls != 1 || len(result.Models) != 1 {
		t.Fatalf("anonymous calls = %d models = %d", reg.anonCalls, len(result.Models))
	}

	reg.anonErr = &csghub.HTTPError{StatusCode: http.StatusNotFound}
	if _, fallback, _ := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16}); !fallback {
		t.Fatal("fallback = false after the anonymous retry also failed, want true")
	}
}

func TestMarketplaceLocalModelFilterResetsExcludePathsAtLimit(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		TotalConfirmed:    0,
		CandidatesQueried: true,
		LocalCheck:        []csghub.LocalModelFilterCandidate{supportedTextCandidate("acme/supported")},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	exclude := make([]string, marketplaceLocalModelFilterExcludeLimit)
	for i := range exclude {
		exclude[i] = "acme/old-" + string(rune('a'+i%26)) + "-" + time.Duration(i).String()
	}
	result, _, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16, ExcludePaths: exclude})
	if err != nil {
		t.Fatalf("listLocalModelFilter: %v", err)
	}
	if len(result.ExcludePaths) != 0 {
		t.Fatalf("exclude paths = %d, want a reset", len(result.ExcludePaths))
	}
}

func TestMarketplaceLocalModelFilterCapabilitiesMirrorRuntimeTables(t *testing.T) {
	capabilities := marketplaceLocalModelFilterCapabilities()
	if len(capabilities.Convert.Architectures) == 0 {
		t.Fatal("convert architectures are empty")
	}
	if capabilities.Convert.DiskMultiplier != 2 {
		t.Fatalf("disk multiplier = %d, want 2", capabilities.Convert.DiskMultiplier)
	}
	if capabilities.Convert.Enabled == nil || !*capabilities.Convert.Enabled {
		t.Fatal("convert.enabled = false, want true")
	}
	if capabilities.AllowEmptyImageClass == nil || !*capabilities.AllowEmptyImageClass {
		t.Fatal("allow_empty_image_class = false, want true")
	}
	if len(capabilities.NativeFormats) != 0 {
		t.Fatalf("native formats = %v, want empty until the MLX backend ships", capabilities.NativeFormats)
	}
	if capabilities.TaskAliases["stable-diffusion"] != "text-to-image" {
		t.Fatalf("task aliases = %v", capabilities.TaskAliases)
	}
	assertContains(t, "formats", capabilities.Formats, "gguf")
	assertContains(t, "formats", capabilities.Formats, "safetensors")
	assertContains(t, "formats", capabilities.Formats, "pytorch")
	assertContains(t, "tasks", capabilities.Tasks, "text-generation")
	assertContains(t, "tasks", capabilities.Tasks, "automatic-speech-recognition")
	assertContains(t, "image pipelines", capabilities.ImagePipelineSubstrings, "stablediffusion")
	assertContains(t, "image pipelines", capabilities.ImagePipelineSubstrings, "flux")
	assertContains(t, "image fallbacks", capabilities.ImageClassFallbackSubstrings, "pipeline")
	assertContains(t, "unsupported image classes", capabilities.ImageUnsupportedClassSubstrings, "videodiffusion")
	assertContains(t, "asr families", capabilities.ASRNameFamilies, "SenseVoiceSmall")
	assertContains(t, "asr architectures", capabilities.ASRArchSubstrings, "WhisperForConditionalGeneration")
	assertContains(t, "tts families", capabilities.TTSNameFamilies, "Kokoro")
	assertContains(t, "tts architectures", capabilities.TTSTaskArchSubstrings, "VitsModel")
	assertContains(t, "tts backends", capabilities.TTSBackendArchSubstrings, "Qwen3TTS")
	assertContains(t, "tts backend names", capabilities.TTSBackendNameSubstrings, "kokoro")
	assertContains(t, "diffusion gguf architectures", capabilities.DiffusionGGUFArchitectures, "flux")
	assertContains(t, "unsupported framework tags", capabilities.UnsupportedFormatTags, "onnx")
	assertContains(t, "unsupported runtime tags", capabilities.UnsupportedRuntimeTags, "flax")
	assertContains(t, "quant substrings", capabilities.QuantSubstrings, "awq")
	assertContains(t, "quant tokens", capabilities.QuantTokens, "int4")
	assertContains(t, "gguf non text keywords", capabilities.GGUFNonTextKeywords, "flux")
}

func assertContains(t *testing.T, label string, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%s = %v, want %q", label, values, want)
}

func TestParseMarketplaceExcludePaths(t *testing.T) {
	overLimit := make([]string, 0, marketplaceLocalModelFilterExcludeLimit+1)
	for i := 0; i <= marketplaceLocalModelFilterExcludeLimit; i++ {
		overLimit = append(overLimit, "org/model-"+strconv.Itoa(i))
	}
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ", nil},
		{"acme/a", []string{"acme/a"}},
		{"acme/a, /org/b/ ,acme/a", []string{"acme/a", "org/b"}},
		{strings.Join(overLimit, ","), nil},
	}
	for _, tt := range tests {
		if got := parseMarketplaceExcludePaths(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("parseMarketplaceExcludePaths(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMarketplaceLocalModelFilterBoostOrgs(t *testing.T) {
	limits := marketplaceLocalInferenceLimits{gpus: []marketplaceLocalInferenceGPU{
		{vendor: "nvidia"},
		{vendor: "nvidia"},
		{vendor: "amd"},
		{vendor: "apple", sharedMemory: true},
	}}
	if got := marketplaceLocalModelFilterBoostOrgs(limits); !reflect.DeepEqual(got, []string{"nvidia", "amd"}) {
		t.Fatalf("boost orgs = %v, want [nvidia amd]", got)
	}
}

func TestMarketplaceLocalModelFilterHardwareSkipsInClusterMode(t *testing.T) {
	limits := marketplaceLocalInferenceLimits{
		maxDownloadBytes: 100,
		maxMemoryBytes:   200,
		ramBytes:         200,
		gpus:             []marketplaceLocalInferenceGPU{{vendor: "nvidia", vramBytes: 100}},
	}
	hardware := marketplaceLocalModelFilterHardware(limits, true)
	if hardware.FreeDiskBytes != 0 || hardware.RAMBytes != 0 || len(hardware.GPUs) != 0 {
		t.Fatalf("cluster hardware = %#v, want no budgets", hardware)
	}
	if hardware.OS == "" || hardware.Arch == "" {
		t.Fatalf("os/arch = %q/%q, want the host values", hardware.OS, hardware.Arch)
	}
}

func TestMarketplaceLocalModelFilterPropagatesPageLimit(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data:              []csghub.Model{},
		TotalExact:        true,
		PageLimitExceeded: true,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil {
		t.Fatalf("listLocalModelFilter: %v", err)
	}
	if fallback {
		t.Fatal("fallback = true, want the endpoint result")
	}
	if !result.PageLimitExceeded {
		t.Fatal("page_limit_exceeded was not propagated")
	}
}

func TestMarketplaceLocalModelFilterSupplementsExhaustedCandidates(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data:                []csghub.Model{},
		TotalExact:          true,
		CandidatesQueried:   true,
		LocalCheckExhausted: true,
		LocalCheck: []csghub.LocalModelFilterCandidate{
			supportedTextCandidate("acme/supported"),
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 2})
	if err != nil {
		t.Fatalf("listLocalModelFilter: %v", err)
	}
	if fallback {
		t.Fatal("fallback = true, want the endpoint result")
	}
	// local_check_exhausted only means the hub returned every candidate; the
	// page still has to be filled from them.
	if len(result.Models) != 1 || result.Models[0].Path != "acme/supported" {
		t.Fatalf("models = %v, want [acme/supported]: exhausted candidates must still be supplemented", result.Models)
	}
}

func TestMarketplaceLocalModelFilterKeepsUnevaluatedCandidatesReachable(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	candidates := make([]csghub.LocalModelFilterCandidate, 0, 20)
	for i := 0; i < 20; i++ {
		// Video candidates are rejected locally without a probe, so the page
		// never fills and the batch budget is what stops the supplement.
		candidates = append(candidates, localModelFilterCandidate("acme/video-"+strconv.Itoa(i),
			localInferenceTag("safetensors", "framework"), localInferenceTag("image-to-video", "task")))
	}
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data:              []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
		TotalConfirmed:    1,
		HasMore:           false,
		CandidatesQueried: true,
		LocalCheck:        candidates,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	if !result.HasMore {
		t.Fatal("has_more = false, want true: unevaluated candidates must reach the next page")
	}
	if !result.Partial {
		t.Fatal("partial = false, want true: the page evaluated only the first batches")
	}
	if got := len(result.ExcludePaths); got != 15 {
		t.Fatalf("exclude paths = %d, want the 15 evaluated candidates", got)
	}
}

func TestMarketplaceLocalModelFilterKeepsCandidateAfterTransientProbeFailure(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.readErrs = map[string]error{
		"acme/flaky": &csghub.HTTPError{StatusCode: http.StatusInternalServerError, Body: "boom"},
	}
	reg.response = &csghub.LocalModelFilterResponse{
		Data:                []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
		TotalConfirmed:      1,
		HasMore:             true,
		CandidatesQueried:   true,
		LocalCheckTruncated: true,
		LocalCheck: []csghub.LocalModelFilterCandidate{
			localModelFilterCandidate("acme/flaky",
				localInferenceTag("safetensors", "framework"),
				localInferenceTag("text-generation", "task")),
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	for _, path := range result.ExcludePaths {
		if path == "acme/flaky" {
			t.Fatal("transient probe failure consumed the candidate; it must stay reachable on the next page")
		}
	}
	if !result.Partial {
		t.Fatal("partial = false, want true: the candidate was not evaluated")
	}
	if !result.HasMore {
		t.Fatal("has_more = false, want the hub value: truncated candidates must stay reachable")
	}
}

func TestMarketplaceLocalModelFilterSupplementProbeHasDeadline(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.readFile = func(ctx context.Context, _, _, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	reg.response = &csghub.LocalModelFilterResponse{
		Data:              []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
		TotalConfirmed:    1,
		HasMore:           true,
		CandidatesQueried: true,
		LocalCheck: []csghub.LocalModelFilterCandidate{
			localModelFilterCandidate("acme/slow",
				localInferenceTag("safetensors", "framework"),
				localInferenceTag("text-generation", "task")),
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}
	filter.probeTimeout = 20 * time.Millisecond

	started := time.Now()
	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("request took %s, want the probe deadline to bound it", elapsed)
	}
	if !result.Partial {
		t.Fatal("partial = false, want true: the probe was cut off")
	}
	for _, path := range result.ExcludePaths {
		if path == "acme/slow" {
			t.Fatal("timed-out probe consumed the candidate; it must stay reachable")
		}
	}
	if !result.HasMore {
		t.Fatal("has_more = false, want the hub value")
	}
}

func TestMarketplaceLocalModelFilterKeepsUnevaluatedCandidatesWhenPageFillsMidBatch(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	data := make([]csghub.Model, 0, 15)
	for i := 0; i < 15; i++ {
		data = append(data, localInferenceModel("acme/data-"+strconv.Itoa(i), localInferenceTag("gguf", "framework")))
	}
	candidates := make([]csghub.LocalModelFilterCandidate, 0, 5)
	candidates = append(candidates, supportedTextCandidate("acme/supported"))
	for i := 1; i < 5; i++ {
		candidates = append(candidates, localModelFilterCandidate("acme/left-over-"+strconv.Itoa(i),
			localInferenceTag("safetensors", "framework"), localInferenceTag("text-generation", "task")))
	}
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data:                data,
		TotalConfirmed:      15,
		HasMore:             false,
		CandidatesQueried:   true,
		LocalCheckExhausted: true,
		LocalCheck:          candidates,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	if len(result.Models) != 16 {
		t.Fatalf("models = %d, want a full page of 16", len(result.Models))
	}
	if !result.HasMore {
		t.Fatal("has_more = false, want true: four candidates were never evaluated")
	}
	if len(result.ExcludePaths) != 1 || result.ExcludePaths[0] != "acme/supported" {
		t.Fatalf("exclude paths = %v, want only the evaluated candidate", result.ExcludePaths)
	}
}

func TestMarketplaceLocalModelFilterStopsWhenCursorIsFull(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	exclude := make([]string, 0, marketplaceLocalModelFilterExcludeLimit)
	for i := 0; i < marketplaceLocalModelFilterExcludeLimit; i++ {
		exclude = append(exclude, "acme/seen-"+strconv.Itoa(i))
	}
	reg := newFakeLocalModelFilterRegistry()
	reg.response = &csghub.LocalModelFilterResponse{
		Data:              []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
		TotalConfirmed:    1,
		HasMore:           true,
		CandidatesQueried: true,
		LocalCheck: []csghub.LocalModelFilterCandidate{
			localModelFilterCandidate("acme/video", localInferenceTag("safetensors", "framework"), localInferenceTag("image-to-video", "task")),
		},
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{
		Per:          16,
		ExcludePaths: exclude,
	})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	if len(result.ExcludePaths) != 0 {
		t.Fatalf("exclude paths = %d, want the cursor dropped", len(result.ExcludePaths))
	}
	if result.HasMore {
		t.Fatal("has_more = true, want false: dropping the cursor would restart the same candidates forever")
	}
	if !result.Partial {
		t.Fatal("partial = false, want true: the page cannot reach the remaining candidates")
	}
}

func TestMarketplaceLocalModelFilterZeroConsumptionKeepsUnevaluatedCandidates(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	reg := newFakeLocalModelFilterRegistry()
	reg.readErrs = make(map[string]error, 20)
	candidates := make([]csghub.LocalModelFilterCandidate, 0, 20)
	for i := 0; i < 20; i++ {
		path := "acme/flaky-" + strconv.Itoa(i)
		reg.readErrs[path] = &csghub.HTTPError{StatusCode: http.StatusInternalServerError, Body: "boom"}
		candidates = append(candidates, localModelFilterCandidate(path,
			localInferenceTag("safetensors", "framework"),
			localInferenceTag("text-generation", "task")))
	}
	reg.response = &csghub.LocalModelFilterResponse{
		Data:              []csghub.Model{localInferenceModel("acme/one", localInferenceTag("gguf", "framework"))},
		TotalConfirmed:    1,
		HasMore:           false,
		CandidatesQueried: true,
		LocalCheck:        candidates,
	}
	filter := newTestLocalInferenceFilter(reg, &clock)
	filter.limits = marketplaceLocalInferenceLimits{}

	result, fallback, err := filter.listLocalModelFilter(context.Background(), csghub.LocalModelFilterRequest{Per: 16})
	if err != nil || fallback {
		t.Fatalf("listLocalModelFilter = (%v, %v), want endpoint result", fallback, err)
	}
	if !result.HasMore {
		t.Fatal("has_more = false, want true: 5 unevaluated candidates must stay reachable")
	}
	if !result.Partial {
		t.Fatal("partial = false, want true: nothing was consumed")
	}
	if len(result.ExcludePaths) != 0 {
		t.Fatalf("exclude paths = %v, want an unchanged cursor", result.ExcludePaths)
	}
}

package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestModelInfoFromRemote_TextModel(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:          "Qwen/Qwen3-0.6B:abc123",
		Task:        "text-generation",
		DisplayName: "Qwen3-0.6B",
		Created:     1773623409,
	})
	if !ok {
		t.Fatal("expected model to be included")
	}
	if info.Source != "cloud" {
		t.Fatalf("Source = %q, want cloud", info.Source)
	}
	if info.DisplayName != "Qwen3-0.6B" {
		t.Fatalf("DisplayName = %q, want Qwen3-0.6B", info.DisplayName)
	}
	if info.Label != "Qwen3-0.6B" {
		t.Fatalf("Label = %q, want Qwen3-0.6B", info.Label)
	}
	if info.PipelineTag != "text-generation" {
		t.Fatalf("PipelineTag = %q, want text-generation", info.PipelineTag)
	}
}

func TestModelInfoFromRemote_LabelFallsBackToID(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:   "deepseek-v3.2",
		Task: "text-generation",
	})
	if !ok {
		t.Fatal("expected model to be included")
	}
	if info.Label != "deepseek-v3.2" {
		t.Fatalf("Label = %q, want model ID fallback", info.Label)
	}
}

func TestModelInfoFromRemote_LabelUsesOfficialName(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:           "Qwen/Qwen3Guard-Gen-0.6B:s-qwen-qwen3guard-gen-0-6b(OpenCSG)",
		Task:         "text-generation",
		OfficialName: "Qwen3Guard-Gen-0.6B",
	})
	if !ok {
		t.Fatal("expected model to be included")
	}
	if info.Label != "Qwen3Guard-Gen-0.6B" {
		t.Fatalf("Label = %q, want official name", info.Label)
	}
}

func TestModelInfoFromRemote_LabelPreservesProviderSuffix(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:          "deepseek-v3.2",
		Task:        "text-generation",
		DisplayName: "deepseek-v3.2(infini-ai)",
	})
	if !ok {
		t.Fatal("expected model to be included")
	}
	if info.Label != "deepseek-v3.2(infini-ai)" {
		t.Fatalf("Label = %q, want display name with provider", info.Label)
	}
}

func TestModelInfoFromRemote_ExtractsPricing(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:      "Qwen/Qwen3Guard-Gen-0.6B",
		Task:    "text-generation",
		OwnedBy: "OpenCSG",
		Metadata: map[string]interface{}{
			"llm_type": "serverless",
			"pricing": map[string]interface{}{
				"input_token_price": map[string]interface{}{
					"currency":          "￥",
					"price_per_million": 0.12,
				},
				"output_token_price": map[string]interface{}{
					"currency":          "￥",
					"price_per_million": "0.24",
				},
			},
		},
	})
	if !ok {
		t.Fatal("expected model to be included")
	}
	if info.LLMType != "serverless" {
		t.Fatalf("LLMType = %q, want serverless", info.LLMType)
	}
	if info.OwnedBy != "OpenCSG" {
		t.Fatalf("OwnedBy = %q, want OpenCSG", info.OwnedBy)
	}
	if info.Provider != "csghub" {
		t.Fatalf("Provider = %q, want csghub", info.Provider)
	}
	if info.Pricing == nil || info.Pricing.InputTokenPrice == nil || info.Pricing.OutputTokenPrice == nil {
		t.Fatalf("Pricing = %#v, want input and output prices", info.Pricing)
	}
	if info.Pricing.InputTokenPrice.Currency != "￥" || info.Pricing.InputTokenPrice.PricePerMillion != 0.12 {
		t.Fatalf("InputTokenPrice = %#v, want ￥0.12", info.Pricing.InputTokenPrice)
	}
	if info.Pricing.OutputTokenPrice.Currency != "￥" || info.Pricing.OutputTokenPrice.PricePerMillion != 0.24 {
		t.Fatalf("OutputTokenPrice = %#v, want ￥0.24", info.Pricing.OutputTokenPrice)
	}
}

func TestModelInfoFromRemote_VisionModelEnablesImages(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:          "Qwen/Qwen3.5-35B-A3B-FP8:xyz",
		Task:        "image-text-to-text",
		DisplayName: "Qwen3.5-35B-A3B-FP8",
	})
	if !ok {
		t.Fatal("expected model to be included")
	}
	if !info.HasMMProj {
		t.Fatal("HasMMProj = false, want true for cloud vision models")
	}
}

func TestModelInfoFromRemote_AllowsCommaSeparatedChatTasks(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:   "provider/multimodal-model",
		Task: "text-generation,image-text-to-text",
	})
	if !ok {
		t.Fatal("expected comma-separated chat model to be included")
	}
	if info.PipelineTag != "text-generation" {
		t.Fatalf("PipelineTag = %q, want normalized text-generation", info.PipelineTag)
	}
	if !info.HasMMProj {
		t.Fatal("HasMMProj = false, want true for comma-separated vision task")
	}
}

func TestModelInfoFromRemote_AllowsCommaSeparatedChatTasksWithSpaces(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID:   "provider/spaced-multimodal-model",
		Task: "text-generation, image-text-to-text",
	})
	if !ok {
		t.Fatal("expected comma-separated chat model with spaces to be included")
	}
	if info.PipelineTag != "text-generation" {
		t.Fatalf("PipelineTag = %q, want normalized text-generation", info.PipelineTag)
	}
	if !info.HasMMProj {
		t.Fatal("HasMMProj = false, want true for comma-separated vision task with spaces")
	}
}

func TestModelInfoFromRemote_AllowsSupportedInferenceTasks(t *testing.T) {
	image, ok := modelInfoFromRemote(remoteModel{
		ID:   "Qwen/Qwen-Image-2512:abc",
		Task: "text-to-image",
	})
	if !ok {
		t.Fatal("expected text-to-image model to be included")
	}
	if image.PipelineTag != "text-to-image" || !slices.Equal(image.InputModalities, []string{"text"}) || !slices.Equal(image.OutputModalities, []string{"image"}) {
		t.Fatalf("image metadata = %#v, want text-to-image text->image", image)
	}

	asr, ok := modelInfoFromRemote(remoteModel{
		ID:   "glm-asr-2512",
		Task: "speech-to-text",
	})
	if !ok {
		t.Fatal("expected speech-to-text model to be included")
	}
	if asr.PipelineTag != "automatic-speech-recognition" || !slices.Equal(asr.InputModalities, []string{"audio"}) || !slices.Equal(asr.OutputModalities, []string{"transcription"}) {
		t.Fatalf("asr metadata = %#v, want ASR audio->transcription", asr)
	}
}

func TestModelInfoFromRemote_FiltersUnsupportedTask(t *testing.T) {
	if _, ok := modelInfoFromRemote(remoteModel{
		ID:   "video/model",
		Task: "text-to-video",
	}); ok {
		t.Fatal("expected unsupported text-to-video model to be filtered")
	}
}

func TestModelInfoFromRemote_AllowsBlankTaskAsTextGeneration(t *testing.T) {
	info, ok := modelInfoFromRemote(remoteModel{
		ID: "claude",
	})
	if !ok {
		t.Fatal("expected blank-task model to be included")
	}
	if info.PipelineTag != "text-generation" {
		t.Fatalf("PipelineTag = %q, want text-generation fallback", info.PipelineTag)
	}
}

func TestModelTokenLimitsFromRemoteMetadata(t *testing.T) {
	limits := modelTokenLimitsFromRemote(remoteModel{
		ID: "provider/model",
		Metadata: map[string]interface{}{
			"limits": map[string]interface{}{
				"contextWindow":   200000,
				"maxOutputTokens": "16384",
			},
		},
	})

	if limits.MaxInputTokens != 200000 {
		t.Fatalf("MaxInputTokens = %d, want 200000", limits.MaxInputTokens)
	}
	if limits.MaxTokens != 16384 {
		t.Fatalf("MaxTokens = %d, want 16384", limits.MaxTokens)
	}
}

func TestModelTokenLimitsFromRemoteTopLevelFields(t *testing.T) {
	limits := modelTokenLimitsFromRemote(remoteModel{
		ID:              "provider/model",
		MaxInputTokens:  131072,
		MaxOutputTokens: 8192,
	})

	if limits.MaxInputTokens != 131072 {
		t.Fatalf("MaxInputTokens = %d, want 131072", limits.MaxInputTokens)
	}
	if limits.MaxTokens != 8192 {
		t.Fatalf("MaxTokens = %d, want 8192", limits.MaxTokens)
	}
}

func TestRefreshChatModelsSendsAccessTokenWhenSet(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.URL.Query().Get("page"); got != cloudModelListPage {
			t.Fatalf("page query = %q, want %q", got, cloudModelListPage)
		}
		if got := r.URL.Query().Get("per"); got != cloudModelListPer {
			t.Fatalf("per query = %q, want %q", got, cloudModelListPer)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":   "auth/model",
					"task": "text-generation",
				},
			},
		})
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)
	svc.SetAccessToken(" access-token ")

	models, err := svc.RefreshChatModels(context.Background())
	if err != nil {
		t.Fatalf("RefreshChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "auth/model" {
		t.Fatalf("models = %#v, want auth/model", models)
	}
}

func TestRefreshChatModelsOmitsAuthorizationWithoutAccessToken(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":   "public/model",
					"task": "text-generation",
				},
			},
		})
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)

	models, err := svc.RefreshChatModels(context.Background())
	if err != nil {
		t.Fatalf("RefreshChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "public/model" {
		t.Fatalf("models = %#v, want public/model", models)
	}
}

func TestRefreshChatModelsFallsBackToPublicListWhenAccessTokenUnauthorized(t *testing.T) {
	requests := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "" {
			http.Error(w, "expired token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":   "public/model",
					"task": "text-generation",
				},
			},
		})
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)
	svc.SetAccessToken("expired-token")

	models, err := svc.RefreshChatModels(context.Background())
	if err != nil {
		t.Fatalf("RefreshChatModels returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want authenticated request plus public fallback", requests)
	}
	if len(models) != 1 || models[0].Model != "public/model" {
		t.Fatalf("models = %#v, want public/model", models)
	}
}

func TestRefreshChatModelsBypassesCache(t *testing.T) {
	requests := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           "fresh/model",
					"task":         "text-generation",
					"display_name": "Fresh Model",
				},
			},
		})
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)
	svc.cached = []api.ModelInfo{{Model: "stale/model", Source: "cloud"}}
	svc.cachedAt = time.Now()

	models, err := svc.RefreshChatModels(context.Background())
	if err != nil {
		t.Fatalf("RefreshChatModels returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if len(models) != 1 || models[0].Model != "fresh/model" {
		t.Fatalf("models = %#v, want fresh/model", models)
	}

	cached, err := svc.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("ListChatModels returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after cached list = %d, want 1", requests)
	}
	if len(cached) != 1 || cached[0].Model != "fresh/model" {
		t.Fatalf("cached models = %#v, want fresh/model", cached)
	}
}

func TestRefreshChatModelsCachesTokenLimits(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           "fresh/model",
					"task":         "text-generation",
					"display_name": "Fresh Model",
					"metadata": map[string]any{
						"context_window":    262144,
						"max_output_tokens": 12288,
					},
				},
			},
		})
	}))
	defer apiServer.Close()

	svc := NewService(apiServer.URL)
	models, err := svc.RefreshChatModels(context.Background())
	if err != nil {
		t.Fatalf("RefreshChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "fresh/model" {
		t.Fatalf("models = %#v, want fresh/model", models)
	}

	limits, ok := svc.ChatModelTokenLimits("fresh/model")
	if !ok {
		t.Fatal("expected cached token limits for fresh/model")
	}
	if limits.MaxInputTokens != 262144 {
		t.Fatalf("MaxInputTokens = %d, want 262144", limits.MaxInputTokens)
	}
	if limits.MaxTokens != 12288 {
		t.Fatalf("MaxTokens = %d, want 12288", limits.MaxTokens)
	}
}

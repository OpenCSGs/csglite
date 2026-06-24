package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	DefaultBaseURL        = "https://ai.space.opencsg.com"
	DefaultLoginURL       = "https://iam.opencsg.com/login/oauth/authorize?client_id=d623c957e69976c8a7a8&response_type=code&redirect_uri=https://hub.opencsg.com/api/v1/callback/casdoor&scope=read&state=casdoor"
	DefaultAccessTokenURL = "https://opencsg.com/settings/access-token"
	defaultCacheTTL       = 5 * time.Minute
	cloudModelListPage    = "1"
	cloudModelListPer     = "40"
)

type Service struct {
	baseURL     string
	accessToken string
	client      *http.Client
	ttl         time.Duration

	mu       sync.RWMutex
	cached   []api.ModelInfo
	limits   map[string]ModelTokenLimits
	cachedAt time.Time
}

type ModelTokenLimits struct {
	MaxInputTokens int
	MaxTokens      int
}

type modelListResponse struct {
	Object string        `json:"object"`
	Data   []remoteModel `json:"data"`
}

type remoteModel struct {
	ID              string                 `json:"id"`
	Object          string                 `json:"object"`
	Created         int64                  `json:"created"`
	OwnedBy         string                 `json:"owned_by"`
	Task            string                 `json:"task"`
	DisplayName     string                 `json:"display_name"`
	OfficialName    string                 `json:"official_name"`
	Public          bool                   `json:"public"`
	MaxInputTokens  int                    `json:"max_input_tokens"`
	MaxTokens       int                    `json:"max_tokens"`
	MaxOutputTokens int                    `json:"max_output_tokens"`
	ContextWindow   int                    `json:"context_window"`
	ContextLength   int                    `json:"context_length"`
	Metadata        map[string]interface{} `json:"metadata"`
}

type cloudTaskSpec struct {
	PipelineTag      string
	InputModalities  []string
	OutputModalities []string
}

var supportedCloudTasks = map[string]cloudTaskSpec{
	"text-generation": {
		PipelineTag:      "text-generation",
		InputModalities:  []string{"text"},
		OutputModalities: []string{"text"},
	},
	"image-text-to-text": {
		PipelineTag:      "image-text-to-text",
		InputModalities:  []string{"text", "image"},
		OutputModalities: []string{"text"},
	},
	"text-to-image": {
		PipelineTag:      "text-to-image",
		InputModalities:  []string{"text"},
		OutputModalities: []string{"image"},
	},
	"image-to-image": {
		PipelineTag:      "image-to-image",
		InputModalities:  []string{"text", "image"},
		OutputModalities: []string{"image"},
	},
	"speech-to-text": {
		PipelineTag:      "automatic-speech-recognition",
		InputModalities:  []string{"audio"},
		OutputModalities: []string{"transcription"},
	},
	"automatic-speech-recognition": {
		PipelineTag:      "automatic-speech-recognition",
		InputModalities:  []string{"audio"},
		OutputModalities: []string{"transcription"},
	},
}

var cloudTaskPriority = []string{
	"text-generation",
	"image-text-to-text",
	"text-to-image",
	"image-to-image",
	"speech-to-text",
	"automatic-speech-recognition",
}

func NewService(baseURL string) *Service {
	return &Service{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 15 * time.Second},
		ttl:     defaultCacheTTL,
	}
}

func (s *Service) SetAccessToken(token string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.accessToken = strings.TrimSpace(token)
	s.cached = nil
	s.limits = nil
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Service) SetBaseURL(baseURL string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	s.cached = nil
	s.limits = nil
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Service) BaseURL() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.baseURL
}

func (s *Service) ListChatModels(ctx context.Context) ([]api.ModelInfo, error) {
	if s == nil || s.BaseURL() == "" {
		return nil, nil
	}
	if models, ok := s.cachedModels(); ok {
		return models, nil
	}
	return s.refresh(ctx)
}

func (s *Service) RefreshChatModels(ctx context.Context) ([]api.ModelInfo, error) {
	if s == nil || s.BaseURL() == "" {
		return nil, nil
	}
	return s.refresh(ctx)
}

func (s *Service) InvalidateChatModels() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.cached = nil
	s.limits = nil
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Service) ChatModelTokenLimits(modelID string) (ModelTokenLimits, bool) {
	if s == nil {
		return ModelTokenLimits{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	limits, ok := s.limits[strings.TrimSpace(modelID)]
	if !ok {
		return ModelTokenLimits{}, false
	}
	return limits, limits.MaxInputTokens > 0 || limits.MaxTokens > 0
}

func (s *Service) cachedModels() ([]api.ModelInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cached) == 0 || time.Since(s.cachedAt) > s.ttl {
		return nil, false
	}
	return cloneModels(s.cached), true
}

func (s *Service) refresh(ctx context.Context) ([]api.ModelInfo, error) {
	baseURL := s.BaseURL()
	token := s.currentAccessToken()
	models, limits, err := fetchCloudModels(ctx, s.client, baseURL, token)
	if err != nil && token != "" && isUnauthorizedStatus(err) {
		models, limits, err = fetchCloudModels(ctx, s.client, baseURL, "")
	}
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cached = cloneModels(models)
	s.limits = limits
	s.cachedAt = time.Now()
	s.mu.Unlock()

	return models, nil
}

type cloudModelListStatusError struct {
	statusCode int
	body       string
}

func (e cloudModelListStatusError) Error() string {
	return fmt.Sprintf("cloud model list returned %d: %s", e.statusCode, e.body)
}

func isUnauthorizedStatus(err error) bool {
	if statusErr, ok := err.(cloudModelListStatusError); ok {
		return statusErr.statusCode == http.StatusUnauthorized || statusErr.statusCode == http.StatusForbidden
	}
	return false
}

func fetchCloudModels(ctx context.Context, client *http.Client, baseURL, token string) ([]api.ModelInfo, map[string]ModelTokenLimits, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models?page="+cloudModelListPage+"&per="+cloudModelListPer, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating cloud model request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching cloud models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, cloudModelListStatusError{
			statusCode: resp.StatusCode,
			body:       strings.TrimSpace(string(body)),
		}
	}

	var payload modelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("decoding cloud model list: %w", err)
	}

	models := make([]api.ModelInfo, 0, len(payload.Data))
	limits := make(map[string]ModelTokenLimits, len(payload.Data))
	for _, item := range payload.Data {
		info, ok := modelInfoFromRemote(item)
		if !ok {
			continue
		}
		models = append(models, info)
		limits[strings.TrimSpace(info.Model)] = modelTokenLimitsFromRemote(item)
	}

	return models, limits, nil
}

func (s *Service) currentAccessToken() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accessToken
}

func modelInfoFromRemote(item remoteModel) (api.ModelInfo, bool) {
	spec, ok := cloudTaskSpecForModel(item)
	if !ok {
		return api.ModelInfo{}, false
	}

	displayName := strings.TrimSpace(item.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(item.OfficialName)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(item.ID)
	}

	var modifiedAt time.Time
	if item.Created > 0 {
		modifiedAt = time.Unix(item.Created, 0).UTC()
	}
	limits := modelTokenLimitsFromRemote(item)

	llmType := extractLLMType(item.Metadata)
	ownedBy := strings.TrimSpace(item.OwnedBy)
	provider := cloudModelProvider(ownedBy)
	pricing := extractPricing(item.Metadata)

	return api.ModelInfo{
		Name:             item.ID,
		Model:            item.ID,
		Format:           "cloud",
		ModifiedAt:       modifiedAt,
		Label:            displayName,
		DisplayName:      displayName,
		Source:           "cloud",
		Provider:         provider,
		PipelineTag:      spec.PipelineTag,
		InputModalities:  cloneStringSlice(spec.InputModalities),
		OutputModalities: cloneStringSlice(spec.OutputModalities),
		HasMMProj:        hasCloudTask(item, "image-text-to-text"),
		ContextWindow:    int64(limits.MaxInputTokens),
		LLMType:          llmType,
		OwnedBy:          ownedBy,
		Pricing:          pricing,
	}, true
}

func cloudModelProvider(string) string {
	return "csghub"
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string{}, values...)
}

func cloudTaskSpecForModel(item remoteModel) (cloudTaskSpec, bool) {
	tasks := cloudTasks(item)
	if len(tasks) == 0 {
		return supportedCloudTasks["text-generation"], true
	}
	for _, task := range cloudTaskPriority {
		if hasTask(tasks, task) {
			return supportedCloudTasks[task], true
		}
	}
	for _, task := range tasks {
		if spec, ok := supportedCloudTasks[task]; ok {
			return spec, true
		}
	}
	return cloudTaskSpec{}, false
}

func hasCloudTask(item remoteModel, want string) bool {
	return hasTask(cloudTasks(item), want)
}

func hasTask(tasks []string, want string) bool {
	want = strings.TrimSpace(strings.ToLower(want))
	for _, task := range tasks {
		if task == want {
			return true
		}
	}
	return false
}

func cloudTasks(item remoteModel) []string {
	fields := strings.FieldsFunc(item.Task, func(r rune) bool {
		return r == ','
	})
	tasks := make([]string, 0, len(fields))
	for _, field := range fields {
		task := strings.TrimSpace(strings.ToLower(field))
		if task != "" {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func modelTokenLimitsFromRemote(item remoteModel) ModelTokenLimits {
	input := firstPositive(
		item.MaxInputTokens,
		item.ContextWindow,
		item.ContextLength,
		extractTokenLimit(item.Metadata,
			"max_input_tokens",
			"max_input_token",
			"context_window",
			"context_length",
			"max_context_tokens",
			"max_context_length",
			"input_token_limit",
			"input_tokens_limit",
			"num_ctx",
			"n_ctx",
		),
	)
	output := firstPositive(
		item.MaxOutputTokens,
		item.MaxTokens,
		extractTokenLimit(item.Metadata,
			"max_output_tokens",
			"max_output_token",
			"max_completion_tokens",
			"max_completion_token",
			"output_token_limit",
			"completion_token_limit",
			"max_tokens",
		),
	)
	return ModelTokenLimits{
		MaxInputTokens: input,
		MaxTokens:      output,
	}
}

func extractTokenLimit(metadata map[string]interface{}, keys ...string) int {
	if len(metadata) == 0 {
		return 0
	}
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[normalizeMetadataKey(key)] = struct{}{}
	}
	return extractTokenLimitValue(metadata, wanted)
}

func extractTokenLimitValue(value interface{}, wanted map[string]struct{}) int {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if _, ok := wanted[normalizeMetadataKey(key)]; ok {
				if limit := numericTokenLimit(child); limit > 0 {
					return limit
				}
			}
		}
		for _, child := range v {
			if limit := extractTokenLimitValue(child, wanted); limit > 0 {
				return limit
			}
		}
	case []interface{}:
		for _, child := range v {
			if limit := extractTokenLimitValue(child, wanted); limit > 0 {
				return limit
			}
		}
	}
	return 0
}

func numericTokenLimit(value interface{}) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 0
}

func normalizeMetadataKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(key)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func extractLLMType(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return ""
	}
	if v, ok := metadata["llm_type"]; ok {
		switch val := v.(type) {
		case string:
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func extractPricing(metadata map[string]interface{}) *api.ModelPricing {
	if len(metadata) == 0 {
		return nil
	}

	pricingMap, ok := metadata["pricing"].(map[string]interface{})
	if !ok || len(pricingMap) == 0 {
		return nil
	}

	input := extractTokenPrice(pricingMap["input_token_price"])
	output := extractTokenPrice(pricingMap["output_token_price"])
	if input == nil && output == nil {
		return nil
	}

	return &api.ModelPricing{
		InputTokenPrice:  input,
		OutputTokenPrice: output,
	}
}

func extractTokenPrice(value interface{}) *api.ModelTokenPrice {
	priceMap, ok := value.(map[string]interface{})
	if !ok || len(priceMap) == 0 {
		return nil
	}

	price, ok := numericPriceValue(priceMap["price_per_million"])
	if !ok {
		return nil
	}

	return &api.ModelTokenPrice{
		Currency:        stringValue(priceMap["currency"]),
		PricePerMillion: price,
	}
}

func stringValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func numericPriceValue(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func cloneModels(models []api.ModelInfo) []api.ModelInfo {
	if len(models) == 0 {
		return nil
	}
	out := make([]api.ModelInfo, len(models))
	copy(out, models)
	return out
}

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csghub-lite/internal/csghub"
	"github.com/opencsgs/csghub-lite/internal/ggufpick"
	"github.com/opencsgs/csghub-lite/internal/localinference"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const marketplaceListCacheTTL = 2 * time.Minute

type marketplaceListCacheEntry struct {
	body      []byte
	updatedAt time.Time
}

var marketplaceListCache = struct {
	sync.Mutex
	entries map[string]marketplaceListCacheEntry
}{
	entries: make(map[string]marketplaceListCacheEntry),
}

// GET /api/marketplace/models -- proxy to CSGHub Hub model listing
func (s *Server) handleMarketplaceModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per"))
	if page <= 0 {
		page = 1
	}
	if per <= 0 {
		per = 16
	}

	requestedFramework := normalizeMarketplaceFramework(q.Get("framework"))
	listParams := csghub.ModelListParams{
		Search:         q.Get("search"),
		Sort:           q.Get("sort"),
		Page:           page,
		PerPage:        per,
		Source:         q.Get("source"),
		ModelParamsMin: q.Get("model_params_min"),
		ModelParamsMax: q.Get("model_params_max"),
	}
	if requestedFramework != "" {
		listParams.TagCategory = "framework"
		listParams.TagName = requestedFramework
	}
	cacheKey := marketplaceCacheKey("models", r.URL.RawQuery)
	if body, ok := getFreshMarketplaceCache(cacheKey, time.Now()); ok {
		writeCachedMarketplaceResponse(w, body, "fresh")
		return
	}

	client := csghub.NewClient(s.cfg.ServerURL, s.cfg.Token)
	models, total, err := client.ListModels(r.Context(), listParams)
	if err != nil {
		if body, ok := getAnyMarketplaceCache(cacheKey); ok {
			writeCachedMarketplaceResponse(w, body, "stale")
			return
		}
		writeError(w, marketplaceErrorStatus(err), err.Error())
		return
	}
	if requestedFramework != "" && !marketplaceModelsMatchFramework(models, requestedFramework) {
		models, total, err = listMarketplaceModelsWithFrameworkFallback(r.Context(), client, listParams, requestedFramework)
		if err != nil {
			if body, ok := getAnyMarketplaceCache(cacheKey); ok {
				writeCachedMarketplaceResponse(w, body, "stale")
				return
			}
			writeError(w, marketplaceErrorStatus(err), err.Error())
			return
		}
	}

	writeMarketplaceListResponse(w, cacheKey, map[string]interface{}{
		"data":  models,
		"total": total,
	})
}

type marketplaceModelQuantization struct {
	Name        string `json:"name"`
	FileCount   int    `json:"file_count"`
	ExamplePath string `json:"example_path"`
}

type marketplaceLocalModelStatus struct {
	Downloaded bool   `json:"downloaded"`
	FullName   string `json:"full_name,omitempty"`
	PublicID   string `json:"public_id,omitempty"`
}

type marketplaceModelDetailResponse struct {
	Details        *csghub.Model                  `json:"details"`
	Quantizations  []marketplaceModelQuantization `json:"quantizations,omitempty"`
	LocalInference api.LocalInferenceSupport      `json:"local_inference"`
	LocalModel     marketplaceLocalModelStatus    `json:"local_model"`
}

// GET /api/marketplace/models/{namespace}/{name} -- proxy to CSGHub model detail
func (s *Server) handleMarketplaceModelDetail(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "missing namespace or name")
		return
	}
	requestedModelID := strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)

	client := csghub.NewClient(s.cfg.ServerURL, s.cfg.Token)
	details, err := client.GetModel(r.Context(), namespace, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	format := marketplaceModelFormat(details.Tags)
	architecture := strings.TrimSpace(details.Metadata.Architecture)
	configMetadata := marketplaceModelConfigMetadata{}

	var (
		files         []csghub.RepoFile
		filesFetched  bool
		quantizations []marketplaceModelQuantization
	)
	if format == "gguf" || format == "" || architecture == "" {
		if fetched, err := client.GetModelTree(r.Context(), namespace, name); err == nil {
			files = fetched
			filesFetched = true
		}
	}
	if format == "" && filesFetched {
		format = marketplaceModelFormatFromFiles(files)
	}
	if architecture == "" && filesFetched && marketplaceRepoHasFile(files, "config.json") {
		if rawConfig, err := client.GetModelRawFile(r.Context(), namespace, name, "config.json"); err == nil {
			configMetadata = marketplaceMetadataFromConfig(rawConfig)
			architecture = configMetadata.Architecture
		}
	}
	enrichMarketplaceModelDetail(details, format, architecture, configMetadata)
	pipelineTag := marketplaceModelTaskTag(details.Tags)
	if format == "gguf" {
		if !filesFetched {
			if fetched, err := client.GetModelTree(r.Context(), namespace, name); err == nil {
				files = fetched
				filesFetched = true
			}
		}
		if filesFetched {
			quantizations = summarizeMarketplaceQuantizations(files)
		}
	}

	writeJSON(w, http.StatusOK, marketplaceModelDetailResponse{
		Details:       details,
		Quantizations: quantizations,
		LocalInference: localinference.FromMarketplaceModel(
			format,
			architecture,
			details.Metadata.ClassName,
			details.Path,
			pipelineTag,
		),
		LocalModel: s.marketplaceLocalModelStatus(details, requestedModelID),
	})
}

func (s *Server) marketplaceLocalModelStatus(details *csghub.Model, requestedModelID string) marketplaceLocalModelStatus {
	models, err := s.manager.List()
	if err != nil {
		return marketplaceLocalModelStatus{}
	}
	publicIDs := model.PublicModelIDs(models)
	candidates := marketplaceLocalModelCandidates(details, requestedModelID)
	for _, lm := range models {
		if lm == nil {
			continue
		}
		fullName := strings.TrimSpace(lm.FullName())
		publicID := strings.TrimSpace(publicIDs[fullName])
		if publicID == "" {
			publicID = model.InferenceModelID(lm)
		}
		if candidates[fullName] || candidates[publicID] || candidates[model.InferenceModelID(lm)] {
			return marketplaceLocalModelStatus{
				Downloaded: true,
				FullName:   fullName,
				PublicID:   publicID,
			}
		}
	}
	return marketplaceLocalModelStatus{}
}

func marketplaceLocalModelCandidates(details *csghub.Model, requestedModelID string) map[string]bool {
	candidates := make(map[string]bool)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			candidates[value] = true
		}
	}
	add(requestedModelID)
	if details == nil {
		return candidates
	}
	add(details.Path)
	add(details.HFPath)
	add(details.Name)
	return candidates
}

// GET /api/marketplace/datasets -- proxy to CSGHub Hub dataset listing
func (s *Server) handleMarketplaceDatasets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per"))
	if page <= 0 {
		page = 1
	}
	if per <= 0 {
		per = 16
	}

	cacheKey := marketplaceCacheKey("datasets", r.URL.RawQuery)
	if body, ok := getFreshMarketplaceCache(cacheKey, time.Now()); ok {
		writeCachedMarketplaceResponse(w, body, "fresh")
		return
	}

	client := csghub.NewClient(s.cfg.ServerURL, s.cfg.Token)
	datasets, total, err := client.ListDatasets(r.Context(), csghub.DatasetListParams{
		Search:  q.Get("search"),
		Sort:    q.Get("sort"),
		Page:    page,
		PerPage: per,
		Source:  q.Get("source"),
	})
	if err != nil {
		if body, ok := getAnyMarketplaceCache(cacheKey); ok {
			writeCachedMarketplaceResponse(w, body, "stale")
			return
		}
		writeError(w, marketplaceErrorStatus(err), err.Error())
		return
	}

	writeMarketplaceListResponse(w, cacheKey, map[string]interface{}{
		"data":  datasets,
		"total": total,
	})
}

func marketplaceCacheKey(kind, rawQuery string) string {
	return kind + "?" + rawQuery
}

func getFreshMarketplaceCache(key string, now time.Time) ([]byte, bool) {
	marketplaceListCache.Lock()
	defer marketplaceListCache.Unlock()
	entry, ok := marketplaceListCache.entries[key]
	if !ok || now.Sub(entry.updatedAt) > marketplaceListCacheTTL {
		return nil, false
	}
	return append([]byte(nil), entry.body...), true
}

func getAnyMarketplaceCache(key string) ([]byte, bool) {
	marketplaceListCache.Lock()
	defer marketplaceListCache.Unlock()
	entry, ok := marketplaceListCache.entries[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), entry.body...), true
}

func setMarketplaceCache(key string, body []byte) {
	marketplaceListCache.Lock()
	marketplaceListCache.entries[key] = marketplaceListCacheEntry{
		body:      append([]byte(nil), body...),
		updatedAt: time.Now(),
	}
	marketplaceListCache.Unlock()
}

func clearMarketplaceCache() {
	marketplaceListCache.Lock()
	marketplaceListCache.entries = make(map[string]marketplaceListCacheEntry)
	marketplaceListCache.Unlock()
}

func writeMarketplaceListResponse(w http.ResponseWriter, cacheKey string, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setMarketplaceCache(cacheKey, body)
	writeCachedMarketplaceResponse(w, body, "miss")
}

func writeCachedMarketplaceResponse(w http.ResponseWriter, body []byte, cacheStatus string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-CSGHUB-Lite-Cache", cacheStatus)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}

func marketplaceErrorStatus(err error) int {
	msg := err.Error()
	if strings.Contains(msg, "API error 429") {
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

func marketplaceModelFormat(tags []csghub.Tag) string {
	for _, tag := range tags {
		if tag.Category != "framework" {
			continue
		}
		name := normalizeMarketplaceFramework(tag.Name)
		switch name {
		case "gguf", "safetensors":
			return name
		}
	}
	return ""
}

func marketplaceModelFormatFromFiles(files []csghub.RepoFile) string {
	hasSafeTensors := false
	hasPyTorch := false
	for _, file := range files {
		path := strings.ToLower(marketplaceRepoFilePath(file))
		switch {
		case strings.HasSuffix(path, ".gguf"):
			return string(model.FormatGGUF)
		case strings.HasSuffix(path, ".safetensors"):
			hasSafeTensors = true
		case strings.HasSuffix(path, ".bin") || strings.HasSuffix(path, ".pt") || strings.HasSuffix(path, ".pth"):
			hasPyTorch = true
		}
	}
	if hasSafeTensors {
		return string(model.FormatSafeTensors)
	}
	if hasPyTorch {
		return string(model.FormatPyTorch)
	}
	return ""
}

func marketplaceRepoHasFile(files []csghub.RepoFile, name string) bool {
	name = strings.TrimSpace(name)
	for _, file := range files {
		if marketplaceRepoFilePath(file) == name {
			return true
		}
	}
	return false
}

type marketplaceModelConfigMetadata struct {
	Architecture    string
	ModelType       string
	TensorType      string
	SupportedModels []string
}

func marketplaceMetadataFromConfig(rawConfig string) marketplaceModelConfigMetadata {
	var cfg struct {
		Architectures   []string `json:"architectures"`
		SupportedArchs  []string `json:"supported_archs"`
		SupportedModels []string `json:"supported_models"`
		ModelType       string   `json:"model_type"`
		TorchDType      string   `json:"torch_dtype"`
	}
	if json.Unmarshal([]byte(rawConfig), &cfg) != nil {
		return marketplaceModelConfigMetadata{}
	}
	metadata := marketplaceModelConfigMetadata{
		ModelType:       strings.TrimSpace(cfg.ModelType),
		TensorType:      strings.TrimSpace(cfg.TorchDType),
		SupportedModels: cfg.SupportedModels,
	}
	if len(cfg.Architectures) > 0 {
		metadata.Architecture = strings.TrimSpace(cfg.Architectures[0])
	}
	if metadata.Architecture == "" && len(cfg.SupportedArchs) > 0 {
		metadata.Architecture = strings.TrimSpace(cfg.SupportedArchs[0])
	}
	if metadata.Architecture == "" {
		switch strings.ToLower(strings.TrimSpace(cfg.ModelType)) {
		case "glm_asr", "glm-asr":
			metadata.Architecture = "GlmAsrForConditionalGeneration"
		case "qwen3_asr", "qwen3-asr":
			metadata.Architecture = "Qwen3ASRForConditionalGeneration"
		case "whisper":
			metadata.Architecture = "WhisperForConditionalGeneration"
		}
	}
	return metadata
}

func enrichMarketplaceModelDetail(details *csghub.Model, format, architecture string, configMetadata marketplaceModelConfigMetadata) {
	if details == nil {
		return
	}
	if details.Tags == nil {
		details.Tags = []csghub.Tag{}
	}
	if format != "" {
		details.Tags = marketplaceEnsureTag(details.Tags, format, "framework", marketplaceFrameworkShowName(format))
	}
	if inferredTask := marketplaceInferredTaskTag(details, architecture, configMetadata.SupportedModels); inferredTask != "" {
		details.Tags = marketplaceEnsureTag(details.Tags, inferredTask, "task", marketplaceTaskShowName(inferredTask))
	}
	if strings.TrimSpace(details.Metadata.Architecture) == "" {
		details.Metadata.Architecture = architecture
	}
	if strings.TrimSpace(details.Metadata.ModelType) == "" {
		details.Metadata.ModelType = configMetadata.ModelType
	}
	if strings.TrimSpace(details.Metadata.TensorType) == "" {
		details.Metadata.TensorType = configMetadata.TensorType
	}
}

func marketplaceEnsureTag(tags []csghub.Tag, name, category, showName string) []csghub.Tag {
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	normalizedCategory := strings.TrimSpace(category)
	if normalizedName == "" || normalizedCategory == "" {
		return tags
	}
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag.Name), normalizedName) && strings.TrimSpace(tag.Category) == normalizedCategory {
			return tags
		}
	}
	return append(tags, csghub.Tag{
		Name:     normalizedName,
		Category: normalizedCategory,
		ShowName: showName,
	})
}

func marketplaceModelTaskTag(tags []csghub.Tag) string {
	for _, tag := range tags {
		if tag.Category == "task" {
			return strings.TrimSpace(tag.Name)
		}
	}
	return ""
}

func marketplaceFrameworkShowName(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case string(model.FormatGGUF):
		return "GGUF"
	case string(model.FormatSafeTensors):
		return "SafeTensors"
	case string(model.FormatPyTorch):
		return "PyTorch"
	default:
		return format
	}
}

func marketplaceInferredTaskTag(details *csghub.Model, architecture string, supportedModels []string) string {
	haystack := strings.ToLower(strings.Join([]string{
		details.Path,
		details.Name,
		details.Nickname,
		details.Metadata.ModelType,
		architecture,
		strings.Join(supportedModels, " "),
	}, " "))
	switch {
	case model.IsASRModelFamily(haystack):
		return "automatic-speech-recognition"
	case strings.Contains(haystack, "whisper") ||
		strings.Contains(haystack, "wav2vec") ||
		strings.Contains(haystack, "speech-recognition") ||
		strings.Contains(haystack, "automatic-speech-recognition") ||
		strings.Contains(haystack, "asr"):
		return "automatic-speech-recognition"
	case strings.Contains(haystack, "embedding"):
		return "feature-extraction"
	case strings.Contains(haystack, "reranker"):
		return "sentence-similarity"
	default:
		return ""
	}
}

func marketplaceTaskShowName(task string) string {
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "feature-extraction":
		return "Feature Extraction"
	case "sentence-similarity":
		return "Sentence Similarity"
	case "automatic-speech-recognition":
		return "Automatic Speech Recognition"
	default:
		return task
	}
}

func normalizeMarketplaceFramework(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gguf":
		return "gguf"
	case "safetensors":
		return "safetensors"
	default:
		return ""
	}
}

func marketplaceModelHasFramework(tags []csghub.Tag, framework string) bool {
	framework = normalizeMarketplaceFramework(framework)
	if framework == "" {
		return true
	}
	for _, tag := range tags {
		if tag.Category != "framework" {
			continue
		}
		if normalizeMarketplaceFramework(tag.Name) == framework {
			return true
		}
	}
	return false
}

func marketplaceModelsMatchFramework(models []csghub.Model, framework string) bool {
	for _, model := range models {
		if !marketplaceModelHasFramework(model.Tags, framework) {
			return false
		}
	}
	return true
}

func listMarketplaceModelsWithFrameworkFallback(
	ctx context.Context,
	client *csghub.Client,
	params csghub.ModelListParams,
	framework string,
) ([]csghub.Model, int, error) {
	const maxFallbackPages = 3

	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PerPage <= 0 {
		params.PerPage = 16
	}

	offset := (params.Page - 1) * params.PerPage
	upstreamPerPage := params.PerPage * 8
	if upstreamPerPage < 64 {
		upstreamPerPage = 64
	}
	if upstreamPerPage > 100 {
		upstreamPerPage = 100
	}

	items := make([]csghub.Model, 0, params.PerPage)
	matchedCount := 0

	for upstreamPage := 1; upstreamPage <= maxFallbackPages; upstreamPage++ {
		batch, upstreamTotal, err := client.ListModels(ctx, csghub.ModelListParams{
			Search:         params.Search,
			Sort:           params.Sort,
			Page:           upstreamPage,
			PerPage:        upstreamPerPage,
			Source:         params.Source,
			TagCategory:    params.TagCategory,
			TagName:        params.TagName,
			ModelParamsMin: params.ModelParamsMin,
			ModelParamsMax: params.ModelParamsMax,
		})
		if err != nil {
			if len(items) > 0 || matchedCount > 0 {
				return items, approximateMarketplaceFilteredTotal(offset, len(items), matchedCount), nil
			}
			return nil, 0, err
		}

		for _, model := range batch {
			if !marketplaceModelHasFramework(model.Tags, framework) {
				continue
			}
			if matchedCount >= offset && len(items) < params.PerPage {
				items = append(items, model)
			}
			matchedCount++
		}

		exhausted := len(batch) == 0 || upstreamPage*upstreamPerPage >= upstreamTotal
		if len(items) >= params.PerPage {
			if exhausted {
				return items, matchedCount, nil
			}
			// Upstream doesn't honor the filter reliably, so expose the current page
			// and keep the next page navigable without claiming a fake exact total.
			return items, offset + len(items) + 1, nil
		}
		if exhausted {
			return items, matchedCount, nil
		}
	}

	return items, approximateMarketplaceFilteredTotal(offset, len(items), matchedCount), nil
}

func approximateMarketplaceFilteredTotal(offset, itemCount, matchedCount int) int {
	total := matchedCount
	minimumNextPageTotal := offset + itemCount + 1
	if total < minimumNextPageTotal {
		total = minimumNextPageTotal
	}
	return total
}

func summarizeMarketplaceQuantizations(files []csghub.RepoFile) []marketplaceModelQuantization {
	type agg struct {
		item marketplaceModelQuantization
		rank int
	}

	byName := make(map[string]*agg)
	for _, file := range files {
		path := marketplaceRepoFilePath(file)
		if !ggufpick.IsWeightGGUF(path) {
			continue
		}
		label := ggufpick.QuantLabelFromRepoPath(path)
		if label == "" {
			continue
		}
		entry, ok := byName[label]
		if !ok {
			entry = &agg{
				item: marketplaceModelQuantization{
					Name:        label,
					ExamplePath: path,
				},
				rank: ggufpick.QuantRankFromRepoPath(path),
			}
			byName[label] = entry
		}
		entry.item.FileCount++
		if entry.item.ExamplePath == "" || strings.Compare(path, entry.item.ExamplePath) < 0 {
			entry.item.ExamplePath = path
		}
	}

	out := make([]marketplaceModelQuantization, 0, len(byName))
	keys := make([]string, 0, len(byName))
	for key := range byName {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := byName[keys[i]]
		right := byName[keys[j]]
		if left.rank != right.rank {
			return left.rank > right.rank
		}
		return left.item.Name < right.item.Name
	})
	for _, key := range keys {
		out = append(out, byName[key].item)
	}
	return out
}

func marketplaceRepoFilePath(file csghub.RepoFile) string {
	if strings.TrimSpace(file.Path) != "" {
		return file.Path
	}
	return file.Name
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

const xiaozhiAppID = "xiaozhi"

var xiaozhiModelSlots = []struct {
	task     string
	required bool
}{
	{task: "language_model", required: true},
	{task: "speech_recognition"},
	{task: "embedding"},
	{task: "image_generation"},
}

var xiaozhiScenarioKeys = map[string][]string{
	"language_model": {
		"chat",
		"coding",
		"complex_text_generation",
		"quick_decision_making",
		"quick_text_generation",
		"polish_and_summarize",
	},
	"speech_recognition": {"audio_transcribing"},
	"embedding":          {"embedding"},
	"image_generation":   {"image"},
}

func (s *Server) enrichXiaozhiModelSlots(ctx context.Context, info *api.AIAppInfo) {
	if info == nil || info.ID != xiaozhiAppID {
		return
	}
	models, err := s.listXiaozhiAvailableModels(ctx, false)
	if err != nil {
		return
	}
	saved := s.savedXiaozhiModelBindings()
	slots := recommendXiaozhiModelSlots(models, saved)
	info.ModelSlots = slots
	info.ModelBindings = bindingsFromXiaozhiSlots(slots)
	for _, binding := range info.ModelBindings {
		if binding.Task == "language_model" {
			info.ModelID = binding.ModelID
			break
		}
	}
}

// prepareXiaozhiLaunch resolves stale or missing bindings before synchronizing
// the mounted Xiaozhi configuration.
func (s *Server) prepareXiaozhiLaunch(ctx context.Context) error {
	models, err := s.listXiaozhiAvailableModels(ctx, true)
	if err != nil {
		return fmt.Errorf("listing models for Xiaozhi: %w", err)
	}
	slots := recommendXiaozhiModelSlots(models, s.savedXiaozhiModelBindings())
	bindings := bindingsFromXiaozhiSlots(slots)
	if _, err := validateXiaozhiModelBindings(bindings, models); err != nil {
		return err
	}
	if err := s.persistXiaozhiModelBindings(bindings); err != nil {
		return err
	}
	return s.syncXiaozhiConfig()
}

func (s *Server) saveXiaozhiModelBindings(ctx context.Context, requested []api.AIAppModelBinding) error {
	if s == nil || s.cfg == nil {
		return errors.New("server configuration is unavailable")
	}
	models, err := s.listXiaozhiAvailableModels(ctx, true)
	if err != nil {
		return fmt.Errorf("listing models for Xiaozhi: %w", err)
	}
	bindings, err := validateXiaozhiModelBindings(requested, models)
	if err != nil {
		return err
	}
	return s.persistXiaozhiModelBindings(bindings)
}

func (s *Server) persistXiaozhiModelBindings(bindings []api.AIAppModelBinding) error {
	if s == nil || s.cfg == nil {
		return errors.New("server configuration is unavailable")
	}
	s.prefsMu.Lock()
	if s.cfg.AIAppModelBindings == nil {
		s.cfg.AIAppModelBindings = map[string][]api.AIAppModelBinding{}
	}
	previous, hadPrevious := s.cfg.AIAppModelBindings[xiaozhiAppID]
	s.cfg.AIAppModelBindings[xiaozhiAppID] = append([]api.AIAppModelBinding(nil), bindings...)
	err := config.Save(s.cfg)
	if err != nil {
		if hadPrevious {
			s.cfg.AIAppModelBindings[xiaozhiAppID] = previous
		} else {
			delete(s.cfg.AIAppModelBindings, xiaozhiAppID)
		}
	}
	s.prefsMu.Unlock()
	if err != nil {
		return fmt.Errorf("saving Xiaozhi model bindings: %w", err)
	}
	return nil
}

func (s *Server) savedXiaozhiModelBindings() []api.AIAppModelBinding {
	if s == nil || s.cfg == nil {
		return nil
	}
	s.prefsMu.Lock()
	defer s.prefsMu.Unlock()
	return append([]api.AIAppModelBinding(nil), s.cfg.AIAppModelBindings[xiaozhiAppID]...)
}

func (s *Server) listXiaozhiAvailableModels(ctx context.Context, refreshCloud bool) ([]api.ModelInfo, error) {
	localModels, err := s.listLocalModelInfos()
	if err != nil {
		return nil, err
	}
	models := append([]api.ModelInfo(nil), localModels...)
	cloudModels, cloudErr := s.listCloudModels(ctx, refreshCloud)
	if cloudErr == nil {
		models = append(models, cloudModels...)
	}
	models = append(models, s.listSelectedThirdPartyProviderModels(ctx)...)
	sort.SliceStable(models, func(i, j int) bool {
		iPriority := xiaozhiSourcePriority(models[i].Source)
		jPriority := xiaozhiSourcePriority(models[j].Source)
		if iPriority != jPriority {
			return iPriority < jPriority
		}
		if models[i].Model != models[j].Model {
			return models[i].Model < models[j].Model
		}
		return models[i].Source < models[j].Source
	})
	return models, nil
}

func recommendXiaozhiModelSlots(models []api.ModelInfo, saved []api.AIAppModelBinding) []api.AIAppModelSlot {
	slots := make([]api.AIAppModelSlot, 0, len(xiaozhiModelSlots))
	for _, definition := range xiaozhiModelSlots {
		slot := api.AIAppModelSlot{Task: definition.task, Required: definition.required}
		for _, binding := range saved {
			if binding.Task == definition.task && xiaozhiBindingMatchesModel(binding, models) {
				value := binding
				slot.Binding = &value
				break
			}
		}
		if slot.Binding == nil {
			for _, model := range models {
				if xiaozhiModelMatchesTask(model, definition.task) &&
					xiaozhiModelIDUnambiguous(model.Model, definition.task, models) {
					value := api.AIAppModelBinding{
						Task:    definition.task,
						ModelID: strings.TrimSpace(model.Model),
						Source:  strings.TrimSpace(model.Source),
					}
					slot.Binding = &value
					break
				}
			}
		}
		slots = append(slots, slot)
	}
	return slots
}

func bindingsFromXiaozhiSlots(slots []api.AIAppModelSlot) []api.AIAppModelBinding {
	bindings := make([]api.AIAppModelBinding, 0, len(slots))
	for _, slot := range slots {
		if slot.Binding != nil {
			bindings = append(bindings, *slot.Binding)
		}
	}
	return bindings
}

func validateXiaozhiModelBindings(requested []api.AIAppModelBinding, models []api.ModelInfo) ([]api.AIAppModelBinding, error) {
	if len(requested) == 0 {
		return nil, errors.New("model_bindings are required for Xiaozhi")
	}
	seenTasks := make(map[string]struct{}, len(requested))
	validated := make([]api.AIAppModelBinding, 0, len(requested))
	hasLanguageModel := false
	for _, binding := range requested {
		binding.Task = strings.TrimSpace(binding.Task)
		binding.ModelID = strings.TrimSpace(binding.ModelID)
		binding.Source = strings.TrimSpace(binding.Source)
		if _, ok := xiaozhiScenarioKeys[binding.Task]; !ok {
			return nil, fmt.Errorf("unsupported Xiaozhi model task %q", binding.Task)
		}
		if _, exists := seenTasks[binding.Task]; exists {
			return nil, fmt.Errorf("duplicate Xiaozhi model task %q", binding.Task)
		}
		seenTasks[binding.Task] = struct{}{}
		if binding.ModelID == "" {
			return nil, fmt.Errorf("model_id is required for Xiaozhi task %q", binding.Task)
		}

		matches := make([]api.ModelInfo, 0, 1)
		for _, model := range models {
			if strings.TrimSpace(model.Model) != binding.ModelID || !xiaozhiModelMatchesTask(model, binding.Task) {
				continue
			}
			if binding.Source == "" || strings.EqualFold(strings.TrimSpace(model.Source), binding.Source) {
				matches = append(matches, model)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("model %q from source %q is not available for Xiaozhi task %q", binding.ModelID, binding.Source, binding.Task)
		}
		if !xiaozhiModelIDUnambiguous(binding.ModelID, binding.Task, models) {
			return nil, fmt.Errorf("model %q is available from multiple sources and Xiaozhi cannot preserve source on inference requests; choose a model ID unique to one source", binding.ModelID)
		}
		if binding.Source == "" {
			sources := make(map[string]struct{}, len(matches))
			for _, match := range matches {
				sources[strings.ToLower(strings.TrimSpace(match.Source))] = struct{}{}
			}
			if len(sources) > 1 {
				return nil, fmt.Errorf("model %q is available from multiple sources; source is required", binding.ModelID)
			}
			binding.Source = strings.TrimSpace(matches[0].Source)
		}
		validated = append(validated, binding)
		hasLanguageModel = hasLanguageModel || binding.Task == "language_model"
	}
	if !hasLanguageModel {
		return nil, errors.New("a language_model binding is required for Xiaozhi")
	}
	return validated, nil
}

func xiaozhiBindingMatchesModel(binding api.AIAppModelBinding, models []api.ModelInfo) bool {
	if !xiaozhiModelIDUnambiguous(binding.ModelID, binding.Task, models) {
		return false
	}
	for _, model := range models {
		if strings.TrimSpace(model.Model) == strings.TrimSpace(binding.ModelID) &&
			strings.EqualFold(strings.TrimSpace(model.Source), strings.TrimSpace(binding.Source)) &&
			xiaozhiModelMatchesTask(model, binding.Task) {
			return true
		}
	}
	return false
}

func xiaozhiModelIDUnambiguous(modelID, task string, models []api.ModelInfo) bool {
	sources := map[string]struct{}{}
	for _, model := range models {
		if strings.TrimSpace(model.Model) != strings.TrimSpace(modelID) || !xiaozhiModelMatchesTask(model, task) {
			continue
		}
		sources[strings.ToLower(strings.TrimSpace(model.Source))] = struct{}{}
	}
	return len(sources) <= 1
}

func xiaozhiModelMatchesTask(model api.ModelInfo, task string) bool {
	return categoryForPipelineTag(model.PipelineTag) == task
}

func xiaozhiSourcePriority(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "local":
		return 0
	case "cloud":
		return 1
	default:
		if providerIDFromSource(source) != "" {
			return 2
		}
		return 3
	}
}

// syncXiaozhiConfig is called by the Xiaozhi lifecycle before startup. It
// updates only the OpenAI provider and the scenarios managed by csghub-lite.
func (s *Server) syncXiaozhiConfig() error {
	if s == nil || s.cfg == nil {
		return errors.New("server configuration is unavailable")
	}
	bindings := s.savedXiaozhiModelBindings()
	if len(bindings) == 0 {
		return errors.New("Xiaozhi has no saved model bindings")
	}
	scenarios := xiaozhiScenarioMap(bindings)
	if scenarios["chat"] == "" {
		return errors.New("Xiaozhi requires a language_model binding")
	}

	path := filepath.Join(s.cfg.StorageDir(), "apps", xiaozhiAppID, "config", "config.json")
	root := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing Xiaozhi config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading Xiaozhi config: %w", err)
	}

	copilot := objectMap(root["copilot"])
	root["copilot"] = copilot
	baseURL, err := xiaozhiLiteBaseURL(s.cfg.ListenAddr)
	if err != nil {
		return err
	}
	provider := objectMap(copilot["providers.openai"])
	provider["baseURL"] = baseURL
	apiKey := strings.TrimSpace(s.cfg.Token)
	if apiKey == "" {
		apiKey = "csghub-lite"
	}
	provider["apiKey"] = apiKey
	copilot["providers.openai"] = provider

	scenarioConfig := objectMap(copilot["scenarios"])
	scenarioConfig["override_enabled"] = true
	configuredScenarios := objectMap(scenarioConfig["scenarios"])
	for _, keys := range xiaozhiScenarioKeys {
		for _, key := range keys {
			delete(configuredScenarios, key)
		}
	}
	for key, modelID := range scenarios {
		configuredScenarios[key] = modelID
	}
	scenarioConfig["scenarios"] = configuredScenarios
	copilot["scenarios"] = scenarioConfig

	data, err = json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding Xiaozhi config: %w", err)
	}
	data = append(data, '\n')
	if err := writeXiaozhiConfigAtomic(path, data); err != nil {
		return fmt.Errorf("writing Xiaozhi config: %w", err)
	}
	return nil
}

func xiaozhiLiteBaseURL(listenAddr string) (string, error) {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		listenAddr = config.DefaultListenAddr
	}
	if strings.Contains(listenAddr, "://") {
		parsed, err := neturl.Parse(listenAddr)
		if err != nil {
			return "", fmt.Errorf("parsing csghub-lite listen address: %w", err)
		}
		listenAddr = parsed.Host
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		if strings.HasPrefix(listenAddr, ":") {
			port = strings.TrimPrefix(listenAddr, ":")
		} else {
			return "", fmt.Errorf("Xiaozhi requires a listen address with a port, got %q", listenAddr)
		}
	}
	if port == "" {
		return "", errors.New("Xiaozhi requires csghub-lite to listen on a TCP port")
	}
	normalizedHost := strings.Trim(strings.ToLower(host), "[]")
	if normalizedHost == "localhost" || normalizedHost == "127.0.0.1" || normalizedHost == "::1" {
		return "", errors.New("Xiaozhi requires csghub-lite to listen on all interfaces (for example :11435) so its Docker container can reach the model API")
	}
	return "http://host.docker.internal:" + port + "/v1", nil
}

func xiaozhiScenarioMap(bindings []api.AIAppModelBinding) map[string]string {
	scenarios := make(map[string]string)
	for _, binding := range bindings {
		modelID := strings.TrimSpace(binding.ModelID)
		for _, scenario := range xiaozhiScenarioKeys[strings.TrimSpace(binding.Task)] {
			scenarios[scenario] = modelID
		}
	}
	return scenarios
}

func objectMap(value interface{}) map[string]interface{} {
	if object, ok := value.(map[string]interface{}); ok {
		return object
	}
	return map[string]interface{}{}
}

func writeXiaozhiConfigAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

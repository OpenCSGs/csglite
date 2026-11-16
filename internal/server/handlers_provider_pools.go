package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/provider-pools
func (s *Server) handleProviderPoolsList(w http.ResponseWriter, r *http.Request) {
	pools := config.GetProviderPools()
	out := make([]api.ProviderPool, 0, len(pools))
	for _, pool := range pools {
		out = append(out, s.providerPoolView(pool))
	}
	writeJSON(w, http.StatusOK, api.ProviderPoolsResponse{Pools: out})
}

// POST /api/provider-pools
func (s *Server) handleProviderPoolCreate(w http.ResponseWriter, r *http.Request) {
	var req api.ProviderPoolCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pool, err := s.newProviderPool(r, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pools := config.GetProviderPools()
	pools = append(pools, pool)
	if err := config.SaveProviderPools(pools); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider pool")
		return
	}
	writeJSON(w, http.StatusCreated, s.providerPoolView(pool))
}

// PUT /api/provider-pools/{id}
func (s *Server) handleProviderPoolUpdate(w http.ResponseWriter, r *http.Request) {
	var req api.ProviderPoolUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	pools := config.GetProviderPools()
	for i := range pools {
		if pools[i].ID != id {
			continue
		}
		candidate := pools[i]
		if req.Name != nil {
			candidate.Name = strings.TrimSpace(*req.Name)
		}
		if req.Model != nil {
			candidate.Model = strings.TrimSpace(*req.Model)
		}
		if req.Enabled != nil {
			candidate.Enabled = *req.Enabled
		}
		if req.Category != nil {
			candidate.Category = strings.TrimSpace(*req.Category)
		}
		if req.Policy != nil {
			candidate.Policy = strings.TrimSpace(*req.Policy)
		}
		if req.Members != nil {
			candidate.Members = providerPoolMembersFromAPI(*req.Members)
		}
		if err := s.validateProviderPool(r, candidate, pools, id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		pools[i] = candidate
		if err := config.SaveProviderPools(pools); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save provider pool")
			return
		}
		writeJSON(w, http.StatusOK, s.providerPoolView(candidate))
		return
	}
	writeError(w, http.StatusNotFound, "provider pool not found")
}

// DELETE /api/provider-pools/{id}
func (s *Server) handleProviderPoolDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	pools := config.GetProviderPools()
	out := make([]config.ProviderPool, 0, len(pools))
	found := false
	for _, pool := range pools {
		if pool.ID == id {
			found = true
			continue
		}
		out = append(out, pool)
	}
	if !found {
		writeError(w, http.StatusNotFound, "provider pool not found")
		return
	}
	if err := config.SaveProviderPools(out); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider pools")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) newProviderPool(r *http.Request, req api.ProviderPoolCreateRequest) (config.ProviderPool, error) {
	pool := config.ProviderPool{
		ID:       config.GenerateProviderID(),
		Name:     strings.TrimSpace(req.Name),
		Model:    strings.TrimSpace(req.Model),
		Enabled:  boolDefault(req.Enabled, true),
		Policy:   config.NormalizeProviderPoolPolicy(req.Policy),
		Category: strings.TrimSpace(req.Category),
		Members:  providerPoolMembersFromAPI(req.Members),
	}
	if strings.TrimSpace(req.Policy) != "" && strings.TrimSpace(req.Policy) != pool.Policy {
		return config.ProviderPool{}, errProviderPool("unsupported provider pool policy")
	}
	return pool, s.validateProviderPool(r, pool, config.GetProviderPools(), "")
}

func (s *Server) validateProviderPool(_ *http.Request, pool config.ProviderPool, pools []config.ProviderPool, excludeID string) error {
	pool.Name = strings.TrimSpace(pool.Name)
	pool.Model = strings.TrimSpace(pool.Model)
	if pool.Name == "" {
		return errProviderPool("name is required")
	}
	if pool.Model == "" {
		return errProviderPool("model is required")
	}
	if len(pool.Members) == 0 {
		return errProviderPool("at least one member is required")
	}
	switch pool.Policy {
	case "", config.ProviderPoolPolicyPriorityWeight, config.ProviderPoolPolicyFixed, config.ProviderPoolPolicyFailover, config.ProviderPoolPolicyWeighted:
	default:
		return errProviderPool("unsupported provider pool policy")
	}
	if pool.Policy == config.ProviderPoolPolicyFixed && len(pool.Members) != 1 {
		return errProviderPool("fixed routing requires exactly one source")
	}
	if pool.Category != "" && pool.Category != "chat" && pool.Category != "embedding" && pool.Category != "image" && pool.Category != "asr" && pool.Category != "tts" && pool.Category != "decision" {
		return errProviderPool("category must be chat, embedding, image, asr, tts or decision")
	}
	for _, existing := range pools {
		if existing.ID == excludeID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(existing.Name), pool.Name) {
			return errProviderPool("provider pool name already exists")
		}
		if strings.EqualFold(strings.TrimSpace(existing.Model), pool.Model) {
			return errProviderPool("model service ID already exists; add sources to the existing service")
		}
	}
	memberIDs := make(map[string]struct{}, len(pool.Members))
	memberModels := make(map[string]bool)
	for _, member := range pool.Members {
		if member.ID == "" || member.Model == "" {
			return errProviderPool("each member requires an id and model")
		}
		if _, exists := memberIDs[member.ID]; exists {
			return errProviderPool("provider pool member IDs must be unique")
		}
		memberIDs[member.ID] = struct{}{}
		source := strings.TrimSpace(member.Source)
		key := source + "\x00" + member.Model
		if memberModels[key] {
			return errProviderPool("duplicate source and model in service")
		}
		memberModels[key] = true
		if cluster.IsClusterSource(source) {
			continue
		}
		switch source {
		case "local", "cloud":
		default:
			providerID := providerIDFromSource(source)
			if providerID == "" {
				return errProviderPool("member source must be local, cloud, cluster, node:<uuid>, or provider:<id>")
			}
			_, ok := getThirdPartyProvider(providerID)
			if !ok {
				retained := false
				for _, existing := range pools {
					if existing.ID != excludeID {
						continue
					}
					for _, prior := range existing.Members {
						if prior.ID == member.ID && prior.Source == member.Source && prior.Model == member.Model {
							retained = true
						}
					}
				}
				if !retained {
					return errProviderPool("member provider is unavailable")
				}
			}
		}
	}
	return nil
}

type providerPoolError string

func (e providerPoolError) Error() string { return string(e) }

func errProviderPool(message string) error { return providerPoolError(message) }

func providerPoolAPI(pool config.ProviderPool) api.ProviderPool {
	return api.ProviderPool{
		ID:       pool.ID,
		Name:     pool.Name,
		Model:    pool.Model,
		Enabled:  pool.Enabled,
		Policy:   config.NormalizeProviderPoolPolicy(pool.Policy),
		Category: pool.Category,
		Members:  providerPoolMembersAPI(pool.Members),
	}
}

func providerPoolMembersAPI(members []config.ProviderPoolMember) []api.ProviderPoolMember {
	out := make([]api.ProviderPoolMember, len(members))
	for i, member := range members {
		out[i] = api.ProviderPoolMember(member)
	}
	return out
}

func providerPoolMembersFromAPI(members []api.ProviderPoolMember) []config.ProviderPoolMember {
	out := make([]config.ProviderPoolMember, len(members))
	for i, member := range members {
		out[i] = config.ProviderPoolMember(member)
		out[i].ID = strings.TrimSpace(out[i].ID)
		out[i].Source = strings.TrimSpace(out[i].Source)
		out[i].Model = strings.TrimSpace(out[i].Model)
		if out[i].Weight < 1 {
			out[i].Weight = 100
		}
		if out[i].Priority < 0 {
			out[i].Priority = 0
		}
		if out[i].RequestsPM < 0 {
			out[i].RequestsPM = 0
		}
		if out[i].TokensPM < 0 {
			out[i].TokensPM = 0
		}
		if out[i].MaxConcurrent < 0 {
			out[i].MaxConcurrent = 0
		}
	}
	return out
}

// Source availability includes current cluster telemetry, without an inference probe.
func (s *Server) providerPoolView(pool config.ProviderPool) api.ProviderPool {
	out := providerPoolAPI(pool)
	out.SourceStates = make(map[string]string, len(pool.Members))
	for _, member := range pool.Members {
		out.SourceStates[member.ID] = s.providerPoolSourceState(member)
	}
	s.fillPoolProtocols(pool, &out)
	return out
}

func (s *Server) providerPoolSourceState(member config.ProviderPoolMember) string {
	if member.Disabled {
		return "disabled"
	}
	switch member.Source {
	case "local":
		if !s.isLocalAPIUsageModel(member.Model) {
			return "missing"
		}
	case "cloud":
		if s.cfg != nil && !s.cfg.IsCloudProviderEnabled() {
			return "disabled"
		}
	default:
		if cluster.IsClusterSource(member.Source) {
			if s.cluster == nil {
				return "missing"
			}
			return s.cluster.ModelSourceState(context.Background(), member.Model, member.Source)
		} else {
			provider, ok := getThirdPartyProvider(providerIDFromSource(member.Source))
			if !ok {
				return "missing"
			}
			if !provider.Enabled {
				return "disabled"
			}
		}
	}
	return "configured"
}

func (s *Server) providerPoolAvailable(pool config.ProviderPool) bool {
	if !pool.Enabled {
		return false
	}
	for i, member := range pool.Members {
		if pool.Policy == config.ProviderPoolPolicyFixed && i > 0 {
			break
		}
		if s.providerPoolSourceState(member) == "configured" {
			return true
		}
	}
	return false
}

func providerPoolUnavailableError(pool config.ProviderPool) error {
	for i, member := range pool.Members {
		if pool.Policy == config.ProviderPoolPolicyFixed && i > 0 {
			break
		}
		if !member.Disabled && cluster.IsClusterSource(member.Source) {
			return inference.NewHTTPStatusErrorWithCause(http.StatusServiceUnavailable, &cluster.ErrNoNode{Model: member.Model})
		}
	}
	return inference.NewHTTPStatusError(http.StatusServiceUnavailable, "model service has no available sources")
}

// Preserve unscoped legacy routing when a service shares an ID with a direct
// model. The unified gateway explicitly selects the service before routing.
func (s *Server) hasDirectModelID(model string) bool {
	if s.isLocalAPIUsageModel(model) || (s.cluster != nil && s.cluster.RemoteHasModel(model)) {
		return true
	}
	for _, known := range s.applyCloudProviderModelSelections(s.cloud.KnownChatModels()) {
		if strings.EqualFold(model, known.Model) {
			return true
		}
	}
	ids := []string{cloudProviderRouteID}
	for _, provider := range config.GetProviders() {
		ids = append(ids, provider.ID)
	}
	for _, id := range ids {
		for _, selected := range config.GetProviderModelSelections(id) {
			if strings.EqualFold(model, selected.Model) {
				return true
			}
		}
	}
	return false
}

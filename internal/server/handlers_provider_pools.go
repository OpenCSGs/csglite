package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

const semanticEmbeddingModel = "text-embedding-3-small"

// GET /api/provider-pools
func (s *Server) handleProviderPoolsList(w http.ResponseWriter, r *http.Request) {
	pools := config.GetProviderPools()
	out := make([]api.ProviderPool, 0, len(pools))
	capabilities := s.providerPoolPolicyCapabilities(r.Context(), false)
	for _, pool := range pools {
		out = append(out, providerPoolAPI(pool, capabilities))
	}
	writeJSON(w, http.StatusOK, api.ProviderPoolsResponse{Pools: out})
}

// GET /api/provider-pool-policies
func (s *Server) handleProviderPoolPolicies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.ProviderPoolPolicyCapabilitiesResponse{
		Policies: s.providerPoolPolicyCapabilities(r.Context(), requestWantsModelRefresh(r)),
	})
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
	s.enqueueRouterCuration(pool.ID)
	writeJSON(w, http.StatusCreated, providerPoolAPI(pool, s.providerPoolPolicyCapabilities(r.Context(), false)))
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
		s.enqueueRouterCuration(candidate.ID)
		writeJSON(w, http.StatusOK, providerPoolAPI(candidate, s.providerPoolPolicyCapabilities(r.Context(), false)))
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
		ID:      config.GenerateProviderID(),
		Name:    strings.TrimSpace(req.Name),
		Model:   strings.TrimSpace(req.Model),
		Enabled: boolDefault(req.Enabled, true),
		Policy:  config.NormalizeProviderPoolPolicy(req.Policy),
		Members: providerPoolMembersFromAPI(req.Members),
	}
	if strings.TrimSpace(req.Policy) != "" && strings.TrimSpace(req.Policy) != pool.Policy {
		return config.ProviderPool{}, errProviderPool("unsupported provider pool policy")
	}
	return pool, s.validateProviderPool(r, pool, config.GetProviderPools(), "")
}

func (s *Server) validateProviderPool(r *http.Request, pool config.ProviderPool, pools []config.ProviderPool, excludeID string) error {
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
	case "", config.ProviderPoolPolicyPriorityWeight:
		pool.Policy = config.ProviderPoolPolicyPriorityWeight
	case config.ProviderPoolPolicySemantic:
		capability := semanticPolicyCapability(s.providerPoolPolicyCapabilities(r.Context(), true))
		if !capability.Available {
			return errProviderPool("semantic policy is unavailable: " + capability.Reason)
		}
	default:
		return errProviderPool("unsupported provider pool policy")
	}
	for _, existing := range pools {
		if existing.ID == excludeID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(existing.Name), pool.Name) {
			return errProviderPool("provider pool name already exists")
		}
		if strings.EqualFold(strings.TrimSpace(existing.Model), pool.Model) {
			return errProviderPool("provider pool model already exists")
		}
	}
	if s.isLocalAPIUsageModel(pool.Model) || s.thirdPartyProviderSourceForModel(r.Context(), pool.Model) != "" {
		return errProviderPool("provider pool model conflicts with an existing model ID")
	}
	memberIDs := make(map[string]struct{}, len(pool.Members))
	for _, member := range pool.Members {
		if member.ID == "" || member.Model == "" {
			return errProviderPool("each member requires an id and model")
		}
		if _, exists := memberIDs[member.ID]; exists {
			return errProviderPool("provider pool member IDs must be unique")
		}
		memberIDs[member.ID] = struct{}{}
		source := strings.TrimSpace(member.Source)
		switch source {
		case "local", "cloud":
		default:
			providerID := providerIDFromSource(source)
			if providerID == "" {
				return errProviderPool("member source must be local, cloud, or provider:<id>")
			}
			provider, ok := getThirdPartyProvider(providerID)
			if !ok || !provider.Enabled {
				return errProviderPool("member provider is unavailable")
			}
		}
	}
	return nil
}

func (s *Server) providerPoolPolicyCapabilities(ctx context.Context, refresh bool) []api.ProviderPoolPolicyCapability {
	auth := s.cloudAuthStatus(ctx)
	if !hasProviderPoolSemanticCredential(auth) {
		return providerPoolPolicyCapabilitiesFor(false, nil, nil)
	}
	models, err := s.listCloudModelCatalog(ctx, refresh)
	return providerPoolPolicyCapabilitiesFor(true, models, err)
}

func hasProviderPoolSemanticCredential(auth cloudAuthStatus) bool {
	return auth.Authenticated || auth.HasAPIKey
}

func providerPoolPolicyCapabilitiesFor(hasCloudCredential bool, models []api.ModelInfo, catalogErr error) []api.ProviderPoolPolicyCapability {
	capabilities := []api.ProviderPoolPolicyCapability{{
		Type:      config.ProviderPoolPolicyPriorityWeight,
		Available: true,
	}, {
		Type:         config.ProviderPoolPolicySemantic,
		Experimental: true,
	}}
	semantic := &capabilities[1]
	if !hasCloudCredential {
		semantic.Reason = "opencsg_login_required"
		return capabilities
	}
	if catalogErr != nil {
		semantic.Reason = "gateway_catalog_unavailable"
		return capabilities
	}
	for _, model := range models {
		if strings.TrimSpace(model.Model) == semanticEmbeddingModel &&
			strings.TrimSpace(model.PipelineTag) == "feature-extraction" {
			semantic.Available = true
			return capabilities
		}
	}
	semantic.Reason = "required_embedding_model_unavailable"
	return capabilities
}

func semanticPolicyCapability(capabilities []api.ProviderPoolPolicyCapability) api.ProviderPoolPolicyCapability {
	for _, capability := range capabilities {
		if capability.Type == config.ProviderPoolPolicySemantic {
			return capability
		}
	}
	return api.ProviderPoolPolicyCapability{
		Type:   config.ProviderPoolPolicySemantic,
		Reason: "gateway_catalog_unavailable",
	}
}

type providerPoolError string

func (e providerPoolError) Error() string { return string(e) }

func errProviderPool(message string) error { return providerPoolError(message) }

func providerPoolAPI(pool config.ProviderPool, capabilities []api.ProviderPoolPolicyCapability) api.ProviderPool {
	policy := config.NormalizeProviderPoolPolicy(pool.Policy)
	available := true
	reason := ""
	if policy == config.ProviderPoolPolicySemantic {
		capability := semanticPolicyCapability(capabilities)
		available = capability.Available
		reason = capability.Reason
	}
	return api.ProviderPool{
		ID:                      pool.ID,
		Name:                    pool.Name,
		Model:                   pool.Model,
		Enabled:                 pool.Enabled,
		Policy:                  policy,
		PolicyAvailable:         available,
		PolicyUnavailableReason: reason,
		Members:                 providerPoolMembersAPI(pool.Members),
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

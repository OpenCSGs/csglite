package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	routerprofile "github.com/opencsgs/semantic-router"
)

const (
	semanticFallbackMemberDrift        = "member_drift"
	semanticFallbackLegacyIncompatible = "legacy_profile_incompatible"
)

type providerPoolSemanticInput struct {
	messages []routerprofile.Message
}

func semanticInputFromInference(messages []inference.Message) providerPoolSemanticInput {
	out := make([]routerprofile.Message, 0, len(messages))
	for _, message := range messages {
		if content := providerPoolSemanticContentText(message.Content); content != "" {
			out = append(out, routerprofile.Message{Role: message.Role, Content: content})
		}
	}
	return providerPoolSemanticInput{messages: out}
}

func semanticInputFromRequest(body map[string]interface{}) providerPoolSemanticInput {
	out := make([]routerprofile.Message, 0)
	appendMessage := func(message map[string]interface{}) {
		role := strings.TrimSpace(fmt.Sprint(message["role"]))
		if content := providerPoolSemanticContentText(message["content"]); role != "" && content != "" {
			out = append(out, routerprofile.Message{Role: role, Content: content})
		}
	}
	switch messages := body["messages"].(type) {
	case []interface{}:
		for _, value := range messages {
			if message, ok := value.(map[string]interface{}); ok {
				appendMessage(message)
			}
		}
	case []map[string]interface{}:
		for _, message := range messages {
			appendMessage(message)
		}
	}
	return providerPoolSemanticInput{messages: out}
}

func semanticInputFromPrompt(prompt string) providerPoolSemanticInput {
	return providerPoolSemanticInput{messages: []routerprofile.Message{{Role: "user", Content: strings.TrimSpace(prompt)}}}
}

func (s *Server) refreshAllRouterProfiles(ctx context.Context) error {
	if s.routerProfiles == nil {
		return nil
	}
	next := make(map[string]*routerprofile.Profile)
	for _, pool := range config.GetProviderPools() {
		if config.NormalizeProviderPoolPolicy(pool.Policy) != config.ProviderPoolPolicySemantic {
			continue
		}
		profile, err := s.routerProfiles.ActiveProfile(ctx, pool.ID)
		if errors.Is(err, sql.ErrNoRows) {
			next[pool.ID] = nil
			continue
		}
		if err != nil {
			return fmt.Errorf("loading active profile for pool %s: %w", pool.ID, err)
		}
		copy := profile.Clone()
		next[pool.ID] = &copy
	}
	s.routerProfileMu.Lock()
	s.routerProfileCache = next
	s.routerProfileMu.Unlock()
	return nil
}

func (s *Server) reopenRouterProfiles(storageRoot string) error {
	s.routerStoreMu.RLock()
	hotOpenUnsupported := s.routerProfiles == nil && s.routerCurationCancel != nil
	s.routerStoreMu.RUnlock()
	if hotOpenUnsupported {
		return errors.New("router profile store hot reopen after startup failure is unsupported; restart required")
	}
	nextStore, err := routerprofile.Open(storageRoot)
	if err != nil {
		return fmt.Errorf("opening router profile store: %w", err)
	}
	nextCache := make(map[string]*routerprofile.Profile)
	for _, pool := range config.GetProviderPools() {
		if config.NormalizeProviderPoolPolicy(pool.Policy) != config.ProviderPoolPolicySemantic {
			continue
		}
		profile, loadErr := nextStore.ActiveProfile(context.Background(), pool.ID)
		if errors.Is(loadErr, sql.ErrNoRows) {
			nextCache[pool.ID] = nil
			continue
		}
		if loadErr != nil {
			_ = nextStore.Close()
			return fmt.Errorf("loading active profile for pool %s: %w", pool.ID, loadErr)
		}
		copy := profile.Clone()
		nextCache[pool.ID] = &copy
	}
	s.routerStoreMu.Lock()
	defer s.routerStoreMu.Unlock()
	s.routerProfileMu.Lock()
	previous := s.routerProfiles
	s.routerProfiles = nextStore
	s.routerProfileCache = nextCache
	if previous != nil {
		// Workers may still hold an operation on the old single-connection
		// store. Retire it at shutdown instead of racing Close with that call.
		s.retiredRouterProfiles = append(s.retiredRouterProfiles, previous)
	}
	s.routerProfileMu.Unlock()
	return nil
}

func (s *Server) activeRouterProfile(poolID string) (*routerprofile.Profile, bool) {
	s.routerProfileMu.RLock()
	profile, cached := s.routerProfileCache[poolID]
	if profile != nil {
		copy := profile.Clone()
		s.routerProfileMu.RUnlock()
		return &copy, true
	}
	s.routerProfileMu.RUnlock()
	return nil, cached
}

func validateRouterProfileForPool(pool config.ProviderPool, profile routerprofile.Profile, heldOutGate bool) error {
	if profile.PoolID != pool.ID {
		return errors.New("router profile pool ID does not match")
	}
	if err := profile.ValidateArtifact(); err != nil {
		return err
	}
	members := make(map[string]config.ProviderPoolMember, len(pool.Members))
	for _, member := range pool.Members {
		members[member.ID] = member
	}
	switch profile.ArtifactSchemaVersion() {
	case routerprofile.SchemaVersionV1:
		for _, candidate := range profile.Profile.Candidates {
			member, ok := members[candidate.MemberID]
			if !ok || !strings.EqualFold(strings.TrimSpace(member.Source), strings.TrimSpace(candidate.Source)) ||
				strings.TrimSpace(member.Model) != strings.TrimSpace(candidate.Model) {
				return fmt.Errorf("router profile candidate %q does not match pool member", candidate.MemberID)
			}
		}
		if heldOutGate && (profile.Profile.Evaluation.HeldOutQueryCount == 0 ||
			!profile.Profile.Evaluation.SemanticDifferentiation ||
			profile.Profile.Evaluation.AllClustersOneMember) {
			return errors.New("router profile did not pass held-out semantic differentiation gates")
		}
	case routerprofile.SchemaVersionV2:
		if profile.ProfileV2.PoolID != pool.ID {
			return errors.New("router profile V2 artifact pool ID does not match")
		}
		for _, candidate := range profile.ProfileV2.Candidates {
			member, ok := members[candidate.MemberID]
			if !ok || !strings.EqualFold(strings.TrimSpace(member.Source), strings.TrimSpace(candidate.Source)) ||
				strings.TrimSpace(member.Model) != strings.TrimSpace(candidate.Model) {
				return fmt.Errorf("router profile candidate %q does not match pool member", candidate.MemberID)
			}
		}
		if _, ok := members[profile.ProfileV2.Calibration.Policy.SafeFallbackMemberID]; !ok {
			return errors.New("router profile safe fallback member is unavailable")
		}
		if heldOutGate && !profile.ProfileV2.Validation.ActivationAllowed {
			if profile.ProfileV2.Validation.CollapsedSingleMember &&
				!profile.ProfileV2.Validation.CollapsedQualityPassed {
				return errors.New("collapsed router profile did not pass conservative held-out quality")
			}
			return errors.New("router profile V2 is infeasible")
		}
	default:
		return errors.New("unsupported router profile schema")
	}
	return nil
}

// ValidateRouterProfile validates a stored profile against the current pool and
// the activation held-out gates. It performs no mutation.
func (s *Server) ValidateRouterProfile(ctx context.Context, poolID, profileID string) (routerprofile.Profile, error) {
	pool, ok := providerPoolByID(poolID)
	if !ok {
		return routerprofile.Profile{}, errors.New("provider pool not found")
	}
	s.routerProfileMu.RLock()
	store := s.routerProfiles
	if store == nil {
		s.routerProfileMu.RUnlock()
		return routerprofile.Profile{}, errors.New("router profile store is unavailable")
	}
	profile, err := store.GetProfile(ctx, poolID, profileID)
	s.routerProfileMu.RUnlock()
	if err != nil {
		return routerprofile.Profile{}, err
	}
	return profile, validateRouterProfileForPool(pool, profile, true)
}

// ActivateRouterProfile validates, activates, and publishes a profile while
// holding the runtime profile lock, so requests see either cache generation.
func (s *Server) ActivateRouterProfile(ctx context.Context, poolID, profileID, actor, reason string) (routerprofile.Activation, error) {
	pool, ok := providerPoolByID(poolID)
	if !ok {
		return routerprofile.Activation{}, errors.New("provider pool not found")
	}
	s.routerProfileMu.Lock()
	defer s.routerProfileMu.Unlock()
	if s.routerProfiles == nil {
		return routerprofile.Activation{}, errors.New("router profile store is unavailable")
	}
	profile, err := s.routerProfiles.GetProfile(ctx, poolID, profileID)
	if err != nil {
		return routerprofile.Activation{}, err
	}
	if err := validateRouterProfileForPool(pool, profile, true); err != nil {
		return routerprofile.Activation{}, err
	}
	activation, err := s.routerProfiles.ActivateProfile(ctx, poolID, profileID, actor, reason)
	if err != nil {
		return routerprofile.Activation{}, err
	}
	copy := profile.Clone()
	s.routerProfileCache[poolID] = &copy
	return activation, nil
}

// RollbackRouterProfile rolls back in storage, validates the resulting active
// profile against current members, and atomically refreshes the runtime cache.
func (s *Server) RollbackRouterProfile(ctx context.Context, poolID, expectedCurrent, actor, reason string) (routerprofile.Activation, error) {
	pool, ok := providerPoolByID(poolID)
	if !ok {
		return routerprofile.Activation{}, errors.New("provider pool not found")
	}
	s.routerProfileMu.Lock()
	defer s.routerProfileMu.Unlock()
	if s.routerProfiles == nil {
		return routerprofile.Activation{}, errors.New("router profile store is unavailable")
	}
	current, err := s.routerProfiles.ActiveProfile(ctx, poolID)
	if err != nil {
		return routerprofile.Activation{}, err
	}
	if expectedCurrent == "" || current.ID != expectedCurrent {
		return routerprofile.Activation{}, fmt.Errorf("active router profile changed: %w", routerprofile.ErrConflict)
	}
	history, err := s.routerProfiles.ListActivationHistory(ctx, poolID, routerprofile.ListOptions{Limit: 100})
	if err != nil {
		return routerprofile.Activation{}, err
	}
	targetID := ""
	for _, item := range history {
		if item.ToProfileID == current.ID && item.FromProfileID != "" {
			targetID = item.FromProfileID
			break
		}
	}
	if targetID == "" {
		return routerprofile.Activation{}, sql.ErrNoRows
	}
	target, err := s.routerProfiles.GetProfile(ctx, poolID, targetID)
	if err != nil {
		return routerprofile.Activation{}, err
	}
	if err := validateRouterProfileForPool(pool, target, true); err != nil {
		return routerprofile.Activation{}, fmt.Errorf("rollback target is incompatible: %w", err)
	}
	activation, err := s.routerProfiles.Rollback(ctx, poolID, expectedCurrent, actor, reason)
	if err != nil {
		return routerprofile.Activation{}, err
	}
	copy := target.Clone()
	s.routerProfileCache[poolID] = &copy
	return activation, nil
}

func (s *Server) providerPoolSemanticRouter(pool config.ProviderPool) func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
	if config.NormalizeProviderPoolPolicy(pool.Policy) != config.ProviderPoolPolicySemantic {
		return nil
	}
	profile, cached := s.activeRouterProfile(pool.ID)
	if !cached {
		// A pool created after startup gets one safe lazy cache fill.
		s.routerProfileMu.Lock()
		if cachedProfile, exists := s.routerProfileCache[pool.ID]; exists {
			profile = cachedProfile
		} else {
			if s.routerProfiles != nil {
				loaded, err := s.routerProfiles.ActiveProfile(context.Background(), pool.ID)
				if err == nil {
					copy := loaded.Clone()
					profile = &copy
				}
			}
			s.routerProfileCache[pool.ID] = profile
		}
		s.routerProfileMu.Unlock()
	}
	if profile == nil {
		compatible, err := legacySemanticPoolCompatible(pool.Members)
		if err != nil {
			return func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
				return routerprofile.Decision{Fallback: true, FallbackReason: routerprofile.FallbackProfileInvalid}
			}
		}
		if !compatible {
			return func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
				return routerprofile.Decision{Fallback: true, FallbackReason: semanticFallbackLegacyIncompatible}
			}
		}
		legacy := s.legacyProviderPoolSemanticRouter(pool)
		return func(ctx context.Context, input providerPoolSemanticInput) routerprofile.Decision {
			text, _, err := routerprofile.BuildRoutingText(input.messages, 8192)
			if err != nil || text == "" {
				return routerprofile.Decision{Fallback: true, FallbackReason: routerprofile.FallbackRoutingTextEmpty}
			}
			route, err := legacy(ctx, text)
			if err != nil {
				return routerprofile.Decision{Fallback: true, FallbackReason: routerprofile.FallbackEmbedding}
			}
			return route
		}
	}
	if err := validateRouterProfileForPool(pool, *profile, false); err != nil {
		reason := routerprofile.FallbackProfileInvalid
		if strings.Contains(err.Error(), "candidate") || strings.Contains(err.Error(), "fallback member") {
			reason = semanticFallbackMemberDrift
		}
		return func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
			return profileFallback(pool, *profile, reason)
		}
	}
	snapshot := profile.Clone()
	model, dims := embeddingConfig(snapshot)
	encoder := routerprofile.EncodeFunc(func(ctx context.Context, text string) ([]float64, error) {
		return s.providerPoolGatewayEmbeddingModel(ctx, text, model, dims)
	})
	router, err := routerprofile.NewRouter(snapshot, encoder)
	if err != nil {
		reason := semanticFallbackMemberDrift
		if strings.Contains(err.Error(), "artifact") {
			reason = routerprofile.FallbackProfileInvalid
		}
		return func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
			return profileFallback(pool, snapshot, reason)
		}
	}
	return func(ctx context.Context, input providerPoolSemanticInput) routerprofile.Decision {
		decision, err := router.Match(ctx, input.messages)
		if err != nil {
			return routerprofile.Decision{
				ProfileID: snapshot.ID, ProfileVersion: snapshot.Version,
				ProfileSchemaVersion: snapshot.ArtifactSchemaVersion(),
				RouterAlgorithm:      routerprofile.RouterAlgorithmClusterV1,
				Fallback:             true, FallbackReason: routerprofile.FallbackProfileInvalid,
			}
		}
		if decision.Fallback && decision.MemberID == "" {
			if memberID, ok := profileV1FallbackMember(pool, snapshot); ok {
				decision.MemberID, decision.Applied = memberID, true
			}
		}
		return decision
	}
}

func profileV1FallbackMember(pool config.ProviderPool, profile routerprofile.Profile) (string, bool) {
	if profile.ArtifactSchemaVersion() != routerprofile.SchemaVersionV1 {
		return "", false
	}
	target := strings.TrimSpace(profile.Profile.FallbackMemberID)
	if target == "" {
		return "", false
	}
	for _, member := range pool.Members {
		if member.ID == target {
			return member.ID, true
		}
	}
	return "", false
}

func profileFallback(pool config.ProviderPool, profile routerprofile.Profile, reason string) routerprofile.Decision {
	route := routerprofile.Decision{
		ProfileID: profile.ID, ProfileVersion: profile.Version,
		Fallback: true, FallbackReason: reason,
	}
	if profile.ArtifactSchemaVersion() == routerprofile.SchemaVersionV2 && profile.ProfileV2 != nil {
		route.ProfileSchemaVersion = routerprofile.SchemaVersionV2
		route.RouterAlgorithm = profile.ProfileV2.RouterAlgorithm
		route.RoutingTextVersion = profile.ProfileV2.RoutingText.Version
		for _, member := range pool.Members {
			if member.ID == profile.ProfileV2.Calibration.Policy.SafeFallbackMemberID {
				route.MemberID, route.Applied = member.ID, true
				break
			}
		}
	} else {
		route.ProfileSchemaVersion = routerprofile.SchemaVersionV1
		route.RouterAlgorithm = routerprofile.RouterAlgorithmClusterV1
		route.RoutingTextVersion = profile.Profile.RoutingText.Version
		if memberID, ok := profileV1FallbackMember(pool, profile); ok {
			route.MemberID, route.Applied = memberID, true
		}
	}
	return route
}

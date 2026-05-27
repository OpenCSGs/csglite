package server

import (
	"strings"

	"github.com/opencsgs/csghub-lite/internal/model"
)

func (s *Server) resolveLocalModelStorageID(modelID string) string {
	if s == nil || s.manager == nil {
		return strings.TrimSpace(modelID)
	}
	resolved, err := s.manager.ResolveLocalModelID(strings.TrimSpace(modelID))
	if err != nil {
		return strings.TrimSpace(modelID)
	}
	return resolved
}

func (s *Server) localInferenceModelID(modelID string) string {
	if s == nil || s.manager == nil {
		return strings.TrimSpace(modelID)
	}
	publicID, err := s.manager.PublicModelID(strings.TrimSpace(modelID))
	if err != nil {
		return strings.TrimSpace(modelID)
	}
	return publicID
}

func (s *Server) registerLocalModelAliases(seen map[string]struct{}, modelID string) {
	if s == nil || s.manager == nil || seen == nil {
		return
	}
	lm, err := s.manager.ResolveLocalModel(modelID)
	if err != nil {
		return
	}
	seen[lm.FullName()] = struct{}{}
	if publicID, err := s.manager.PublicModelID(lm.FullName()); err == nil {
		seen[publicID] = struct{}{}
		return
	}
	seen[model.InferenceModelID(lm)] = struct{}{}
}

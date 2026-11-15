package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
)

// ManageLocalModel operates on the exact registry artifact advertised by this
// node. Busy runtimes are rejected, never forcibly terminated by a remote click.
func (h *clusterHost) ManageLocalModel(ctx context.Context, action cluster.ModelAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.Action != "unload" && action.Action != "delete" {
		return errors.New("invalid model action")
	}
	if strings.HasPrefix(action.Model, cluster.DeploymentModelPrefix) {
		return errors.New("use deployment controls for managed models")
	}
	s := h.s
	lm, err := s.manager.ResolveLocalModel(action.Model)
	if err != nil {
		return err
	}
	repo := lm.Repository
	if repo == "" {
		repo = lm.Namespace + "/" + lm.Name
	}
	source := lm.ArtifactSource
	if source == "" {
		source = "opencsg"
	}
	if action.Repository != repo || action.ArtifactSource != source {
		return errors.New("model identity changed; refresh the model list")
	}
	id := lm.FullName()
	if s.pullJobs != nil && s.pullJobs.hasActiveModelAnySource(lm.Namespace+"/"+lm.Name) {
		return errors.New("model download is active; wait for it to finish")
	}
	unload := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		keys := []string{engineCacheKey(id, engineModeChat), engineCacheKey(id, engineModeEmbed)}
		for _, key := range keys {
			if s.loading[key] != nil {
				return errors.New("model is loading; wait for it to finish")
			}
			if me := s.engines[key]; me != nil && me.activeRequests > 0 {
				return errors.New("model has active requests; retry after they finish")
			}
		}
		if s.asrLoading[id] != nil || s.ttsLoading[id] != nil || s.imageLoading[id] != nil {
			return errors.New("model is loading; wait for it to finish")
		}
		if me := s.asrEngines[id]; me != nil && me.activeRequests > 0 {
			return errors.New("model has active requests; retry after they finish")
		}
		if me := s.ttsEngines[id]; me != nil && me.activeRequests > 0 {
			return errors.New("model has active requests; retry after they finish")
		}
		if me := s.imageEngines[id]; me != nil && me.activeRequests > 0 {
			return errors.New("model has active requests; retry after they finish")
		}
		for _, key := range keys {
			if me := s.engines[key]; me != nil {
				if err := me.engine.Close(); err != nil {
					return fmt.Errorf("unload model: %w", err)
				}
				delete(s.engines, key)
			}
		}
		if me := s.asrEngines[id]; me != nil {
			if err := me.engine.Close(); err != nil {
				return err
			}
			delete(s.asrEngines, id)
		}
		if me := s.ttsEngines[id]; me != nil {
			if err := me.engine.Close(); err != nil {
				return err
			}
			delete(s.ttsEngines, id)
		}
		if me := s.imageEngines[id]; me != nil {
			if err := me.engine.Close(); err != nil {
				return err
			}
			delete(s.imageEngines, id)
		}
		return nil
	}
	if action.Action == "delete" {
		return s.manager.RemoveWhen(ctx, id, func() error {
			if ref := s.modelDeploymentReference(id, ""); ref != "" {
				return fmt.Errorf("model is used by managed deployment %s; delete it on the Master first", ref)
			}
			if err := s.checkSharedModelIdleForDeployment(id); err != nil {
				return err
			}
			return unload()
		})
	}
	return unload()
}

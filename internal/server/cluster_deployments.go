package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/modelregistry"
)

func (h *clusterHost) deploymentModelManager(_ cluster.Deployment) *model.Manager {
	return h.s.manager
}

func (h *clusterHost) migrateDeploymentModel(ctx context.Context, d cluster.Deployment) error {
	id, err := uuid.Parse(d.ID)
	if err != nil || id.String() != d.ID {
		return errors.New("invalid deployment cache ID")
	}
	source, err := modelregistry.NormalizeSource(d.ArtifactSource)
	if err != nil {
		return err
	}
	parts := strings.Split(d.Repository, "/")
	if len(parts) != 2 {
		return errors.New("invalid deployment repository")
	}
	// Validate the storage ID before constructing legacy paths.
	if _, err := h.s.manager.Get(deploymentArtifactID(d)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root := filepath.Join(h.s.cfg.StorageDir(), "cluster", "deployment-models", d.ID)
	legacy := model.RegistryModelDir(root, string(source), parts[0], parts[1])
	if err := h.s.manager.AdoptCacheDirectory(ctx, legacy, deploymentArtifactID(d)); err != nil {
		return err
	}
	// Remove only empty parents. Conflicting or unrelated legacy files survive.
	for parent := filepath.Dir(legacy); ; parent = filepath.Dir(parent) {
		if err := os.Remove(parent); err != nil {
			break
		}
		if parent == root {
			break
		}
	}
	_ = os.Remove(filepath.Dir(root))
	return nil
}

func (s *Server) migrateDeploymentModels(m *cluster.Manager) {
	h := &clusterHost{s: s}
	for _, d := range m.LocalDeployments() {
		if d.NodeUUID != m.Identity().UUID || d.Status.Phase == "deleted" {
			continue
		}
		if err := h.migrateDeploymentModel(context.Background(), d); err != nil {
			log.Printf("cluster: model cache migration for %s: %v", d.ID, err)
		}
	}
}

func deploymentArtifactID(d cluster.Deployment) string {
	if d.ArtifactSource == "opencsg" {
		return d.Repository
	}
	return d.ArtifactSource + "/" + d.Repository
}

func (h *clusterHost) PrepareDeployment(ctx context.Context, d cluster.Deployment, report func(cluster.DeploymentStatus)) (int64, error) {
	if _, err := convert.NormalizeRuntimeDType(d.Quant); err != nil {
		return 0, err
	}
	mgr := h.deploymentModelManager(d)
	if err := h.migrateDeploymentModel(ctx, d); err != nil {
		return 0, err
	}
	root := h.s.cfg.ModelDir
	if err := os.MkdirAll(root, 0700); err != nil {
		return 0, err
	}
	_, free, err := diskUsage(root)
	if err != nil {
		return 0, err
	}
	reserve := uint64(0)
	if h.s.cluster != nil {
		reserve = uint64(h.s.cluster.Store().Settings().DiskReserveGB) * (1 << 30)
	}
	if !mgr.Exists(deploymentArtifactID(d)) && free <= reserve {
		return 0, errors.New("insufficient disk space after cluster disk reserve")
	}
	pullCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var diskErr error
	var progressMu sync.Mutex
	lastCheck := time.Time{}
	lastReport := time.Time{}
	var quants []string
	if d.Quant != "" {
		quants = []string{d.Quant}
	}
	lm, err := mgr.EnsureFrom(pullCtx, d.Repository, d.ArtifactSource, d.Revision, quants, func(p csghub.SnapshotProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if time.Since(lastCheck) > time.Second {
			lastCheck = time.Now()
			_, available, e := diskUsage(root)
			remaining := p.BytesTotalAll - p.BytesCompletedAll
			if e == nil && remaining > 0 && (available <= reserve || uint64(remaining) > available-reserve) {
				diskErr = errors.New("model download would exceed the cluster disk reserve")
				cancel()
			}
		}
		// Receipts are durable; avoid an fsync for every downloaded chunk.
		if time.Since(lastReport) >= 500*time.Millisecond {
			lastReport = time.Now()
			report(cluster.DeploymentStatus{Phase: "downloading", Detail: p.FileName, CompletedBytes: p.BytesCompletedAll, TotalBytes: p.BytesTotalAll})
		}
	})
	if diskErr != nil {
		return 0, diskErr
	}
	if err != nil {
		return 0, err
	}
	return lm.Size, nil
}

func deploymentEngineMode(d cluster.Deployment) string {
	if d.Kind == "embedding" {
		return engineModeEmbed
	}
	return engineModeChat
}

func (h *clusterHost) StartDeployment(ctx context.Context, d cluster.Deployment, report func(cluster.DeploymentStatus)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mgr := h.deploymentModelManager(d)
	artifact := deploymentArtifactID(d)
	unlock, err := mgr.LockArtifact(ctx, artifact)
	if err != nil {
		return err
	}
	defer unlock()
	lm, err := mgr.Get(artifact)
	if err != nil {
		return err
	}
	dir, err := mgr.ModelPath(artifact)
	if err != nil {
		return err
	}
	if d.Quant != "" {
		if _, ok, err := convert.FindGGUFForDType(dir, d.Quant); err != nil {
			return err
		} else if !ok && !convert.HasConvertibleHFWeights(dir) {
			return fmt.Errorf("shared model has no %s weights; choose an available quantization", d.Quant)
		}
	}
	if lm.PipelineTag != "" {
		tag := strings.ToLower(lm.PipelineTag)
		if (d.Kind == "embedding" && tag != "sentence-similarity" && tag != "feature-extraction") || (d.Kind == "chat" && tag != "text-generation" && tag != "text2text-generation" && tag != "image-text-to-text") {
			return errors.New("model pipeline is incompatible with the selected deployment kind")
		}
	}
	h.s.mu.RLock()
	occupied := len(h.s.engines) > 0
	h.s.mu.RUnlock()
	if rocmSingleEngineMode() && occupied {
		return errors.New("this runtime supports one active model; stop the existing model before deploying")
	}
	// The engine's alias is isolated from artifact IDs and other deployments.
	copyModel := *lm
	copyModel.Namespace = "deployment"
	copyModel.Name = d.ID
	copyModel.Repository = d.ModelID()
	copyModel.ArtifactSource = ""
	progress := func(step string, current, total int) {
		report(cluster.DeploymentStatus{Phase: "loading", Detail: step, Size: lm.Size})
	}
	loader := loadEngineWithProgress
	if d.Kind == "embedding" {
		loader = loadEmbeddingEngineWithProgress
	}
	engine, err := loader(dir, &copyModel, progress, false, d.NumCtx, d.NumParallel, d.NGPULayers, "", "", d.Quant)
	if err != nil {
		return err
	}
	// The existing loader is synchronous. A stop during loading is acknowledged
	// immediately by the controller and closes the runtime before publication.
	if err := ctx.Err(); err != nil {
		_ = engine.Close()
		return err
	}
	if err := inference.RuntimeError(engine); err != nil {
		_ = engine.Close()
		return err
	}
	key := engineCacheKey(d.ModelID(), deploymentEngineMode(d))
	h.s.mu.Lock()
	if _, exists := h.s.engines[key]; exists {
		h.s.mu.Unlock()
		_ = engine.Close()
		return nil
	}
	h.s.engines[key] = &managedEngine{engine: engine, deploymentModel: lm, deploymentName: d.Name, numCtx: d.NumCtx, numParallel: d.NumParallel, nGPULayers: d.NGPULayers, dtype: d.Quant, lastUsed: time.Now(), keepAlive: -1}
	h.s.mu.Unlock()
	return nil
}

func (h *clusterHost) StopDeployment(d cluster.Deployment) error {
	return h.stopDeployment(d, false)
}

// UnloadDeployment is called only after membership authorization is revoked.
// Closing the isolated engine also interrupts any remaining inference requests.
func (h *clusterHost) UnloadDeployment(d cluster.Deployment) error {
	return h.stopDeployment(d, true)
}

func (h *clusterHost) stopDeployment(d cluster.Deployment, force bool) error {
	key := engineCacheKey(d.ModelID(), deploymentEngineMode(d))
	h.s.mu.Lock()
	me := h.s.engines[key]
	if me == nil {
		h.s.mu.Unlock()
		return nil
	}
	if me.activeRequests > 0 && !force {
		h.s.mu.Unlock()
		return errors.New("waiting for active inference requests")
	}
	defer h.s.mu.Unlock()
	if err := me.engine.Close(); err != nil {
		return err
	}
	delete(h.s.engines, key)
	return nil
}

func (h *clusterHost) DeploymentRunning(d cluster.Deployment) bool {
	h.s.mu.RLock()
	defer h.s.mu.RUnlock()
	me := h.s.engines[engineCacheKey(d.ModelID(), deploymentEngineMode(d))]
	return me != nil
}

func (h *clusterHost) DeploymentHealth(d cluster.Deployment) error {
	h.s.mu.RLock()
	defer h.s.mu.RUnlock()
	if me := h.s.engines[engineCacheKey(d.ModelID(), deploymentEngineMode(d))]; me != nil {
		return inference.RuntimeError(me.engine)
	}
	return nil
}

func (s *Server) deploymentInferenceEngine(modelID, mode string) (inference.Engine, error) {
	if s.cluster == nil || !s.cluster.DeploymentReady(modelID) {
		return nil, inference.NewHTTPStatusError(503, "deployment is not ready")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	me := s.engines[engineCacheKey(modelID, mode)]
	if me == nil {
		return nil, inference.NewHTTPStatusError(503, "deployment runtime is not available for this API type")
	}
	if err := inference.RuntimeError(me.engine); err != nil {
		return nil, inference.NewHTTPStatusError(503, err.Error())
	}
	return me.engine, nil
}

// DeleteDeployment drops an execution instance. Shared weights are removed only
// after its last deployment reference and every local runtime have gone away.
func (h *clusterHost) DeleteDeployment(d cluster.Deployment) (string, error) {
	if _, err := uuid.Parse(d.ID); err != nil {
		return "", errors.New("invalid deployment ID")
	}
	if h.DeploymentRunning(d) {
		return "", errors.New("deployment is still running")
	}
	id := deploymentArtifactID(d)
	if ref := h.s.modelDeploymentReference(id, d.ID); ref != "" {
		return "Model files retained: another deployment still references this model", nil
	}
	err := h.s.manager.RemoveCacheWhen(context.Background(), id, func() error {
		if ref := h.s.modelDeploymentReference(id, d.ID); ref != "" {
			return fmt.Errorf("model is referenced by deployment %s", ref)
		}
		return h.s.checkSharedModelIdle(id)
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return "", err
}

func (s *Server) modelDeploymentReference(id, except string) string {
	if s.cluster == nil {
		return ""
	}
	for _, d := range s.cluster.LocalDeployments() {
		if d.ID != except && d.Status.Phase != "deleted" && d.Desired != "deleted" && deploymentArtifactID(d) == id {
			return d.Name
		}
	}
	return ""
}

func (s *Server) checkSharedModelIdle(id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key, me := range s.engines {
		if engineModelIDFromKey(key) == id || (me.deploymentModel != nil && me.deploymentModel.FullName() == id) {
			return errors.New("model files are still used by a runtime; unload it before deleting")
		}
	}
	if s.loading[id] != nil || s.loading[engineCacheKey(id, engineModeEmbed)] != nil || s.asrLoading[id] != nil || s.ttsLoading[id] != nil || s.imageLoading[id] != nil {
		return errors.New("model is loading; wait before deleting")
	}
	if s.asrEngines[id] != nil || s.ttsEngines[id] != nil || s.imageEngines[id] != nil {
		return errors.New("model files are still used by a runtime; unload it before deleting")
	}
	return nil
}

func (s *Server) checkSharedModelIdleForDeployment(id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, me := range s.engines {
		if me.deploymentModel != nil && me.deploymentModel.FullName() == id {
			return errors.New("model is used by a managed runtime; unload it on the Master first")
		}
	}
	return nil
}

func (h *clusterHost) InspectDeployment(d cluster.Deployment) (cluster.ModelStatus, bool) {
	lm, err := h.s.manager.Get(deploymentArtifactID(d))
	if err != nil {
		return cluster.ModelStatus{}, false
	}
	info := h.s.localModelInfo(lm)
	h.s.mu.RLock()
	defer h.s.mu.RUnlock()
	me := h.s.engines[engineCacheKey(d.ModelID(), deploymentEngineMode(d))]
	if me == nil {
		return cluster.ModelStatus{}, false
	}
	category := info.Category
	if info.DecisionOnly {
		category = "decision"
	}
	return cluster.ModelStatus{ID: d.ModelID(), DeploymentID: d.ID, Repo: d.Repository, Source: d.ArtifactSource, Size: info.Size, Format: info.Format, PipelineTag: info.PipelineTag, Category: category, Loaded: true, Slots: d.NumParallel, Active: me.activeRequests, NGPULayers: d.NGPULayers, NativeToolStreaming: inference.SupportsNativeToolStreaming(me.engine), TypedDecisions: info.TypedDecisions, DecisionOnly: info.DecisionOnly, HasMMProj: info.HasMMProj}, true
}

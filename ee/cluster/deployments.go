// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/internal/safefile"
)

const DeploymentModelPrefix = "deployment/"
const maxDeployments = 64

// DeploymentSpec pins model identity and revision. Runtime settings and quant
// may change only after an acknowledged stop, in a newer generation.
type DeploymentSpec struct {
	Name           string `json:"name"`
	NodeUUID       string `json:"node_uuid"`
	Repository     string `json:"repository"`
	ArtifactSource string `json:"artifact_source"`
	Revision       string `json:"revision"`
	Quant          string `json:"quant,omitempty"`
	Kind           string `json:"kind"`
	NumCtx         int    `json:"num_ctx"`
	NumParallel    int    `json:"num_parallel"`
	NGPULayers     int    `json:"n_gpu_layers"`
}

type DeploymentStatus struct {
	Phase            string    `json:"phase"`
	Error            string    `json:"error,omitempty"`
	Detail           string    `json:"detail,omitempty"`
	CompletedBytes   int64     `json:"completed_bytes,omitempty"`
	TotalBytes       int64     `json:"total_bytes,omitempty"`
	SpeedBytesPerSec int64     `json:"speed_bytes_per_sec,omitempty"`
	Size             int64     `json:"size,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Deployment struct {
	DeploymentSpec
	ID             string           `json:"id"`
	ControllerUUID string           `json:"controller_uuid"`
	Generation     uint64           `json:"generation"`
	Desired        string           `json:"desired"`
	CreatedAt      time.Time        `json:"created_at"`
	Status         DeploymentStatus `json:"status"`
}

func (d Deployment) ModelID() string { return DeploymentModelPrefix + d.ID }

// DeploymentHost uses isolated runtime IDs; stopping a deployment must not stop
// a user's independently loaded model. Artifacts share the normal library.
type DeploymentHost interface {
	PrepareDeployment(context.Context, Deployment, func(DeploymentStatus)) (int64, error)
	StartDeployment(context.Context, Deployment, func(DeploymentStatus)) error
	StopDeployment(Deployment) error
	DeploymentRunning(Deployment) bool
}

// DeploymentUnloader terminates a managed runtime after its controller loses
// authorization, including in-flight inference. Ordinary stops still drain.
type DeploymentUnloader interface {
	UnloadDeployment(Deployment) error
}

type DeploymentInspector interface {
	InspectDeployment(Deployment) (ModelStatus, bool)
}

// DeploymentHealth reports terminal inference failures even when a runtime's
// process is still alive. Older hosts retain their process-liveness behavior.
type DeploymentHealth interface {
	DeploymentHealth(Deployment) error
}

type DeploymentCleaner interface {
	DeleteDeployment(Deployment) (string, error)
}

type deploymentFile struct {
	Version     int                   `json:"version"`
	Owned       map[string]Deployment `json:"owned"`
	Assignments map[string]Deployment `json:"assignments"`
}

// Owned contains this controller's desired deployments. Assignments are the
// worker's durable execution receipts, fenced by controller UUID and generation.
// Neither map is a second model-service configuration or a model-file catalog.
type deploymentStore struct {
	mu   sync.Mutex
	path string
	data deploymentFile
}

func openDeploymentStore(dir string) (*deploymentStore, error) {
	s := &deploymentStore{path: filepath.Join(dir, "deployments.json"), data: deploymentFile{Version: 1}}
	b, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
		if s.data.Version != 1 {
			return nil, errors.New("unsupported deployment store version")
		}
	}
	if s.data.Owned == nil {
		s.data.Owned = map[string]Deployment{}
	}
	if s.data.Assignments == nil {
		s.data.Assignments = map[string]Deployment{}
	}
	// Runtime processes do not survive a Lite restart. Preserve failed/stopped
	// receipts; reconcile previously running assignments on this same node.
	for id, d := range s.data.Assignments {
		if d.Status.Phase != "failed" && d.Status.Phase != "stopped" && d.Status.Phase != "deleted" {
			d.Status = DeploymentStatus{Phase: "pending", UpdatedAt: time.Now().UTC()}
			s.data.Assignments[id] = d
		}
	}
	return s, nil
}

func (s *deploymentStore) list(owned bool) []Deployment {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.data.Assignments
	if owned {
		m = s.data.Owned
	}
	out := make([]Deployment, 0, len(m))
	for _, d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *deploymentStore) get(owned bool, id string) (Deployment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owned {
		d, ok := s.data.Owned[id]
		return d, ok
	}
	d, ok := s.data.Assignments[id]
	return d, ok
}

func (s *deploymentStore) update(owned bool, id string, edit func(*Deployment, bool) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.data.Assignments
	if owned {
		m = s.data.Owned
	}
	old, exists := m[id]
	next := old
	if err := edit(&next, exists); err != nil {
		return err
	}
	if exists && next == old {
		return nil
	}
	live := 0
	for _, record := range m {
		if record.Status.Phase != "deleted" {
			live++
		}
	}
	if !exists && live >= maxDeployments {
		return errors.New("deployment limit reached (64)")
	}
	m[id] = next
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err == nil {
		err = safefile.Write(s.path, b, 0600)
	}
	if err != nil {
		if exists {
			m[id] = old
		} else {
			delete(m, id)
		}
	}
	return err
}

func validateDeployment(d DeploymentSpec) error {
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 100 {
		return errors.New("deployment name is required (maximum 100 characters)")
	}
	if _, err := uuid.Parse(d.NodeUUID); err != nil {
		return errors.New("invalid target node UUID")
	}
	parts := strings.Split(d.Repository, "/")
	if len(parts) != 2 {
		return errors.New("repository must be namespace/name")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\:?#\x00\r\n \t") {
			return errors.New("invalid model repository")
		}
	}
	if len(d.Repository) > 256 || len(d.Revision) > 128 || strings.ContainsAny(d.Revision, "\x00\r\n") || strings.TrimSpace(d.Revision) == "" {
		return errors.New("an explicit model revision is required")
	}
	switch d.ArtifactSource {
	case "opencsg", "huggingface", "modelscope":
	default:
		return errors.New("unsupported artifact source")
	}
	if d.Kind != "chat" && d.Kind != "embedding" {
		return errors.New("kind must be chat or embedding")
	}
	// Explicit context sizes follow model/runtime support, as local model settings
	// do. A fixed 128K ceiling incorrectly rejects native 256K and longer models.
	if d.NumCtx < 512 || d.NumParallel < 1 || d.NumParallel > 16 || d.NGPULayers < -1 || d.NGPULayers > 999 {
		return errors.New("invalid deployment runtime limits")
	}
	if len(d.Quant) > 32 || strings.ContainsAny(d.Quant, "/\\\x00\r\n ") {
		return errors.New("invalid quantization")
	}
	return nil
}

// deploymentController derives public worker ownership from membership. LAN
// peers have no Master and retain their explicit controller selection.
func (m *Manager) deploymentController() string {
	c := m.store.Cluster()
	if c == nil {
		return ""
	}
	controller := m.store.Settings().ManagedBy
	if m.opts.Mode == "worker" {
		controller = c.GatewayUUID
	}
	if _, ok := m.store.Member(controller); !ok {
		return ""
	}
	return controller
}

func (m *Manager) effectiveSettings() Settings {
	s := m.store.Settings()
	if m.opts.Mode == "worker" {
		s.ManagedBy = m.deploymentController()
	}
	return s
}

func (m *Manager) deploymentAuthorized(controller string) bool {
	if controller == m.identity.UUID {
		return m.opts.Mode != "worker" && m.store.InCluster()
	}
	if controller == "" || m.deploymentController() != controller {
		return false
	}
	return m.opts.Mode == "worker" || m.opts.Mode == ""
}

func (m *Manager) deploymentAllowed(d Deployment) bool {
	s := m.store.Settings()
	return m.deploymentAuthorized(d.ControllerUUID) && s.AcceptWork && s.State == NodeStateActive && m.withinNodeLimit(len(m.store.Members())+1) && m.Active()
}

func (m *Manager) deploymentRuntimeError(d Deployment) error {
	if health, ok := m.opts.Host.(DeploymentHealth); ok {
		return health.DeploymentHealth(d)
	}
	return nil
}

func (m *Manager) receiveDeployment(controller string, incoming Deployment) (Deployment, error) {
	if _, ok := m.opts.Host.(DeploymentHost); !ok {
		return Deployment{}, errors.New("deployment runtime is not supported")
	}
	if !m.deploymentAuthorized(controller) {
		return Deployment{}, errors.New("node has not authorized this deployment controller")
	}
	if incoming.ControllerUUID != controller || incoming.NodeUUID != m.identity.UUID || incoming.Generation == 0 {
		return Deployment{}, errors.New("invalid deployment assignment")
	}
	if _, err := uuid.Parse(incoming.ID); err != nil {
		return Deployment{}, errors.New("invalid deployment ID")
	}
	if err := validateDeployment(incoming.DeploymentSpec); err != nil {
		return Deployment{}, err
	}
	if incoming.Desired != "running" && incoming.Desired != "stopped" && incoming.Desired != "deleted" {
		return Deployment{}, errors.New("invalid desired state")
	}
	m.deploymentMu.Lock()
	defer m.deploymentMu.Unlock()
	err := m.deployments.update(false, incoming.ID, func(d *Deployment, exists bool) error {
		if exists {
			if d.ControllerUUID != controller || !sameDeploymentIdentity(d.DeploymentSpec, incoming.DeploymentSpec) {
				return errors.New("deployment identity is immutable")
			}
			if d.Desired == "deleted" && incoming.Desired != "deleted" {
				return errors.New("deleted deployments cannot be restarted")
			}
			if incoming.Generation < d.Generation {
				return errors.New("stale deployment generation")
			}
			if incoming.Generation == d.Generation {
				if d.Desired != incoming.Desired || d.DeploymentSpec != incoming.DeploymentSpec {
					return errors.New("conflicting deployment generation")
				}
				return nil
			}
			if d.DeploymentSpec != incoming.DeploymentSpec {
				_, preparing := m.deploymentRuns[d.ID]
				if d.Desired != "stopped" || d.Status.Phase != "stopped" || preparing || m.opts.Host.(DeploymentHost).DeploymentRunning(*d) {
					return errors.New("stop the deployment before changing its configuration")
				}
			}
		}
		*d = incoming
		d.Status = DeploymentStatus{Phase: "pending", UpdatedAt: time.Now().UTC()}
		return nil
	})
	if err != nil {
		return Deployment{}, err
	}
	m.wakeDeployments()
	d, _ := m.deployments.get(false, incoming.ID)
	return d, nil
}

func (m *Manager) wakeDeployments() {
	select {
	case m.deploymentWake <- struct{}{}:
	default:
	}
}

func (m *Manager) deploymentStatus(id string, gen uint64, status DeploymentStatus) {
	status.UpdatedAt = time.Now().UTC()
	err := m.deployments.update(false, id, func(d *Deployment, exists bool) error {
		if !exists || d.Generation != gen {
			return errors.New("superseded deployment")
		}
		if status.Size == 0 {
			status.Size = d.Status.Size
		}
		d.Status = status
		return nil
	})
	if err != nil && err.Error() != "superseded deployment" {
		m.logf("cluster: persist deployment status: %v", err)
	}
	m.invalidateLocalStatus()
}

type deploymentRun struct {
	generation uint64
	cancel     context.CancelFunc
}

// Called with m.mu held when the manager lifetime is first established.
func (m *Manager) startDeploymentLoopsLocked() {
	ctx := m.ctx
	m.deploymentWG.Add(2)
	go func() { defer m.deploymentWG.Done(); m.deploymentWorkerLoop(ctx) }()
	go func() { defer m.deploymentWG.Done(); m.deploymentControllerLoop(ctx) }()
}

// Revoking membership must release managed runtimes even if an inference
// request never drains. Only isolated deployment IDs reach this path.
func (m *Manager) stopDeployment(host DeploymentHost, d Deployment) error {
	if !m.deploymentAuthorized(d.ControllerUUID) {
		if unloader, ok := host.(DeploymentUnloader); ok {
			return unloader.UnloadDeployment(d)
		}
	}
	return host.StopDeployment(d)
}

// Membership is already revoked before waiting. The lifetime reconciliation
// loop continues cleanup even if the caller disconnects or this wait times out.
func (m *Manager) waitDeploymentUnload(ctx context.Context) error {
	host, ok := m.opts.Host.(DeploymentHost)
	if !ok {
		return nil
	}
	assignments := m.deployments.list(false)
	m.deploymentMu.Lock()
	for _, run := range m.deploymentRuns {
		run.cancel()
	}
	m.deploymentMu.Unlock()
	m.wakeDeployments()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		m.deploymentMu.Lock()
		preparing := len(m.deploymentRuns) > 0
		m.deploymentMu.Unlock()
		unloaded := !preparing
		for _, d := range assignments {
			if host.DeploymentRunning(d) {
				unloaded = false
				break
			}
		}
		if unloaded {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("left cluster; managed model cleanup is still in progress: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

func (m *Manager) deploymentWorkerLoop(ctx context.Context) {
	host, ok := m.opts.Host.(DeploymentHost)
	if !ok {
		return
	}
	mu := &m.deploymentMu
	runs := m.deploymentRuns
	var wg sync.WaitGroup
	defer func() {
		mu.Lock()
		for _, run := range runs {
			run.cancel()
		}
		mu.Unlock()
		wg.Wait()
		for _, d := range m.deployments.list(false) {
			_ = m.stopDeployment(host, d)
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		for _, d := range m.deployments.list(false) {
			mu.Lock()
			allowed := m.deploymentAllowed(d)
			if run, exists := runs[d.ID]; exists {
				if run.generation != d.Generation || !allowed || d.Desired != "running" {
					run.cancel()
					if d.Status.Phase != "stopping" {
						m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: "stopping"})
					}
				}
				mu.Unlock()
				continue
			}
			if d.Desired == "deleted" && m.deploymentAuthorized(d.ControllerUUID) {
				mu.Unlock()
				if d.Status.Phase == "deleted" || d.Status.Phase == "failed" {
					continue
				}
				phase, detail := "deleting", ""
				if err := m.stopDeployment(host, d); err != nil {
					detail = err.Error()
				} else if cleaner, ok := host.(DeploymentCleaner); !ok {
					phase, detail = "failed", "node version does not support deleting deployment files"
				} else {
					retained, err := cleaner.DeleteDeployment(d)
					if err != nil {
						phase, detail = "failed", err.Error()
					} else {
						phase, detail = "deleted", retained
					}
				}
				next := DeploymentStatus{Phase: phase}
				if phase == "failed" {
					next.Error = detail
				} else {
					next.Detail = detail
				}
				if d.Status.Phase != phase || d.Status.Error != next.Error || d.Status.Detail != next.Detail {
					m.deploymentStatus(d.ID, d.Generation, next)
				}
				continue
			}
			if !allowed || d.Desired != "running" {
				mu.Unlock()
				phase := "stopped"
				if !allowed && d.Desired == "running" {
					phase = "blocked"
				}
				if err := m.stopDeployment(host, d); err != nil {
					phase = "stopping"
				}
				if d.Status.Phase != phase {
					m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: phase})
				}
				continue
			}
			if d.Status.Phase == "failed" {
				mu.Unlock()
				// A fatal error can arrive while a request is draining. Keep the
				// failed receipt and retry cleanup, never reload this generation.
				_ = m.stopDeployment(host, d)
				continue
			}
			if err := m.deploymentRuntimeError(d); err != nil {
				mu.Unlock()
				m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: "failed", Error: err.Error()})
				_ = m.stopDeployment(host, d)
				continue
			}
			if host.DeploymentRunning(d) {
				mu.Unlock()
				if d.Status.Phase != "ready" {
					m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: "ready"})
				}
				continue
			}
			if d.Status.Phase == "ready" {
				mu.Unlock()
				m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: "failed", Error: "runtime stopped; retry deployment to restart"})
				continue
			}
			// Bound downloads/load attempts on each worker. Remaining assignments
			// stay pending and are not mistaken for running replicas.
			if len(runs) >= 1 {
				mu.Unlock()
				continue
			}
			runCtx, cancel := context.WithCancel(ctx)
			runs[d.ID] = deploymentRun{d.Generation, cancel}
			wg.Add(1)
			mu.Unlock()
			go func(d Deployment) {
				defer wg.Done()
				defer cancel()
				defer func() { mu.Lock(); delete(runs, d.ID); mu.Unlock(); m.wakeDeployments() }()
				report := func(st DeploymentStatus) { m.deploymentStatus(d.ID, d.Generation, st) }
				report(DeploymentStatus{Phase: "downloading"})
				size, err := host.PrepareDeployment(runCtx, d, report)
				if err == nil && runCtx.Err() == nil {
					report(DeploymentStatus{Phase: "loading", Size: size})
					err = host.StartDeployment(runCtx, d, report)
				}
				current, exists := m.deployments.get(false, d.ID)
				if runCtx.Err() != nil || !exists || current.Generation != d.Generation || !m.deploymentAllowed(current) {
					_ = m.stopDeployment(host, d)
					return
				}
				if err != nil {
					report(DeploymentStatus{Phase: "failed", Error: err.Error()})
					_ = m.stopDeployment(host, d)
					return
				}
				if err := m.deploymentRuntimeError(d); err != nil {
					report(DeploymentStatus{Phase: "failed", Error: err.Error()})
					_ = m.stopDeployment(host, d)
					return
				}
				report(DeploymentStatus{Phase: "ready", Size: size})
			}(d)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-m.deploymentWake:
		}
	}
}

func (m *Manager) deploymentModels() []ModelStatus {
	if m.deployments == nil {
		return nil
	}
	var out []ModelStatus
	for _, d := range m.deployments.list(false) {
		if d.Desired != "running" || d.Status.Phase != "ready" || !m.deploymentAllowed(d) {
			continue
		}
		if m.deploymentRuntimeError(d) != nil {
			continue
		}
		tag := "text-generation"
		if d.Kind == "embedding" {
			tag = "sentence-similarity"
		}
		ms := ModelStatus{ID: d.ModelID(), DeploymentID: d.ID, Repo: d.Repository, Source: d.ArtifactSource, Size: d.Status.Size, Loaded: true, Slots: d.NumParallel, NGPULayers: d.NGPULayers, PipelineTag: tag, Category: d.Kind, NativeToolStreaming: true}
		if inspector, ok := m.opts.Host.(DeploymentInspector); ok {
			if actual, ok := inspector.InspectDeployment(d); ok {
				ms = actual
			}
		}
		out = append(out, ms)
	}
	return out
}

// DeploymentReady is checked for every inference request, not only at discovery.
func (m *Manager) DeploymentReady(model string) bool {
	if m.deployments == nil || !strings.HasPrefix(model, DeploymentModelPrefix) {
		return false
	}
	d, ok := m.deployments.get(false, strings.TrimPrefix(model, DeploymentModelPrefix))
	return ok && d.Desired == "running" && d.Status.Phase == "ready" && m.deploymentAllowed(d) && m.deploymentRuntimeError(d) == nil
}

func (m *Manager) createDeployment(spec DeploymentSpec) (Deployment, error) {
	return m.createDeploymentWithMemory(spec, 0)
}

func (m *Manager) createDeploymentWithMemory(spec DeploymentSpec, minimumMemory uint64) (Deployment, error) {
	if m.opts.Mode == "worker" || !m.store.InCluster() {
		return Deployment{}, errors.New("create deployments on a cluster controller")
	}
	if spec.NodeUUID == "" || spec.NodeUUID == "auto" {
		node, err := selectDeploymentTarget(m.memberViews(context.Background(), true), m.identity.UUID, spec.NGPULayers == 0, minimumMemory)
		if err != nil {
			return Deployment{}, err
		}
		// Persist the concrete assignment. Retrying never moves a deployment or
		// changes the node identity in an existing worker receipt.
		spec.NodeUUID = node
	}
	if err := validateDeployment(spec); err != nil {
		return Deployment{}, err
	}
	st := m.candidateStatus(spec.NodeUUID)
	if spec.NodeUUID != m.identity.UUID {
		if _, ok := m.store.Member(spec.NodeUUID); !ok {
			return Deployment{}, errors.New("target node is not a member")
		}
	}
	if st == nil || !st.DeploymentCapable || (spec.NodeUUID != m.identity.UUID && st.ManagedBy != m.identity.UUID) {
		return Deployment{}, errors.New("target node must report this Master as its deployment controller; check membership and node version (LAN peers select a controller in settings)")
	}
	if minimumMemory > 0 {
		free, known, _ := deploymentMemory(st, spec.NGPULayers == 0)
		if !known || free < minimumMemory {
			return Deployment{}, errors.New("target node no longer reports enough available memory for the recommended quantization; refresh node status or choose a quantization manually")
		}
	}
	for _, existing := range m.deployments.list(true) {
		if existing.Desired != "deleted" && strings.EqualFold(existing.Name, spec.Name) {
			return Deployment{}, errors.New("deployment name already exists")
		}
	}
	d := Deployment{DeploymentSpec: spec, ID: uuid.NewString(), ControllerUUID: m.identity.UUID, Generation: 1, Desired: "running", CreatedAt: time.Now().UTC(), Status: DeploymentStatus{Phase: "pending", UpdatedAt: time.Now().UTC()}}
	err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil })
	return d, err
}

func (m *Manager) changeDeployment(id, desired string) (Deployment, error) {
	if m.opts.Mode == "worker" {
		return Deployment{}, errors.New("manage deployments on the Master")
	}
	if desired != "running" && desired != "stopped" && desired != "deleted" {
		return Deployment{}, errors.New("desired must be running, stopped or deleted")
	}
	err := m.deployments.update(true, id, func(d *Deployment, exists bool) error {
		if !exists {
			return fmt.Errorf("deployment not found")
		}
		if d.Desired == "deleted" {
			if desired != "deleted" {
				return errors.New("deleted deployments cannot be restarted")
			}
			if d.Status.Phase != "failed" {
				return nil
			}
		}
		d.Generation++
		d.Desired = desired
		d.Status = DeploymentStatus{Phase: "pending", UpdatedAt: time.Now().UTC()}
		return nil
	})
	d, _ := m.deployments.get(true, id)
	return d, err
}

// LocalDeployments returns execution receipts on this node, including stopped
// tasks. Artifact references are derived from these receipts, never persisted
// in a second model catalog.
func (m *Manager) LocalDeployments() []Deployment {
	if m == nil || m.deployments == nil {
		return nil
	}
	return m.deployments.list(false)
}

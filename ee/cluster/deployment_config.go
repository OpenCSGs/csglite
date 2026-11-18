// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Pointer fields distinguish an omitted value from CPU-only (0) and auto quant ("").
type DeploymentConfigPatch struct {
	Quant       *string `json:"quant,omitempty"`
	NumCtx      *int    `json:"num_ctx,omitempty"`
	NumParallel *int    `json:"num_parallel,omitempty"`
	NGPULayers  *int    `json:"n_gpu_layers,omitempty"`
	Backend     *string `json:"backend,omitempty"`
	SGLangArgs  *string `json:"sglang_args,omitempty"`
}

var errDeploymentConfigConflict = errors.New("deployment configuration conflict")

func sameDeploymentIdentity(a, b DeploymentSpec) bool {
	a.Quant, a.NumCtx, a.NumParallel, a.NGPULayers, a.Backend = b.Quant, b.NumCtx, b.NumParallel, b.NGPULayers, b.Backend
	a.SGLangArgs = b.SGLangArgs
	return a == b
}

func (m *Manager) configureDeployment(id string, generation uint64, patch DeploymentConfigPatch) (Deployment, error) {
	if m.opts.Mode == "worker" || !m.store.InCluster() {
		return Deployment{}, errors.New("configure deployments on the Master")
	}
	if generation == 0 || (patch.Quant == nil && patch.NumCtx == nil && patch.NumParallel == nil && patch.NGPULayers == nil && patch.Backend == nil && patch.SGLangArgs == nil) {
		return Deployment{}, errors.New("generation and at least one configuration parameter are required")
	}
	current, exists := m.deployments.get(true, id)
	if !exists {
		return Deployment{}, errors.New("deployment not found")
	}
	st := m.candidateStatus(current.NodeUUID)
	if st == nil || !st.DeploymentConfigurable {
		return Deployment{}, fmt.Errorf("%w: upgrade the target node to support deployment configuration", errDeploymentConfigConflict)
	}
	err := m.deployments.update(true, id, func(d *Deployment, exists bool) error {
		if !exists || d.ControllerUUID != m.identity.UUID || d.Generation != generation || d.Desired != "stopped" || d.Status.Phase != "stopped" {
			return fmt.Errorf("%w: refresh and wait for the deployment to stop before editing", errDeploymentConfigConflict)
		}
		spec := d.DeploymentSpec
		if patch.Quant != nil {
			spec.Quant = *patch.Quant
		}
		if patch.NumCtx != nil {
			spec.NumCtx = *patch.NumCtx
		}
		if patch.NumParallel != nil {
			spec.NumParallel = *patch.NumParallel
		}
		if patch.NGPULayers != nil {
			spec.NGPULayers = *patch.NGPULayers
		}
		if patch.Backend != nil {
			spec.Backend = normalizeDeploymentBackend(*patch.Backend)
		}
		if patch.SGLangArgs != nil {
			spec.SGLangArgs = *patch.SGLangArgs
		}
		if strings.TrimSpace(spec.SGLangArgs) != "" && !supportsSGLangDeploymentArgs(st) {
			return fmt.Errorf("%w: upgrade the target node to use per-deployment SGLang arguments", errDeploymentConfigConflict)
		}
		if err := validateDeployment(spec); err != nil {
			return err
		}
		if d.DeploymentSpec == spec {
			return nil
		}
		d.DeploymentSpec = spec
		d.Generation++
		d.Status = DeploymentStatus{Phase: "pending", UpdatedAt: time.Now().UTC()}
		return nil
	})
	d, _ := m.deployments.get(true, id)
	return d, err
}

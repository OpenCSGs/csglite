// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"errors"
	"slices"
)

// deploymentMemory never treats unreported GPU usage as free memory or counts
// unified memory once per GPU. The web deployment recommendation uses the same
// rules on the node status it receives.
func deploymentMemory(st *Status, cpuOnly bool) (free uint64, known, gpu bool) {
	if st == nil {
		return 0, false, false
	}
	unified := st.RAM.Unified
	for _, device := range st.GPUs {
		unified = unified || device.Shared
	}
	gpu = !cpuOnly && len(st.GPUs) > 0
	if cpuOnly || unified || len(st.GPUs) == 0 {
		return st.RAM.Total - min(st.RAM.Total, st.RAM.Used), st.RAM.Total > 0, gpu
	}
	for _, device := range st.GPUs {
		if device.UsageKnown && device.VRAMTotal > 0 {
			free += device.VRAMTotal - min(device.VRAMTotal, device.VRAMUsed)
			known = true
		}
	}
	return free, known, gpu
}

func selectDeploymentTarget(nodes []NodeView, controller string, cpuOnly bool, minimumMemory uint64, needsKeepAlive bool, backend string, legacyAutomatic ...bool) (string, error) {
	type candidate struct {
		uuid        string
		free        uint64
		known       bool
		gpu         bool
		engineReady bool
		inflight    int
	}
	var candidates []candidate
	for _, node := range nodes {
		st := node.Status
		if !node.Online || st == nil || !st.DeploymentCapable || !st.Licensed || !st.AcceptWork || st.State != NodeStateActive ||
			(node.UUID != controller && st.ManagedBy != controller) || (needsKeepAlive && !st.DeploymentKeepAlive) {
			continue
		}
		engineReady := engineReportedAndAvailable(st.Engines, backend)
		if backend != "" {
			// Old clients still create automatic embedding/GGUF deployments.
			// Preserve those legacy nodes as an unknown fallback, while an
			// explicit backend selection requires reported capability.
			allowUnknown := len(legacyAutomatic) > 0 && legacyAutomatic[0] && !enginesKnown(st.Engines)
			if !engineReady && !allowUnknown {
				continue
			}
		} else if len(st.Engines) > 0 && !anyEngineAvailable(st.Engines) {
			continue
		}
		free, known, gpu := deploymentMemory(st, cpuOnly)
		if known && free == 0 {
			continue
		}
		if minimumMemory > 0 && (!known || free < minimumMemory) {
			continue
		}
		candidates = append(candidates, candidate{node.UUID, free, known, gpu, engineReady, st.Inflight})
	}
	if len(candidates) == 0 {
		return "", errors.New("no online, authorized deployment node with available memory")
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.engineReady != b.engineReady {
			if a.engineReady {
				return -1
			}
			return 1
		}
		if a.gpu != b.gpu {
			if a.gpu {
				return -1
			}
			return 1
		}
		if a.known != b.known {
			if a.known {
				return -1
			}
			return 1
		}
		if a.free != b.free {
			if a.free > b.free {
				return -1
			}
			return 1
		}
		if a.inflight != b.inflight {
			if a.inflight < b.inflight {
				return -1
			}
			return 1
		}
		if a.uuid < b.uuid {
			return -1
		}
		if a.uuid > b.uuid {
			return 1
		}
		return 0
	})
	return candidates[0].uuid, nil
}

func anyEngineAvailable(engines []EngineStatus) bool {
	for _, engine := range engines {
		if engine.Known && engine.Available {
			return true
		}
	}
	return false
}

func engineReportedAndAvailable(engines []EngineStatus, backend string) bool {
	if backend == "" {
		return anyEngineAvailable(engines)
	}
	for _, engine := range engines {
		if engine.Name == backend && engine.Known && engine.Available {
			return true
		}
	}
	return false
}

func enginesKnown(engines []EngineStatus) bool {
	for _, engine := range engines {
		if engine.Known {
			return true
		}
	}
	return false
}

func deploymentPlacementBackend(spec DeploymentSpec) string {
	if spec.Backend != "" {
		return spec.Backend
	}
	if spec.Kind == "embedding" || spec.Quant != "" {
		return "llama"
	}
	return ""
}

package server

import (
	"net/http"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/hardware"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

func apiRuntimeCapability(capability inference.RuntimeCapability) api.InferenceRuntimeCapability {
	out := api.InferenceRuntimeCapability{
		Backend:     capability.Backend,
		Known:       capability.Known,
		Installed:   capability.Installed,
		Available:   capability.Available,
		Running:     capability.Running,
		Version:     capability.Version,
		Reason:      capability.Reason,
		CUDA:        capability.CUDA,
		ROCM:        capability.ROCM,
		MaxJobs:     capability.MaxJobs,
		Formats:     capability.Formats,
		Tasks:       capability.Tasks,
		ToolCalling: capability.ToolCalling,
	}
	for _, device := range capability.Devices {
		out.Devices = append(out.Devices, api.InferenceEngineDevice{
			Index:       device.Index,
			Name:        device.Name,
			MemoryTotal: device.MemoryTotal,
		})
	}
	return out
}

// inferenceRuntimeCapabilities reports which inference backends this machine
// can actually start, waiting for SGLang detection when a definitive answer
// matters (model settings and backend validation).
func (s *Server) inferenceRuntimeCapabilities() []api.InferenceRuntimeCapability {
	capabilities := inference.RuntimeCapabilities()
	counts := s.engineRunningCounts()
	out := make([]api.InferenceRuntimeCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		capability.Running = counts[capability.Backend]
		out = append(out, apiRuntimeCapability(capability))
	}
	return out
}

// inferenceRuntimeCapabilitiesCached never waits for a pending SGLang probe.
func (s *Server) inferenceRuntimeCapabilitiesCached() []api.InferenceRuntimeCapability {
	capabilities := inference.RuntimeCapabilitiesCached()
	counts := s.engineRunningCounts()
	out := make([]api.InferenceRuntimeCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		capability.Running = counts[capability.Backend]
		out = append(out, apiRuntimeCapability(capability))
	}
	return out
}

// engineRunningCounts counts loaded engines per effective backend. It is read
// under the same lock that guards the engine cache.
func (s *Server) engineRunningCounts() map[string]int {
	counts := map[string]int{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, managed := range s.engines {
		if managed == nil || managed.engine == nil {
			continue
		}
		backend := managed.effectiveBackend
		if backend == "" {
			backend = managed.backend
		}
		if backend == "" {
			backend = inference.LlamaBackend
		}
		counts[backend]++
	}
	return counts
}

// Engines reports this node's engine capability and loaded/running counts to
// cluster peers. It implements cluster.EngineStatusReporter and never waits for
// a pending runtime probe.
func (s *Server) Engines() []cluster.EngineStatus {
	counts := s.engineRunningCounts()
	capabilities := inference.RuntimeCapabilitiesCached()
	out := make([]cluster.EngineStatus, 0, len(capabilities))
	for _, capability := range capabilities {
		status := cluster.EngineStatus{
			Name:        capability.Backend,
			Known:       capability.Known,
			Installed:   capability.Installed,
			Available:   capability.Available,
			Running:     counts[capability.Backend],
			Version:     capability.Version,
			Reason:      capability.Reason,
			CUDA:        capability.CUDA,
			ROCM:        capability.ROCM,
			Formats:     capability.Formats,
			Tasks:       capability.Tasks,
			ToolCalling: capability.ToolCalling,
			MaxJobs:     capability.MaxJobs,
		}
		for _, device := range capability.Devices {
			status.Devices = append(status.Devices, cluster.EngineDevice{
				Index:       device.Index,
				Name:        device.Name,
				MemoryTotal: device.MemoryTotal,
			})
		}
		out = append(out, status)
	}
	return out
}

// GET /api/inference/runtimes
func (s *Server) handleInferenceRuntimes(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	limits := hardware.ContainerLimits()
	response := api.InferenceRuntimesResponse{
		Runtimes: s.inferenceRuntimeCapabilitiesCached(),
		Limits: api.InferenceResourceLimits{
			CPUCores:    limits.CPUCores,
			MemoryBytes: limits.MemoryBytes,
			Source:      limits.Source,
		},
	}
	if gpus, err := hardware.QueryNVIDIAGPUs(); err == nil {
		for _, gpu := range gpus {
			response.GPUs = append(response.GPUs, api.InferenceGPU{
				Index:       gpu.Index,
				Name:        gpu.Name,
				MemoryTotal: gpu.MemoryTotal,
				MemoryUsed:  gpu.MemoryUsed,
			})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

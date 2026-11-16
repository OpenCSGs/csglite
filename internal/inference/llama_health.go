package inference

import (
	"errors"
	"net/http"
	"strings"
)

// RuntimeError is sticky for this process: a poisoned GPU backend is not made
// usable by a successful HTTP liveness check. A new engine starts healthy.
func (e *llamaEngine) RuntimeError() error {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	return e.runtimeErr
}

func (e *llamaEngine) failRuntime(err error, memory bool) {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	if e.runtimeErr == nil || memory {
		e.runtimeErr = err
	}
}

func (e *llamaEngine) observeRuntimeLog(output string) {
	text := strings.ToLower(output)
	// These report failed compute, unlike allocation warnings during the
	// loader's memory-fit attempts, which may recover by offloading fewer layers.
	metalOOM := strings.Contains(text, "kiogpucommandbuffercallbackerroroutofmemory")
	computeFailed := strings.Contains(text, "backend is in error state") || strings.Contains(text, "ggml_backend_sched_graph_compute_async failed with error -")
	if metalOOM || (computeFailed && hasMemoryError(text)) {
		e.failRuntime(errors.New("llama-server inference failed: insufficient memory (GPU out of memory)"), true)
	} else if computeFailed {
		e.failRuntime(errors.New("llama-server compute backend failed; retry deployment to restart"), false)
	}
}

func hasMemoryError(text string) bool {
	return strings.Contains(text, "out of memory") || strings.Contains(text, "insufficient memory") || strings.Contains(text, "outofmemory")
}

func (e *llamaEngine) runtimeHTTPError(status int, body string) error {
	if status >= http.StatusInternalServerError {
		text := strings.ToLower(body)
		if hasMemoryError(text) {
			e.failRuntime(errors.New("llama-server inference failed: insufficient memory (GPU out of memory)"), true)
		} else if strings.Contains(text, "compute error") {
			e.failRuntime(errors.New("llama-server compute backend failed; retry deployment to restart"), false)
		}
		if err := e.RuntimeError(); err != nil {
			return &inferenceHTTPError{status: status, body: err.Error(), cause: err}
		}
	}
	return &inferenceHTTPError{status: status, body: body}
}

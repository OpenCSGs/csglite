package inference

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLlamaRuntimeRemembersFatalOutputAcrossChunksAndLogRotation(t *testing.T) {
	e := &llamaEngine{logBuf: newCappedWriter(128)}
	e.logBuf.observe = e.observeRuntimeLog
	for _, part := range []string{"E error: Insufficient Memory (00000008:kIOGPUCommandBufferCallback", "ErrorOutOfMemory)\n"} {
		if _, err := e.logBuf.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := RuntimeError(e); err == nil || !strings.Contains(err.Error(), "insufficient memory") {
		t.Fatal("GPU failure was not recorded", err)
	}
	if _, err := e.logBuf.Write([]byte(strings.Repeat("normal output\n", 100))); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.logBuf.String(), "OutOfMemory") {
		t.Fatal("fixture did not rotate the fatal output out of the buffer")
	}
	if RuntimeError(e) == nil {
		t.Fatal("rotating logs made a poisoned runtime healthy again")
	}
	if RuntimeError(&llamaEngine{}) != nil {
		t.Fatal("a replacement engine inherited the previous process's fault")
	}
}

func TestLlamaRuntimeIgnoresRecoverableAllocationWarningsAndAborts(t *testing.T) {
	e := &llamaEngine{}
	for _, output := range []string{
		"CUDA error: out of memory; retrying model fit with fewer GPU layers",
		"ggml_backend_sched_graph_compute_async failed with error 1 (aborted)",
		"request canceled",
	} {
		e.observeRuntimeLog(output)
		if RuntimeError(e) != nil {
			t.Fatalf("recoverable output poisoned the engine: %s", output)
		}
	}
}

func TestLlamaInferenceProtocolsRecordTerminalComputeFailure(t *testing.T) {
	for _, protocol := range []string{"chat", "embedding", "systemone", "adapter"} {
		t.Run(protocol, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"Compute error."}}`))
			}))
			t.Cleanup(backend.Close)
			port := backend.Listener.Addr().(*net.TCPAddr).Port
			e := &llamaEngine{port: port, client: backend.Client(), decisionType: "openjev", decisionLoRA: true}
			var err error
			switch protocol {
			case "chat":
				_, err = e.ChatCompletion(context.Background(), map[string]interface{}{"model": "test"})
			case "embedding":
				_, err = e.Embeddings(context.Background(), map[string]interface{}{"input": "test"})
			case "systemone":
				_, err = e.SystemOne(context.Background(), []byte(`{"state":"test"}`))
			case "adapter":
				_, _, err = e.ScoreDecisionTokens(context.Background(), "test", []int{1, 2})
			}
			if HTTPStatusCode(err) != http.StatusInternalServerError || RuntimeError(e) == nil {
				t.Fatalf("%s compute failure left the runtime healthy: %v", protocol, err)
			}
			// Even an HTTP liveness probe that still succeeds must not reset the
			// fault: /health can remain 200 after a Metal command buffer fails.
			response, probeErr := backend.Client().Get(backend.URL + "/health")
			if probeErr != nil {
				t.Fatal(probeErr)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK || RuntimeError(e) == nil {
				t.Fatal("HTTP liveness hid a terminal compute failure")
			}
		})
	}
}

func TestLlamaClientAndTransientErrorsDoNotFailDeployment(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		e := &llamaEngine{}
		body := "request rejected"
		if status == http.StatusBadRequest {
			body = "input contains the words Compute error and out of memory"
		}
		err := e.runtimeHTTPError(status, body)
		if HTTPStatusCode(err) != status || RuntimeError(e) != nil {
			t.Fatalf("status %d was treated as a terminal runtime error: %v", status, err)
		}
	}
}

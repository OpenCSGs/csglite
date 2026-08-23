package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestStopCmd_ServiceAndModel(t *testing.T) {
	cmd := NewRootCmd("test")

	stopCmd, _, err := cmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("Find(stop) error: %v", err)
	}
	if stopCmd.Name() != "stop" {
		t.Fatalf("Find(stop) = %q, want stop", stopCmd.Name())
	}

	modelCmd, _, err := cmd.Find([]string{"stop", "model"})
	if err != nil {
		t.Fatalf("Find(stop model) error: %v", err)
	}
	if modelCmd.Name() != "model" {
		t.Fatalf("Find(stop model) = %q, want model", modelCmd.Name())
	}

	for _, alias := range []string{"stop-server", "down"} {
		aliasCmd, _, err := cmd.Find([]string{alias})
		if err != nil {
			t.Fatalf("Find(%s) error: %v", alias, err)
		}
		if aliasCmd.Name() != "stop" {
			t.Fatalf("Find(%s) = %q, want stop", alias, aliasCmd.Name())
		}
	}
}

func TestStopServiceCmdDeprecated(t *testing.T) {
	cmd := NewRootCmd("test")

	stopServiceCmd, _, err := cmd.Find([]string{"stop-service"})
	if err != nil {
		t.Fatalf("Find(stop-service) error: %v", err)
	}
	if stopServiceCmd.Name() != "stop-service" {
		t.Fatalf("Find(stop-service) = %q, want stop-service", stopServiceCmd.Name())
	}
	if stopServiceCmd.Deprecated == "" {
		t.Fatal("stop-service should be marked deprecated")
	}
	if !strings.Contains(stopServiceCmd.Deprecated, `use "csghub-lite stop" instead`) {
		t.Fatalf("Deprecated = %q, want a pointer to csghub-lite stop", stopServiceCmd.Deprecated)
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute help error: %v", err)
	}
	if strings.Contains(buf.String(), "stop-service") {
		t.Fatalf("root help should not advertise deprecated stop-service: %s", buf.String())
	}
}

func TestStopRunningModel(t *testing.T) {
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/stop" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req api.StopRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode stop request: %v", err)
		}
		gotModel = req.Model
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"stopped"}`)
	}))
	defer server.Close()

	if err := stopRunningModel(server.URL, "Qwen/Qwen3-0.6B-GGUF"); err != nil {
		t.Fatalf("stopRunningModel returned error: %v", err)
	}
	if gotModel != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("stopped model = %q, want Qwen/Qwen3-0.6B-GGUF", gotModel)
	}
}

func TestStopRunningModelReportsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model \"Qwen/missing\" is not running"}`)
	}))
	defer server.Close()

	err := stopRunningModel(server.URL, "Qwen/missing")
	if err == nil || !strings.Contains(err.Error(), `model "Qwen/missing" is not running`) {
		t.Fatalf("error = %v, want not-running message", err)
	}
}

func TestListRunningModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/ps" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.PsResponse{
			Models: []api.RunningModel{
				{Name: "Qwen/Qwen3-0.6B-GGUF", Model: "Qwen/Qwen3-0.6B-GGUF"},
			},
		})
	}))
	defer server.Close()

	models, err := listRunningModels(server.URL)
	if err != nil {
		t.Fatalf("listRunningModels returned error: %v", err)
	}
	if len(models) != 1 || runningModelID(models[0]) != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("models = %#v, want one Qwen/Qwen3-0.6B-GGUF", models)
	}
}

func TestRunningModelIDPrefersModelField(t *testing.T) {
	got := runningModelID(api.RunningModel{Name: "display", Model: "ns/name"})
	if got != "ns/name" {
		t.Fatalf("runningModelID() = %q, want ns/name", got)
	}
	got = runningModelID(api.RunningModel{Name: "display"})
	if got != "display" {
		t.Fatalf("runningModelID() = %q, want display", got)
	}
}

func TestStopAllRunningModelsAtStopsEachModel(t *testing.T) {
	var stopped []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/ps":
			_ = json.NewEncoder(w).Encode(api.PsResponse{
				Models: []api.RunningModel{
					{Model: "Qwen/A"},
					{Name: "Qwen/B"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/stop":
			var req api.StopRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode stop request: %v", err)
			}
			stopped = append(stopped, req.Model)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"stopped"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	if err := stopAllRunningModelsAt(server.URL); err != nil {
		t.Fatalf("stopAllRunningModelsAt returned error: %v", err)
	}
	if len(stopped) != 2 || stopped[0] != "Qwen/A" || stopped[1] != "Qwen/B" {
		t.Fatalf("stopped = %#v, want [Qwen/A Qwen/B]", stopped)
	}
}

func TestStopAllRunningModelsAtReportsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.PsResponse{})
	}))
	defer server.Close()

	var err error
	output := captureCLIStdout(t, func() {
		err = stopAllRunningModelsAt(server.URL)
	})
	if err != nil {
		t.Fatalf("stopAllRunningModelsAt returned error: %v", err)
	}
	if !strings.Contains(output, "No models currently running.") {
		t.Fatalf("stdout = %q, want empty-state message", output)
	}
}

func TestStopAllRunningModelsAtWarnsOnEmptyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/ps":
			_ = json.NewEncoder(w).Encode(api.PsResponse{
				Models: []api.RunningModel{{}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var err error
	output := captureCLIStderr(t, func() {
		err = stopAllRunningModelsAt(server.URL)
	})
	if err != nil {
		t.Fatalf("stopAllRunningModelsAt returned error: %v", err)
	}
	if !strings.Contains(output, "warning: skipping model with empty id") {
		t.Fatalf("stderr = %q, want empty-id warning", output)
	}
}

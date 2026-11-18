// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func configTestNode(t *testing.T) (*Manager, *deploymentTestHost, Deployment) {
	t.Helper()
	h := &deploymentTestHost{fakeHost: &fakeHost{name: "config", limit: 0}, running: map[string]bool{}}
	m, err := New(Options{Dir: t.TempDir(), Host: h, Discoverer: NopDiscoverer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.store.create("test", "lan", ""); err != nil {
		t.Fatal(err)
	}
	d := testDeployment(m)
	d.Desired = "stopped"
	d.Status.Phase = "stopped"
	for _, owned := range []bool{true, false} {
		if err := m.deployments.update(owned, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return m, h, d
}

func configRequest(m *Manager, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/api/cluster/deployments/"+id, strings.NewReader(body))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	m.Admin().HandleDeploymentUpdate(w, r)
	return w
}

func TestDeploymentConfigurationRoundTripAndLegacyStart(t *testing.T) {
	m, _, d := configTestNode(t)
	w := configRequest(m, d.ID, fmt.Sprintf(`{"generation":%d,"config":{"quant":"q4_k_m","num_ctx":8192,"num_parallel":2,"n_gpu_layers":0}}`, d.Generation))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var changed Deployment
	if err := json.Unmarshal(w.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Generation != d.Generation+1 || changed.Desired != "stopped" || changed.ModelID() != d.ModelID() || !sameDeploymentIdentity(changed.DeploymentSpec, d.DeploymentSpec) {
		t.Fatalf("identity/state changed: %+v", changed)
	}
	if changed.NumCtx != 8192 || changed.NumParallel != 2 || changed.NGPULayers != 0 || changed.Quant != "q4_k_m" {
		t.Fatalf("settings: %+v", changed)
	}
	if _, err := m.receiveDeployment(m.identity.UUID, changed); err != nil {
		t.Fatal(err)
	}
	m.deploymentStatus(d.ID, changed.Generation, DeploymentStatus{Phase: "stopped"})
	// Controller acknowledgement and persisted receipts survive a restart.
	changed.Status.Phase = "stopped"
	if err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { *record = changed; return nil }); err != nil {
		t.Fatal(err)
	}
	reopened, err := openDeploymentStore(m.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, owned := range []bool{true, false} {
		got, _ := reopened.get(owned, d.ID)
		if got.DeploymentSpec != changed.DeploymentSpec || got.Desired != "stopped" {
			t.Fatalf("lost configuration: %+v", got)
		}
	}
	// Partial updates preserve omitted fields; empty quant and CPU-only zero are intentional.
	w = configRequest(m, d.ID, fmt.Sprintf(`{"generation":%d,"config":{"quant":""}}`, changed.Generation))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	changed = Deployment{}
	if err := json.Unmarshal(w.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Quant != "" || changed.NGPULayers != 0 || changed.NumCtx != 8192 {
		t.Fatalf("partial update: %+v", changed)
	}
	if _, err := m.receiveDeployment(m.identity.UUID, changed); err != nil {
		t.Fatal(err)
	}
	m.deploymentStatus(d.ID, changed.Generation, DeploymentStatus{Phase: "stopped"})
	// The original desired-only API still starts the same ID with the edited spec.
	w = configRequest(m, d.ID, `{"desired":"running"}`)
	if w.Code != 202 {
		t.Fatalf("legacy start: %d %s", w.Code, w.Body)
	}
	var running Deployment
	if err := json.Unmarshal(w.Body.Bytes(), &running); err != nil {
		t.Fatal(err)
	}
	if running.DeploymentSpec != changed.DeploymentSpec || running.Generation != changed.Generation+1 {
		t.Fatal("legacy restart lost config")
	}
	if _, err := m.receiveDeployment(m.identity.UUID, running); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentConfigRejectsInvalidOrConflictingUpdates(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		mutate     func(*Deployment)
	}{
		{"stale", `{"generation":9,"config":{"num_ctx":8192}}`, 409, nil},
		{"missing generation", `{"config":{"num_ctx":8192}}`, 400, nil},
		{"empty", `{"generation":1,"config":{}}`, 400, nil},
		{"null", `{"generation":1,"config":null}`, 400, nil},
		{"mixed", `{"generation":1,"desired":"running","config":{"num_ctx":8192}}`, 400, nil},
		{"invalid context", `{"generation":1,"config":{"num_ctx":0}}`, 400, nil},
		{"short context", `{"generation":1,"config":{"num_ctx":511}}`, 400, nil},
		{"invalid parallel", `{"generation":1,"config":{"num_parallel":17}}`, 400, nil},
		{"unsupported image runtime", `{"generation":1,"config":{"num_ctx":262144}}`, 400, func(d *Deployment) { d.Kind = "image" }},
		{"unsupported ASR runtime", `{"generation":1,"config":{"num_ctx":262144}}`, 400, func(d *Deployment) { d.Kind = "asr" }},
		{"unsupported TTS runtime", `{"generation":1,"config":{"num_ctx":262144}}`, 400, func(d *Deployment) { d.Kind = "tts" }},
		{"invalid layers", `{"generation":1,"config":{"n_gpu_layers":-2}}`, 400, nil},
		{"invalid quant", `{"generation":1,"config":{"quant":"../q4"}}`, 400, nil},
		{"immutable node", `{"generation":1,"config":{"node_uuid":"auto"}}`, 400, nil},
		{"running", `{"generation":1,"config":{"num_ctx":8192}}`, 409, func(d *Deployment) { d.Desired = "running"; d.Status.Phase = "ready" }},
		{"stopping", `{"generation":1,"config":{"num_ctx":8192}}`, 409, func(d *Deployment) { d.Status.Phase = "stopping" }},
		{"unreachable", `{"generation":1,"config":{"num_ctx":8192}}`, 409, func(d *Deployment) { d.Status.Phase = "unreachable" }},
		{"failed stop", `{"generation":1,"config":{"num_ctx":8192}}`, 409, func(d *Deployment) { d.Status.Phase = "failed" }},
		{"deleted", `{"generation":1,"config":{"num_ctx":8192}}`, 409, func(d *Deployment) { d.Desired = "deleted"; d.Status.Phase = "deleted" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, d := configTestNode(t)
			if tc.mutate != nil {
				tc.mutate(&d)
				if err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			w := configRequest(m, d.ID, tc.body)
			if w.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			got, _ := m.deployments.get(true, d.ID)
			if got != d {
				t.Fatal("rejected edit changed persisted deployment")
			}
		})
	}
}

func TestDeploymentConfigRequiresNodeSupportAndController(t *testing.T) {
	m, _, d := configTestNode(t)
	if !m.candidateStatus(m.identity.UUID).DeploymentConfigurable {
		t.Fatal("capability not advertised")
	}
	d.NodeUUID = uuid.NewString()
	if err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil }); err != nil {
		t.Fatal(err)
	}
	w := configRequest(m, d.ID, `{"generation":1,"config":{"num_ctx":8192}}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "upgrade") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m.opts.Mode = "worker"
	w = configRequest(m, d.ID, `{"generation":1,"config":{"num_ctx":8192}}`)
	if w.Code != 403 {
		t.Fatalf("worker allowed configuration: %d", w.Code)
	}
	w = configRequest(m, uuid.NewString(), `{"desired":"running"}`)
	if w.Code != 403 {
		t.Fatal("worker bypass")
	}
}

func TestDeploymentConfigWorkerFencesRuntimeAndGeneration(t *testing.T) {
	for _, reason := range []string{"same generation", "stale generation", "running", "preparing", "immutable revision", "deleted"} {
		t.Run(reason, func(t *testing.T) {
			m, h, d := configTestNode(t)
			incoming := d
			incoming.Generation++
			incoming.NumCtx = 8192
			switch reason {
			case "same generation":
				incoming.Generation = d.Generation
			case "stale generation":
				d.Generation = 3
			case "running":
				h.running[d.ID] = true
			case "preparing":
				m.deploymentRuns[d.ID] = deploymentRun{generation: d.Generation}
			case "immutable revision":
				incoming.Revision = "another"
			case "deleted":
				d.Desired = "deleted"
				d.Status.Phase = "deleted"
			}
			if err := m.deployments.update(false, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := m.receiveDeployment(m.identity.UUID, incoming); err == nil {
				t.Fatal("unsafe assignment accepted")
			}
			got, _ := m.deployments.get(false, d.ID)
			if got != d {
				t.Fatal("rejected assignment changed receipt")
			}
		})
	}
}

func TestDeploymentEditedSettingsReachRuntimeAfterExplicitStart(t *testing.T) {
	m, h := deploymentTestNode(t)
	d, err := m.createDeployment(testDeployment(m).DeploymentSpec)
	if err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { current, _ := m.deployments.get(true, d.ID); return current.Status.Phase == "ready" })
	if _, err := m.changeDeployment(d.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { current, _ := m.deployments.get(true, d.ID); return current.Status.Phase == "stopped" })
	stopped, _ := m.deployments.get(true, d.ID)
	ctx, parallel, layers, quant := 262144, 2, 0, "q4_k_m"
	changed, err := m.configureDeployment(d.ID, stopped.Generation, DeploymentConfigPatch{NumCtx: &ctx, NumParallel: &parallel, NGPULayers: &layers, Quant: &quant})
	if err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool {
		current, _ := m.deployments.get(true, d.ID)
		return current.Status.Phase == "stopped" && current.Generation == changed.Generation
	})
	if h.starts.Load() != 1 || h.DeploymentRunning(changed) {
		t.Fatal("saving configuration started runtime")
	}
	if _, err := m.changeDeployment(d.ID, "running"); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { current, _ := m.deployments.get(true, d.ID); return current.Status.Phase == "ready" })
	h.runtimeMu.Lock()
	actual := h.lastStarted
	h.runtimeMu.Unlock()
	if actual.NumCtx != ctx || actual.NumParallel != parallel || actual.NGPULayers != layers || actual.Quant != quant || actual.ModelID() != d.ModelID() {
		t.Fatalf("runtime got wrong configuration: %+v", actual)
	}
}

func TestDeploymentLongContextCreateEditAndWorkerReceipt(t *testing.T) {
	for _, numCtx := range []int{262144, 1048576, 2097152} {
		t.Run(fmt.Sprint(numCtx), func(t *testing.T) {
			m, _, stopped := configTestNode(t)
			spec := testDeployment(m).DeploymentSpec
			spec.Name = fmt.Sprintf("Long-context-%d", numCtx)
			spec.NumCtx = numCtx
			body, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			m.Admin().HandleDeploymentCreate(w, httptest.NewRequest(http.MethodPost, "/api/cluster/deployments", strings.NewReader(string(body))))
			if w.Code != http.StatusAccepted {
				t.Fatalf("create: %d %s", w.Code, w.Body)
			}
			var created Deployment
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.NumCtx != numCtx {
				t.Fatalf("create truncated context: %d", created.NumCtx)
			}
			if _, err := m.receiveDeployment(m.identity.UUID, created); err != nil {
				t.Fatalf("worker rejected created context: %v", err)
			}
			w = configRequest(m, stopped.ID, fmt.Sprintf(`{"generation":%d,"config":{"num_ctx":%d}}`, stopped.Generation, numCtx))
			if w.Code != http.StatusAccepted {
				t.Fatalf("edit: %d %s", w.Code, w.Body)
			}
			var edited Deployment
			if err := json.Unmarshal(w.Body.Bytes(), &edited); err != nil {
				t.Fatal(err)
			}
			if edited.NumCtx != numCtx || edited.Desired != "stopped" {
				t.Fatal("edit changed desired state or truncated context")
			}
			if _, err := m.receiveDeployment(m.identity.UUID, edited); err != nil {
				t.Fatalf("worker rejected edited context: %v", err)
			}
			reopened, err := openDeploymentStore(m.opts.Dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, owned := range []bool{true, false} {
				saved, ok := reopened.get(owned, edited.ID)
				if !ok || saved.NumCtx != numCtx {
					t.Fatal("context lost after reopening deployment store")
				}
			}
		})
	}
}

func TestDeploymentBackendIsEditableAndReachesRuntime(t *testing.T) {
	m, h := deploymentTestNode(t)
	h.engines = []EngineStatus{{Name: "sglang", Known: true, Available: true, DeploymentArgs: true}, {Name: "llama", Known: true, Available: true}}
	d, err := m.createDeployment(testDeployment(m).DeploymentSpec)
	if err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { current, _ := m.deployments.get(true, d.ID); return current.Status.Phase == "ready" })
	if _, err := m.changeDeployment(d.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { current, _ := m.deployments.get(true, d.ID); return current.Status.Phase == "stopped" })
	stopped, _ := m.deployments.get(true, d.ID)
	backend := "sglang"
	args := "--chunked-prefill-size 4096\n--kv-cache-dtype bf16"
	changed, err := m.configureDeployment(d.ID, stopped.Generation, DeploymentConfigPatch{Backend: &backend, SGLangArgs: &args})
	if err != nil {
		t.Fatalf("backend is not editable alongside other stopped settings: %v", err)
	}
	if changed.Backend != "sglang" {
		t.Fatalf("backend = %q, want sglang", changed.Backend)
	}
	if changed.Generation != stopped.Generation+1 {
		t.Fatalf("generation = %d, want %d", changed.Generation, stopped.Generation+1)
	}
	if _, err := m.changeDeployment(d.ID, "running"); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { current, _ := m.deployments.get(true, d.ID); return current.Status.Phase == "ready" })
	h.runtimeMu.Lock()
	actual := h.lastStarted
	h.runtimeMu.Unlock()
	if actual.Backend != "sglang" || actual.SGLangArgs != args {
		t.Fatalf("runtime got backend %q, args %q", actual.Backend, actual.SGLangArgs)
	}
}

func TestDeploymentRejectsSGLangForEmbedding(t *testing.T) {
	m, _ := deploymentTestNode(t)
	spec := testDeployment(m).DeploymentSpec
	spec.Kind = "embedding"
	spec.Backend = "sglang"
	if _, err := m.createDeployment(spec); err == nil || !strings.Contains(err.Error(), "chat only") {
		t.Fatalf("error = %v, want SGLang chat-only rejection", err)
	}
}

func TestDeploymentEnginePlanSurvivesReadyAndClearsOnConfigure(t *testing.T) {
	m, _, d := configTestNode(t)
	plan := &EnginePlanStatus{Resolved: true, Source: "worker-start", ContextLength: 8192, ToolCallParser: "qwen25"}
	seed := DeploymentStatus{Phase: "stopped", EngineBackend: "sglang", EnginePlan: plan}
	for _, owned := range []bool{true, false} {
		if err := m.deployments.update(owned, d.ID, func(record *Deployment, _ bool) error { record.Status = seed; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: "ready"})
	current, _ := m.deployments.get(false, d.ID)
	if current.Status.EnginePlan == nil || current.Status.EngineBackend != "sglang" || current.Status.Phase != "ready" {
		t.Fatalf("ready update erased the started plan: %+v", current.Status)
	}
	m.deploymentStatus(d.ID, d.Generation, DeploymentStatus{Phase: "stopped"})
	current, _ = m.deployments.get(false, d.ID)
	if current.Status.EnginePlan == nil {
		t.Fatal("stop update erased the started plan")
	}
	current, _ = m.deployments.get(true, d.ID)
	ctx := 16384
	changed, err := m.configureDeployment(d.ID, current.Generation, DeploymentConfigPatch{NumCtx: &ctx})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Status.EnginePlan != nil || changed.Status.EngineBackend != "" {
		t.Fatalf("new generation inherited the old runtime plan: %+v", changed.Status)
	}
}

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
)

func placementNode(id string, free uint64) NodeView {
	return NodeView{UUID: id, Online: true, Status: &Status{
		DeploymentCapable: true, Licensed: true, AcceptWork: true, State: NodeStateActive, ManagedBy: "master",
		GPUs: []GPUStatus{{VRAMTotal: 32 << 30, VRAMUsed: (32 << 30) - free, UsageKnown: true}},
		RAM:  RAMStatus{Total: 64 << 30, Used: 8 << 30},
	}}
}

func TestAutomaticEmbeddingPlacementUsesLlamaAndLegacyFallback(t *testing.T) {
	sg := placementNode("sg", 30<<30)
	sg.Status.Engines = []EngineStatus{{Name: "sglang", Known: true, Installed: true, Available: true}}
	llama := placementNode("llama", 16<<30)
	llama.Status.Engines = []EngineStatus{{Name: "llama", Known: true, Installed: true, Available: true}}
	legacy := placementNode("legacy", 31<<30)
	backend := deploymentPlacementBackend(DeploymentSpec{Kind: "embedding"})
	if backend != "llama" {
		t.Fatalf("embedding placement backend = %q", backend)
	}
	if id, err := selectDeploymentTarget([]NodeView{sg, llama, legacy}, "master", false, 0, false, backend, true); err != nil || id != "llama" {
		t.Fatalf("embedding selected %q, %v; want known llama node", id, err)
	}
	if id, err := selectDeploymentTarget([]NodeView{legacy}, "master", false, 0, false, backend, true); err != nil || id != "legacy" {
		t.Fatalf("legacy automatic fallback = %q, %v", id, err)
	}
	if _, err := selectDeploymentTarget([]NodeView{sg}, "master", false, 0, false, backend, true); err == nil {
		t.Fatal("embedding was placed on a known SGLang-only node")
	}
	if _, err := selectDeploymentTarget([]NodeView{legacy}, "master", false, 0, false, backend); err == nil {
		t.Fatal("an unknown legacy node satisfied an explicit engine request")
	}
}

func TestSelectDeploymentTarget(t *testing.T) {
	small := placementNode("small", 12<<30)
	large := placementNode("large", 24<<30)
	for _, reason := range []string{"offline", "unlicensed", "paused", "foreign", "not-accepting", "unsupported", "no-memory"} {
		t.Run(reason, func(t *testing.T) {
			invalid := placementNode("invalid", 32<<30)
			switch reason {
			case "offline":
				invalid.Online = false
			case "unlicensed":
				invalid.Status.Licensed = false
			case "paused":
				invalid.Status.State = NodeStateMaintenance
			case "foreign":
				invalid.Status.ManagedBy = "other-master"
			case "not-accepting":
				invalid.Status.AcceptWork = false
			case "unsupported":
				invalid.Status.DeploymentCapable = false
			case "no-memory":
				invalid.Status.GPUs[0].VRAMUsed = invalid.Status.GPUs[0].VRAMTotal
			}
			id, err := selectDeploymentTarget([]NodeView{small, invalid, large}, "master", false, 0, false, "")
			if err != nil || id != "large" {
				t.Fatalf("selected %q, %v; want large", id, err)
			}
			if _, err := selectDeploymentTarget([]NodeView{invalid}, "master", false, 0, false, ""); err == nil {
				t.Fatal("ineligible target was selected")
			}
		})
	}
}

func TestDeploymentMemoryAndTies(t *testing.T) {
	a, b := placementNode("a", 24<<30), placementNode("b", 24<<30)
	a.Status.Inflight = 1
	if id, _ := selectDeploymentTarget([]NodeView{a, b}, "master", false, 0, false, ""); id != "b" {
		t.Fatal("busy node won a memory tie")
	}
	a.Status.Inflight = 0
	if id, _ := selectDeploymentTarget([]NodeView{b, a}, "master", false, 0, false, ""); id != "a" {
		t.Fatal("UUID tie break was not deterministic")
	}
	a.Status.RAM.Unified = true
	a.Status.GPUs = append(a.Status.GPUs, a.Status.GPUs[0])
	if free, known, _ := deploymentMemory(a.Status, false); !known || free != 56<<30 {
		t.Fatalf("shared RAM was counted incorrectly: %d, %v", free, known)
	}
	a.Status.RAM.Unified = false
	for i := range a.Status.GPUs {
		a.Status.GPUs[i].UsageKnown = false
	}
	if _, known, _ := deploymentMemory(a.Status, false); known {
		t.Fatal("unknown GPU usage was treated as free memory")
	}
	if free, known, gpu := deploymentMemory(a.Status, true); free != 56<<30 || !known || gpu {
		t.Fatal("CPU-only deployment did not use host RAM")
	}
}

func TestAutoPlacementRechecksRecommendedMemory(t *testing.T) {
	// The preview's larger node has filled up before the create request arrives.
	changed := placementNode("previously-large", 2<<30)
	available := placementNode("still-fits", 12<<30)
	unknown := placementNode("unknown", 32<<30)
	unknown.Status.GPUs[0].UsageKnown = false
	id, err := selectDeploymentTarget([]NodeView{changed, unknown, available}, "master", false, 10<<30, false, "")
	if err != nil || id != "still-fits" {
		t.Fatalf("stale recommendation selected %q: %v", id, err)
	}
	if _, err := selectDeploymentTarget([]NodeView{changed, unknown}, "master", false, 10<<30, false, ""); err == nil {
		t.Fatal("assigned a recommended variant without enough reported memory")
	}
}

func TestRecommendedMemoryIsCheckedBeforeCreatingAssignment(t *testing.T) {
	m, _ := deploymentTestNode(t)
	body := `{"name":"oversized-recommendation","repository":"Qwen/demo","node_uuid":"auto","minimum_memory_bytes":9223372036854775807}`
	w := httptest.NewRecorder()
	m.Admin().HandleDeploymentCreate(w, httptest.NewRequest(http.MethodPost, "/api/cluster/deployments", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest || len(m.deployments.list(true)) != 0 {
		t.Fatalf("persisted a deployment with an unverifiable recommendation: %d %s", w.Code, w.Body.String())
	}
}

func TestCreateDeploymentAutoPersistsConcreteAssignment(t *testing.T) {
	m, _ := deploymentTestNode(t)
	for index, nodeField := range []string{`,"node_uuid":"auto"`, `,"node_uuid":""`, ""} {
		t.Run(nodeField, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"auto-%d","repository":"Qwen/demo"%s}`, index, nodeField)
			w := httptest.NewRecorder()
			m.Admin().HandleDeploymentCreate(w, httptest.NewRequest(http.MethodPost, "/api/cluster/deployments", strings.NewReader(body)))
			if w.Code != http.StatusAccepted {
				t.Fatalf("auto create: %d %s", w.Code, w.Body.String())
			}
			var created Deployment
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.NodeUUID != m.identity.UUID {
				t.Fatalf("persisted unresolved target: %q", created.NodeUUID)
			}
			reopened, err := openDeploymentStore(m.opts.Dir)
			if err != nil {
				t.Fatal(err)
			}
			stored, exists := reopened.get(true, created.ID)
			if !exists || stored.NodeUUID != created.NodeUUID {
				t.Fatal("concrete Auto assignment did not survive reload")
			}
			updated, err := m.changeDeployment(created.ID, "stopped")
			if err != nil || updated.NodeUUID != created.NodeUUID {
				t.Fatal("changing desired state moved an Auto deployment", err)
			}
		})
	}
}

func TestSelectDeploymentTargetFiltersByEngineCapability(t *testing.T) {
	sg := placementNode("sg", 24<<30)
	sg.Status.Engines = []EngineStatus{{Name: "sglang", Known: true, Installed: true, Available: true}}
	llama := placementNode("llama", 32<<30)
	llama.Status.Engines = []EngineStatus{{Name: "llama", Known: true, Installed: true, Available: true}}
	legacy := placementNode("legacy", 40<<30) // no engine report: unknown, never assumed

	if id, err := selectDeploymentTarget([]NodeView{sg, llama, legacy}, "master", false, 0, false, "sglang"); err != nil || id != "sg" {
		t.Fatalf("explicit sglang selected %q, %v; want sg", id, err)
	}
	if id, err := selectDeploymentTarget([]NodeView{sg, llama, legacy}, "master", false, 0, false, "llama"); err != nil || id != "llama" {
		t.Fatalf("explicit llama selected %q, %v; want llama", id, err)
	}
	if id, err := selectDeploymentTarget([]NodeView{legacy, llama}, "master", false, 0, false, "sglang"); err == nil {
		t.Fatalf("explicit sglang selected an unverified node %q", id)
	}
	// Automatic placement prefers a node with a known available engine over an
	// unknown legacy node, but the legacy node remains eligible when nothing
	// known is available.
	if id, err := selectDeploymentTarget([]NodeView{legacy, llama}, "master", false, 0, false, ""); err != nil || id != "llama" {
		t.Fatalf("auto selected %q, %v; want known llama node", id, err)
	}
	legacyOnly := placementNode("legacy", 24<<30)
	if id, err := selectDeploymentTarget([]NodeView{legacyOnly}, "master", false, 0, false, ""); err != nil || id != "legacy" {
		t.Fatalf("legacy-only auto selected %q, %v", id, err)
	}
}

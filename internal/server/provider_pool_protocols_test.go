package server

import (
	"github.com/opencsgs/csglite/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMissingReplicaDoesNotDisableHealthyDecisionService(t *testing.T) {
	s := newNativeDecisionTestServer(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	s.engines["org/laya"] = &managedEngine{engine: &fakeSystemOneEngine{}, numCtx: 8192, numParallel: 1}
	p := config.ProviderPool{ID: "review", Name: "review", Model: "review-laya", Category: "chat", Enabled: true, Policy: "failover", Members: []config.ProviderPoolMember{{ID: "healthy", Source: "local", Model: "org/laya"}, {ID: "removed", Source: "local", Model: "org/removed-laya"}}}
	for _, disabled := range []bool{false, true} {
		p.Members[1].Disabled = disabled
		if err := config.SaveProviderPools([]config.ProviderPool{p}); err != nil {
			t.Fatal(err)
		}
		body := `{"model":"review-laya","input":"I need a refund","questions":[{"type":"predicate","name":"refund","instructions":"Refund requested?"}]}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/ai-gateway/v1/decisions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		s.routes().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("disabled=%v: %d %s, protocols=%v", disabled, w.Code, w.Body.String(), s.providerPoolView(p).InferenceProtocols)
		}
	}
	// A fixed service still uses its first member, even when that member is missing.
	p.Policy = "fixed"
	p.Members[0], p.Members[1] = p.Members[1], p.Members[0]
	p.Members[0].Disabled = false
	if len(s.providerPoolView(p).InferenceProtocols) != 0 {
		t.Fatal("fixed policy silently selected a different replica")
	}
}

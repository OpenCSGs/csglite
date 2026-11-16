package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestGatewayClusterServicesFollowNodeAvailability(t *testing.T) {
	useIsolatedStorageHome(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	config.ResetProviders()
	t.Cleanup(config.ResetProviders)
	bus := cluster.NewMemoryBus()
	master := newClusterTestServer(t, bus)
	worker := newClusterTestServer(t, bus)
	mustSaveLocalModel(t, worker.cfg.ModelDir, &model.LocalModel{Namespace: "acme", Name: "jev", Format: model.FormatGGUF, Files: []string{"model.gguf"}, DownloadedAt: time.Now()})
	id := worker.localInferenceModelID("acme/jev")
	_, token, err := master.cluster.CreateCluster("availability")
	if err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "worker discovery", func() bool { return len(worker.cluster.DiscoveredNodes()) == 1 })
	if _, err := worker.cluster.Join(context.Background(), token, ""); err != nil {
		t.Fatal(err)
	}
	uuid := worker.cluster.Identity().UUID
	clusterWait(t, "worker model telemetry", func() bool {
		rt, ok := master.cluster.Directory().Get(uuid)
		if !ok || rt.Status == nil {
			return false
		}
		_, has := rt.Status.Model(id)
		return rt.Online() && has
	})
	// Freeze background polling so state transitions below are deterministic.
	runtime, _ := master.cluster.Directory().Get(uuid)
	status := *runtime.Status
	master.cluster.Stop()
	worker.cluster.Stop()
	status.Licensed = true
	status.State = cluster.NodeStateActive
	status.AcceptWork = true
	master.cluster.Directory().MarkSuccess(uuid, "127.0.0.1:11438", &status)
	member := config.ProviderPoolMember{ID: "worker", Source: "node:" + uuid, Model: id}
	backup := config.ProviderPoolMember{ID: "backup", Source: "provider:backup", Model: "fallback-model"}
	if err := config.SaveProviders([]config.ThirdPartyProvider{{ID: "backup", Name: "Backup", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	pools := []config.ProviderPool{
		{ID: "fixed", Name: "JEV", Model: "published-jev", Enabled: true, Policy: "fixed", Members: []config.ProviderPoolMember{member}},
		{ID: "failover", Name: "Failover", Model: "fallback", Enabled: true, Policy: "failover", Members: []config.ProviderPoolMember{member, backup}},
		{ID: "fixed-first", Name: "Fixed", Model: "fixed-first", Enabled: true, Policy: "fixed", Members: []config.ProviderPoolMember{member, backup}},
	}
	if err := config.SaveProviderPools(pools); err != nil {
		t.Fatal(err)
	}
	listed := func() []string {
		t.Helper()
		w := httptest.NewRecorder()
		master.handleGatewayModels(w, httptest.NewRequest("GET", "/ai-gateway/v1/models", nil))
		var result api.OpenAIModelList
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, m := range result.Data {
			ids = append(ids, m.ID)
		}
		return ids
	}
	if got := listed(); len(got) != 3 {
		t.Fatalf("online models: %v", got)
	}
	rt, _ := master.cluster.Directory().Get(uuid)
	for i := 0; i < 6; i++ {
		master.cluster.Directory().MarkFailure(uuid, errors.New("offline"))
	}
	if got := master.providerPoolView(pools[0]).SourceStates[member.ID]; got != "offline" {
		t.Fatalf("offline source state: %s", got)
	}
	if got := listed(); len(got) != 1 || got[0] != "fallback" {
		t.Fatalf("offline model leaked or live fallback hidden: %v", got)
	}
	catalog, err := master.listAvailableModelsForProvider(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog {
		if model.Source == poolSource("fixed") || model.Source == poolSource("fixed-first") {
			t.Fatalf("offline service leaked into application model choices: %+v", model)
		}
	}
	// Management retains the configuration so recovery does not require republishing.
	w := httptest.NewRecorder()
	master.handleProviderPoolsList(w, httptest.NewRequest("GET", "/api/provider-pools", nil))
	var configured api.ProviderPoolsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &configured); err != nil {
		t.Fatal(err)
	}
	if len(configured.Pools) != 3 {
		t.Fatal("offline service configuration disappeared")
	}
	for _, path := range []string{"chat/completions", "responses", "messages", "embeddings", "decisions", "images/generations", "images/edits", "audio/transcriptions", "audio/speech"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/ai-gateway/v1/"+path, strings.NewReader(`{"model":"published-jev"}`))
		req.Header.Set("Content-Type", "application/json")
		master.withGatewayProtocol(func(http.ResponseWriter, *http.Request) { t.Error("offline source reached inference") }, false)(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	master.cluster.Directory().MarkSuccess(uuid, "127.0.0.1:11438", rt.Status)
	if got := listed(); len(got) != 3 {
		t.Fatalf("recovered services did not return: %v", got)
	}
	if got := master.providerPoolView(pools[0]).SourceStates[member.ID]; got != "configured" {
		t.Fatalf("recovered source state: %s", got)
	}
}

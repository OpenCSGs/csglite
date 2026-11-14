package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestCloudProviderToggleRetainsConfigurationAndBlocksInference(t *testing.T) {
	s := newTestServer(t)
	requests, modelID := 0, "fixture/model"
	upstream := newCloudModelListServer(&requests, &modelID)
	defer upstream.Close()
	s.cloud = cloud.NewService(upstream.URL)
	s.cfg.Token = "fixture-token"
	s.cfg.OpenCSGAPIKey = "fixture-key"
	if err := config.ReplaceProviderModelAllowlist(config.DefaultCloudProviderName, []string{modelID}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	models, err := s.listCloudModels(ctx, false)
	if err != nil || len(models) != 1 {
		t.Fatalf("default enabled models = %v, %v", models, err)
	}

	setEnabled := func(body string, want bool) {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleSettingsUpdate(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("settings: %d %s", w.Code, w.Body.String())
		}
		var response api.SettingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.CloudProviderEnabled != want {
			t.Fatal("incorrect settings response")
		}
	}
	setEnabled(`{"cloud_provider_enabled":false}`, false)
	// Legacy settings patches must not silently re-enable the provider.
	setEnabled(`{"cloud_provider_name":"Example Hub"}`, false)
	home, err := config.AppHome()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, config.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	var saved config.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.IsCloudProviderEnabled() || saved.Token != "fixture-token" || saved.OpenCSGAPIKey != "fixture-key" {
		t.Fatal("toggle was not persisted or credentials changed")
	}

	before := requests
	models, err = s.listCloudModels(ctx, true)
	if err != nil || len(models) != 0 {
		t.Fatalf("disabled models = %v, %v", models, err)
	}
	providers, err := s.listModelProviders(ctx, true, "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if p.Source == "cloud" {
			t.Fatal("disabled provider advertised")
		}
	}
	if s.cloudModelListContainsMatching(ctx, modelID, func(api.ModelInfo) bool { return true }) {
		t.Fatal("disabled model found by inference fallback")
	}
	if requests != before {
		t.Fatal("disabled inference discovery contacted upstream")
	}
	if _, err := s.newCloudEngine(ctx, modelID); inference.HTTPStatusCode(err) != http.StatusForbidden {
		t.Fatalf("cloud engine error = %v", err)
	}
	for _, id := range []string{"csghub", "cloud"} {
		r := httptest.NewRequest(http.MethodPost, "/providers/"+id+"/v1/chat/completions", nil)
		r.SetPathValue("providerID", id)
		w := httptest.NewRecorder()
		s.withProviderRouteSource(func(http.ResponseWriter, *http.Request) { t.Fatal("disabled route dispatched") })(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("route %s: %d", id, w.Code)
		}
	}
	setEnabled(`{"cloud_provider_enabled":true}`, true)
	models, err = s.listCloudModels(ctx, false)
	if err != nil || len(models) != 1 || models[0].Model != modelID {
		t.Fatalf("restored models = %v, %v", models, err)
	}
	if len(config.GetProviderModelSelections(config.DefaultCloudProviderName)) != 1 {
		t.Fatal("model selections lost")
	}
	if _, err := s.newCloudEngine(ctx, modelID); err != nil {
		t.Fatalf("re-enabled engine: %v", err)
	}
}

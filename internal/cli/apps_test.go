package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestAppsCmdSubcommands(t *testing.T) {
	cmd := NewRootCmd("test")
	appsCmd, _, err := cmd.Find([]string{"apps"})
	if err != nil {
		t.Fatalf("Find(apps) error: %v", err)
	}

	subcommands := make(map[string]bool)
	for _, sub := range appsCmd.Commands() {
		subcommands[sub.Name()] = true
	}
	for _, name := range []string{"list", "install", "uninstall"} {
		if !subcommands[name] {
			t.Fatalf("apps %s subcommand not found", name)
		}
	}
}

func TestAppsUninstallCmdRequiresApp(t *testing.T) {
	cmd := NewRootCmd("test")
	cmd.SetArgs([]string{"apps", "uninstall"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when app id is missing")
	}
}

func TestRequestAIAppUninstallPostsAppID(t *testing.T) {
	var gotPath string
	var gotAppID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req api.AIAppUninstallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotAppID = req.AppID
		_ = json.NewEncoder(w).Encode(api.AIAppInfo{
			ID:        req.AppID,
			Installed: true,
			Managed:   true,
			Status:    "uninstalling",
		})
	}))
	defer server.Close()

	info, err := requestAIAppUninstall(server.URL, "pi")
	if err != nil {
		t.Fatalf("requestAIAppUninstall returned error: %v", err)
	}
	if gotPath != "/api/apps/uninstall" {
		t.Fatalf("path = %q, want uninstall endpoint", gotPath)
	}
	if gotAppID != "pi" {
		t.Fatalf("app_id = %q, want pi", gotAppID)
	}
	if info.ID != "pi" || info.Status != "uninstalling" {
		t.Fatalf("unexpected response info: %#v", info)
	}
}

func TestRenderAIAppActionLine(t *testing.T) {
	got := renderAIAppActionLine("uninstall", api.AIAppInfo{
		ID:           "pi",
		Phase:        "removing",
		ProgressMode: "percent",
		Progress:     42,
	})
	if !strings.Contains(got, "Uninstalling pi: removing (42%)") {
		t.Fatalf("renderAIAppActionLine = %q", got)
	}
}

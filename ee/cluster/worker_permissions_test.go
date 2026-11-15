// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPublicWorkerCannotManageCluster(t *testing.T) {
	h := &deploymentTestHost{fakeHost: &fakeHost{name: "worker", limit: 2}, running: map[string]bool{}}
	m, err := New(Options{Dir: t.TempDir(), Host: h, Mode: "worker", GatewayURL: "https://gateway.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	master := uuid.NewString()
	if _, _, err := m.store.create("worker", "public", master); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.Upsert(Member{UUID: master, Name: "Master", CertFingerprint: "test"}, true); err != nil {
		t.Fatal(err)
	}
	admin := m.Admin()
	for name, handler := range map[string]http.HandlerFunc{
		"create": admin.HandleCreate, "invite": admin.HandleInvite, "rotate": admin.HandleTokenRotate,
		"admission-code": admin.HandleCode, "remove-member": admin.HandleNodeRemove,
		"edit-other-node": admin.HandleNodeUpdate, "sync-model": admin.HandleModelSync,
		"pull-on-node": admin.HandleNodePull, "create-deployment": admin.HandleDeploymentCreate,
		"update-deployment": admin.HandleDeploymentUpdate, "manage-model": admin.HandleModelAction,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/cluster", strings.NewReader(`{}`))
			req.SetPathValue("uuid", master)
			req.SetPathValue("id", uuid.NewString())
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "master_required") {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
	if _, err := m.Invite(context.Background(), uuid.NewString(), "123456", "127.0.0.1:1"); err == nil {
		t.Fatal("worker invited a peer")
	}
	if err := m.RemoveMember(context.Background(), master); err == nil {
		t.Fatal("worker removed Master")
	}
	if _, ok := m.store.Member(master); !ok {
		t.Fatal("forbidden request changed membership")
	}
	if _, err := m.changeDeployment(uuid.NewString(), "running"); err == nil {
		t.Fatal("worker changed a deployment")
	}
	d := testDeployment(m)
	if _, err := m.receiveDeployment(m.identity.UUID, d); err == nil {
		t.Fatal("worker authorized itself as controller")
	}
	for _, body := range []string{`{"routing_mode":"balanced"}`, `{"affinity_max_queue":999}`} {
		req := httptest.NewRequest(http.MethodPut, "/api/cluster/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		admin.HandleSettingsUpdate(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("worker routing edit: %d %s", rec.Code, rec.Body)
		}
	}
	// Local maintenance and explicit authorization stay in the owner's hands.
	req := httptest.NewRequest(http.MethodPut, "/api/cluster/settings", strings.NewReader(`{"accept_work":false,"managed_by":"`+master+`"}`))
	rec := httptest.NewRecorder()
	admin.HandleSettingsUpdate(rec, req)
	if rec.Code != http.StatusOK || m.store.Settings().AcceptWork || m.effectiveSettings().ManagedBy != master || m.store.Settings().ManagedBy != "" {
		t.Fatalf("local settings: %d %s", rec.Code, rec.Body)
	}
	req = httptest.NewRequest(http.MethodPut, "/api/cluster/nodes/self", strings.NewReader(`{"name":"renamed-worker"}`))
	req.SetPathValue("uuid", m.identity.UUID)
	rec = httptest.NewRecorder()
	admin.HandleNodeUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local rename: %d %s", rec.Code, rec.Body)
	}
}

func TestWorkerControlComesFromMembership(t *testing.T) {
	for _, legacy := range []string{"absent", "master", "other"} {
		t.Run(legacy, func(t *testing.T) {
			dir := t.TempDir()
			master, other := uuid.NewString(), uuid.NewString()
			st, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = st.create("public", "public", master); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{master, other} {
				if _, err = st.Upsert(Member{UUID: id, Name: id, CertFingerprint: "test"}, true); err != nil {
					t.Fatal(err)
				}
			}
			// Seed the on-disk shape written by previous optional-authorization builds.
			if legacy != "absent" {
				st.data.Settings.ManagedBy = master
				if legacy == "other" {
					st.data.Settings.ManagedBy = other
				}
				raw, _ := json.Marshal(st.data)
				if err = os.WriteFile(filepath.Join(dir, clusterFile), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			host := &deploymentTestHost{fakeHost: &fakeHost{name: "worker", limit: 3}, running: map[string]bool{}}
			m, err := New(Options{Dir: dir, Host: host, Mode: "worker", GatewayURL: "https://master.example.test"})
			if err != nil {
				t.Fatal(err)
			}
			if !m.deploymentAuthorized(master) || m.deploymentAuthorized(other) || m.deploymentAuthorized(m.identity.UUID) {
				t.Fatal("only the joined Master should control deployments")
			}
			if m.View(context.Background()).Settings.ManagedBy != master || m.LocalStatus(context.Background()).ManagedBy != master {
				t.Fatal("UI and heartbeat disagree with membership")
			}
			for _, value := range []string{"", master} {
				req := httptest.NewRequest(http.MethodPut, "/api/cluster/settings", strings.NewReader(`{"managed_by":"`+value+`"}`))
				rec := httptest.NewRecorder()
				m.Admin().HandleSettingsUpdate(rec, req)
				var settings Settings
				if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil || rec.Code != 200 || settings.ManagedBy != master || !m.deploymentAuthorized(master) {
					t.Fatalf("legacy request changed controller: %d %s", rec.Code, rec.Body.String())
				}
			}
			req := httptest.NewRequest(http.MethodPut, "/api/cluster/settings", strings.NewReader(`{"managed_by":"`+other+`"}`))
			rec := httptest.NewRecorder()
			m.Admin().HandleSettingsUpdate(rec, req)
			if rec.Code != 400 || !m.deploymentAuthorized(master) {
				t.Fatal("worker could select a different Master")
			}
			raw, err := os.ReadFile(filepath.Join(dir, clusterFile))
			if err != nil || strings.Contains(string(raw), `"managed_by"`) {
				t.Fatal("derived public controller was persisted", err)
			}
			restarted, err := New(Options{Dir: dir, Host: host, Mode: "worker", GatewayURL: "https://master.example.test"})
			if err != nil || !restarted.deploymentAuthorized(master) {
				t.Fatal("restart lost membership control", err)
			}
			if err := restarted.store.Leave(); err != nil {
				t.Fatal(err)
			}
			if restarted.deploymentAuthorized(master) || restarted.effectiveSettings().ManagedBy != "" {
				t.Fatal("leaving retained Master control")
			}
		})
	}
}

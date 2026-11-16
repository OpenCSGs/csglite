// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type leaveDeploymentHost struct {
	*deploymentTestHost
	loadStarted  chan struct{}
	loadCanceled chan struct{}
	loadRelease  chan struct{}
	unloads      atomic.Int32
	unloadFail   atomic.Bool
	drainBusy    atomic.Bool
}

func (h *leaveDeploymentHost) StartDeployment(ctx context.Context, d Deployment, report func(DeploymentStatus)) error {
	if h.loadStarted != nil {
		close(h.loadStarted)
		<-ctx.Done()
		close(h.loadCanceled)
		<-h.loadRelease
		// A synchronous runtime loader can finish after cancellation. The worker
		// must close that late runtime before reporting that leave has completed.
		return h.deploymentTestHost.StartDeployment(context.Background(), d, report)
	}
	return h.deploymentTestHost.StartDeployment(ctx, d, report)
}
func (h *leaveDeploymentHost) StopDeployment(d Deployment) error {
	if h.drainBusy.Load() && h.DeploymentRunning(d) {
		return errors.New("active request is still draining")
	}
	return h.deploymentTestHost.StopDeployment(d)
}
func (h *leaveDeploymentHost) UnloadDeployment(d Deployment) error {
	h.unloads.Add(1)
	if h.unloadFail.Load() {
		return errors.New("temporary close failure")
	}
	return h.deploymentTestHost.StopDeployment(d)
}

func leaveDeploymentNode(t *testing.T, h *leaveDeploymentHost) (*Manager, Deployment) {
	t.Helper()
	m, err := New(Options{Dir: t.TempDir(), Host: h, Discoverer: NopDiscoverer{}, ListenAddr: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	if _, _, err := m.CreateCluster("leave-test"); err != nil {
		t.Fatal(err)
	}
	d := testDeployment(m)
	if _, err := m.receiveDeployment(d.ControllerUUID, d); err != nil {
		t.Fatal(err)
	}
	return m, d
}
func newLeaveHost() *leaveDeploymentHost {
	h := &leaveDeploymentHost{deploymentTestHost: &deploymentTestHost{fakeHost: &fakeHost{name: "worker", limit: 0}, running: map[string]bool{}}}
	h.drainBusy.Store(true)
	return h
}

func TestLeaveUnloadsBusyDeploymentBeforeHTTPResponse(t *testing.T) {
	h := newLeaveHost()
	m, d := leaveDeploymentNode(t, h)
	waitDeployment(t, m, d.ID, "ready")
	if err := m.stopDeployment(h, d); err == nil {
		t.Fatal("ordinary stop must drain")
	}
	rec := httptest.NewRecorder()
	m.Admin().HandleLeave(rec, httptest.NewRequest(http.MethodDelete, "/api/cluster", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("leave: %d %s", rec.Code, rec.Body)
	}
	if h.DeploymentRunning(d) || m.DeploymentReady(d.ModelID()) || m.store.InCluster() {
		t.Fatal("leave returned before managed runtime cleanup")
	}
	if h.unloads.Load() == 0 || h.deletes.Load() != 0 {
		t.Fatal("leave must unload without deleting model files")
	}
}

func TestLeaveCancelsDeploymentDownload(t *testing.T) {
	h := newLeaveHost()
	h.block = make(chan struct{})
	m, d := leaveDeploymentNode(t, h)
	waitPublic(t, func() bool { return h.prepares.Load() > 0 })
	if err := m.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.starts.Load() != 0 || h.DeploymentRunning(d) {
		t.Fatal("cancelled download started a runtime")
	}
}

func TestLeaveWaitsForLateLoaderAndCleansAfterCallerCancellation(t *testing.T) {
	for _, cancelCaller := range []bool{false, true} {
		t.Run(map[bool]string{false: "wait", true: "caller-cancelled"}[cancelCaller], func(t *testing.T) {
			h := newLeaveHost()
			h.loadStarted = make(chan struct{})
			h.loadCanceled = make(chan struct{})
			h.loadRelease = make(chan struct{})
			m, d := leaveDeploymentNode(t, h)
			// Registered after the manager cleanup so a failed assertion cannot leave
			// Stop waiting forever on this deliberately uninterruptible test loader.
			t.Cleanup(func() {
				select {
				case <-h.loadRelease:
				default:
					close(h.loadRelease)
				}
			})
			select {
			case <-h.loadStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("loader did not start")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- m.Leave(ctx) }()
			select {
			case <-h.loadCanceled:
			case <-time.After(5 * time.Second):
				t.Fatal("leave did not cancel loading")
			}
			if cancelCaller {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("leave cancellation: %v", err)
				}
			} else {
				select {
				case err := <-done:
					t.Fatalf("leave returned while loader can publish a runtime: %v", err)
				default:
				}
			}
			close(h.loadRelease)
			if !cancelCaller {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("leave did not complete")
				}
			}
			waitPublic(t, func() bool { return h.starts.Load() == 1 && !h.DeploymentRunning(d) })
			if m.DeploymentReady(d.ModelID()) {
				t.Fatal("late runtime remained available")
			}
		})
	}
}

func TestLeaveRetriesFailedRuntimeCleanup(t *testing.T) {
	h := newLeaveHost()
	m, d := leaveDeploymentNode(t, h)
	waitDeployment(t, m, d.ID, "ready")
	h.unloadFail.Store(true)
	done := make(chan error, 1)
	go func() { done <- m.Leave(context.Background()) }()
	waitPublic(t, func() bool { return h.unloads.Load() > 0 })
	select {
	case err := <-done:
		t.Fatalf("leave reported completion after failed close: %v", err)
	default:
	}
	h.unloadFail.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup was not retried")
	}
	if h.DeploymentRunning(d) {
		t.Fatal("runtime survived cleanup retry")
	}
}

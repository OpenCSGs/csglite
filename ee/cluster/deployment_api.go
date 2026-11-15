// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	publictunnel "github.com/opencsgs/csglite/ee/cluster/public"
)

const deploymentPeerPath = publictunnel.DeploymentPath

func (m *Manager) deploymentHandler(controller string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var d Deployment
		if err := decodePeerJSON(r, &d, 1<<20); err != nil {
			writeError(w, 400, "invalid deployment assignment")
			return
		}
		result, err := m.receiveDeployment(controller, d)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, result)
	})
}

func (m *Manager) sendDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	if d.NodeUUID == m.identity.UUID {
		return m.receiveDeployment(m.identity.UUID, d)
	}
	mem, ok := m.store.Member(d.NodeUUID)
	if !ok {
		return Deployment{}, errors.New("target node is no longer a member")
	}
	st := m.candidateStatus(d.NodeUUID)
	if st == nil || !st.DeploymentCapable || st.ManagedBy != m.identity.UUID {
		return Deployment{}, errors.New("target node has not authorized this controller")
	}
	var result Deployment
	if m.opts.Mode == "gateway" {
		g := m.publicHub()
		if g == nil {
			return Deployment{}, publictunnel.ErrOffline
		}
		data, _ := json.Marshal(d)
		resp, err := g.AssignDeployment(ctx, d.NodeUUID, data)
		if err != nil {
			return Deployment{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return Deployment{}, err
		}
		if resp.StatusCode != 200 {
			return Deployment{}, peerErrorFrom(resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return Deployment{}, err
		}
	} else {
		addresses := m.dir.Candidates(mem.UUID)
		if len(addresses) == 0 {
			return Deployment{}, errors.New("node is offline")
		}
		if err := m.peerJSON(ctx, mem, addresses[0], http.MethodPost, deploymentPeerPath, d, &result); err != nil {
			return Deployment{}, err
		}
	}
	if result.ID != d.ID || result.ControllerUUID != d.ControllerUUID || result.Generation != d.Generation || result.DeploymentSpec != d.DeploymentSpec || result.Desired != d.Desired {
		return Deployment{}, errors.New("invalid deployment receipt")
	}
	return result, nil
}

func (m *Manager) deploymentControllerLoop(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		if m.Active() && m.opts.Mode != "worker" && m.store.InCluster() {
			for _, d := range m.deployments.list(true) {
				if d.Desired == "deleted" && d.Status.Phase == "deleted" {
					continue
				}
				if ctx.Err() != nil {
					return
				}
				attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
				result, err := m.sendDeployment(attempt, d)
				cancel()
				st := result.Status
				if err != nil {
					st = DeploymentStatus{Phase: "unreachable", Error: err.Error(), UpdatedAt: time.Now().UTC()}
				}
				if saveErr := m.deployments.update(true, d.ID, func(current *Deployment, exists bool) error {
					if !exists || current.Generation != d.Generation {
						return nil
					}
					current.Status = st
					return nil
				}); saveErr != nil {
					m.logf("cluster: persist deployment receipt: %v", saveErr)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (a *adminAPI) HandleDeployments(w http.ResponseWriter, r *http.Request) {
	owned := a.m.deployments.list(true)
	assigned := a.m.deployments.list(false)
	if value := r.URL.Query().Get("include_deleted"); value != "" {
		include, err := strconv.ParseBool(value)
		if err != nil {
			writeError(w, 400, "include_deleted must be a boolean")
			return
		}
		if !include {
			owned = visibleDeployments(owned)
			assigned = visibleDeployments(assigned)
		}
	}
	writeJSON(w, 200, map[string]any{"deployments": owned, "assignments": assigned})
}

func visibleDeployments(all []Deployment) []Deployment {
	visible := make([]Deployment, 0, len(all))
	for _, d := range all {
		if d.Desired != "deleted" || d.Status.Phase != "deleted" {
			visible = append(visible, d)
		}
	}
	return visible
}

// HandleDeploymentDelete uses the same durable reconciliation as legacy PUT.
func (a *adminAPI) HandleDeploymentDelete(w http.ResponseWriter, r *http.Request) {
	if !a.requireController(w) {
		return
	}
	id := r.PathValue("id")
	if _, exists := a.m.deployments.get(true, id); !exists {
		writeError(w, 404, "deployment not found")
		return
	}
	d, err := a.m.changeDeployment(id, "deleted")
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

func (a *adminAPI) HandleDeploymentCreate(w http.ResponseWriter, r *http.Request) {
	if !a.requireController(w) {
		return
	}
	spec := DeploymentSpec{ArtifactSource: "opencsg", Revision: "main", Kind: "chat", NumCtx: 4096, NumParallel: 1, NGPULayers: -1}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		writeError(w, 400, "invalid deployment specification")
		return
	}
	spec.Name = strings.TrimSpace(spec.Name)
	d, err := a.m.createDeployment(spec)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

func (a *adminAPI) HandleDeploymentUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.requireController(w) {
		return
	}
	var body struct {
		Desired string `json:"desired"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, "invalid desired state")
		return
	}
	id := r.PathValue("id")
	if _, exists := a.m.deployments.get(true, id); !exists {
		writeError(w, 404, "deployment not found")
		return
	}
	d, err := a.m.changeDeployment(id, body.Desired)
	if err != nil {
		writeError(w, 400, fmt.Sprint(err))
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

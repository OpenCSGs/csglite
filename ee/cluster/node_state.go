// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	publictunnel "github.com/opencsgs/csglite/ee/cluster/public"
)

type nodeStateRequest struct {
	State NodeState `json:"state"`
}

type nodeStateResult struct {
	Settings Settings `json:"settings"`
	Status   Status   `json:"status"`
}

func validNodeState(state NodeState) bool {
	return state == NodeStateActive || state == NodeStateDrain || state == NodeStateMaintenance
}

func (m *Manager) nodeStateHandler(controller string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.deploymentAuthorized(controller) {
			writeError(w, http.StatusForbidden, "node has not authorized this controller")
			return
		}
		var req nodeStateRequest
		if err := decodePeerJSON(r, &req, 1024); err != nil || !validNodeState(req.State) {
			writeError(w, http.StatusBadRequest, "state must be active, drain or maintenance")
			return
		}
		settings, err := m.UpdateSettings(func(s *Settings) { s.State, s.AcceptWork = req.State, true })
		if err != nil {
			writeOpError(w, err)
			return
		}
		m.wakeGossip()
		status := m.LocalStatus(r.Context())
		if m.opts.Mode == "worker" {
			status = *m.sharedModelStatus(&status)
			status.Net.Addrs = nil
		}
		writeJSON(w, http.StatusOK, nodeStateResult{Settings: settings, Status: status})
	})
}

func (m *Manager) setRemoteNodeState(ctx context.Context, node string, state NodeState) (Settings, error) {
	mem, ok := m.store.Member(node)
	if !ok {
		return Settings{}, errors.New("target node is not a member")
	}
	st := m.candidateStatus(node)
	if st == nil || !st.NodeStateManagement || st.ManagedBy != m.identity.UUID {
		return Settings{}, errors.New("target node must authorize this Master and support node state management")
	}
	request := nodeStateRequest{State: state}
	var result nodeStateResult
	if m.opts.Mode == "gateway" {
		hub := m.publicHub()
		if hub == nil {
			return Settings{}, publictunnel.ErrOffline
		}
		data, _ := json.Marshal(request)
		resp, err := hub.SetNodeState(ctx, node, data)
		if err != nil {
			return Settings{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return Settings{}, err
		}
		if resp.StatusCode != http.StatusOK {
			return Settings{}, peerErrorFrom(resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return Settings{}, err
		}
	} else {
		addrs := m.dir.Candidates(node)
		if len(addrs) == 0 {
			return Settings{}, errors.New("target node is offline")
		}
		if err := m.peerJSON(ctx, mem, addrs[0], http.MethodPost, publictunnel.NodeStatePath, request, &result); err != nil {
			return Settings{}, err
		}
	}
	if result.Status.UUID != node || result.Status.State != state || result.Settings.State != state {
		return Settings{}, errors.New("invalid node state receipt")
	}
	runtime, _ := m.dir.Get(node)
	m.dir.MarkSuccess(node, runtime.Addr, &result.Status)
	return result.Settings, nil
}

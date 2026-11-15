// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	publictunnel "github.com/opencsgs/csglite/ee/cluster/public"
)

type ModelAction struct {
	Model          string `json:"model"`
	Repository     string `json:"repository"`
	ArtifactSource string `json:"artifact_source"`
	Action         string `json:"action"`
}

type ModelActionHost interface {
	ManageLocalModel(context.Context, ModelAction) error
}

func validateModelAction(action ModelAction) error {
	if action.Action != "unload" && action.Action != "delete" {
		return errors.New("action must be unload or delete")
	}
	if strings.TrimSpace(action.Model) == "" || len(action.Model) > 512 || strings.HasPrefix(action.Model, DeploymentModelPrefix) {
		return errors.New("use deployment controls for managed models")
	}
	if action.Repository == "" || len(action.Repository) > 256 {
		return errors.New("repository is required")
	}
	switch action.ArtifactSource {
	case "opencsg", "huggingface", "modelscope":
	default:
		return errors.New("artifact_source is required")
	}
	return nil
}

func (m *Manager) performModelAction(ctx context.Context, controller string, action ModelAction) error {
	if !m.deploymentAuthorized(controller) {
		return errors.New("node has not authorized this controller")
	}
	if err := validateModelAction(action); err != nil {
		return err
	}
	host, ok := m.opts.Host.(ModelActionHost)
	if !ok {
		return errors.New("upgrade node to support remote model management")
	}
	if err := host.ManageLocalModel(ctx, action); err != nil {
		return err
	}
	m.invalidateLocalStatus()
	m.wakeGossip()
	return nil
}

func (m *Manager) modelActionHandler(controller string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var action ModelAction
		if err := decodePeerJSON(r, &action, 4096); err != nil {
			writeError(w, 400, "invalid model action")
			return
		}
		if err := m.performModelAction(r.Context(), controller, action); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"status": "completed"})
	})
}

func (m *Manager) sendModelAction(ctx context.Context, node string, action ModelAction) error {
	if m.opts.Mode == "worker" {
		return errors.New("manage models on the Master")
	}
	if err := validateModelAction(action); err != nil {
		return err
	}
	if node == m.identity.UUID {
		return m.performModelAction(ctx, node, action)
	}
	mem, ok := m.store.Member(node)
	if !ok {
		return errors.New("target node is not a member")
	}
	st := m.candidateStatus(node)
	if st == nil || !st.ModelManagement || st.ManagedBy != m.identity.UUID {
		return errors.New("target node must authorize this Master and support model management")
	}
	if m.opts.Mode == "gateway" {
		hub := m.publicHub()
		if hub == nil {
			return publictunnel.ErrOffline
		}
		data, _ := json.Marshal(action)
		resp, err := hub.ManageModel(ctx, node, data)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			return peerErrorFrom(resp.StatusCode, body)
		}
		return nil
	}
	addresses := m.dir.Candidates(node)
	if len(addresses) == 0 {
		return errors.New("node is offline")
	}
	return m.peerJSON(ctx, mem, addresses[0], http.MethodPost, publictunnel.ModelActionPath, action, nil)
}

func (a *adminAPI) HandleModelAction(w http.ResponseWriter, r *http.Request) {
	if !a.requireController(w) {
		return
	}
	var action ModelAction
	if err := decodePeerJSON(r, &action, 4096); err != nil {
		writeError(w, 400, "invalid model action")
		return
	}
	if err := validateModelAction(action); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := a.m.sendModelAction(ctx, r.PathValue("uuid"), action); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "completed"})
}

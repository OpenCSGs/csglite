package server

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/inference"
)

// Preserve the cluster's machine-readable details across protocol envelopes.
// Workers may return a JSON error body over the tunnel; never nest it as text.
func addClusterInferenceErrorDetails(payload map[string]any, err error) {
	message := strings.TrimSpace(inference.HTTPErrorMessage(err))
	var noNode *cluster.ErrNoNode
	code := ""
	var tried []string
	if errors.As(err, &noNode) {
		code = cluster.ErrNoNodeCode
		tried = append([]string{}, noNode.Tried...)
	} else {
		var remote struct {
			Code  string   `json:"code"`
			Tried []string `json:"tried"`
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(message), &remote) == nil {
			if remote.Code == "" {
				remote.Code = remote.Error.Code
			}
			if strings.HasPrefix(remote.Code, "cluster_") {
				code, tried = remote.Code, remote.Tried
				message = strings.TrimSpace(remote.Error.Message)
			}
		} else if inference.HTTPStatusCode(err) == 403 && message == "model is not shared by this node" {
			// Compatibility with older workers' plain-text http.Error response.
			code = "cluster_model_not_shared"
		}
	}
	if object, ok := payload["error"].(map[string]any); ok {
		object["message"] = message
		if code != "" {
			object["code"] = code
		}
	}
	if code != "" {
		payload["code"] = code
	}
	if tried != nil {
		payload["tried"] = tried
	}
}

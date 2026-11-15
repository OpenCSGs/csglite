package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
)

func TestInferenceErrorsRetainClusterDetailsAcrossProtocols(t *testing.T) {
	for _, render := range []func(http.ResponseWriter, error){writeOpenAIInferenceError, writeAnthropicInferenceError} {
		for _, tc := range []struct {
			err        error
			status     int
			code, kind string
		}{
			{inference.NewHTTPStatusErrorWithCause(503, &cluster.ErrNoNode{Model: "m", Tried: []string{"node-1"}}), 503, cluster.ErrNoNodeCode, ""},
			{inference.NewHTTPStatusError(403, `{"code":"cluster_model_not_shared","error":{"message":"model is not shared by this node\n","type":"permission_error"}}`), 403, "cluster_model_not_shared", "permission_error"},
			{inference.NewHTTPStatusError(403, "model is not shared by this node\n"), 403, "cluster_model_not_shared", "permission_error"},
			{inference.NewHTTPStatusError(401, "key expired"), 401, "", "authentication_error"},
		} {
			w := httptest.NewRecorder()
			render(w, fmt.Errorf("routing failed: %w", tc.err))
			var payload map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			object := payload["error"].(map[string]any)
			if w.Code != tc.status || (tc.code != "" && payload["code"] != tc.code) || (tc.kind != "" && object["type"] != tc.kind) {
				t.Fatalf("error details lost: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(object["message"].(string), "\n") {
				t.Fatalf("untrimmed message: %s", w.Body.String())
			}
			if tc.code == cluster.ErrNoNodeCode && len(payload["tried"].([]any)) != 1 {
				t.Fatalf("tried nodes lost: %s", w.Body.String())
			}
		}
	}
}

func TestPublicGatewayKeepsNoAvailableNodeErrorCode(t *testing.T) {
	s := newClusterTestServer(t, cluster.NewMemoryBus())
	if _, _, err := s.cluster.CreateCluster("empty"); err != nil {
		t.Fatal(err)
	}
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	if err := config.SaveProviderPools([]config.ProviderPool{{ID: "cluster", Name: "Cluster", Model: "published", Category: "chat", Enabled: true, Policy: "fixed", Members: []config.ProviderPoolMember{{ID: "node", Source: "cluster", Model: "missing"}}}}); err != nil {
		t.Fatal(err)
	}
	_, key, err := s.apiKeys.Create("cluster-errors")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/embeddings", "/ai-gateway/v1/chat/completions", "/ai-gateway/v1/messages"} {
		model := "missing"
		if strings.HasPrefix(path, gatewayPrefix) {
			model = "published"
		}
		w := gatewayRequest(s.publicClusterInferenceRoutes(), http.MethodPost, path, `{"model":"`+model+`","messages":[{"role":"user","content":"hello"}],"input":"hello","max_tokens":8}`, key)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"cluster_no_available_node"`) || !strings.Contains(w.Body.String(), `"tried":[]`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func runModelCLI(args []string, stdin string) (string, error) {
	cmd := NewRootCmd("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestModelCLIRequests(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const chatResponse = "{\"choices\":[{\"message\":{\"content\":\"hello\"}}]}"
	tests := []struct {
		name                                  string
		args                                  []string
		stdin, method, target, body, response string
	}{
		{"generate", []string{"generate", "org/name"}, "one\ntwo", "POST", "/ai-gateway/providers/local/v1/chat/completions", `{"model":"org/name","messages":[{"role":"user","content":"one\ntwo"}]}`, chatResponse},
		{"cloud", []string{"generate", "same-id", "hello", "--source", "cloud", "--json"}, "", "POST", "/ai-gateway/providers/cloud/v1/chat/completions", `{"model":"same-id","messages":[{"role":"user","content":"hello"}]}`, chatResponse},
		{"embed", []string{"embed", "same-id", "first", "second", "--source", "cloud"}, "", "POST", "/ai-gateway/providers/cloud/v1/embeddings", `{"model":"same-id","input":["first","second"]}`, ""},
		{"embed stdin", []string{"embed", "same-id"}, "one\ntwo", "POST", "/ai-gateway/providers/local/v1/embeddings", `{"model":"same-id","input":["one\ntwo"]}`, ""},
		{"load", []string{"load", "org/name", "--keep-alive", "-1"}, "", "POST", "/api/load", `{"model":"org/name","keep_alive":"-1"}`, ""},
		{"get config", []string{"model-config", "huggingface/org/name"}, "", "GET", "/api/models/huggingface%2Forg%2Fname/config", "", ""},
		{"patch config", []string{"model-config", "org/name", "--num-ctx", "0", "--backend="}, "", "PUT", "/api/models/org%2Fname/config", `{"num_ctx":0,"backend":""}`, ""},
		{"edge status", []string{"edge", "status"}, "", "GET", "/api/edge", "", ""},
		{"edge enable", []string{"edge", "enable"}, "", "PUT", "/api/edge", `{"enabled":true}`, ""},
		{"edge disable", []string{"edge", "disable"}, "", "PUT", "/api/edge", `{"enabled":false}`, ""},
		{"edge apply", []string{"edge", "apply", "service:test", "--name", "my-model", "--prompt-price", "123", "--completion-price", "0"}, "", "POST", "/api/edge/applications", `{"model_name":"my-model","local_model_id":"service:test","description":"","prompt_price":123,"completion_price":0,"max_concurrency":4}`, ""},
		{"applications", []string{"edge", "applications", "--per", "5", "--page", "2"}, "", "GET", "/api/edge/applications?page=2&per=5", "", ""},
		{"withdraw", []string{"edge", "withdraw", "42"}, "", "POST", "/api/edge/applications/42/withdraw", "", ""},
		{"earnings", []string{"edge", "earnings", "--month", "2026-01"}, "", "GET", "/api/edge/earnings?month=2026-01", "", ""},
		{"balance", []string{"edge", "balance"}, "", "GET", "/api/edge/balance", "", ""},
		{"statements", []string{"edge", "statements"}, "", "GET", "/api/edge/statements", "", ""},
		{"list deployments", []string{"cluster", "deployments", "list"}, "", "GET", "/api/cluster/deployments?include_deleted=false", "", ""},
		{"create deployment", []string{"cluster", "deployments", "create", "my-model", "org/model", "--node", id, "--source", "huggingface", "--revision", "v2"}, "", "POST", "/api/cluster/deployments", `{"name":"my-model","node_uuid":"` + id + `","repository":"org/model","artifact_source":"huggingface","revision":"v2","kind":"chat","num_ctx":4096,"num_parallel":1,"n_gpu_layers":-1}`, ""},
		{"start deployment", []string{"cluster", "deployments", "start", id}, "", "PUT", "/api/cluster/deployments/" + id, `{"desired":"running"}`, ""},
		{"stop deployment", []string{"cluster", "deployments", "stop", id}, "", "PUT", "/api/cluster/deployments/" + id, `{"desired":"stopped"}`, ""},
		{"delete deployment", []string{"cluster", "deployments", "delete", id}, "", "DELETE", "/api/cluster/deployments/" + id, "", ""},
	}
	setupCLIConfigHome(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := tc.response
			if response == "" {
				response = `{"id":9007199254740993}`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.RequestURI() != tc.target {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if tc.body == "" {
					if len(body) > 0 {
						t.Errorf("unexpected body %s", body)
					}
				} else {
					var got, want any
					if json.Unmarshal(body, &got) != nil || json.Unmarshal([]byte(tc.body), &want) != nil || !reflect.DeepEqual(got, want) {
						t.Errorf("body %s, want %s", body, tc.body)
					}
				}
				_, _ = io.WriteString(w, response)
			}))
			defer srv.Close()
			saveListenAddr(t, strings.TrimPrefix(srv.URL, "http://"))
			output, err := runModelCLI(tc.args, tc.stdin)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "generate" {
				if output != "hello\n" {
					t.Errorf("got %q", output)
				}
			} else {
				var compact bytes.Buffer
				if err := json.Compact(&compact, []byte(output)); err != nil || compact.String() != response {
					t.Errorf("output %q, want %s", output, response)
				}
			}
		})
	}
}

func TestModelCLIReportsRefusal(t *testing.T) {
	setupCLIConfigHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model quota exceeded", http.StatusForbidden)
	}))
	defer srv.Close()
	saveListenAddr(t, strings.TrimPrefix(srv.URL, "http://"))
	output, err := runModelCLI([]string{"edge", "apply", "org/test", "--name", "test", "--prompt-price", "0", "--completion-price", "1"}, "")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "model quota exceeded") || output != "" {
		t.Fatalf("output %q, err %v", output, err)
	}
}

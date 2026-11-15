package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

func TestGatewayEchoesPublishedModelIDInJSONAndStreams(t *testing.T) {
	s, key := gatewayFixture(t)
	for _, listener := range []http.Handler{s.routes(), s.publicClusterInferenceRoutes(), s.externalAPIRoutes()} {
		for _, protocol := range []string{"chat/completions", "embeddings", "responses"} {
			for _, stream := range []bool{false, true} {
				if stream && protocol == "embeddings" {
					continue
				}
				model := "published"
				if protocol == "embeddings" {
					model = "vector"
				}
				body := fmt.Sprintf(`{"model":%q,"stream":%v,"input":"hello","messages":[{"role":"user","content":"hello"}]}`, model, stream)
				w := gatewayRequest(listener, http.MethodPost, "/ai-gateway/v1/"+protocol, body, key)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"`+model+`"`) {
					t.Fatalf("%s stream=%v: %d %s", protocol, stream, w.Code, w.Body.String())
				}
				if stream && !strings.Contains(w.Body.String(), "[DONE]") && !strings.Contains(w.Body.String(), "response.completed") {
					t.Fatalf("completion marker lost: %s", w.Body.String())
				}
			}
		}
	}
}

func TestGatewayModelRewritePreservesContentAndFragmentedSSE(t *testing.T) {
	input := `{"model":"runtime","choices":[{"delta":{"content":"model runtime"}}],"provider_extension":{"exact":1234567890123456789}}`
	updated := rewriteResponseModel([]byte(input), "public")
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(updated, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["model"]) != `"public"` || string(payload["provider_extension"]) != `{"exact":1234567890123456789}` || !bytes.Contains(payload["choices"], []byte("model runtime")) {
		t.Fatalf("content or metadata corrupted: %s", updated)
	}
	stream := ": heartbeat\r\nevent: message\r\ndata: " + input + "\r\n\r\ndata: [DONE]"
	var out bytes.Buffer
	if err := copyGatewayModelStream(&out, iotest.OneByteReader(strings.NewReader(stream)), "public"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), ": heartbeat\r\nevent: message\r\n") || !strings.Contains(out.String(), `"model":"public"`) || !strings.HasSuffix(out.String(), "data: [DONE]") {
		t.Fatalf("SSE framing changed: %s", out.String())
	}
}

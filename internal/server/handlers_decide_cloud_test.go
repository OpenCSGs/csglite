package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
)

const cloudDecisionModels = `{"object":"list","data":[
  {"id":"jev-1.13","task":"typed-decisions","display_name":"jev-1.13"},
  {"id":"gpt-4o-mini","task":"text-generation","display_name":"gpt-4o-mini"}
]}`

// fakeSystemOneGateway serves the cloud model list and /v1/systemone so tests
// can exercise the cloud decision path without a real AIGateway.
type fakeSystemOneGateway struct {
	models          string
	modelsStatus    int
	systemOneStatus int
	systemOneBody   string
	systemOneBodies []string
	authHeaders     []string
}

func (g *fakeSystemOneGateway) start(t *testing.T) *httptest.Server {
	t.Helper()
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if g.modelsStatus != 0 {
				w.WriteHeader(g.modelsStatus)
				_, _ = io.WriteString(w, "upstream failed")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, g.models)
		case "/v1/systemone":
			data, _ := io.ReadAll(r.Body)
			g.systemOneBodies = append(g.systemOneBodies, string(data))
			g.authHeaders = append(g.authHeaders, r.Header.Get("Authorization"))
			if g.systemOneStatus != 0 {
				w.WriteHeader(g.systemOneStatus)
				_, _ = io.WriteString(w, "systemone failed")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, g.systemOneBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(apiServer.Close)
	return apiServer
}

func postDecisions(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleDecisions(w, req)
	return w
}

func newCloudDecisionTestServer(t *testing.T, g *fakeSystemOneGateway) *Server {
	t.Helper()
	s := newTestServer(t)
	s.cloud = cloud.NewService(g.start(t).URL)
	s.cfg.OpenCSGAPIKey = "test-key"
	return s
}

func TestHandleDecisionsServesCloudTypedDecisionModels(t *testing.T) {
	g := &fakeSystemOneGateway{
		models:        cloudDecisionModels,
		systemOneBody: `{"answers":{"q000":{"type":"noul","noul":0.9},"q001":{"type":"choice","probabilities":{"billing":0.8,"technical":0.2}}},"usage":{"input_tokens":40}}`,
	}
	s := newCloudDecisionTestServer(t, g)

	body := `{"model":"jev-1.13","input":"I was charged twice.","questions":[
	  {"type":"predicate","name":"refund","instructions":"Is this a refund request?"},
	  {"type":"choice","name":"department","instructions":"Which department?","choices":[{"value":"billing"},{"value":"technical"}]}
	]}`
	w := postDecisions(t, s, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Answers []map[string]any `json:"answers"`
		Model   string           `json:"model"`
		Usage   map[string]any   `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) != 2 || resp.Answers[0]["probability"] != 0.9 || resp.Answers[1]["choice"] != "billing" {
		t.Fatalf("answers = %v", resp.Answers)
	}
	if resp.Model != "jev-1.13" || resp.Usage["input_tokens"] != float64(40) {
		t.Fatalf("response = %s", w.Body.String())
	}
	if len(g.systemOneBodies) != 1 || !strings.Contains(g.systemOneBodies[0], `"model":"jev-1.13"`) ||
		!strings.Contains(g.systemOneBodies[0], `"state":"I was charged twice."`) {
		t.Fatalf("System One bodies = %q", g.systemOneBodies)
	}
	if g.authHeaders[0] != "Bearer test-key" {
		t.Fatalf("Authorization = %q", g.authHeaders[0])
	}
}

func TestHandleDecisionsKeepsLocalModelsBeforeCloud(t *testing.T) {
	g := &fakeSystemOneGateway{models: `{"object":"list","data":[{"id":"org/plain","task":"typed-decisions"}]}`}
	s := newDecisionTestServer(t, false)
	s.cloud = cloud.NewService(g.start(t).URL)
	s.cfg.OpenCSGAPIKey = "test-key"

	w := postDecisions(t, s, `{"model":"org/plain","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not a typed-decision model") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(g.systemOneBodies) != 0 {
		t.Fatalf("local model was routed to the gateway: %q", g.systemOneBodies)
	}
}

func TestHandleDecisionsRejectsCloudChatModels(t *testing.T) {
	g := &fakeSystemOneGateway{models: cloudDecisionModels}
	s := newCloudDecisionTestServer(t, g)

	w := postDecisions(t, s, `{"model":"gpt-4o-mini","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not a typed-decision model") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(g.systemOneBodies) != 0 {
		t.Fatalf("chat model was routed to System One: %q", g.systemOneBodies)
	}
}

func TestHandleDecisionsReportsCloudModelListErrors(t *testing.T) {
	g := &fakeSystemOneGateway{modelsStatus: http.StatusInternalServerError}
	s := newCloudDecisionTestServer(t, g)

	w := postDecisions(t, s, `{"model":"jev-1.13","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "cloud model list returned 500") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestHandleDecisionsRequiresCloudLogin(t *testing.T) {
	g := &fakeSystemOneGateway{models: cloudDecisionModels}
	s := newTestServer(t)
	s.cloud = cloud.NewService(g.start(t).URL)

	w := postDecisions(t, s, `{"model":"jev-1.13","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "Cloud login required") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(g.systemOneBodies) != 0 {
		t.Fatalf("System One was called without credentials: %q", g.systemOneBodies)
	}
}

func TestHandleDecisionsTranslatesOldGateway(t *testing.T) {
	g := &fakeSystemOneGateway{models: cloudDecisionModels, systemOneStatus: http.StatusNotFound}
	s := newCloudDecisionTestServer(t, g)

	w := postDecisions(t, s, `{"model":"jev-1.13","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "newer AIGateway") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"model_not_found"`) {
		t.Fatalf("error type = %s", w.Body.String())
	}
}

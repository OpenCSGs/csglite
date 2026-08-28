package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	routerprofile "github.com/opencsgs/semantic-router"
)

func TestRouterCurationQueueCoalescesByPool(t *testing.T) {
	server := &Server{
		routerProfiles:      &routerprofile.Store{},
		routerCurationState: make(map[string]uint8),
		routerCurationQueue: make(chan string, 1),
	}
	server.enqueueRouterCuration("pool-a")
	server.enqueueRouterCuration("pool-a")
	if len(server.routerCurationQueue) != 1 || server.routerCurationState["pool-a"] != 2 {
		t.Fatalf("queue len=%d state=%d", len(server.routerCurationQueue), server.routerCurationState["pool-a"])
	}
	server.enqueueRouterCuration("pool-b")
	if _, exists := server.routerCurationState["pool-b"]; exists {
		t.Fatal("full bounded queue retained an unqueued pool")
	}
}

func TestProviderPoolSemanticInputUsesMultiTurnRoutingText(t *testing.T) {
	messages := []inference.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: []inference.ContentPart{
			{Type: "image_url"},
			{Type: "text", Text: "latest"},
		}},
		{Role: "tool", Content: "tool output"},
	}
	native := semanticInputFromInference(messages)
	got, _, err := routerprofile.BuildRoutingText(native.messages, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != "User: first\nUser: latest" {
		t.Fatalf("routing text = %q", got)
	}

	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "old"},
			map[string]interface{}{"role": "assistant", "content": "answer"},
			map[string]interface{}{"role": "user", "content": []interface{}{
				map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "ignored"}},
				map[string]interface{}{"type": "text", "text": "new"},
			}},
		},
	}
	proxied := semanticInputFromRequest(body)
	got, _, err = routerprofile.BuildRoutingText(proxied.messages, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != "User: old\nUser: new" {
		t.Fatalf("request routing text = %q", got)
	}

	proxyBody := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "assistant", "content": "old answer"},
			{"role": "user", "content": "proxy user query"},
		},
	}
	got, _, err = routerprofile.BuildRoutingText(semanticInputFromRequest(proxyBody).messages, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != "proxy user query" {
		t.Fatalf("proxy routing text = %q", got)
	}
}

func TestProviderPoolSemanticRouterUsesGatewayEmbedding(t *testing.T) {
	artifact, err := loadProviderPoolSemanticArtifact()
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["model"] != semanticEmbeddingModel || request["input"] != "route this" {
			t.Fatalf("embedding request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{map[string]interface{}{"embedding": artifact.Clusters[0].Center}},
		})
	}))
	defer gateway.Close()

	s := newTestServerWithConfig(t, &config.Config{
		ServerURL:     "https://hub.opencsg.com",
		AIGatewayURL:  gateway.URL,
		OpenCSGAPIKey: "test-key",
	})
	s.cloud = cloud.NewService(gateway.URL)
	target := artifact.Clusters[0].Target
	members := make([]config.ProviderPoolMember, 0)
	seen := make(map[string]struct{})
	for _, cluster := range artifact.Clusters {
		key := semanticTargetKey(cluster.Target.Source, cluster.Target.Model)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		id := "bootstrap-" + cluster.Target.Model
		if key == semanticTargetKey(target.Source, target.Model) {
			id = "target"
		}
		members = append(members, config.ProviderPoolMember{
			ID: id, Source: cluster.Target.Source, Model: cluster.Target.Model,
		})
	}
	pool := config.ProviderPool{
		Policy:  config.ProviderPoolPolicySemantic,
		Members: members,
	}
	route := s.providerPoolSemanticRouter(pool)(t.Context(), semanticInputFromPrompt("route this"))
	if route.MemberID != "target" || route.Cluster != 0 || route.Distance != 0 || !route.Applied {
		t.Fatalf("route = %#v", route)
	}
}

func TestNearestProviderPoolSemanticCluster(t *testing.T) {
	clusters := []providerPoolSemanticCluster{
		{Center: []float64{0, 0}},
		{Center: []float64{1, 1}},
	}
	cluster, distance := nearestProviderPoolSemanticCluster([]float64{0.9, 0.8}, clusters)
	if cluster != 1 || distance < 0.049 || distance > 0.051 {
		t.Fatalf("cluster=%d distance=%f", cluster, distance)
	}
}

func TestLegacySemanticPoolCompatibilityRequiresEveryBootstrapTarget(t *testing.T) {
	artifact, err := loadProviderPoolSemanticArtifact()
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	unique := map[string]config.ProviderPoolMember{}
	for index, cluster := range artifact.Clusters {
		key := semanticTargetKey(cluster.Target.Source, cluster.Target.Model)
		unique[key] = config.ProviderPoolMember{
			ID:     cluster.Target.Model + string(rune('a'+index)),
			Source: cluster.Target.Source,
			Model:  cluster.Target.Model,
		}
	}
	members := make([]config.ProviderPoolMember, 0, len(unique))
	for _, member := range unique {
		members = append(members, member)
	}
	compatible, err := legacySemanticPoolCompatible(members)
	if err != nil || !compatible {
		t.Fatalf("compatible members = %v, err = %v", compatible, err)
	}
	if len(members) > 1 {
		compatible, err = legacySemanticPoolCompatible(members[:1])
		if err != nil || compatible {
			t.Fatalf("incomplete members compatible = %v, err = %v", compatible, err)
		}
	}
}

func TestProviderPoolSemanticRouterFallsBackWhenLegacyProfileIsIncompatible(t *testing.T) {
	s := newTestServer(t)
	pool := config.ProviderPool{
		ID: "arbitrary", Policy: config.ProviderPoolPolicySemantic,
		Members: []config.ProviderPoolMember{{
			ID: "custom", Source: "cloud", Model: "custom/model", Weight: 100,
		}},
	}
	route := s.providerPoolSemanticRouter(pool)(t.Context(), semanticInputFromPrompt("collect this trace"))
	if !route.Fallback || route.FallbackReason != semanticFallbackLegacyIncompatible || route.Applied {
		t.Fatalf("legacy-incompatible route = %+v", route)
	}
	engine := &providerPoolEngine{
		poolID: "arbitrary", members: []providerPoolEngineMember{{member: pool.Members[0]}},
		mu: &sync.Mutex{}, current: map[string]int{}, runtime: map[string]*providerPoolMemberRuntime{},
		semantic: s.providerPoolSemanticRouter(pool),
	}
	ordered := engine.orderedMembers(t.Context(), semanticInputFromPrompt("collect this trace"))
	if len(ordered) != 1 || ordered[0].member.ID != "custom" ||
		!engine.route.Fallback || engine.route.FallbackReason != semanticFallbackLegacyIncompatible {
		t.Fatalf("priority fallback ordering = %+v route=%+v", ordered, engine.route)
	}
}

func TestProviderPoolSemanticOrderingAndFailureFallback(t *testing.T) {
	engine := &providerPoolEngine{
		poolID: "pool",
		members: []providerPoolEngineMember{
			{member: config.ProviderPoolMember{ID: "legacy", Priority: 0, Weight: 100}},
			{member: config.ProviderPoolMember{ID: "semantic", Priority: 1, Weight: 100}},
		},
		mu:       &sync.Mutex{},
		current:  map[string]int{},
		runtime:  map[string]*providerPoolMemberRuntime{},
		affinity: map[string]providerPoolAffinityEntry{},
		semantic: func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
			return routerprofile.Decision{MemberID: "semantic", Cluster: 2, Distance: 0.5}
		},
	}
	ordered := engine.orderedMembers(t.Context(), semanticInputFromPrompt("route me"))
	if ordered[0].member.ID != "semantic" || engine.route.Cluster != 2 {
		t.Fatalf("semantic order = %#v route=%#v", ordered, engine.route)
	}

	engine.semantic = func(context.Context, providerPoolSemanticInput) routerprofile.Decision {
		return routerprofile.Decision{Fallback: true, FallbackReason: routerprofile.FallbackEmbedding}
	}
	ordered = engine.orderedMembers(t.Context(), semanticInputFromPrompt("route me"))
	if ordered[0].member.ID != "legacy" || !engine.route.Fallback {
		t.Fatalf("fallback order = %#v route=%#v", ordered, engine.route)
	}
}

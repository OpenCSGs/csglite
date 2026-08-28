package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	routerprofile "github.com/opencsgs/semantic-router"
)

//go:embed semantic_router_v1.json
var providerPoolSemanticArtifactJSON []byte

type providerPoolSemanticTarget struct {
	Source string `json:"source"`
	Model  string `json:"model"`
}

type providerPoolSemanticCluster struct {
	Center []float64                  `json:"center"`
	Target providerPoolSemanticTarget `json:"target"`
}

type providerPoolSemanticArtifact struct {
	SchemaVersion     int    `json:"schema_version"`
	MatrixFingerprint string `json:"matrix_fingerprint"`
	Embedding         struct {
		Model      string `json:"model"`
		Dimensions int    `json:"dimensions"`
	} `json:"embedding"`
	Distance   string                        `json:"distance"`
	Candidates []providerPoolSemanticTarget  `json:"candidates"`
	Clusters   []providerPoolSemanticCluster `json:"clusters"`
}

var (
	providerPoolSemanticArtifactOnce  sync.Once
	providerPoolSemanticArtifactValue providerPoolSemanticArtifact
	providerPoolSemanticArtifactErr   error
)

func loadProviderPoolSemanticArtifact() (providerPoolSemanticArtifact, error) {
	providerPoolSemanticArtifactOnce.Do(func() {
		if err := json.Unmarshal(providerPoolSemanticArtifactJSON, &providerPoolSemanticArtifactValue); err != nil {
			providerPoolSemanticArtifactErr = fmt.Errorf("decode semantic router artifact: %w", err)
			return
		}
		artifact := providerPoolSemanticArtifactValue
		if artifact.SchemaVersion != 1 {
			providerPoolSemanticArtifactErr = fmt.Errorf("unsupported semantic router schema %d", artifact.SchemaVersion)
			return
		}
		if strings.TrimSpace(artifact.MatrixFingerprint) == "" ||
			artifact.Embedding.Model != semanticEmbeddingModel ||
			artifact.Embedding.Dimensions <= 0 ||
			artifact.Distance != "squared_euclidean" ||
			len(artifact.Clusters) == 0 {
			providerPoolSemanticArtifactErr = fmt.Errorf("semantic router artifact metadata is invalid")
			return
		}
		for index, cluster := range artifact.Clusters {
			if len(cluster.Center) != artifact.Embedding.Dimensions ||
				strings.TrimSpace(cluster.Target.Source) == "" ||
				strings.TrimSpace(cluster.Target.Model) == "" {
				providerPoolSemanticArtifactErr = fmt.Errorf("semantic router cluster %d is invalid", index)
				return
			}
		}
	})
	return providerPoolSemanticArtifactValue, providerPoolSemanticArtifactErr
}

func legacySemanticPoolCompatible(members []config.ProviderPoolMember) (bool, error) {
	artifact, err := loadProviderPoolSemanticArtifact()
	if err != nil {
		return false, err
	}
	available := make(map[string]struct{}, len(members))
	for _, member := range members {
		available[semanticTargetKey(member.Source, member.Model)] = struct{}{}
	}
	required := make(map[string]struct{})
	for _, cluster := range artifact.Clusters {
		required[semanticTargetKey(cluster.Target.Source, cluster.Target.Model)] = struct{}{}
	}
	for key := range required {
		if _, ok := available[key]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func semanticTargetKey(source, model string) string {
	return strings.ToLower(strings.TrimSpace(source)) + "\x00" + strings.TrimSpace(model)
}

func (s *Server) legacyProviderPoolSemanticRouter(pool config.ProviderPool) func(context.Context, string) (routerprofile.Decision, error) {
	if config.NormalizeProviderPoolPolicy(pool.Policy) != config.ProviderPoolPolicySemantic {
		return nil
	}
	artifact, err := loadProviderPoolSemanticArtifact()
	if err != nil {
		return func(context.Context, string) (routerprofile.Decision, error) {
			return routerprofile.Decision{}, err
		}
	}
	memberIDs := make(map[string]string, len(pool.Members))
	for _, member := range pool.Members {
		memberIDs[semanticTargetKey(member.Source, member.Model)] = member.ID
	}
	return func(ctx context.Context, query string) (routerprofile.Decision, error) {
		vector, err := s.providerPoolGatewayEmbeddingModel(ctx, query, artifact.Embedding.Model, artifact.Embedding.Dimensions)
		if err != nil {
			return routerprofile.Decision{}, err
		}
		cluster, distance := nearestProviderPoolSemanticCluster(vector, artifact.Clusters)
		target := artifact.Clusters[cluster].Target
		memberID := memberIDs[semanticTargetKey(target.Source, target.Model)]
		if memberID == "" {
			return routerprofile.Decision{}, fmt.Errorf("semantic target %s:%s is not in the pool", target.Source, target.Model)
		}
		return routerprofile.Decision{
			MemberID:  memberID,
			Cluster:   cluster,
			ClusterID: fmt.Sprintf("%d", cluster),
			Distance:  math.Sqrt(distance),
			Applied:   true,
		}, nil
	}
}

func (s *Server) providerPoolGatewayEmbeddingModel(ctx context.Context, query, model string, dimensions int) ([]float64, error) {
	eng, err := s.newCloudEngine(ctx, model)
	if err != nil {
		return nil, err
	}
	defer eng.Close()
	proxy, ok := eng.(inference.EmbeddingsProxier)
	if !ok {
		return nil, fmt.Errorf("gateway engine does not support embeddings")
	}
	response, err := proxy.Embeddings(ctx, map[string]interface{}{
		"model": model,
		"input": query,
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("gateway embedding response is empty")
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("gateway embedding failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode gateway embedding: %w", err)
	}
	if len(payload.Data) != 1 || len(payload.Data[0].Embedding) != dimensions {
		return nil, fmt.Errorf("gateway embedding dimensions do not match semantic router artifact")
	}
	return payload.Data[0].Embedding, nil
}

func nearestProviderPoolSemanticCluster(vector []float64, clusters []providerPoolSemanticCluster) (int, float64) {
	best, bestDistance := 0, math.Inf(1)
	for index, cluster := range clusters {
		distance := 0.0
		for dimension, value := range vector {
			delta := value - cluster.Center[dimension]
			distance += delta * delta
		}
		if distance < bestDistance {
			best, bestDistance = index, distance
		}
	}
	return best, bestDistance
}

func embeddingConfig(profile routerprofile.Profile) (model string, dimensions int) {
	switch profile.ArtifactSchemaVersion() {
	case routerprofile.SchemaVersionV2:
		if profile.ProfileV2 != nil {
			return profile.ProfileV2.Embedding.Model, profile.ProfileV2.Embedding.Dimensions
		}
		fallthrough
	default:
		return profile.Profile.Embedding.Model, profile.Profile.Embedding.Dimensions
	}
}

func providerPoolSemanticContentText(value interface{}) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case []interface{}:
		parts := make([]string, 0, len(value))
		for _, part := range value {
			if text := providerPoolSemanticContentText(part); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case []inference.ContentPart:
		parts := make([]string, 0, len(value))
		for _, part := range value {
			if strings.EqualFold(part.Type, "text") && strings.TrimSpace(part.Text) != "" {
				parts = append(parts, strings.TrimSpace(part.Text))
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		partType := strings.ToLower(strings.TrimSpace(fmt.Sprint(value["type"])))
		if partType == "" || partType == "text" || partType == "input_text" {
			if text, ok := value["text"].(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

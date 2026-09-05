package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	routerprofile "github.com/opencsgs/semantic-router"
)

// injectBuiltinBenchmark loads the embedded benchmark dataset from
// semantic-router, generates embeddings for each entry, and stores
// them as query snapshots so the evaluation pipeline can use them.
func (s *Server) injectBuiltinBenchmark(ctx context.Context, poolID string) error {
	entries, err := routerprofile.LoadBenchmarkV1()
	if err != nil {
		return fmt.Errorf("loading builtin benchmark: %w", err)
	}
	if len(entries) == 0 {
		return errors.New("builtin benchmark is empty")
	}
	artifact, err := loadProviderPoolSemanticArtifact()
	if err != nil {
		return fmt.Errorf("loading semantic artifact for embedding config: %w", err)
	}
	knownHashes, err := s.routerProfiles.ListQuerySnapshotHashes(ctx, poolID)
	if err != nil {
		return fmt.Errorf("listing existing snapshot hashes: %w", err)
	}
	var snapshots []routerprofile.QuerySnapshot
	for _, entry := range entries {
		if _, exists := knownHashes[entry.SourceHash]; exists {
			continue
		}
		snapshot := routerprofile.BenchmarkEntryAsSnapshot(entry, poolID)
		embedding, embedErr := s.providerPoolGatewayEmbeddingModel(ctx, snapshot.RoutingText, artifact.Embedding.Model, artifact.Embedding.Dimensions)
		if embedErr != nil {
			log.Printf("SEMANTIC ROUTER: builtin benchmark embedding for %s failed: %v", entry.ID, embedErr)
			continue
		}
		snapshot.Embedding = embedding
		snapshot.EmbeddingModel = artifact.Embedding.Model
		snapshots = append(snapshots, snapshot)
	}
	if len(snapshots) == 0 {
		return errors.New("all builtin benchmark entries already exist or embedding failed")
	}
	if saveErr := s.routerProfiles.SaveBenchmark(ctx, poolID, snapshots, nil); saveErr != nil {
		return fmt.Errorf("saving builtin benchmark snapshots: %w", saveErr)
	}
	log.Printf("SEMANTIC ROUTER: injected %d builtin benchmark snapshots into pool %s", len(snapshots), poolID)
	return nil
}

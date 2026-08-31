package server

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	routerprofile "github.com/opencsgs/semantic-router"
)

//go:embed benchmark_v1.jsonl
var benchmarkV1Data []byte

// BenchmarkEntry represents one query from the embedded benchmark dataset.
type BenchmarkEntry struct {
	ID               string `json:"id"`
	Query            string `json:"query"`
	Family           string `json:"family"`
	Source           string `json:"source"`
	SourceHash       string `json:"source_hash"`
	Split            string `json:"split"`
	SplitGroup       string `json:"split_group"`
	ExpectedBehavior string `json:"expected_behavior"`
	Skill            string `json:"skill"`
	SourceDate       string `json:"source_date"`
}

// LoadBenchmarkV1 parses the embedded benchmark_v1.jsonl and returns all entries.
func LoadBenchmarkV1() ([]BenchmarkEntry, error) {
	reader := bufio.NewReader(bytes.NewReader(benchmarkV1Data))
	var entries []BenchmarkEntry
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading benchmark: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}
		var entry BenchmarkEntry
		if jsonErr := json.Unmarshal([]byte(line), &entry); jsonErr != nil {
			return nil, fmt.Errorf("parsing benchmark entry: %w", jsonErr)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// BenchmarkEntryAsSnapshot converts a BenchmarkEntry into a QuerySnapshot
// suitable for the evaluation pipeline. The Embedding field must be filled
// separately by calling the embedding API.
func BenchmarkEntryAsSnapshot(entry BenchmarkEntry, poolID string) routerprofile.QuerySnapshot {
	sourceTime := time.Time{}
	if entry.SourceDate != "" {
		if parsed, err := time.Parse("2006-01-02", entry.SourceDate); err == nil {
			sourceTime = parsed
		}
	}
	return routerprofile.QuerySnapshot{
		PoolID:           poolID,
		ID:               "bmk-" + entry.ID,
		QueryHash:        entry.SourceHash,
		RedactedQuery:    entry.Query,
		RedactedMessages: []routerprofile.Message{{Role: "user", Content: entry.Query}},
		RoutingText:      entry.Query,
		Split:            entry.Split,
		SplitGroup:       entry.SplitGroup,
		SourceTime:       sourceTime,
	}
}

// injectBuiltinBenchmark loads the embedded benchmark dataset, generates
// embeddings for each entry, and stores them as query snapshots so the
// evaluation pipeline can use them as input.
func (s *Server) injectBuiltinBenchmark(ctx context.Context, poolID string) error {
	entries, err := LoadBenchmarkV1()
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
		snapshot := BenchmarkEntryAsSnapshot(entry, poolID)
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

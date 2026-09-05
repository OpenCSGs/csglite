package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	routerprofile "github.com/opencsgs/semantic-router"
)

func (s *Server) enqueueRouterCuration(poolID string) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || s.routerProfiles == nil || s.routerCurationQueue == nil {
		return
	}
	s.routerCurationMu.Lock()
	if state := s.routerCurationState[poolID]; state != 0 {
		s.routerCurationState[poolID] = 2
		s.routerCurationMu.Unlock()
		return
	}
	s.routerCurationState[poolID] = 1
	select {
	case s.routerCurationQueue <- poolID:
		s.routerCurationMu.Unlock()
	default:
		delete(s.routerCurationState, poolID)
		s.routerCurationMu.Unlock()
	}
}

func (s *Server) startRouterCuration(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case poolID := <-s.routerCurationQueue:
			runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := s.curateProviderPool(runCtx, poolID); err != nil && ctx.Err() == nil {
				log.Printf("SEMANTIC ROUTER: curation for pool %s failed: %v", poolID, err)
			}
			cancel()
			s.routerCurationMu.Lock()
			if s.routerCurationState[poolID] == 2 {
				s.routerCurationState[poolID] = 1
				select {
				case s.routerCurationQueue <- poolID:
				default:
					delete(s.routerCurationState, poolID)
				}
			} else {
				delete(s.routerCurationState, poolID)
			}
			s.routerCurationMu.Unlock()
		}
	}
}

func (s *Server) curateProviderPool(ctx context.Context, poolID string) error {
	s.routerStoreMu.RLock()
	defer s.routerStoreMu.RUnlock()
	if s.routerProfiles == nil {
		return nil
	}
	pool, ok := providerPoolByID(poolID)
	if !ok {
		return nil
	}
	if _, _, err := s.routerProfiles.DetectEvaluationOpportunity(ctx, poolID, providerPoolMemberFingerprint(pool)); err != nil {
		return err
	}
	s.observabilityMu.RLock()
	store := s.observability
	if store == nil {
		s.observabilityMu.RUnlock()
		return nil
	}
	records, err := store.ListCompletedPoolRequests(ctx, poolID, 128)
	s.observabilityMu.RUnlock()
	if err != nil {
		return err
	}
	existing, err := s.routerProfiles.ListQuerySnapshots(ctx, poolID, routerprofile.ListOptions{Limit: routerprofile.BenchmarkLimit})
	if err != nil {
		return err
	}
	knownHashes, err := s.routerProfiles.ListQuerySnapshotHashes(ctx, poolID)
	if err != nil {
		return err
	}
	requests := make([]routerprofile.CapturedRequest, 0, len(records))
	for _, record := range records {
		memberID := ""
		for _, member := range pool.Members {
			if strings.EqualFold(strings.TrimSpace(member.Source), strings.TrimSpace(record.Source)) &&
				strings.TrimSpace(member.Model) == strings.TrimSpace(record.MemberModel) {
				memberID = member.ID
				break
			}
		}
		requests = append(requests, routerprofile.CapturedRequest{
			PoolID: poolID, RequestID: record.RequestID, TraceID: record.TraceID,
			Body: record.RequestBody, Status: record.Status, Protocol: record.Protocol,
			Source: record.Source, Model: record.MemberModel, MemberID: memberID,
			CompletedAt: record.CompletedAt, BodyTruncated: record.RequestBodyTruncated,
		})
	}
	artifact, err := loadProviderPoolSemanticArtifact()
	if err != nil {
		return err
	}
	result, err := routerprofile.RefreshBenchmarkWithKnown(existing, requests, knownHashes, semanticEmbeddingModel,
		func(text string) ([]float64, error) {
			return s.providerPoolGatewayEmbeddingModel(ctx, text, artifact.Embedding.Model, artifact.Embedding.Dimensions)
		})
	if err != nil {
		return err
	}
	if result.Eligible > 0 || len(result.Audits) > 0 {
		if err := s.routerProfiles.SaveBenchmark(ctx, poolID, result.Snapshots, result.Audits); err != nil {
			return err
		}
	}
	_, _, err = s.routerProfiles.DetectEvaluationOpportunity(ctx, poolID, providerPoolMemberFingerprint(pool))
	return err
}

func providerPoolMemberFingerprint(pool config.ProviderPool) string {
	members := append([]config.ProviderPoolMember(nil), pool.Members...)
	sort.Slice(members, func(i, j int) bool {
		if members[i].ID != members[j].ID {
			return members[i].ID < members[j].ID
		}
		if members[i].Source != members[j].Source {
			return members[i].Source < members[j].Source
		}
		return members[i].Model < members[j].Model
	})
	payload, _ := json.Marshal(members)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

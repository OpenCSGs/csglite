package server

import (
	"context"
	"fmt"

	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/modelmetadata"
)

func (s *Server) localModelMetadata(storageID, modelDir, manifestPipelineTag string, format model.Format) modelmetadata.Metadata {
	fingerprint, fingerprintErr := modelmetadata.Fingerprint(modelDir)
	if fingerprintErr == nil {
		s.modelMetadataMu.RLock()
		if s.modelMetadata != nil {
			cached, ok, err := s.modelMetadata.Get(context.Background(), storageID, fingerprint)
			s.modelMetadataMu.RUnlock()
			if err == nil && ok {
				return cached
			}
		} else {
			s.modelMetadataMu.RUnlock()
		}
	}

	metadata := modelmetadata.Metadata{
		PipelineTag:   s.resolvedLocalPipelineTag(storageID, manifestPipelineTag),
		HasMMProj:     model.FindMMProj(modelDir) != "",
		ContextWindow: int64(inference.ResolveNumCtxWithModelMax(modelDir, 0, s.cfg.Inference.LlamaUseModelMaxCtx)),
	}
	metadata.MaxModelLen, _ = model.MaxModelLen(modelDir, format)

	if fingerprintErr == nil {
		s.modelMetadataMu.RLock()
		if s.modelMetadata != nil {
			_ = s.modelMetadata.Put(context.Background(), storageID, fingerprint, metadata)
		}
		s.modelMetadataMu.RUnlock()
	}
	return metadata
}

func (s *Server) reopenModelMetadata(storageRoot string) error {
	next, err := modelmetadata.Open(storageRoot)
	if err != nil {
		return fmt.Errorf("opening model metadata cache: %w", err)
	}
	s.modelMetadataMu.Lock()
	previous := s.modelMetadata
	s.modelMetadata = next
	s.modelMetadataMu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

package imagegen

import (
	"context"

	"github.com/opencsgs/csglite/pkg/api"
)

// Engine is the interface for local image generation backends.
type Engine interface {
	Generate(ctx context.Context, req api.OpenAIImagesGenerationRequest) (*api.OpenAIImagesGenerationResponse, error)
	Close() error
	ModelName() string
}

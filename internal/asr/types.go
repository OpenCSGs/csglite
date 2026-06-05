package asr

import (
	"context"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

// Engine is the interface for local automatic speech recognition backends.
type Engine interface {
	Transcribe(ctx context.Context, req api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error)
	Close() error
	ModelName() string
}

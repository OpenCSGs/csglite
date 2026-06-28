package asr

import (
	"context"

	"github.com/opencsgs/csglite/pkg/api"
)

// Engine is the interface for local automatic speech recognition backends.
type Engine interface {
	Transcribe(ctx context.Context, req api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error)
	TranscribeStream(ctx context.Context, req api.OpenAIAudioTranscriptionRequest, onChunk func(api.OpenAIAudioTranscriptionResponse) error) error
	Close() error
	ModelName() string
}

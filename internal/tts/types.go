package tts

import (
	"context"

	"github.com/opencsgs/csglite/pkg/api"
)

// Audio is one synthesised result. Format is the container the bytes are in, as
// requested by the caller.
type Audio struct {
	Data       []byte
	Format     string
	SampleRate int
}

// Chunk is one piece of a streamed synthesis. Chunks carry raw bytes of the
// requested container so a handler can forward them to the client unchanged.
type Chunk struct {
	Data []byte
	Done bool
}

// Engine is the interface for local text-to-speech backends. It mirrors
// asr.Engine: speech models run in the Python runtime, never through llama.cpp,
// because the vocoder or codec decoder that turns model output into a waveform
// only exists in the model's own inference stack.
type Engine interface {
	Speak(ctx context.Context, req api.OpenAIAudioSpeechRequest) (*Audio, error)
	SpeakStream(ctx context.Context, req api.OpenAIAudioSpeechRequest, onChunk func(Chunk) error) error
	// Info reports the voices, native sample rate and backend of the loaded
	// model, so clients can discover what it can render.
	Info(ctx context.Context) (*api.SpeechVoicesResponse, error)
	Close() error
	ModelName() string
}

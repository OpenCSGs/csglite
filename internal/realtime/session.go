package realtime

import (
	"encoding/json"
	"strings"
)

// SessionConfig is the session object clients send with session.update and
// receive back in session.created. It follows the OpenAI GA shape, with two
// csglite additions noted below, because csglite composes separate ASR and TTS
// models where OpenAI has one end-to-end model.
type SessionConfig struct {
	// Type is "realtime" or "transcription". A transcription session only
	// recognises speech and rejects response.create.
	Type string `json:"type,omitempty"`
	// Model is the conversation model. Leaving it empty selects the pure
	// speech-to-speech pipeline: the server does not invent replies and only
	// speaks what a response.create asks it to.
	Model        string        `json:"model,omitempty"`
	Instructions string        `json:"instructions,omitempty"`
	Modalities   []string      `json:"output_modalities,omitempty"`
	Audio        *SessionAudio `json:"audio,omitempty"`
}

type SessionAudio struct {
	Input  *SessionAudioInput  `json:"input,omitempty"`
	Output *SessionAudioOutput `json:"output,omitempty"`
}

type SessionAudioInput struct {
	Format        *AudioFormat   `json:"format,omitempty"`
	TurnDetection *TurnDetection `json:"turn_detection,omitempty"`
	Transcription *Transcription `json:"transcription,omitempty"`
}

type SessionAudioOutput struct {
	Format *AudioFormat `json:"format,omitempty"`
	Voice  string       `json:"voice,omitempty"`
	Speed  float64      `json:"speed,omitempty"`
	// Model is a csglite addition: OpenAI's single model covers synthesis, while
	// here the text-to-speech model is chosen separately.
	Model string `json:"model,omitempty"`
}

type AudioFormat struct {
	Type string `json:"type,omitempty"`
	Rate int    `json:"rate,omitempty"`
}

// TurnDetection configures server-side voice activity detection. A nil
// TurnDetection means the client commits turns itself.
type TurnDetection struct {
	Type              string  `json:"type,omitempty"`
	Threshold         float64 `json:"threshold,omitempty"`
	PrefixPaddingMS   int     `json:"prefix_padding_ms,omitempty"`
	SilenceDurationMS int     `json:"silence_duration_ms,omitempty"`
	CreateResponse    *bool   `json:"create_response,omitempty"`
	InterruptResponse *bool   `json:"interrupt_response,omitempty"`
}

type Transcription struct {
	Model    string   `json:"model,omitempty"`
	Language string   `json:"language,omitempty"`
	Prompt   string   `json:"prompt,omitempty"`
	Hotwords []string `json:"hotwords,omitempty"`
	ITN      *bool    `json:"itn,omitempty"`
}

// Pipeline names which models a session drives.
type Pipeline string

const (
	// PipelineASROnly recognises speech and never speaks.
	PipelineASROnly Pipeline = "asr_only"
	// PipelineASRTTS recognises speech and speaks text the client supplies;
	// no language model is involved.
	PipelineASRTTS Pipeline = "asr_tts"
	// PipelineASRLLMTTS additionally generates replies with a language model.
	PipelineASRLLMTTS Pipeline = "asr_llm_tts"
)

// DefaultInputSampleRate is the rate the transports feed audio in at. The
// report asks for 24 kHz mono PCM16 on the wire; the ASR engine resamples to
// whatever it needs.
const DefaultInputSampleRate = 24000

// DefaultOutputSampleRate is used until a loaded speech model reports its own.
const DefaultOutputSampleRate = 24000

// Normalize fills in defaults and reports the pipeline the configuration
// selects. It is total: an empty config yields a usable transcription-capable
// session rather than an error, so a client that connects and sends nothing
// still gets session.created.
func (c *SessionConfig) Normalize() Pipeline {
	if c.Type == "" {
		c.Type = "realtime"
	}
	if c.Audio == nil {
		c.Audio = &SessionAudio{}
	}
	if c.Audio.Input == nil {
		c.Audio.Input = &SessionAudioInput{}
	}
	if c.Audio.Input.Format == nil {
		c.Audio.Input.Format = &AudioFormat{Type: "audio/pcm", Rate: DefaultInputSampleRate}
	}
	if c.Audio.Input.Format.Rate == 0 {
		c.Audio.Input.Format.Rate = DefaultInputSampleRate
	}
	if c.Audio.Output == nil {
		c.Audio.Output = &SessionAudioOutput{}
	}
	if c.Audio.Output.Format == nil {
		c.Audio.Output.Format = &AudioFormat{Type: "audio/pcm", Rate: DefaultOutputSampleRate}
	}

	if strings.EqualFold(c.Type, "transcription") {
		return PipelineASROnly
	}
	if strings.TrimSpace(c.Model) != "" {
		return PipelineASRLLMTTS
	}
	return PipelineASRTTS
}

// TranscriptionModel reports the ASR model the session should use.
func (c *SessionConfig) TranscriptionModel() string {
	if c.Audio == nil || c.Audio.Input == nil || c.Audio.Input.Transcription == nil {
		return ""
	}
	return strings.TrimSpace(c.Audio.Input.Transcription.Model)
}

// SpeechModel reports the text-to-speech model the session should use.
func (c *SessionConfig) SpeechModel() string {
	if c.Audio == nil || c.Audio.Output == nil {
		return ""
	}
	return strings.TrimSpace(c.Audio.Output.Model)
}

// Voice reports the requested output voice, empty for the model's default.
func (c *SessionConfig) Voice() string {
	if c.Audio == nil || c.Audio.Output == nil {
		return ""
	}
	return strings.TrimSpace(c.Audio.Output.Voice)
}

// Merge applies a session.update patch. Only the fields present in the patch
// change, so a client can adjust the voice mid-session without restating the
// whole configuration.
func (c *SessionConfig) Merge(patch json.RawMessage) error {
	if len(patch) == 0 {
		return nil
	}
	// Decoding onto the existing value leaves absent fields untouched, which is
	// exactly the patch semantics; nested pointers are replaced wholesale, which
	// matches how clients send them.
	return json.Unmarshal(patch, c)
}

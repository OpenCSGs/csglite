// Package realtime implements the session and event protocol shared by the
// realtime transports. It is transport-neutral on purpose: the WebSocket and
// WebRTC endpoints differ only in how audio travels -- as base64 events or on a
// media track -- so the session logic lives here and is written once.
package realtime

import "encoding/json"

// Client event types, as named by the OpenAI Realtime API.
const (
	ClientSessionUpdate       = "session.update"
	ClientInputAudioAppend    = "input_audio_buffer.append"
	ClientInputAudioCommit    = "input_audio_buffer.commit"
	ClientInputAudioClear     = "input_audio_buffer.clear"
	ClientConversationItemAdd = "conversation.item.create"
	ClientResponseCreate      = "response.create"
	ClientResponseCancel      = "response.cancel"
	ClientOutputAudioClear    = "output_audio_buffer.clear"
)

// Server event types.
const (
	ServerSessionCreated      = "session.created"
	ServerSessionUpdated      = "session.updated"
	ServerSpeechStarted       = "input_audio_buffer.speech_started"
	ServerSpeechStopped       = "input_audio_buffer.speech_stopped"
	ServerInputAudioCommitted = "input_audio_buffer.committed"
	ServerInputAudioCleared   = "input_audio_buffer.cleared"
	ServerTranscriptDelta     = "conversation.item.input_audio_transcription.delta"
	ServerTranscriptCompleted = "conversation.item.input_audio_transcription.completed"
	ServerTranscriptFailed    = "conversation.item.input_audio_transcription.failed"
	ServerResponseCreated     = "response.created"
	ServerAudioDelta          = "response.output_audio.delta"
	ServerAudioDone           = "response.output_audio.done"
	ServerResponseDone        = "response.done"
	ServerOutputAudioStarted  = "output_audio_buffer.started"
	ServerOutputAudioStopped  = "output_audio_buffer.stopped"
	ServerOutputAudioCleared  = "output_audio_buffer.cleared"
	ServerError               = "error"
)

// ClientEvent is an inbound event. Only the fields the session acts on are
// decoded; unknown fields are ignored so a client sending the full OpenAI shape
// still works.
type ClientEvent struct {
	Type    string          `json:"type"`
	EventID string          `json:"event_id,omitempty"`
	Audio   string          `json:"audio,omitempty"`
	Session json.RawMessage `json:"session,omitempty"`
	Item    json.RawMessage `json:"item,omitempty"`
	// Response carries the per-response overrides of response.create.
	Response json.RawMessage `json:"response,omitempty"`
}

// ServerEvent is an outbound event. Every event carries the identifiers the
// report asked for -- session, sequence, timestamp and model -- so a client can
// correlate and order them.
type ServerEvent struct {
	Type      string `json:"type"`
	EventID   string `json:"event_id"`
	SessionID string `json:"session_id"`
	Sequence  int64  `json:"sequence"`
	Timestamp int64  `json:"timestamp"`
	Model     string `json:"model,omitempty"`

	// Transcription
	ItemID     string `json:"item_id,omitempty"`
	Delta      string `json:"delta,omitempty"`
	Transcript string `json:"transcript,omitempty"`

	// Audio output, base64 PCM16 on the WebSocket transport.
	Audio      string `json:"audio,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`

	// Response lifecycle
	ResponseID string `json:"response_id,omitempty"`
	Status     string `json:"status,omitempty"`

	Session *SessionConfig `json:"session,omitempty"`
	Error   *EventError    `json:"error,omitempty"`
}

// EventError is the error shape shared with the OpenAI-compatible HTTP errors.
type EventError struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	EventID string `json:"event_id,omitempty"`
}

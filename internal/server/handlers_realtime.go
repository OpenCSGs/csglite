package server

import "net/http"

// Realtime audio endpoints are designed in docs/guides/realtime-audio-api.md and
// tracked by https://github.com/OpenCSGs/csglite/issues/147.
//
// They are registered ahead of their implementation so clients get an explicit,
// machine-readable 501 instead of a misleading response. Without a registered
// handler, POST requests return 404 and -- worse -- GET requests match the
// "GET /" static fallback in routes() and receive the embedded web UI's
// index.html with status 200, so a client probing for realtime support sees
// success and HTML rather than an error. handleOpenAIResponsesUnsupported
// guards GET /v1/responses for the same reason.

const realtimeUnsupportedMessage = "realtime audio is not supported yet; see https://github.com/OpenCSGs/csglite/issues/147"

const audioSpeechUnsupportedMessage = "local text-to-speech is not supported yet; see https://github.com/OpenCSGs/csglite/issues/147"

// POST /v1/audio/speech -- OpenAI-compatible speech synthesis, not implemented yet.
func (s *Server) handleOpenAIAudioSpeechUnsupported(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "unsupported_error", audioSpeechUnsupportedMessage)
}

// POST /v1/realtime/calls -- OpenAI Realtime WebRTC SDP exchange, not implemented yet.
func (s *Server) handleRealtimeCallsUnsupported(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "unsupported_error", realtimeUnsupportedMessage)
}

// GET /v1/realtime, GET /v1/realtime/transcription and
// GET /v1/audio/transcriptions/realtime -- OpenAI Realtime WebSocket transports,
// not implemented yet.
func (s *Server) handleRealtimeUnsupported(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "unsupported_error", realtimeUnsupportedMessage)
}

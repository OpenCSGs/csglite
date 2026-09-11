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

const realtimeUnsupportedMessage = "the WebRTC transport is not implemented yet; " +
	"use the WebSocket transport at GET /v1/realtime, which serves the same session and event protocol"

// POST /v1/realtime/calls -- OpenAI Realtime WebRTC SDP exchange, not implemented yet.
func (s *Server) handleRealtimeCallsUnsupported(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented, "unsupported_error", realtimeUnsupportedMessage)
}

// GET /v1/realtime/transcription and GET /v1/audio/transcriptions/realtime --
// a realtime session fixed to transcription, so a client that only wants
// recognition does not have to send a session object to say so. The second path
// is not part of the OpenAI API; it is accepted because clients in
// https://github.com/OpenCSGs/csglite/issues/147 address it.
func (s *Server) handleRealtimeTranscriptionWebSocket(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	query.Set("intent", "transcription")
	r.URL.RawQuery = query.Encode()
	s.handleRealtimeWebSocket(w, r)
}

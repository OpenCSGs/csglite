package server

import "net/http"

// Realtime audio endpoints are designed in docs/guides/realtime-audio-api.md and
// tracked by https://github.com/OpenCSGs/csglite/issues/147. The WebSocket
// transport lives in handlers_realtime_ws.go and the WebRTC one in
// handlers_realtime_webrtc.go; both drive the same session layer.
//
// Every realtime path must stay registered even when it can only fail, because
// an unrouted GET matches the "GET /" static fallback in routes() and returns
// the embedded web UI's index.html with status 200 -- so a client probing for
// realtime support would see success and HTML rather than an error.

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

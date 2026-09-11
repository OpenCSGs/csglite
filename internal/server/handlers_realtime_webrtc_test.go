package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/realtime"
)

func newRealtimeTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithConfig(t, &config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	})
}

// newTestOffer builds the same offer a browser sends: one bidirectional audio
// transceiver plus the oai-events DataChannel.
func newTestOffer(t *testing.T) (*webrtc.PeerConnection, string) {
	t.Helper()
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Skipf("this environment cannot create a peer connection: %v", err)
	}
	if _, err := peer.CreateDataChannel(realtimeEventsChannel, nil); err != nil {
		t.Fatalf("create data channel: %v", err)
	}
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv}); err != nil {
		t.Fatalf("add transceiver: %v", err)
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local description: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(10 * time.Second):
		t.Skip("ICE gathering did not finish; this environment has no usable network interface")
	}
	return peer, peer.LocalDescription().SDP
}

// The WebRTC transport must answer a real SDP offer with a usable answer, tell
// the client where to hang up, and release the call on DELETE. This is the
// whole contract a client depends on before any media flows.
func TestWebRTCCallNegotiatesAndHangsUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ICE gathering is unreliable on the Windows CI runner")
	}
	s := newRealtimeTestServer(t)
	handler := s.routes()
	client, offer := newTestOffer(t)
	defer client.Close()

	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	if err := form.WriteField("sdp", offer); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("session", `{"type":"realtime","audio":{"output":{"voice":"alloy"}}}`); err != nil {
		t.Fatal(err)
	}
	form.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Accept", "application/sdp")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, truncateLogString(w.Body.String(), 300))
	}
	if got := w.Header().Get("Content-Type"); got != "application/sdp" {
		t.Fatalf("content type = %q, want application/sdp", got)
	}
	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/v1/realtime/calls/") {
		t.Fatalf("Location = %q, want the call resource path", location)
	}
	answer := w.Body.String()
	// The answer must be an answer the client can actually apply, and it must
	// carry an audio section; an answer without media would negotiate a call
	// with nothing to say.
	if err := client.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: answer,
	}); err != nil {
		t.Fatalf("the client rejected the answer: %v\n%s", err, answer)
	}
	if !strings.Contains(answer, "m=audio") {
		t.Fatalf("answer has no audio section:\n%s", answer)
	}
	if !strings.Contains(answer, "PCMU/8000") {
		t.Fatalf("answer does not offer the PCMU codec this build encodes:\n%s", answer)
	}

	callID := strings.TrimPrefix(location, "/v1/realtime/calls/")
	s.mu.RLock()
	_, tracked := s.realtimeCalls[callID]
	s.mu.RUnlock()
	if !tracked {
		t.Fatalf("call %q is not in the registry, so DELETE could never find it", callID)
	}

	del := httptest.NewRequest(http.MethodDelete, location, nil)
	dw := httptest.NewRecorder()
	handler.ServeHTTP(dw, del)
	if dw.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200 (body=%s)", dw.Code, truncateLogString(dw.Body.String(), 200))
	}

	// Hanging up twice must not report success the second time, and must not
	// panic on the already-closed peer connection.
	dw2 := httptest.NewRecorder()
	handler.ServeHTTP(dw2, httptest.NewRequest(http.MethodDelete, location, nil))
	if dw2.Code != http.StatusNotFound {
		t.Fatalf("second DELETE status = %d, want 404", dw2.Code)
	}
}

// A request with no offer is the client's mistake, not the server's, so it must
// be a 400 with a JSON body rather than a 500 or the web UI.
func TestWebRTCCallRejectsMissingOffer(t *testing.T) {
	s := newRealtimeTestServer(t)
	handler := s.routes()

	for name, req := range map[string]*http.Request{
		"empty body": httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", strings.NewReader("")),
		"bare sdp content type with empty body": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", strings.NewReader("   "))
			r.Header.Set("Content-Type", "application/sdp")
			return r
		}(),
		"multipart without an sdp field": func() *http.Request {
			body := &bytes.Buffer{}
			form := multipart.NewWriter(body)
			_ = form.WriteField("session", `{"type":"realtime"}`)
			form.Close()
			r := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", body)
			r.Header.Set("Content-Type", form.FormDataContentType())
			return r
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", w.Code, truncateLogString(w.Body.String(), 200))
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Fatalf("content type = %q, want application/json", ct)
			}
		})
	}
}

// A malformed offer must be reported as the caller's fault too, since accepting
// it would leave a half-built call behind.
func TestWebRTCCallRejectsMalformedOffer(t *testing.T) {
	s := newRealtimeTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", strings.NewReader("not an sdp offer"))
	req.Header.Set("Content-Type", "application/sdp")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)

	// A malformed offer is the caller's fault, so it must not be reported as a
	// server error.
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, truncateLogString(w.Body.String(), 200))
	}
	s.mu.RLock()
	live := len(s.realtimeCalls)
	s.mu.RUnlock()
	if live != 0 {
		t.Fatalf("%d calls left in the registry after a rejected offer, want 0", live)
	}
}

// The bare-SDP form carries its configuration in the query string, which is the
// shape the OpenAI ephemeral-key clients use.
func TestParseRealtimeCallRequestReadsBothForms(t *testing.T) {
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	_ = form.WriteField("sdp", "v=0 offer")
	_ = form.WriteField("session", `{"type":"transcription","audio":{"input":{"transcription":{"model":"acme/asr"}}}}`)
	form.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", body)
	req.Header.Set("Content-Type", form.FormDataContentType())

	sdp, cfg, err := parseRealtimeCallRequest(req)
	if err != nil {
		t.Fatalf("multipart form: %v", err)
	}
	// The parser restores the line terminator the SDP grammar requires.
	if sdp != "v=0 offer\r\n" {
		t.Fatalf("sdp = %q", sdp)
	}
	if cfg.TranscriptionModel() != "acme/asr" {
		t.Fatalf("transcription model = %q, want acme/asr", cfg.TranscriptionModel())
	}

	raw := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?model=acme/asr", strings.NewReader("v=0 raw offer"))
	raw.Header.Set("Content-Type", "application/sdp")
	sdp, cfg, err = parseRealtimeCallRequest(raw)
	if err != nil {
		t.Fatalf("bare sdp: %v", err)
	}
	if sdp != "v=0 raw offer\r\n" {
		t.Fatalf("sdp = %q", sdp)
	}
	if cfg.TranscriptionModel() != "acme/asr" {
		t.Fatalf("transcription model = %q, want the query model", cfg.TranscriptionModel())
	}

	bad := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", strings.NewReader("v=0"))
	bad.Header.Set("Content-Type", "multipart/form-data")
	if _, _, err := parseRealtimeCallRequest(bad); err == nil {
		t.Fatal("a multipart request without a boundary was accepted")
	}
}

// Synthesis chunks do not line up with 20ms frames, so the packetiser has to
// carry the tail over. Losing it would drop audio at every chunk boundary.
func TestWebRTCSenderCutsWholeFramesAndCarriesTheRest(t *testing.T) {
	sender := &webrtcSender{sourceRate: realtime.PCMUSampleRate}

	// One and a half frames in: one frame out, half a frame held back.
	frames, _ := sender.nextFrames(make([]byte, pcmuFrameBytes*3/2), realtime.PCMUSampleRate)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if len(frames[0]) != pcmuFrameBytes/2 {
		t.Fatalf("frame size = %d bytes of mu-law, want %d", len(frames[0]), pcmuFrameBytes/2)
	}
	if len(sender.residual) != pcmuFrameBytes/2 {
		t.Fatalf("residual = %d, want the half frame to be held back", len(sender.residual))
	}

	// The next half frame completes it, so exactly one more frame comes out and
	// nothing is left over.
	frames, _ = sender.nextFrames(make([]byte, pcmuFrameBytes/2), realtime.PCMUSampleRate)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want the carried half to complete one frame", len(frames))
	}
	if len(sender.residual) != 0 {
		t.Fatalf("residual = %d, want 0", len(sender.residual))
	}
}

// 24kHz synthesis output is the common case: a frame must still be 20ms of
// 8kHz mu-law after resampling, not 20ms of the source rate.
func TestWebRTCSenderResamplesToTheCodecRate(t *testing.T) {
	sender := &webrtcSender{sourceRate: realtime.DefaultOutputSampleRate}
	// 100ms at 24kHz PCM16.
	frames, _ := sender.nextFrames(make([]byte, 24000/10*2), 24000)
	if len(frames) != 5 {
		t.Fatalf("frames = %d, want 5 frames of 20ms for 100ms of audio", len(frames))
	}
	for i, frame := range frames {
		if len(frame) != realtime.PCMUSampleRate/(1000/outboundFrameMS) {
			t.Fatalf("frame %d = %d samples, want 160", i, len(frame))
		}
	}
}

// output_audio_buffer.clear must stop playback promptly: frames already cut
// from an earlier chunk belong to a stale generation and are dropped.
func TestWebRTCSenderDiscardsPendingAudio(t *testing.T) {
	sender := &webrtcSender{sourceRate: realtime.PCMUSampleRate}
	_, generation := sender.nextFrames(make([]byte, pcmuFrameBytes), realtime.PCMUSampleRate)
	sender.nextFrames(make([]byte, pcmuFrameBytes/2), realtime.PCMUSampleRate)
	sender.DiscardPendingAudio()

	if len(sender.residual) != 0 {
		t.Fatalf("residual = %d after a clear, want 0", len(sender.residual))
	}
	if _, current := sender.nextFrames(nil, realtime.PCMUSampleRate); current == generation {
		t.Fatal("the generation did not change, so stale frames would still be written")
	}
}

// Events produced before the client opens the DataChannel -- session.created
// above all -- must be queued rather than dropped.
func TestWebRTCSenderQueuesEventsUntilTheChannelOpens(t *testing.T) {
	sender := &webrtcSender{}
	if err := sender.Send(realtime.ServerEvent{Type: realtime.ServerSessionCreated}); err != nil {
		t.Fatalf("Send before the channel opened: %v", err)
	}
	if len(sender.pending) != 1 {
		t.Fatalf("pending = %d, want the event to be queued", len(sender.pending))
	}
	if sender.pending[0].Type != realtime.ServerSessionCreated {
		t.Fatalf("queued event = %q", sender.pending[0].Type)
	}
}

func TestRealtimeCallDeleteRequiresAnId(t *testing.T) {
	s := newRealtimeTestServer(t)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/realtime/calls/rtc_missing", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if !strings.Contains(body.Error.Message, "rtc_missing") {
		t.Fatalf("message = %q, want it to name the call", body.Error.Message)
	}
}

// The full handshake must end with a connected peer whose oai-events channel
// carries the session, because that is the only way a client learns the session
// exists and can send events back. This exercises ICE over loopback, so it is
// skipped where that is not available.
func TestWebRTCCallDeliversSessionOverTheDataChannel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ICE connectivity is unreliable on the Windows CI runner")
	}
	s := newRealtimeTestServer(t)
	handler := s.routes()

	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Skipf("this environment cannot create a peer connection: %v", err)
	}
	defer client.Close()

	events := make(chan string, 8)
	channel, err := client.CreateDataChannel(realtimeEventsChannel, nil)
	if err != nil {
		t.Fatalf("create data channel: %v", err)
	}
	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		select {
		case events <- string(msg.Data):
		default:
		}
	})
	if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv}); err != nil {
		t.Fatalf("add transceiver: %v", err)
	}

	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local description: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(10 * time.Second):
		t.Skip("ICE gathering did not finish; this environment has no usable network interface")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?model=acme/asr",
		strings.NewReader(client.LocalDescription().SDP))
	req.Header.Set("Content-Type", "application/sdp")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, truncateLogString(w.Body.String(), 300))
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: w.Body.String(),
	}); err != nil {
		t.Fatalf("apply answer: %v", err)
	}
	t.Cleanup(func() {
		s.endRealtimeCall(strings.TrimPrefix(w.Header().Get("Location"), "/v1/realtime/calls/"))
	})

	var raw string
	select {
	case raw = <-events:
	case <-time.After(20 * time.Second):
		t.Skip("the peers never connected; loopback ICE is unavailable here")
	}

	var ev struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Session   struct {
			Audio struct {
				Input struct {
					Transcription struct {
						Model string `json:"model"`
					} `json:"transcription"`
				} `json:"input"`
			} `json:"audio"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	if ev.Type != string(realtime.ServerSessionCreated) {
		t.Fatalf("first event = %q, want session.created", ev.Type)
	}
	if ev.SessionID == "" {
		t.Fatal("session.created carried no session id")
	}
	// The query model must reach the session, since that is how the clients in
	// the issue name their recognition model on this transport.
	if got := ev.Session.Audio.Input.Transcription.Model; got != "acme/asr" {
		t.Fatalf("transcription model = %q, want acme/asr", got)
	}
}

// realtime.max_sessions has to bound both transports together, because a
// session of either kind can hold a recognition and a synthesis model.
func TestRealtimeSessionCapCoversBothTransports(t *testing.T) {
	s := newRealtimeTestServer(t)
	s.cfg.Realtime.MaxSessions = 1

	if err := s.admitRealtimeSession(); err != nil {
		t.Fatalf("the first session was refused: %v", err)
	}

	// A live WebRTC call fills the single slot.
	s.mu.Lock()
	s.realtimeCalls["rtc_test"] = &realtimeCall{id: "rtc_test"}
	s.mu.Unlock()
	if err := s.admitRealtimeSession(); !errors.Is(err, errTooManyRealtimeSessions) {
		t.Fatalf("error = %v, want the session cap to refuse the second call", err)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/realtime", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("websocket status = %d, want 503 while the cap is reached", w.Code)
	}
	s.mu.Lock()
	delete(s.realtimeCalls, "rtc_test")
	s.mu.Unlock()

	// A live WebSocket session fills it just the same.
	s.addRealtimeSocket(1)
	if err := s.admitRealtimeSession(); !errors.Is(err, errTooManyRealtimeSessions) {
		t.Fatalf("error = %v, want a websocket session to count toward the cap", err)
	}
	s.addRealtimeSocket(-1)
	if err := s.admitRealtimeSession(); err != nil {
		t.Fatalf("the slot was not released: %v", err)
	}

	// A negative cap is how an operator turns the guard off.
	s.cfg.Realtime.MaxSessions = -1
	for i := 0; i < 10; i++ {
		s.addRealtimeSocket(1)
	}
	if err := s.admitRealtimeSession(); err != nil {
		t.Fatalf("an unlimited cap refused a session: %v", err)
	}
}

// The configured default models fill in the half of the pipeline the client did
// not name, which is what the field reports ask for: they connect with only
// ?model=<asr model>.
func TestRealtimeDefaultModelsFillInThePipeline(t *testing.T) {
	s := newRealtimeTestServer(t)
	s.cfg.Realtime.DefaultTTSModel = "hexgrad/Kokoro-82M"

	if synth := s.newRealtimeSynthesizer(realtime.SessionConfig{}); synth == nil {
		t.Fatal("no synthesizer was built, so the configured default was ignored")
	} else if got := synth.(*realtimeSynth).model; got != "hexgrad/Kokoro-82M" {
		t.Fatalf("synthesis model = %q, want the configured default", got)
	}

	// A model named by the session still wins over the default.
	cfg := realtime.SessionConfig{Audio: &realtime.SessionAudio{
		Output: &realtime.SessionAudioOutput{Model: "acme/voice"},
	}}
	if got := s.newRealtimeSynthesizer(cfg).(*realtimeSynth).model; got != "acme/voice" {
		t.Fatalf("synthesis model = %q, want the session's model", got)
	}

	// With neither a session model nor a default there is nothing to speak with.
	s.cfg.Realtime.DefaultTTSModel = ""
	if synth := s.newRealtimeSynthesizer(realtime.SessionConfig{}); synth != nil {
		t.Fatal("a synthesizer was built with no model configured")
	}
}

// Synthesis runs much faster than real time and pion does not pace samples, so
// the sender has to. Without pacing a whole utterance lands in the receiver's
// jitter buffer at once and a barge-in has nothing left to cut.
func TestWebRTCSenderPacesFramesAtRealTime(t *testing.T) {
	sender := &webrtcSender{sourceRate: realtime.PCMUSampleRate}

	start := time.Now()
	// The first frames may go out immediately, up to the prebuffer.
	for i := 0; i < 3; i++ {
		if !sender.waitForFrameSlot(0) {
			t.Fatal("a frame was dropped with no clear pending")
		}
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("the prebuffer took %s, want the first frames to go out at once", elapsed)
	}

	// Once the cushion is spent, each further frame waits its 20ms turn.
	paced := time.Now()
	for i := 0; i < 5; i++ {
		sender.waitForFrameSlot(0)
	}
	if elapsed := time.Since(paced); elapsed < 40*time.Millisecond {
		t.Fatalf("5 frames took %s, want them paced at roughly 20ms each", elapsed)
	}
}

// A clear must abandon the frames still waiting to be played, and pacing must
// then restart for the next utterance instead of catching up on a stale
// schedule.
func TestWebRTCSenderStopsPacingAfterAClear(t *testing.T) {
	sender := &webrtcSender{sourceRate: realtime.PCMUSampleRate}
	sender.waitForFrameSlot(0)
	sender.DiscardPendingAudio()

	if sender.waitForFrameSlot(0) {
		t.Fatal("a frame from the cleared generation was still accepted")
	}
	if !sender.nextFrame.IsZero() {
		t.Fatal("pacing was not reset, so the next utterance would be delayed")
	}
	start := time.Now()
	if !sender.waitForFrameSlot(sender.generation) {
		t.Fatal("the next utterance was refused")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("the first frame of the next utterance waited %s", elapsed)
	}
}

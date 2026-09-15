package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/opencsgs/csglite/internal/realtime"
)

const (
	// realtimeEventsChannel is the DataChannel name the OpenAI Realtime WebRTC
	// clients open; it is fixed by that protocol.
	realtimeEventsChannel = "oai-events"
	// outboundFrameMS is the packetisation interval. 20ms is what browsers
	// expect from a voice track.
	outboundFrameMS = 20
	maxSDPBodyBytes = 1 << 20
	// opusSampleRate is the rate Opus always decodes to.
	opusSampleRate = 48000
	// inboundPlayoutTail is how long after the last reply frame was written the
	// microphone stays closed. Frames are paced onto the track in real time,
	// so the reply has finished leaving when speaking ends, but the browser's
	// jitter buffer and the room are still playing it.
	inboundPlayoutTail = 400 * time.Millisecond
)

// errInvalidSDPOffer marks a setup failure the caller caused, so it is answered
// with 400 rather than 500.
var errInvalidSDPOffer = errors.New("invalid SDP offer")

// webrtcSender delivers session events on the DataChannel and audio on the
// media track. It is the only part of a realtime session that differs between
// the two transports.
type webrtcSender struct {
	mu    sync.Mutex
	track *webrtc.TrackLocalStaticSample
	// codec encodes the reply for the track. It is chosen once per call from
	// what the caller offered, and its encoder is stateful, so every frame goes
	// through it under mu and in order.
	codec *outboundCodec
	// events is nil until the client opens the channel; events produced before
	// that are queued so session.created is not lost.
	events  *webrtc.DataChannel
	pending []realtime.ServerEvent

	// generation is bumped by DiscardPendingAudio so frames synthesised before
	// an output_audio_buffer.clear are dropped instead of played late.
	generation uint64
	// residual holds PCM left over from the previous chunk, since synthesis
	// chunk sizes do not line up with 20ms frames.
	residual   []byte
	sourceRate int

	// nextFrame is when the next 20ms frame is due. Synthesis runs far faster
	// than real time, and pion does not pace samples, so without this the whole
	// utterance is written to the track in a fraction of its duration: the
	// receiver's jitter buffer cannot hold seconds of audio, and a barge-in has
	// nothing left to cut because everything has already been sent.
	nextFrame time.Time
}

const (
	// outboundPrebuffer is how much audio may be sent ahead of real time, to
	// give the receiver's jitter buffer a cushion against scheduling noise.
	outboundPrebuffer = 60 * time.Millisecond
	// outboundGapReset is the silence after which pacing restarts from now
	// rather than catching up on a schedule that has gone stale -- the gap
	// between two utterances, typically.
	outboundGapReset = 400 * time.Millisecond
)

func (w *webrtcSender) attach(dc *webrtc.DataChannel) {
	w.mu.Lock()
	w.events = dc
	queued := w.pending
	w.pending = nil
	w.mu.Unlock()
	for _, ev := range queued {
		_ = w.sendOn(dc, ev)
	}
}

func (w *webrtcSender) sendOn(dc *webrtc.DataChannel, ev realtime.ServerEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return dc.SendText(string(payload))
}

func (w *webrtcSender) Send(ev realtime.ServerEvent) error {
	w.mu.Lock()
	dc := w.events
	if dc == nil {
		w.pending = append(w.pending, ev)
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()
	return w.sendOn(dc, ev)
}

// nextFrames resamples a synthesis chunk to the codec's rate and cuts it into
// whole 20ms packets, holding back the tail that did not fill one. Synthesis
// chunk boundaries have nothing to do with frame boundaries, so without the
// carry every chunk would lose up to 20ms of audio. It also returns the
// generation the frames belong to, so a clear that arrives mid-write can drop
// them.
func (w *webrtcSender) nextFrames(pcm []byte, sampleRate int) ([][]byte, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sampleRate > 0 {
		w.sourceRate = sampleRate
	}
	rate := w.sourceRate
	if rate <= 0 {
		rate = realtime.DefaultOutputSampleRate
	}
	frameBytes := w.codec.frameBytes
	buffered := append(w.residual, realtime.ResamplePCM16(pcm, rate, w.codec.sampleRate)...)
	whole := len(buffered) / frameBytes * frameBytes
	frames := make([][]byte, 0, whole/frameBytes)
	for offset := 0; offset < whole; offset += frameBytes {
		packet, err := w.codec.encode(buffered[offset : offset+frameBytes])
		if err != nil {
			// A frame that will not encode is dropped rather than failing the
			// response: the next one is independent of it, and 20ms of silence
			// is a better answer than an aborted reply.
			log.Printf("REALTIME: encoding outbound audio: %v", err)
			continue
		}
		frames = append(frames, packet)
	}
	// The residual is kept at the codec's rate so it is never resampled twice.
	w.residual = append([]byte(nil), buffered[whole:]...)
	return frames, w.generation
}

// waitForFrameSlot blocks until the next frame is due and reports whether the
// frame is still wanted. Returning false means a clear arrived, in which case
// the rest of the chunk is dropped.
func (w *webrtcSender) waitForFrameSlot(generation uint64) bool {
	w.mu.Lock()
	if w.generation != generation {
		w.mu.Unlock()
		return false
	}
	now := time.Now()
	if w.nextFrame.IsZero() || now.Sub(w.nextFrame) > outboundGapReset {
		// Start of an utterance: allow a short burst so the receiver has
		// something buffered before playback begins.
		w.nextFrame = now.Add(-outboundPrebuffer)
	}
	due := w.nextFrame
	w.nextFrame = w.nextFrame.Add(outboundFrameMS * time.Millisecond)
	w.mu.Unlock()

	if wait := time.Until(due); wait > 0 {
		time.Sleep(wait)
		// A clear may have arrived while this frame waited its turn.
		w.mu.Lock()
		fresh := w.generation == generation
		w.mu.Unlock()
		return fresh
	}
	return true
}

// SendAudio packetises synthesised PCM onto the media track. Audio arrives in
// synthesis-sized chunks at the model's rate and has to leave as 20ms packets
// at the negotiated codec's rate, paced at real time.
func (w *webrtcSender) SendAudio(audio realtime.AudioFrame) error {
	frames, generation := w.nextFrames(audio.PCM, audio.SampleRate)
	for _, frame := range frames {
		if !w.waitForFrameSlot(generation) {
			// output_audio_buffer.clear happened while this chunk was being
			// played out; the rest of it must not reach the caller.
			return nil
		}
		if err := w.track.WriteSample(media.Sample{
			Data:     frame,
			Duration: outboundFrameMS * time.Millisecond,
		}); err != nil {
			return err
		}
	}
	return nil
}

// DiscardPendingAudio drops buffered audio so output_audio_buffer.clear stops
// playback promptly rather than after the queue drains.
func (w *webrtcSender) DiscardPendingAudio() {
	w.mu.Lock()
	w.generation++
	w.residual = nil
	// Pacing restarts with the next utterance rather than catching up on the
	// schedule of the one that was cut.
	w.nextFrame = time.Time{}
	w.mu.Unlock()
}

// realtimeCall is a live WebRTC call, kept so DELETE can hang it up and so
// shutdown can release the peer connections it still holds.
type realtimeCall struct {
	id      string
	peer    *webrtc.PeerConnection
	session *realtime.Session
	cancel  context.CancelFunc
	started time.Time
}

func (s *Server) registerRealtimeCall(call *realtimeCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.realtimeCalls == nil {
		s.realtimeCalls = make(map[string]*realtimeCall)
	}
	s.realtimeCalls[call.id] = call
}

// endRealtimeCall tears a call down once. It is reached from three directions
// -- DELETE, the connection-state callback and shutdown -- so removal from the
// registry is what makes it idempotent.
func (s *Server) endRealtimeCall(id string) bool {
	s.mu.Lock()
	call, ok := s.realtimeCalls[id]
	if ok {
		delete(s.realtimeCalls, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	call.cancel()
	call.session.Close()
	if err := call.peer.Close(); err != nil {
		log.Printf("REALTIME: closing call %s: %v", id, err)
	}
	log.Printf("REALTIME: call %s ended after %s", id, time.Since(call.started).Round(time.Millisecond))
	return true
}

// closeRealtimeCalls releases every live call, so a restart does not leave
// peer connections and their ASR/TTS sessions behind.
func (s *Server) closeRealtimeCalls() {
	s.mu.RLock()
	ids := make([]string, 0, len(s.realtimeCalls))
	for id := range s.realtimeCalls {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	for _, id := range ids {
		s.endRealtimeCall(id)
	}
}

// POST /v1/realtime/calls -- OpenAI-compatible WebRTC session setup: the client
// posts an SDP offer and gets an SDP answer, then talks events over the
// oai-events DataChannel and audio over the media tracks.
func (s *Server) handleRealtimeCalls(w http.ResponseWriter, r *http.Request) {
	offer, cfg, err := parseRealtimeCallRequest(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if err := s.admitRealtimeSession(); err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", err.Error())
		return
	}

	call, answer, err := s.startRealtimeCall(r.Context(), offer, cfg)
	if err != nil {
		log.Printf("REALTIME: webrtc setup failed: %v", err)
		if errors.Is(err, errInvalidSDPOffer) {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	// The Location header is how the client learns the id it must DELETE to
	// hang up.
	w.Header().Set("Location", "/v1/realtime/calls/"+call)
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, answer); err != nil {
		log.Printf("REALTIME: writing SDP answer failed: %v", err)
	}
}

// DELETE /v1/realtime/calls/{call_id} -- hang up. The path parameter keeps
// OpenAI's spelling so the documented API matches theirs.
func (s *Server) handleRealtimeCallDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("call_id")
	if id == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "a call id is required")
		return
	}
	if !s.endRealtimeCall(id) {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "no such call: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "realtime.call", "status": "ended"})
}

// parseRealtimeCallRequest accepts both shapes clients use: a multipart body
// with `sdp` and `session` fields, and a bare application/sdp body configured
// through the query string.
func parseRealtimeCallRequest(r *http.Request) (string, realtime.SessionConfig, error) {
	var cfg realtime.SessionConfig
	contentType := r.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", cfg, fmt.Errorf("multipart body has no boundary")
		}
		if err := r.ParseMultipartForm(maxSDPBodyBytes); err != nil {
			return "", cfg, fmt.Errorf("invalid multipart body: %w", err)
		}
		sdp := normalizeSDP(r.FormValue("sdp"))
		if sdp == "" {
			return "", cfg, fmt.Errorf("the sdp field is required")
		}
		if session := strings.TrimSpace(r.FormValue("session")); session != "" {
			if err := json.Unmarshal([]byte(session), &cfg); err != nil {
				return "", cfg, fmt.Errorf("the session field is not valid JSON: %w", err)
			}
		}
		return sdp, cfg, nil
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxSDPBodyBytes))
	if err != nil {
		return "", cfg, fmt.Errorf("reading request body: %w", err)
	}
	sdp := normalizeSDP(string(body))
	if sdp == "" {
		return "", cfg, fmt.Errorf("an SDP offer is required, either as the body or as a multipart sdp field")
	}
	// The bare-SDP form carries configuration in the query, since there is no
	// room for it in the body.
	queryCfg, err := realtimeSessionFromRequest(r)
	if err != nil {
		return "", cfg, err
	}
	return sdp, queryCfg, nil
}

// normalizeSDP trims surrounding whitespace but restores the trailing line
// break: an SDP body whose last line is unterminated fails to unmarshal, and
// both transports here receive it through a form field or an HTTP body that may
// have been trimmed on the way.
func normalizeSDP(raw string) string {
	sdp := strings.TrimSpace(raw)
	if sdp == "" {
		return ""
	}
	return sdp + "\r\n"
}

// errTooManyRealtimeSessions is returned when the configured session cap is
// reached; each session can hold a recognition and a synthesis model, so the
// cap protects memory rather than request rate.
var errTooManyRealtimeSessions = errors.New("too many realtime sessions")

// admitRealtimeSession enforces realtime.max_sessions across both transports.
func (s *Server) admitRealtimeSession() error {
	limit := s.cfg.RealtimeMaxSessions()
	if limit <= 0 {
		return nil
	}
	s.mu.RLock()
	live := len(s.realtimeCalls) + s.realtimeSockets
	s.mu.RUnlock()
	if live >= limit {
		return fmt.Errorf("%w: %d of %d in use", errTooManyRealtimeSessions, live, limit)
	}
	return nil
}

// startRealtimeCall negotiates the peer connection and wires it to a session.
func (s *Server) startRealtimeCall(ctx context.Context, offerSDP string, cfg realtime.SessionConfig) (string, string, error) {
	// Both directions speak Opus where the caller offers it: pion decodes the
	// inbound track and now encodes the outbound one, both in pure Go, so the
	// synthesised 24kHz audio no longer has to be thrown away down to G.711's
	// telephone bandwidth. PCMU stays registered as the fallback for a caller
	// that offers nothing else.
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: opusCapability,
		PayloadType:        opusPayloadType,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return "", "", err
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: pcmuCapability,
		PayloadType:        pcmuPayloadType,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return "", "", err
	}

	// ICE is configurable because a deployed server may need its media ports
	// pinned for a firewall, or its public address advertised behind 1:1 NAT.
	settings := webrtc.SettingEngine{}
	if low, high, ok := s.cfg.RealtimeICEPortRange(); ok {
		if err := settings.SetEphemeralUDPPortRange(low, high); err != nil {
			return "", "", fmt.Errorf("realtime.ice_udp_port_range: %w", err)
		}
	}
	if ips := s.cfg.Realtime.ICEExtraHostIPs; len(ips) > 0 {
		// Replace the host candidates' addresses, which is what a server behind
		// 1:1 NAT needs: the address it sees locally is not reachable.
		if err := settings.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External:        ips,
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteReplace,
		}); err != nil {
			return "", "", fmt.Errorf("realtime.ice_extra_host_ips: %w", err)
		}
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithSettingEngine(settings))
	// No ICE servers by default: csglite is reached on a LAN or loopback, where
	// host candidates suffice and a STUN round trip would only add latency.
	rtcConfig := webrtc.Configuration{}
	if servers := s.cfg.Realtime.ICEServers; len(servers) > 0 {
		rtcConfig.ICEServers = []webrtc.ICEServer{{URLs: servers}}
	}
	peer, err := api.NewPeerConnection(rtcConfig)
	if err != nil {
		return "", "", err
	}

	// The track has to carry one codec and is created before the answer, so the
	// choice is read from the offer rather than from the negotiated result.
	outbound := newOutboundCodec(offerSDP)
	track, err := webrtc.NewTrackLocalStaticSample(outbound.capability, "audio", "csglite-tts")
	if err != nil {
		_ = peer.Close()
		return "", "", err
	}
	if _, err := peer.AddTrack(track); err != nil {
		_ = peer.Close()
		return "", "", err
	}

	sender := &webrtcSender{track: track, codec: outbound, sourceRate: realtime.DefaultOutputSampleRate}
	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	synth := s.newRealtimeSynthesizer()
	// Recognition loads in the background so the SDP answer is not held up by a
	// cold model; audio that arrives first is buffered and replayed.
	transcriber, startTranscription := s.newRealtimePipeline(sessionCtx, cfg)

	session, err := realtime.NewSession(cfg, sender, transcriber, synth)
	if err != nil {
		cancel()
		_ = peer.Close()
		return "", "", err
	}
	call := &realtimeCall{id: session.ID, peer: peer, session: session, cancel: cancel, started: time.Now()}
	s.registerRealtimeCall(call)
	session.SetResponder(sessionCtx, s.newRealtimeResponder())
	log.Printf("REALTIME: call %s pipeline=%s model=%q output=%s/%d",
		session.ID, session.Pipeline(), cfg.Model, outbound.capability.MimeType, outbound.sampleRate)
	if startTranscription != nil {
		startTranscription(session)
	}

	peer.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != realtimeEventsChannel {
			return
		}
		dc.OnOpen(func() { sender.attach(dc) })
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			var ev realtime.ClientEvent
			if err := json.Unmarshal(msg.Data, &ev); err != nil {
				session.EmitError("invalid_event", "event is not valid JSON", "")
				return
			}
			if err := s.dispatchRealtimeEvent(sessionCtx, session, ev); err != nil {
				log.Printf("REALTIME: dispatch failed: %v", err)
			}
		})
	})

	peer.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go forwardRemoteAudio(sessionCtx, remote, session)
	})

	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("REALTIME: call %s state=%s", session.ID, state)
		switch state {
		case webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			s.endRealtimeCall(call.id)
		}
	})

	if err := peer.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: offerSDP,
	}); err != nil {
		s.endRealtimeCall(call.id)
		return "", "", fmt.Errorf("%w: %v", errInvalidSDPOffer, err)
	}
	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		s.endRealtimeCall(call.id)
		return "", "", err
	}
	// Wait for ICE gathering so the answer carries its candidates: clients in
	// this flow post an offer and read an answer with no channel for trickled
	// candidates afterwards.
	gathered := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(answer); err != nil {
		s.endRealtimeCall(call.id)
		return "", "", err
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		// Proceed with what was gathered; on a LAN the host candidates are
		// already present and waiting longer only delays the call.
		log.Printf("REALTIME: call %s proceeding before ICE gathering finished", session.ID)
	}
	return call.id, peer.LocalDescription().SDP, nil
}

// realtimeSilenceDuration bounds the session's turn_detection.silence_duration_ms
// to something a detector can act on; zero selects the default.
func realtimeSilenceDuration(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	d := time.Duration(ms) * time.Millisecond
	if d < inboundSilenceMin {
		return inboundSilenceMin
	}
	if d > inboundSilenceMax {
		return inboundSilenceMax
	}
	return d
}

// forwardRemoteAudio decodes the caller's track and feeds it to recognition.
func forwardRemoteAudio(ctx context.Context, remote *webrtc.TrackRemote, session *realtime.Session) {
	codec := strings.ToLower(remote.Codec().MimeType)
	// The decoder is asked for mono at 48kHz: recognition takes mono, and
	// letting the decoder do the mixing avoids a second pass over the samples.
	decoder, err := opus.NewDecoderWithOutput(opusSampleRate, 1)
	if err != nil {
		log.Printf("REALTIME: opus decoder: %v", err)
		return
	}
	// An Opus packet carries at most 120ms of audio.
	samples := make([]int16, opusSampleRate/1000*120)
	pcm := make([]byte, 0, len(samples)*2)

	// Nothing in the protocol says when an utterance ends, so the server has to
	// hear it: the turn is committed once the caller falls silent, or once it
	// has run long enough that waiting for a pause is no longer worth it.
	turn := newInboundTurn(realtimeSilenceDuration(session.TurnSilenceMS()))
	ticks := time.NewTicker(inboundTurnTick)
	defer ticks.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticks.C:
				if !turn.due(now) {
					continue
				}
				log.Printf("REALTIME: call %s turn ended %s", session.ID, turn.report())
				if err := session.CommitTurn(); err != nil {
					return
				}
			}
		}
	}()

	// The call is half-duplex: while the reply plays, the microphone is not
	// listened to. Browsers cancel echo, but not well enough that the reply
	// coming back through the room stays under the speech threshold, and a
	// turn made of the session's own words is worse than a missed interruption
	// -- it is transcribed, answered, and the answer is to nothing the caller
	// said. Barge-in needs echo cancellation the server can trust, which it
	// does not have.
	var quietUntil time.Time

	for {
		if ctx.Err() != nil {
			return
		}
		packet, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		if len(packet.Payload) == 0 {
			continue
		}
		var frame []byte
		switch {
		case strings.Contains(codec, "opus"):
			// DecodeToInt16 reports how many samples the packet actually held;
			// using the whole buffer instead would feed recognition up to 120ms
			// of stale audio for every 20ms packet.
			count, err := decoder.DecodeToInt16(packet.Payload, samples)
			if err != nil || count == 0 {
				continue
			}
			pcm = pcm[:0]
			for _, sample := range samples[:count] {
				pcm = append(pcm, byte(uint16(sample)&0xFF), byte(uint16(sample)>>8))
			}
			frame = realtime.ResamplePCM16(pcm, opusSampleRate, realtime.DefaultInputSampleRate)
		case strings.Contains(codec, "pcmu"):
			frame = realtime.ResamplePCM16(
				realtime.MuLawToPCM16(packet.Payload), realtime.PCMUSampleRate, realtime.DefaultInputSampleRate)
		default:
			continue
		}
		if len(frame) == 0 {
			continue
		}
		now := time.Now()
		if session.Speaking() {
			quietUntil = now.Add(inboundPlayoutTail)
			turn.abandon()
			continue
		}
		if now.Before(quietUntil) {
			continue
		}
		turn.observe(frame, now)
		if err := session.HandleAudio(frame); err != nil {
			return
		}
	}
}

package server

import (
	"fmt"
	"strings"

	"github.com/pion/opus"
	"github.com/pion/webrtc/v4"

	"github.com/opencsgs/csglite/internal/realtime"
)

// outboundOpusBitrate is what the reply is encoded at. Synthesised speech is
// one voice in a quiet room, which is the case SILK is tuned for; measured
// against real synthesis, 24kbps reproduced the waveform's level exactly and
// costs a third of what G.711 spends on telephone bandwidth.
const outboundOpusBitrate = 24000

// opusPayloadType and pcmuPayloadType are the static assignments the browsers
// use. Opus has no reserved number, but 111 is what every WebRTC stack offers.
const (
	opusPayloadType = 111
	pcmuPayloadType = 0
)

// opusCapability is the codec line every browser offers for audio. The reply is
// mono, which a stereo-declared stream carries without trouble -- an Opus packet
// states its own channel count -- and declaring it the way the offer does is
// what makes the two match.
var opusCapability = webrtc.RTPCodecCapability{
	MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	SDPFmtpLine: "minptime=10;useinbandfec=1",
}

var pcmuCapability = webrtc.RTPCodecCapability{
	MimeType: webrtc.MimeTypePCMU, ClockRate: 8000, Channels: 1,
}

// outboundCodec encodes synthesised audio for the media track. It exists
// because the two codecs differ in every dimension the sender cares about --
// the rate the PCM has to be resampled to, how many bytes make up 20ms, and how
// those bytes are packetised -- and the sender should not know which one it
// holds.
type outboundCodec struct {
	capability webrtc.RTPCodecCapability
	// sampleRate is what the synthesised PCM is resampled to before encoding.
	sampleRate int
	// frameBytes is 20ms of PCM16 at sampleRate.
	frameBytes int

	// encode turns one frame of PCM16 into one packet. Opus is stateful and
	// must see the frames in order, so callers serialise their calls.
	encode func(pcm []byte) ([]byte, error)
}

func frameBytesAt(sampleRate int) int {
	return sampleRate / (1000 / outboundFrameMS) * 2
}

// newPCMUOutbound is the fallback: G.711 mu-law at 8kHz, which every WebRTC
// implementation supports and which costs the reply its bandwidth above 4kHz.
func newPCMUOutbound() *outboundCodec {
	return &outboundCodec{
		capability: pcmuCapability,
		sampleRate: realtime.PCMUSampleRate,
		frameBytes: frameBytesAt(realtime.PCMUSampleRate),
		encode: func(pcm []byte) ([]byte, error) {
			return realtime.PCM16ToMuLaw(pcm), nil
		},
	}
}

// newOpusOutbound encodes at 48kHz, so synthesis at 24kHz reaches the caller
// without the drop to telephone bandwidth that G.711 forces on it.
func newOpusOutbound() (*outboundCodec, error) {
	encoder, err := opus.NewEncoder(
		opus.WithSampleRate(48000),
		opus.WithChannels(1),
		// The payload is a single synthesised voice, which is what VoIP mode
		// is for; music mode would spend the bitrate on bandwidth this audio
		// does not carry.
		opus.WithApplication(opus.ApplicationVoIP),
		opus.WithBitrate(outboundOpusBitrate),
	)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	// One packet per frame, and a frame is 20ms; the ceiling is the largest an
	// Opus packet may be.
	packet := make([]byte, 1500)
	return &outboundCodec{
		capability: opusCapability,
		sampleRate: 48000,
		frameBytes: frameBytesAt(48000),
		encode: func(pcm []byte) ([]byte, error) {
			n, err := encoder.Encode(pcm, packet)
			if err != nil {
				return nil, err
			}
			// The buffer is reused for the next frame, so the packet has to be
			// copied out: the track holds on to what it is given.
			return append([]byte(nil), packet[:n]...), nil
		},
	}, nil
}

// offerHasOpus reports whether the caller's offer lists Opus for audio. An
// offer that does not is answered in G.711, which is the only other codec this
// build can produce.
func offerHasOpus(offerSDP string) bool {
	parsed, err := (&webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}).Unmarshal()
	if err != nil {
		// Unparseable here means unparseable later too, where it is reported
		// properly; guessing no leaves the answer on the codec that always
		// works.
		return false
	}
	for _, media := range parsed.MediaDescriptions {
		if !strings.EqualFold(media.MediaName.Media, "audio") {
			continue
		}
		for _, attr := range media.Attributes {
			if attr.Key == "rtpmap" && strings.Contains(strings.ToLower(attr.Value), "opus/") {
				return true
			}
		}
	}
	return false
}

// newOutboundCodec picks what to send the caller: Opus when they offered it,
// G.711 otherwise. An Opus encoder that will not start is not fatal -- the
// fallback still speaks, just narrowly.
func newOutboundCodec(offerSDP string) *outboundCodec {
	if !offerHasOpus(offerSDP) {
		return newPCMUOutbound()
	}
	codec, err := newOpusOutbound()
	if err != nil {
		return newPCMUOutbound()
	}
	return codec
}

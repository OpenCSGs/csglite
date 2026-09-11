package realtime

// G.711 mu-law coding. The outbound WebRTC track uses PCMU rather than Opus
// because the released pion/opus exposes only a decoder; encoding Opus would
// need either an unreleased dependency or cgo, and the release binaries are
// built with CGO_ENABLED=0. PCMU is mandatory-to-implement in browsers, so the
// trade is telephone-band audio for a pure Go path. Swapping in Opus later is a
// change to this file and the negotiated codec, nothing else.

const (
	// PCMUSampleRate is fixed by the codec.
	PCMUSampleRate = 8000
	muLawBias      = 0x84
	muLawClip      = 32635
)

// muLawEncodeTable maps the top bits of a magnitude to its mu-law exponent.
var muLawExponent = [256]byte{
	0, 0, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 3,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
}

// EncodeMuLaw converts one signed 16-bit sample to mu-law.
func EncodeMuLaw(sample int16) byte {
	sign := byte(0)
	value := int(sample)
	if value < 0 {
		value = -value
		sign = 0x80
	}
	if value > muLawClip {
		value = muLawClip
	}
	value += muLawBias
	exponent := muLawExponent[(value>>7)&0xFF]
	mantissa := (value >> (int(exponent) + 3)) & 0x0F
	return ^(sign | (exponent << 4) | byte(mantissa))
}

// PCM16ToMuLaw converts little-endian PCM16 bytes to mu-law bytes.
func PCM16ToMuLaw(pcm []byte) []byte {
	out := make([]byte, 0, len(pcm)/2)
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		out = append(out, EncodeMuLaw(sample))
	}
	return out
}

// DecodeMuLaw converts one mu-law byte back to a signed 16-bit sample. It is
// the inverse of EncodeMuLaw, used for inbound tracks that negotiated PCMU.
func DecodeMuLaw(value byte) int16 {
	value = ^value
	sign := value & 0x80
	exponent := int((value >> 4) & 0x07)
	mantissa := int(value & 0x0F)
	// The bias the encoder added is what carries the segment's implicit leading
	// bit, so it is re-added before the shift and removed after it.
	magnitude := ((mantissa << 3) + muLawBias) << exponent
	magnitude -= muLawBias
	if sign != 0 {
		return int16(-magnitude)
	}
	return int16(magnitude)
}

// MuLawToPCM16 converts mu-law bytes to little-endian PCM16 bytes.
func MuLawToPCM16(payload []byte) []byte {
	out := make([]byte, 0, len(payload)*2)
	for _, value := range payload {
		sample := DecodeMuLaw(value)
		out = append(out, byte(uint16(sample)&0xFF), byte(uint16(sample)>>8))
	}
	return out
}

// ResamplePCM16 converts mono PCM16 between sample rates with linear
// interpolation. It is adequate for speech at these rates and keeps the audio
// path free of a resampling dependency.
func ResamplePCM16(pcm []byte, from, to int) []byte {
	if from == to || from <= 0 || to <= 0 || len(pcm) < 2 {
		return pcm
	}
	samples := len(pcm) / 2
	at := func(i int) int32 {
		if i < 0 {
			i = 0
		}
		if i >= samples {
			i = samples - 1
		}
		return int32(int16(uint16(pcm[2*i]) | uint16(pcm[2*i+1])<<8))
	}
	outCount := samples * to / from
	out := make([]byte, 0, outCount*2)
	for i := 0; i < outCount; i++ {
		pos := float64(i) * float64(from) / float64(to)
		base := int(pos)
		frac := pos - float64(base)
		value := float64(at(base))*(1-frac) + float64(at(base+1))*frac
		sample := int16(value)
		out = append(out, byte(uint16(sample)&0xFF), byte(uint16(sample)>>8))
	}
	return out
}

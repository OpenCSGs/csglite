package realtime

import "testing"

// Round-tripping through mu-law must preserve sign and rough magnitude: a codec
// that inverts or clips the waveform would still produce audio, just the wrong
// audio, so silence and polarity are checked explicitly.
func TestEncodeMuLawPreservesSignAndMagnitude(t *testing.T) {
	cases := []int16{0, 1, -1, 100, -100, 1000, -1000, 16000, -16000, 32767, -32768}
	for _, sample := range cases {
		encoded := EncodeMuLaw(sample)
		decoded := decodeMuLaw(encoded)
		if sample > 200 && decoded <= 0 {
			t.Errorf("sample %d decoded to %d; sign lost", sample, decoded)
		}
		if sample < -200 && decoded >= 0 {
			t.Errorf("sample %d decoded to %d; sign lost", sample, decoded)
		}
		// mu-law is lossy by design; 12% of full scale bounds the companding
		// error across the range.
		if diff := int(sample) - int(decoded); diff > 4000 || diff < -4000 {
			t.Errorf("sample %d decoded to %d; error %d exceeds the companding budget", sample, decoded, diff)
		}
	}
}

// decodeMuLaw is the standard inverse, used only to check the encoder.
func decodeMuLaw(value byte) int16 {
	value = ^value
	sign := value & 0x80
	exponent := (value >> 4) & 0x07
	mantissa := value & 0x0F
	magnitude := (int(mantissa) << 3) + muLawBias
	magnitude <<= exponent
	magnitude -= muLawBias
	if sign != 0 {
		return int16(-magnitude)
	}
	return int16(magnitude)
}

func TestPCM16ToMuLawHalvesTheLength(t *testing.T) {
	pcm := make([]byte, 320)
	if got := len(PCM16ToMuLaw(pcm)); got != 160 {
		t.Fatalf("length = %d, want 160", got)
	}
	// An odd trailing byte is an incomplete sample and must be ignored rather
	// than read past the end.
	if got := len(PCM16ToMuLaw(make([]byte, 321))); got != 160 {
		t.Fatalf("length = %d for an odd input, want 160", got)
	}
}

func TestResamplePCM16(t *testing.T) {
	// 24 kHz to 8 kHz is the conversion the outbound track performs.
	pcm := make([]byte, 2*2400)
	out := ResamplePCM16(pcm, 24000, 8000)
	if want := 2 * 800; len(out) != want {
		t.Fatalf("length = %d, want %d", len(out), want)
	}
	if same := ResamplePCM16(pcm, 8000, 8000); len(same) != len(pcm) {
		t.Fatalf("a same-rate conversion changed the length")
	}
	if len(ResamplePCM16(nil, 24000, 8000)) != 0 {
		t.Fatal("empty input must stay empty")
	}
}

// A constant tone must survive resampling with its amplitude intact, which
// catches an interpolation that reads the wrong neighbour.
func TestResamplePreservesAConstantSignal(t *testing.T) {
	const value int16 = 12000
	pcm := make([]byte, 0, 2*2400)
	for i := 0; i < 2400; i++ {
		pcm = append(pcm, byte(uint16(value)&0xFF), byte(uint16(value)>>8))
	}
	out := ResamplePCM16(pcm, 24000, 8000)
	for i := 0; i+1 < len(out); i += 2 {
		got := int16(uint16(out[i]) | uint16(out[i+1])<<8)
		if got < value-2 || got > value+2 {
			t.Fatalf("sample %d = %d, want about %d", i/2, got, value)
		}
	}
}

// Mu-law is lossy by design, but a decode of an encode must land close to the
// original sample; a sign or exponent mistake would show up as a large error.
func TestMuLawRoundTripStaysClose(t *testing.T) {
	for _, sample := range []int16{0, 1, -1, 100, -100, 1000, -1000, 8000, -8000, 32000, -32000} {
		decoded := DecodeMuLaw(EncodeMuLaw(sample))
		// Samples inside the first quantisation step legitimately decode to
		// zero, so only a genuine sign flip is an error.
		if decoded != 0 && (sample < 0) != (decoded < 0) {
			t.Errorf("DecodeMuLaw(EncodeMuLaw(%d)) = %d, sign flipped", sample, decoded)
		}
		diff := int(sample) - int(decoded)
		if diff < 0 {
			diff = -diff
		}
		// Mu-law quantisation error grows with magnitude; 8% of full scale
		// bounds it for every segment.
		if diff > 2600 {
			t.Errorf("DecodeMuLaw(EncodeMuLaw(%d)) = %d, off by %d", sample, decoded, diff)
		}
	}
}

func TestMuLawToPCM16DoublesTheLength(t *testing.T) {
	pcm := MuLawToPCM16([]byte{0x00, 0x7F, 0xFF, 0x80})
	if len(pcm) != 8 {
		t.Fatalf("length = %d, want 8", len(pcm))
	}
	// 0xFF is mu-law silence; it must decode to a near-zero sample.
	silence := int16(uint16(pcm[4]) | uint16(pcm[5])<<8)
	if silence > 8 || silence < -8 {
		t.Fatalf("0xFF decoded to %d, want near zero", silence)
	}
}

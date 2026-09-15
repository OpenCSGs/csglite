package server

import (
	"math"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/realtime"
)

// realtimeTurnFrameSamples is 20ms at the session's input rate.
const realtimeTurnFrameSamples = realtimeTurnFrameMS * 24000 / 1000

const realtimeTurnFrameMS = 20

// turnFrame builds one frame of mono PCM16 whose RMS is level: a square wave,
// so every sample carries it.
func turnFrame(level float64) []byte {
	pcm := make([]byte, realtimeTurnFrameSamples*2)
	value := int16(math.Round(level))
	for i := 0; i < realtimeTurnFrameSamples; i++ {
		v := value
		if i%2 == 1 {
			v = -value
		}
		pcm[i*2] = byte(uint16(v) & 0xFF)
		pcm[i*2+1] = byte(uint16(v) >> 8)
	}
	return pcm
}

// feed pushes level for span, one frame at a time, asking the turn whether it
// is due after each one. It reports when the first commit landed.
func feed(t *inboundTurn, start time.Time, level float64, span time.Duration) (time.Duration, bool) {
	at, _, committed := feedCounting(t, start, level, span)
	return at, committed
}

// feedCounting is feed, also reporting how many commits the whole span produced.
func feedCounting(t *inboundTurn, start time.Time, level float64, span time.Duration) (time.Duration, int, bool) {
	const step = realtimeTurnFrameMS * time.Millisecond
	first, commits := span, 0
	for at := time.Duration(0); at < span; at += step {
		now := start.Add(at)
		t.observe(turnFrame(level), now)
		if t.due(now) {
			if commits == 0 {
				first = at
			}
			commits++
		}
	}
	return first, commits, commits > 0
}

// settle runs the floor tracker over the room for long enough that it has found
// it, ignoring whatever the unsettled start committed. A call opening into a
// noisy room cannot tell the room from a speaker until it has heard both, and
// what it commits in the meantime is noise the recogniser answers as empty.
func settle(t *inboundTurn, start time.Time, level float64, span time.Duration) time.Time {
	feedCounting(t, start, level, span)
	return start.Add(span)
}

func TestInboundTurnCommitsWhenTheSpeakerStops(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)

	if _, committed := feed(turn, start, 6000, 900*time.Millisecond); committed {
		t.Fatal("committed while the caller was still speaking")
	}

	silence := start.Add(900 * time.Millisecond)
	after, committed := feed(turn, silence, 90, 2*time.Second)
	if !committed {
		t.Fatal("silence after speech never committed the turn")
	}
	if after > inboundSilenceCommit+100*time.Millisecond {
		t.Fatalf("commit took %s after the speaker stopped, want about %s", after, inboundSilenceCommit)
	}
}

func TestInboundTurnIgnoresARoomThatIsOnlyNoisy(t *testing.T) {
	turn := &inboundTurn{}
	// Well above speechFloorRMS, so only the tracked floor tells it from speech.
	start := settle(turn, time.Unix(0, 0), 900, 3*time.Second)

	if _, commits, _ := feedCounting(turn, start, 900, 30*time.Second); commits != 0 {
		t.Fatalf("steady room noise was committed as speech %d times", commits)
	}
}

func TestInboundTurnHearsSpeechOverANoisyRoom(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 900, 3*time.Second)

	if _, committed := feed(turn, start, 9000, time.Second); committed {
		t.Fatal("committed mid-utterance")
	}
	if _, committed := feed(turn, start.Add(time.Second), 900, 2*time.Second); !committed {
		t.Fatal("the pause after speech did not commit the turn")
	}
}

func TestInboundTurnBoundsAnUnbrokenTurn(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)

	// Speech that never pauses: only the cap can end this, and the floor must
	// not climb into it and cut the speaker off early.
	after, committed := feed(turn, start, 6000, inboundMaxTurn+2*time.Second)
	if !committed {
		t.Fatal("an unbroken turn was never committed")
	}
	if after < inboundMaxTurn {
		t.Fatalf("committed after %s, before the %s cap", after, inboundMaxTurn)
	}
	if after > inboundMaxTurn+time.Second {
		t.Fatalf("committed after %s, well past the %s cap", after, inboundMaxTurn)
	}
}

func TestInboundTurnCommitsWhenTheSenderStopsTransmitting(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)

	if _, committed := feed(turn, start, 6000, 300*time.Millisecond); committed {
		t.Fatal("committed while the caller was still speaking")
	}
	// Discontinuous transmission: no further frames arrive at all, so nothing
	// refreshes the last speech and the same silence rule closes the turn.
	last := start.Add(280 * time.Millisecond)
	if turn.due(last.Add(inboundSilenceCommit - 50*time.Millisecond)) {
		t.Fatal("committed before the silence was long enough")
	}
	if !turn.due(last.Add(inboundSilenceCommit)) {
		t.Fatal("a sender that stopped transmitting never ended its turn")
	}
	if turn.due(last.Add(time.Minute)) {
		t.Fatal("committed again with no audio since")
	}
}

func TestInboundTurnDoesNotAgeOutOnRoomNoiseAlone(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)

	// A call that sits unspoken-into for much longer than the cap must not then
	// cut off the first sentence it does hear.
	if _, commits, _ := feedCounting(turn, start, 90, 2*inboundMaxTurn); commits != 0 {
		t.Fatalf("an idle call committed %d times", commits)
	}
	spoken := start.Add(2 * inboundMaxTurn)
	if _, committed := feed(turn, spoken, 6000, 2*time.Second); committed {
		t.Fatal("the idle stretch counted against the turn that followed it")
	}
	if _, committed := feed(turn, spoken.Add(2*time.Second), 90, 2*time.Second); !committed {
		t.Fatal("the turn never committed once the speaker stopped")
	}
}

func TestInboundTurnCommitsOncePerTurn(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)

	if _, committed := feed(turn, start, 6000, 300*time.Millisecond); committed {
		t.Fatal("committed mid-utterance")
	}
	silence := start.Add(300 * time.Millisecond)
	if _, commits, committed := feedCounting(turn, silence, 0, 4*time.Second); !committed || commits != 1 {
		// The recogniser's buffer is empty after the first commit; committing it
		// again answers the caller with an empty transcript for audio they never
		// spoke.
		t.Fatalf("got %d commits over one utterance, want 1", commits)
	}
}

func TestRMSPCM16ReadsTheSampleScale(t *testing.T) {
	if got := rmsPCM16(nil); got != -1 {
		t.Fatalf("empty frame: got %v, want -1", got)
	}
	if got := rmsPCM16([]byte{0x01}); got != -1 {
		t.Fatalf("partial sample: got %v, want -1", got)
	}
	if got := rmsPCM16(turnFrame(1000)); math.Abs(got-1000) > 1 {
		t.Fatalf("got %v, want about 1000", got)
	}
}

func TestInboundTurnIgnoresImpulseNoise(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)

	// A key press or a knock: loud, but over before a syllable would be.
	at := start
	for i := 0; i < 20; i++ {
		feed(turn, at, 9000, 60*time.Millisecond)
		at = at.Add(60 * time.Millisecond)
		feed(turn, at, 90, 400*time.Millisecond)
		at = at.Add(400 * time.Millisecond)
	}
	if _, commits, _ := feedCounting(turn, at, 90, 3*time.Second); commits != 0 {
		t.Fatalf("clicks opened %d turns the recogniser would answer empty", commits)
	}

	// Speech still gets through: the run only has to outlast speechOnset.
	if _, committed := feed(turn, at, 9000, 600*time.Millisecond); committed {
		t.Fatal("committed mid-utterance")
	}
	if _, committed := feed(turn, at.Add(600*time.Millisecond), 90, 2*time.Second); !committed {
		t.Fatal("real speech was debounced away")
	}
}

func TestInboundTurnHonoursTheConfiguredSilence(t *testing.T) {
	turn := newInboundTurn(300 * time.Millisecond)
	start := settle(turn, time.Unix(0, 0), 90, time.Second)
	feed(turn, start, 6000, 500*time.Millisecond)
	after, committed := feed(turn, start.Add(500*time.Millisecond), 90, 2*time.Second)
	if !committed || after > 400*time.Millisecond {
		t.Fatalf("committed=%v after %s, want within about 300ms", committed, after)
	}

	// The zero value keeps the default, so tests and callers that do not care
	// need not know about it.
	if got := (&inboundTurn{}).silenceCommit(); got != inboundSilenceCommit {
		t.Fatalf("default silence = %s, want %s", got, inboundSilenceCommit)
	}
}

func TestInboundTurnReportsTheTurnItEnded(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)
	feed(turn, start, 6000, time.Second)
	if _, committed := feed(turn, start.Add(time.Second), 90, 2*time.Second); !committed {
		t.Fatal("never committed")
	}
	r := turn.report()
	if r.speech < 800*time.Millisecond || r.speech > 1100*time.Millisecond {
		t.Fatalf("reported speech %s for a one second utterance", r.speech)
	}
	if math.Abs(r.peak-6000) > 1 || r.noiseFloor > speechFloorRMS {
		t.Fatalf("report = %s, want peak 6000 over a quiet floor", r)
	}
}

func TestInboundTurnAbandonDropsTheOpenTurn(t *testing.T) {
	turn := &inboundTurn{}
	start := settle(turn, time.Unix(0, 0), 90, time.Second)
	feed(turn, start, 6000, 500*time.Millisecond)
	turn.abandon()
	if _, committed := feed(turn, start.Add(500*time.Millisecond), 90, 3*time.Second); committed {
		t.Fatal("an abandoned turn was still committed")
	}
}

func TestRealtimeSilenceDurationIsBounded(t *testing.T) {
	if got := realtimeSilenceDuration(0); got != 0 {
		t.Fatalf("unset: %s, want 0 (default)", got)
	}
	if got := realtimeSilenceDuration(500); got != 500*time.Millisecond {
		t.Fatalf("500ms: got %s", got)
	}
	if got := realtimeSilenceDuration(10); got != inboundSilenceMin {
		t.Fatalf("10ms: got %s, want the floor %s", got, inboundSilenceMin)
	}
	if got := realtimeSilenceDuration(60000); got != inboundSilenceMax {
		t.Fatalf("60s: got %s, want the cap %s", got, inboundSilenceMax)
	}
}

// The session reads its own configuration, since session.update may rewrite it
// while the audio loop is running.
func TestSessionReportsTheConfiguredSilence(t *testing.T) {
	cfg := realtime.SessionConfig{Audio: &realtime.SessionAudio{Input: &realtime.SessionAudioInput{
		TurnDetection: &realtime.TurnDetection{SilenceDurationMS: 450},
	}}}
	session, err := realtime.NewSession(cfg, discardSender{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.TurnSilenceMS(); got != 450 {
		t.Fatalf("TurnSilenceMS() = %d, want 450", got)
	}
	plain, err := realtime.NewSession(realtime.SessionConfig{}, discardSender{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.TurnSilenceMS(); got != 0 {
		t.Fatalf("TurnSilenceMS() = %d for a session with no turn_detection, want 0", got)
	}
}

type discardSender struct{}

func (discardSender) Send(realtime.ServerEvent) error     { return nil }
func (discardSender) SendAudio(realtime.AudioFrame) error { return nil }

package server

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	// inboundSilenceCommit ends the turn once the caller has stopped speaking
	// for this long, unless the session names its own silence_duration_ms. It
	// is the normal way a turn ends: it is what makes the pause at the end of
	// an utterance -- rather than a lull in the packet stream -- the thing the
	// model waits for. 700ms cut sentences at their commas in the field, since
	// a level detector cannot tell a pause for breath from the end of a
	// thought the way a turn model could; 900ms is the usual compromise for
	// detectors that only hear silence.
	inboundSilenceCommit = 900 * time.Millisecond
	// inboundSilenceMin and inboundSilenceMax bound what a session may ask for.
	inboundSilenceMin = 200 * time.Millisecond
	inboundSilenceMax = 3 * time.Second
	// inboundMaxTurn bounds a turn that never contains a pause, so a caller who
	// talks straight through still gets an answer. It sits well above an
	// ordinary utterance and below the recogniser's own buffer cap, so nothing
	// but a detector that has lost the speaker should reach it.
	inboundMaxTurn = 15 * time.Second
	// inboundTurnTick is how often the turn is re-examined. It has to be well
	// under inboundSilenceCommit, or the silence is only noticed a tick late.
	inboundTurnTick = 100 * time.Millisecond
)

const (
	// speechNoiseRatio is how far above the tracked noise floor a frame must
	// sit to count as speech.
	speechNoiseRatio = 2.5
	// speechFloorRMS is the level below which audio is always silence, on the
	// PCM16 sample scale. Browsers apply noise suppression to a microphone
	// track, so their silence is close to zero and a floor tracked from it
	// would make every faint bump clear the ratio.
	speechFloorRMS = 250.0
	// noiseFloorRise is how fast the floor may climb, as a fraction per second,
	// while nothing quieter is heard. A room that gets noisier is followed
	// within a few seconds.
	noiseFloorRise = 0.35
	// maxNoiseFloorRMS caps that climb. Audio this loud is not a room any more,
	// and without a ceiling an utterance with no pause in it would raise its own
	// threshold until the speaker fell below it and was cut off mid-sentence.
	maxNoiseFloorRMS = 2000.0
	// speechOnset is how much loud audio must arrive in an unbroken run before
	// the turn counts as holding speech. A key press, a knock or a breath clears
	// the threshold for a frame or two and would otherwise open a turn that the
	// recogniser then answers with an empty transcript; a spoken syllable does
	// not stop this soon.
	speechOnset = 120 * time.Millisecond
)

// inboundTurn decides when the caller's turn has ended.
//
// The turn used to end only when the RTP stream itself went quiet, which
// assumes the sender stops transmitting during silence. Browsers do not: a live
// microphone keeps sending room noise, so the turn ran on until a gap in the
// packets happened to look like silence -- twelve seconds at a time in
// practice, every second of it added to the wait before the model saw a word.
// Measuring the audio ends the turn when the speaker stops instead.
//
// A sender that does use discontinuous transmission needs nothing special: when
// its packets stop, no louder audio arrives either, and the same silence rule
// closes the turn.
//
// Audio arrives on the track reader while the clock is read from a ticker, so
// every field is guarded.
type inboundTurn struct {
	mu sync.Mutex
	// silence overrides inboundSilenceCommit when set.
	silence time.Duration

	noiseFloor float64
	// peak is the loudest frame of the current turn, kept for the report.
	peak float64
	// last describes the turn most recently ended, for the operator's log.
	last turnReport
	// floorAt is when the floor was last advanced, so its climb is measured in
	// real time rather than in frames, whose length the codec chooses.
	floorAt time.Time
	// speech records whether this turn holds any speech yet. A turn of pure
	// room noise has nothing to recognise and must not be committed, or an idle
	// call would commit on a loop.
	speech bool
	// loudSince is when the current unbroken run of loud audio began, and is
	// cleared by the first quiet frame. Speech is latched only once such a run
	// has lasted speechOnset.
	loudSince time.Time

	started    time.Time
	lastSpeech time.Time
}

// turnReport summarises a turn once it has ended, so the log shows what the
// detector took for speech: how long it lasted, how loud it got, and what it
// judged the room to be at the time.
type turnReport struct {
	speech     time.Duration
	peak       float64
	noiseFloor float64
}

func (r turnReport) String() string {
	return fmt.Sprintf("speech=%s peak=%.0f floor=%.0f", r.speech.Round(10*time.Millisecond), r.peak, r.noiseFloor)
}

// newInboundTurn makes a detector that ends turns after silence of the given
// length; zero means inboundSilenceCommit.
func newInboundTurn(silence time.Duration) *inboundTurn {
	return &inboundTurn{silence: silence}
}

func (t *inboundTurn) silenceCommit() time.Duration {
	if t.silence > 0 {
		return t.silence
	}
	return inboundSilenceCommit
}

// observe takes one decoded frame of mono PCM16 at the session's input rate.
func (t *inboundTurn) observe(pcm []byte, now time.Time) {
	level := rmsPCM16(pcm)
	if level < 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Until something is actually said the turn keeps restarting, so a call left
	// sitting in a noisy room does not spend its whole length cap on the room
	// and then cut off the first sentence spoken into it.
	if !t.speech {
		t.started = now
		t.peak = 0
	}
	t.trackNoiseFloor(level, now)
	if level > t.peak {
		t.peak = level
	}

	if level <= math.Max(speechFloorRMS, t.noiseFloor*speechNoiseRatio) {
		t.loudSince = time.Time{}
		return
	}
	if t.loudSince.IsZero() {
		t.loudSince = now
	}
	if !t.speech && now.Sub(t.loudSince) < speechOnset {
		return
	}
	t.speech = true
	t.lastSpeech = now
}

// trackNoiseFloor follows the quietest audio heard recently: the floor drops to
// any quieter level at once and climbs back only slowly. Tracking the minimum
// this way keeps it on the room even through a long unbroken utterance, which
// an average over recent audio would instead drift up towards until the speaker
// no longer cleared their own threshold.
func (t *inboundTurn) trackNoiseFloor(level float64, now time.Time) {
	if t.floorAt.IsZero() {
		// A call can open in the middle of a sentence, so the first frame is no
		// evidence about the room. Assume a quiet one and climb if it is not:
		// being briefly too sensitive costs one turn of committed noise, while
		// being too deaf costs every turn until the caller pauses.
		t.noiseFloor = math.Min(level, speechFloorRMS)
		t.floorAt = now
		return
	}
	if elapsed := now.Sub(t.floorAt).Seconds(); elapsed > 0 {
		t.noiseFloor = math.Min(t.noiseFloor*math.Exp(noiseFloorRise*elapsed), maxNoiseFloorRMS)
		t.floorAt = now
	}
	if level < t.noiseFloor {
		t.noiseFloor = level
	}
}

// due reports whether the turn should be committed now. It answers true at most
// once per turn: saying yes also starts the next one, since the commit it asks
// for is what clears the recogniser's buffer.
func (t *inboundTurn) due(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started.IsZero() {
		return false
	}
	switch {
	case !t.speech:
		// Nothing has been said. An empty commit costs a round trip through the
		// recogniser and answers with an empty transcript, so an idle call would
		// spend itself recognising its own room.
		return false
	case now.Sub(t.lastSpeech) >= t.silenceCommit(), now.Sub(t.started) >= inboundMaxTurn:
		t.last = turnReport{speech: t.lastSpeech.Sub(t.started), peak: t.peak, noiseFloor: t.noiseFloor}
		t.startTurn()
		return true
	default:
		return false
	}
}

// report describes the turn that due most recently ended.
func (t *inboundTurn) report() turnReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// abandon drops the turn in progress without committing it: the audio it
// covers is not going to be recognised, so there is nothing to end.
func (t *inboundTurn) abandon() {
	t.mu.Lock()
	t.startTurn()
	t.mu.Unlock()
}

// startTurn begins a new turn. The noise floor carries over: it describes the
// room, which does not change because the caller stopped talking.
func (t *inboundTurn) startTurn() {
	t.speech = false
	t.loudSince = time.Time{}
	t.started = time.Time{}
	t.lastSpeech = time.Time{}
}

// rmsPCM16 reports the root-mean-square level of mono PCM16 on the sample
// scale, or -1 when the frame holds no whole sample.
func rmsPCM16(pcm []byte) float64 {
	count := len(pcm) / 2
	if count == 0 {
		return -1
	}
	var sum float64
	for i := 0; i < count*2; i += 2 {
		sample := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		sum += sample * sample
	}
	return math.Sqrt(sum / float64(count))
}

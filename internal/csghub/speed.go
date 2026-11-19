package csghub

import (
	"sort"
	"sync"
	"time"
)

// SpeedMeter turns a monotonic byte counter into a smoothed transfer rate.
// Update may be called from concurrent download goroutines.
//
// Rates are derived from per-interval deltas and reduced with a median, so a
// one-off jump in the counter - a resumed download that finds a file already
// complete, for instance - cannot turn into a bogus multi-GB/s reading.
type SpeedMeter struct {
	mu      sync.Mutex
	window  time.Duration
	last    int64
	lastAt  time.Time
	rate    float64
	samples []speedSample
}

type speedSample struct {
	at    time.Time
	bytes int64
}

const (
	// speedSampleInterval merges bursts of chunk callbacks into a single
	// sample, so a 32 KiB read loop does not create thousands of samples.
	speedSampleInterval = 100 * time.Millisecond
	// speedMinSamples is the number of samples needed before a rate is
	// reported: two intervals, so a lone outlier can never be the median.
	speedMinSamples = 3
	// speedStaleSnapshotAge is how long the counter may stay behind before the
	// meter treats it as a restarted transfer instead of an out-of-order
	// snapshot. An active transfer accepts a sample every speedSampleInterval,
	// so this only fires once nothing has moved for a while.
	speedStaleSnapshotAge = 5 * speedSampleInterval
)

// NewSpeedMeter smooths the rate over window. A non-positive window defaults
// to 3s.
func NewSpeedMeter(window time.Duration) *SpeedMeter {
	if window <= 0 {
		window = 3 * time.Second
	}
	return &SpeedMeter{window: window}
}

// Update records the completed byte count at now and returns the smoothed rate
// in bytes per second. A count that steps back is dropped as a snapshot that a
// concurrent download delivered out of order; a count that stays behind for
// speedStaleSnapshotAge resets the samples, because the transfer restarted.
func (m *SpeedMeter) Update(completed int64, now time.Time) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if completed < m.last {
		if now.Sub(m.lastAt) <= speedStaleSnapshotAge {
			// Concurrent file downloads report the shared total without
			// ordering it, so a step back is a stale snapshot that arrived
			// after a newer one. Dropping it also keeps it from skewing the
			// next interval.
			return m.rate
		}
		// Nothing has been accepted for a while and the counter is still
		// behind, so the transfer restarted.
		m.samples = m.samples[:0]
		m.rate = 0
	}
	m.last = completed

	if !m.lastAt.IsZero() {
		gap := now.Sub(m.lastAt)
		if gap > m.window {
			// The counter was idle for longer than the window. Start over so
			// the pause is not averaged into the first reading after it ends.
			m.samples = m.samples[:0]
			m.rate = 0
		} else if gap < speedSampleInterval {
			// Too soon for a new sample; these bytes fold into the next one.
			return m.rate
		}
	}

	m.samples = append(m.samples, speedSample{at: now, bytes: completed})
	m.lastAt = now

	// Keep one sample at or before the cutoff as the baseline so the rate
	// always spans a full window once enough history exists.
	cutoff := now.Add(-m.window)
	for len(m.samples) > 2 && !m.samples[1].at.After(cutoff) {
		m.samples = m.samples[1:]
	}
	if len(m.samples) < speedMinSamples {
		m.rate = 0
		return 0
	}

	rates := make([]float64, 0, len(m.samples)-1)
	for i := 1; i < len(m.samples); i++ {
		elapsed := m.samples[i].at.Sub(m.samples[i-1].at).Seconds()
		if elapsed <= 0 {
			continue
		}
		rates = append(rates, float64(m.samples[i].bytes-m.samples[i-1].bytes)/elapsed)
	}
	if len(rates) == 0 {
		m.rate = 0
		return 0
	}
	sort.Float64s(rates)
	// The lower median keeps a single fast interval from inflating the rate.
	m.rate = rates[(len(rates)-1)/2]
	return m.rate
}

// Rate returns the current rate without recording a sample. It reports zero
// once the last sample is older than the window, so a stalled transfer stops
// showing the speed it had before it stalled.
func (m *SpeedMeter) Rate(now time.Time) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastAt.IsZero() || now.Sub(m.lastAt) > m.window {
		return 0
	}
	return m.rate
}

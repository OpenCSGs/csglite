package csghub

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	speedMiB = 1 << 20
	speedGiB = 1 << 30
)

func almostEqual(got, want float64) bool {
	return math.Abs(got-want) <= math.Max(1, math.Abs(want)/1000)
}

func TestSpeedMeterNeedsThreeSamples(t *testing.T) {
	m := NewSpeedMeter(3 * time.Second)
	now := time.Unix(0, 0)
	if rate := m.Update(0, now); rate != 0 {
		t.Fatalf("first sample rate = %v, want 0", rate)
	}
	if rate := m.Update(1<<20, now.Add(200*time.Millisecond)); rate != 0 {
		t.Fatalf("second sample rate = %v, want 0", rate)
	}
	if rate := m.Update(2<<20, now.Add(400*time.Millisecond)); !almostEqual(rate, 5*speedMiB) {
		t.Fatalf("rate = %v, want 5 MiB/s", rate)
	}
}

func TestSpeedMeterAveragesOverWindow(t *testing.T) {
	m := NewSpeedMeter(2 * time.Second)
	start := time.Unix(0, 0)
	for i := int64(0); i <= 5; i++ {
		m.Update(i*speedMiB, start.Add(time.Duration(i)*time.Second))
	}
	// Samples older than the window are pruned, so the rate stays at the
	// steady 1 MiB/s instead of averaging in the warm-up period.
	if rate := m.Update(6*speedMiB, start.Add(6*time.Second)); !almostEqual(rate, speedMiB) {
		t.Fatalf("rate = %v, want 1 MiB/s", rate)
	}
}

func TestSpeedMeterDropsOutOfOrderSnapshots(t *testing.T) {
	m := NewSpeedMeter(time.Second)
	start := time.Unix(0, 0)
	for i := int64(0); i <= 5; i++ {
		m.Update(i*speedMiB, start.Add(time.Duration(i)*100*time.Millisecond))
	}
	if rate := m.Rate(start.Add(500 * time.Millisecond)); !almostEqual(rate, 10*speedMiB) {
		t.Fatalf("rate = %v, want 10 MiB/s", rate)
	}

	// Three concurrent files delivered their snapshots in reverse order. Both
	// stale snapshots arrive long before the staleness window, so neither may
	// clear the history.
	for _, stale := range []int64{4, 3} {
		if rate := m.Update(stale*speedMiB, start.Add(600*time.Millisecond)); !almostEqual(rate, 10*speedMiB) {
			t.Fatalf("rate after stale snapshot = %v, want 10 MiB/s", rate)
		}
	}
	if len(m.samples) != 6 {
		t.Fatalf("samples = %d, want both stale snapshots dropped", len(m.samples))
	}

	// The next in-order sample is accepted and keeps the rate steady.
	if rate := m.Update(6*speedMiB, start.Add(700*time.Millisecond)); !almostEqual(rate, 10*speedMiB) {
		t.Fatalf("rate after recovery = %v, want 10 MiB/s", rate)
	}
	if len(m.samples) != 7 {
		t.Fatalf("samples after recovery = %d, want 7", len(m.samples))
	}
}

func TestSpeedMeterResetsWhenCounterStaysBehind(t *testing.T) {
	m := NewSpeedMeter(time.Second)
	start := time.Unix(0, 0)
	m.Update(0, start)
	m.Update(2*speedMiB, start.Add(100*time.Millisecond))

	// A step back right after an accepted sample is a stale snapshot.
	if rate := m.Update(0, start.Add(200*time.Millisecond)); rate != 0 {
		t.Fatalf("rate after one step back = %v, want the unchanged 0", rate)
	}
	if len(m.samples) != 2 {
		t.Fatalf("samples after one step back = %d, want 2", len(m.samples))
	}

	// Still behind after the staleness window: the transfer restarted.
	if rate := m.Update(1*speedMiB, start.Add(700*time.Millisecond)); rate != 0 {
		t.Fatalf("rate after restart = %v, want 0", rate)
	}
	if len(m.samples) != 1 {
		t.Fatalf("samples after restart = %d, want 1", len(m.samples))
	}
	if rate := m.Update(2*speedMiB, start.Add(800*time.Millisecond)); rate != 0 {
		t.Fatalf("rate with two fresh samples = %v, want 0", rate)
	}
	if rate := m.Update(3*speedMiB, start.Add(900*time.Millisecond)); !almostEqual(rate, 10*speedMiB) {
		t.Fatalf("rate after restart = %v, want 10 MiB/s", rate)
	}
}

// A resumed download reports files that are already complete as one large
// jump. That is not transfer, so it must not turn into a huge reading.
func TestSpeedMeterIgnoresCompletedFileJump(t *testing.T) {
	m := NewSpeedMeter(3 * time.Second)
	start := time.Unix(0, 0)
	step := 100 * time.Millisecond
	var completed int64
	for i := 1; i <= 10; i++ {
		completed += speedMiB
		if rate := m.Update(completed, start.Add(time.Duration(i)*step)); rate > 11*speedMiB {
			t.Fatalf("steady rate = %v, want ~10 MiB/s", rate)
		}
	}

	completed += speedGiB
	if rate := m.Update(completed, start.Add(1100*time.Millisecond)); rate > 11*speedMiB {
		t.Fatalf("rate after completed-file jump = %v, want ~10 MiB/s", rate)
	}

	for i := 12; i <= 20; i++ {
		completed += speedMiB
		if rate := m.Update(completed, start.Add(time.Duration(i)*step)); rate > 11*speedMiB {
			t.Fatalf("rate after jump = %v, want ~10 MiB/s", rate)
		}
	}
}

func TestSpeedMeterMergesRapidUpdates(t *testing.T) {
	m := NewSpeedMeter(3 * time.Second)
	start := time.Unix(0, 0)
	var completed int64
	for i := 1; i <= 300; i++ {
		completed += 10 << 10
		at := start.Add(time.Duration(i) * time.Millisecond)
		if rate := m.Update(completed, at); rate > 11*speedMiB {
			t.Fatalf("rate = %v, want ~10 MiB/s", rate)
		}
	}
	if rate := m.Rate(start.Add(300 * time.Millisecond)); rate < 9*speedMiB || rate > 11*speedMiB {
		t.Fatalf("rate = %v, want ~10 MiB/s", rate)
	}
}

func TestSpeedMeterDecaysWhenStalled(t *testing.T) {
	m := NewSpeedMeter(time.Second)
	start := time.Unix(0, 0)
	for i := int64(1); i <= 5; i++ {
		m.Update(i*speedMiB, start.Add(time.Duration(i)*100*time.Millisecond))
	}
	if rate := m.Rate(start.Add(500 * time.Millisecond)); !almostEqual(rate, 10*speedMiB) {
		t.Fatalf("rate = %v, want 10 MiB/s", rate)
	}
	if rate := m.Rate(start.Add(500*time.Millisecond + time.Second + time.Millisecond)); rate != 0 {
		t.Fatalf("rate while stalled = %v, want 0", rate)
	}

	// The first update after a pause starts a fresh window instead of
	// averaging the pause into the reading.
	if rate := m.Update(6*speedMiB, start.Add(2500*time.Millisecond)); rate != 0 {
		t.Fatalf("rate after pause = %v, want 0", rate)
	}
	if rate := m.Update(7*speedMiB, start.Add(2600*time.Millisecond)); rate != 0 {
		t.Fatalf("rate after pause = %v, want 0", rate)
	}
	if rate := m.Update(8*speedMiB, start.Add(2700*time.Millisecond)); !almostEqual(rate, 10*speedMiB) {
		t.Fatalf("rate after pause = %v, want 10 MiB/s", rate)
	}
}

func TestSpeedMeterDefaultsWindow(t *testing.T) {
	m := NewSpeedMeter(0)
	if m.window != 3*time.Second {
		t.Fatalf("window = %v, want 3s", m.window)
	}
}

func TestSpeedMeterKeepsHistoryAtWindowBoundary(t *testing.T) {
	m := NewSpeedMeter(time.Second)
	start := time.Unix(0, 0)
	m.Update(0, start)
	m.Update(speedMiB, start.Add(time.Second))
	if len(m.samples) != 2 {
		t.Fatalf("samples at gap == window = %d, want 2", len(m.samples))
	}
	m.Update(2*speedMiB, start.Add(2*time.Second+time.Nanosecond))
	if len(m.samples) != 1 {
		t.Fatalf("samples after gap > window = %d, want 1", len(m.samples))
	}
}

func TestSpeedMeterDropsToZeroWhenCounterStalls(t *testing.T) {
	m := NewSpeedMeter(time.Second)
	start := time.Unix(0, 0)
	var completed int64
	for i := 0; i <= 5; i++ {
		completed += speedMiB
		m.Update(completed, start.Add(time.Duration(i)*100*time.Millisecond))
	}
	for i := 6; i <= 10; i++ {
		m.Update(completed, start.Add(time.Duration(i)*100*time.Millisecond))
	}
	if rate := m.Rate(start.Add(time.Second)); rate != 0 {
		t.Fatalf("rate while the counter stalls = %v, want 0", rate)
	}
}

func TestSpeedMeterConcurrentUpdates(t *testing.T) {
	m := NewSpeedMeter(3 * time.Second)
	var completed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				now := time.Now()
				m.Update(completed.Add(1<<10), now)
				_ = m.Rate(now)
			}
		}()
	}
	wg.Wait()
}

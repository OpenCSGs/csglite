package keygov

import (
	"sync"
	"time"
)

const rateWindow = time.Minute

// Limiter counts requests and tokens per subject over the last minute. It
// lives in memory, so a restart forgets the window, which errs on the side
// of letting requests through.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	requests []time.Time
	tokens   []tokenMark
}

type tokenMark struct {
	at     time.Time
	tokens int64
}

func NewLimiter() *Limiter {
	return &Limiter{windows: map[string]*window{}}
}

// Allow admits one request for subject unless that would pass rpm, or the
// tokens already used in the window reached tpm. A zero cap is not checked.
// When refused it says how long until the window has room.
func (l *Limiter) Allow(subject string, rpm, tpm int, now time.Time) (bool, time.Duration) {
	if l == nil || (rpm <= 0 && tpm <= 0) {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[subject]
	if w == nil {
		w = &window{}
		l.windows[subject] = w
	}
	w.trim(now)
	if rpm > 0 && len(w.requests) >= rpm {
		return false, w.requests[len(w.requests)-rpm].Add(rateWindow).Sub(now)
	}
	if tpm > 0 {
		var used int64
		for _, mark := range w.tokens {
			used += mark.tokens
		}
		if used >= int64(tpm) {
			// Room comes back as the oldest tokens leave the window.
			return false, w.tokens[0].at.Add(rateWindow).Sub(now)
		}
	}
	w.requests = append(w.requests, now)
	return true, 0
}

// AddTokens counts tokens a finished request used. Subjects without a rate
// limit are not tracked.
func (l *Limiter) AddTokens(subject string, tokens int64, now time.Time) {
	if l == nil || tokens <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[subject]
	if w == nil {
		return
	}
	w.trim(now)
	w.tokens = append(w.tokens, tokenMark{at: now, tokens: tokens})
}

func (w *window) trim(now time.Time) {
	cutoff := now.Add(-rateWindow)
	i := 0
	for i < len(w.requests) && !w.requests[i].After(cutoff) {
		i++
	}
	w.requests = w.requests[i:]
	j := 0
	for j < len(w.tokens) && !w.tokens[j].at.After(cutoff) {
		j++
	}
	w.tokens = w.tokens[j:]
}

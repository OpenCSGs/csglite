package server

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/logutil"
)

var baseLogWriter = log.Writer()

// LogBuffer is a thread-safe ring buffer for log lines, supporting SSE subscribers.
type LogBuffer struct {
	mu      sync.RWMutex
	lines   []string
	maxSize int

	subMu sync.Mutex
	subs  map[chan string]struct{}
}

func NewLogBuffer(maxSize int) *LogBuffer {
	return &LogBuffer{
		maxSize: maxSize,
		subs:    make(map[chan string]struct{}),
	}
}

func (lb *LogBuffer) Write(p []byte) (n int, err error) {
	line := string(p)
	lb.mu.Lock()
	lb.lines = append(lb.lines, line)
	if len(lb.lines) > lb.maxSize {
		lb.lines = lb.lines[len(lb.lines)-lb.maxSize:]
	}
	lb.mu.Unlock()

	lb.subMu.Lock()
	for ch := range lb.subs {
		select {
		case ch <- line:
		default:
		}
	}
	lb.subMu.Unlock()

	return len(p), nil
}

func (lb *LogBuffer) Recent(n int) []string {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	if n > len(lb.lines) {
		n = len(lb.lines)
	}
	result := make([]string, n)
	copy(result, lb.lines[len(lb.lines)-n:])
	return result
}

func (lb *LogBuffer) Subscribe() chan string {
	ch := make(chan string, 64)
	lb.subMu.Lock()
	lb.subs[ch] = struct{}{}
	lb.subMu.Unlock()
	return ch
}

func (lb *LogBuffer) Unsubscribe(ch chan string) {
	lb.subMu.Lock()
	delete(lb.subs, ch)
	lb.subMu.Unlock()
	close(ch)
}

// GET /api/logs -- SSE stream of server logs
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.logBuf == nil {
		writeError(w, http.StatusServiceUnavailable, "log streaming not available")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Send recent history
	for _, line := range s.logBuf.Recent(50) {
		fmt.Fprintf(w, "data: %s\n\n", trimNewline(line))
	}
	flusher.Flush()

	ch := s.logBuf.Subscribe()
	defer s.logBuf.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", trimNewline(line))
			flusher.Flush()
		}
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// SetupLogging redirects log output to the in-memory buffer and, by default,
// mirrors it to stderr and ~/.csghub-lite/logs/csghub-lite.log.
func SetupLogging(buf *LogBuffer) {
	writers := make([]io.Writer, 0, 3)
	if config.LogStderrEnabled() {
		writers = append(writers, baseLogWriter)
	}
	if buf != nil {
		writers = append(writers, buf)
	}
	if config.FileLoggingEnabled() {
		if path, err := config.ServerLogPath(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not resolve csghub-lite log path: %v\n", err)
		} else if file, err := logutil.OpenAppendFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open csghub-lite log file %s: %v\n", path, err)
		} else {
			writers = append(writers, file)
		}
	}
	if len(writers) == 0 {
		writers = append(writers, io.Discard)
	}
	log.SetOutput(io.MultiWriter(writers...))
	log.SetFlags(log.LstdFlags)
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *logResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *logResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *logResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// LogMiddleware logs HTTP requests to the standard logger with timing info.
func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &logResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lw, r)
		elapsed := time.Since(start)
		if lw.status == 0 {
			lw.status = http.StatusOK
		}
		if isQuietRequestLog(r) && lw.status < http.StatusBadRequest && elapsed < 5*time.Second {
			return
		}
		log.Printf("REQUEST: %s %s (%s)", r.Method, r.URL.Path, elapsed.Round(time.Millisecond))
	})
}

func isQuietRequestLog(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/api/apps", "/api/conversations", "/api/ps", "/api/system", "/api/tags":
		return true
	default:
		return false
	}
}

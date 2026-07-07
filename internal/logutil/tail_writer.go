package logutil

import (
	"bytes"
	"sync"
)

// TailWriter keeps only the last maxBytes of data written to it.
// It is safe for concurrent use by subprocess stdout/stderr writers.
type TailWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	maxBytes int
}

func NewTailWriter(maxBytes int) *TailWriter {
	return &TailWriter{maxBytes: maxBytes}
}

func (w *TailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if w.buf.Len() > w.maxBytes {
		b := w.buf.Bytes()
		w.buf.Reset()
		w.buf.Write(b[len(b)-w.maxBytes:])
	}
	return len(p), nil
}

func (w *TailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

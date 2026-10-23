package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/ctxcompress"
)

// contextCompressionHeader reports on the response what was done to the
// request, so a client or a curl session can see the effect directly.
const contextCompressionHeader = "X-CSGLite-Context-Compression"

// contextCompressionOptions returns the compressor settings for the current
// mode, and false when compression is off.
func (s *Server) contextCompressionOptions() (string, ctxcompress.Options, bool) {
	mode := config.NormalizeContextCompression(s.cfg.Inference.ContextCompression)
	switch mode {
	case config.ContextCompressionSafe:
		return mode, ctxcompress.Options{}, true
	case config.ContextCompressionAggressive:
		return mode, ctxcompress.Options{Sample: true}, true
	}
	return mode, ctxcompress.Options{}, false
}

// withContextCompression rewrites the tool output in a chat request body
// before next sees it. It wraps only the routes clients call; requests a
// cluster member forwards arrive on the peer handler and were compressed
// once already on the node that received them.
//
// The observability store keeps the body as the client sent it, and records
// what compression saved beside it.
func (s *Server) withContextCompression(protocol ctxcompress.Protocol, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode, opts, enabled := s.contextCompressionOptions()
		if !enabled || r.Body == nil {
			next(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			// Let the handler report the broken body as it always has.
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), errorReader{err}))
			next(w, r)
			return
		}
		result, err := ctxcompress.CompressRequest(protocol, body, opts)
		if err != nil {
			// Not JSON the compressor understands; the handler decides.
			r.Body = io.NopCloser(bytes.NewReader(body))
			next(w, r)
			return
		}
		if result.Stats.Blocks > 0 {
			observationFromContext(r.Context()).setContextCompression(mode, result.Stats)
			w.Header().Set(contextCompressionHeader, fmt.Sprintf("mode=%s; blocks=%d; compressed=%d; bytes=%d->%d; tokens_saved=%d",
				mode, result.Stats.Blocks, result.Stats.Compressed, result.Stats.BytesBefore, result.Stats.BytesAfter, result.Stats.TokensSaved))
		}
		if result.Changed {
			log.Printf("CONTEXT COMPRESSION %s %s: %d of %d tool results, %d -> %d bytes, ~%d tokens saved",
				mode, r.URL.Path, result.Stats.Compressed, result.Stats.Blocks,
				result.Stats.BytesBefore, result.Stats.BytesAfter, result.Stats.TokensSaved)
		}
		r.Body = io.NopCloser(bytes.NewReader(result.Body))
		r.ContentLength = int64(len(result.Body))
		r.Header.Set("Content-Length", strconv.Itoa(len(result.Body)))
		next(w, r)
	}
}

type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }

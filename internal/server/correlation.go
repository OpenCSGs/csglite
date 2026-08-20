package server

import (
	"net/http"

	"github.com/opencsgs/csglite/internal/correlation"
)

func correlationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := correlation.FromHeaders(r.Header)
		correlation.ApplyResponseHeaders(w.Header(), values)
		next.ServeHTTP(w, r.WithContext(correlation.WithContext(r.Context(), values)))
	})
}

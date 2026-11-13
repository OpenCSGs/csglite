// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package edge

import (
	"net/http"
)

// servedPaths are the only requests the tunnel may carry. The gateway relay
// enforces the same list; checking again here means a compromised or buggy
// gateway still cannot reach management, apps, files or the shell.
var servedPaths = map[string]string{
	"/v1/chat/completions": http.MethodPost,
	"/v1/completions":      http.MethodPost,
	"/v1/messages":         http.MethodPost,
	"/v1/responses":        http.MethodPost,
	"/v1/embeddings":       http.MethodPost,
}

// maxRequestBody bounds one remote request.
const maxRequestBody = 32 << 20

// guard admits only inference requests and strips anything that could act
// as a local credential before the server's handlers see the request.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, ok := servedPaths[r.URL.Path]
		if !ok || r.Method != method {
			http.Error(w, `{"error":{"message":"not served to the gateway","type":"edge_path_not_allowed"}}`, http.StatusNotFound)
			return
		}
		r.Header.Del("Authorization")
		r.Header.Del("Cookie")
		r.Header.Del("X-Api-Key")
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package team

import (
	"net/http"
	"strings"
)

// PublicPath reports whether a path answers without signing in once accounts
// are on: the web UI shell (so the login page can load), sign-in itself, and
// the health probe load balancers poll.
func PublicPath(path string) bool {
	if !isAPIPath(path) {
		return true
	}
	return path == "/api/health" || path == "/api/auth" || strings.HasPrefix(path, "/api/auth/")
}

// Allowed reports whether role may make this request. Paths reached through
// the provider-scoped prefix /providers/{id}/v1/... are judged as the /v1/...
// path they serve.
//
// Administrators may do everything. Members read the instance, run
// inference, and manage their own conversations, images and API keys; the
// handlers narrow those to the member's own records. Viewers only read, and
// cannot run inference.
func Allowed(role Role, method, path string) bool {
	path = inferencePathOf(path)
	switch role {
	case RoleAdmin:
		return true
	case RoleMember:
		if adminOnlyRead(path) {
			return false
		}
		if safeMethod(method) {
			return true
		}
		return InferencePath(path) || memberWrite(method, path)
	case RoleViewer:
		if adminOnlyRead(path) || !safeMethod(method) {
			return false
		}
		return !InferencePath(path) || path == "/v1/models"
	}
	return false
}

// InferencePath reports whether a path runs a model, which is what members
// may do and viewers may not.
func InferencePath(path string) bool {
	path = inferencePathOf(path)
	if strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/anthropic/") {
		return true
	}
	switch path {
	case "/api/chat", "/api/generate", "/api/load":
		return true
	}
	return false
}

func isAPIPath(path string) bool {
	for _, prefix := range []string{"/api/", "/v1/", "/anthropic/", "/providers/"} {
		if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// inferencePathOf maps /providers/{id}/v1/... onto /v1/....
func inferencePathOf(path string) string {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) == 3 && parts[0] == "providers" && strings.HasPrefix(parts[2], "v1/") {
		return "/" + parts[2]
	}
	return path
}

// adminOnlyRead lists what a member must not read even though it is a GET:
// other people's prompts and responses, server logs, the credentials that
// admit a machine to the cluster, an interactive shell, and the account list.
func adminOnlyRead(path string) bool {
	switch path {
	case "/api/logs", "/api/apps/logs", "/api/cluster/token", "/api/cluster/code":
		return true
	}
	for _, prefix := range []string{"/api/observability", "/api/apps/shell/", "/api/team/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return path == "/api/team"
}

// memberWrite lists the changes a member may make besides running inference.
func memberWrite(method, path string) bool {
	switch {
	case path == "/api/conversations" && method == http.MethodPost,
		strings.HasPrefix(path, "/api/conversations/") && (method == http.MethodPut || method == http.MethodDelete),
		path == "/api/api-keys" && method == http.MethodPost,
		strings.HasPrefix(path, "/api/api-keys/") && path != "/api/api-keys/settings" && method == http.MethodDelete,
		path == "/api/images/jobs" && method == http.MethodPost,
		strings.HasPrefix(path, "/api/images/jobs/") && method == http.MethodDelete:
		return true
	}
	// Lookups that use POST only to carry a body.
	switch path {
	case "/api/show", "/api/datasets/show", "/api/marketplace/models/extra":
		return method == http.MethodPost
	}
	return false
}

package team

import (
	"net/http"
	"strings"
)

// PublicPath reports whether a path answers without signing in once accounts
// are on: the web UI shell (so the sign-in and invitation pages can load),
// sign-in and invitation acceptance, and the health probe load balancers
// poll.
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
// handlers narrow those to the member's own records. The auditor role is an
// Enterprise feature whose rules live in ee/team; here it is refused
// everything.
func Allowed(role Role, method, path string) bool {
	path = InferencePathOf(path)
	switch role {
	case RoleAdmin:
		return true
	case RoleMember:
		if SecretRead(path) || adminOnlyRead(path) {
			return false
		}
		if SafeMethod(method) {
			return true
		}
		return InferencePath(path) || memberWrite(method, path)
	}
	return false
}

// SeesEverything reports whether a role reads every user's API keys and the
// instance-wide records an administrator reads.
func SeesEverything(role Role) bool {
	return role == RoleAdmin || role == RoleAuditor
}

// InferencePath reports whether a path runs a model, which is what members
// may do and auditors may not.
func InferencePath(path string) bool {
	path = InferencePathOf(path)
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

// SafeMethod reports whether a request only reads.
func SafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// InferencePathOf maps /providers/{id}/v1/... onto /v1/....
func InferencePathOf(path string) string {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) == 3 && parts[0] == "providers" && strings.HasPrefix(parts[2], "v1/") {
		return "/" + parts[2]
	}
	return path
}

// SecretRead lists reads that hand out access itself: the credentials that
// admit a machine to the cluster, and an interactive shell on the host.
func SecretRead(path string) bool {
	return path == "/api/cluster/token" || path == "/api/cluster/code" || strings.HasPrefix(path, "/api/apps/shell/")
}

// adminOnlyRead lists what a member must not read even though it is a GET:
// other people's prompts and responses, server logs, and the account list.
func adminOnlyRead(path string) bool {
	switch path {
	case "/api/logs", "/api/apps/logs", "/api/team":
		return true
	}
	return strings.HasPrefix(path, "/api/observability") || strings.HasPrefix(path, "/api/team/")
}

// OwnKeyWrite reports whether a request creates or deletes one of the
// caller's own API keys.
func OwnKeyWrite(method, path string) bool {
	return (path == "/api/api-keys" && method == http.MethodPost) ||
		(strings.HasPrefix(path, "/api/api-keys/") && path != "/api/api-keys/settings" && method == http.MethodDelete)
}

// memberWrite lists the changes a member may make besides running inference.
func memberWrite(method, path string) bool {
	if OwnKeyWrite(method, path) {
		return true
	}
	switch {
	case path == "/api/conversations" && method == http.MethodPost,
		strings.HasPrefix(path, "/api/conversations/") && (method == http.MethodPut || method == http.MethodDelete),
		path == "/api/images/jobs" && method == http.MethodPost,
		strings.HasPrefix(path, "/api/images/jobs/") && method == http.MethodDelete:
		return true
	}
	return LookupPost(method, path)
}

// LookupPost lists reads that use POST only to carry a body.
func LookupPost(method, path string) bool {
	switch path {
	case "/api/show", "/api/datasets/show", "/api/marketplace/models/extra":
		return method == http.MethodPost
	}
	return false
}

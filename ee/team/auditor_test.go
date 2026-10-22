// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package team

import (
	"net/http"
	"testing"
)

func TestAuditorAllowed(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/tags", true},
		{http.MethodGet, "/v1/models", true},
		{http.MethodGet, "/api/observability/requests", true},
		{http.MethodGet, "/api/logs", true},
		{http.MethodGet, "/api/team/users", true},
		{http.MethodPost, "/api/show", true},
		{http.MethodPost, "/api/api-keys", true},
		{http.MethodDelete, "/api/api-keys/abc", true},
		{http.MethodGet, "/api/cluster/token", false},
		{http.MethodGet, "/api/apps/shell/abc/ws", false},
		{http.MethodPost, "/v1/chat/completions", false},
		{http.MethodGet, "/v1/realtime", false},
		{http.MethodPost, "/api/conversations", false},
		{http.MethodPost, "/api/team/users", false},
		{http.MethodPut, "/api/team/settings", false},
		{http.MethodPost, "/api/settings", false},
		{http.MethodPost, "/providers/abc/v1/messages", false},
	}
	for _, tc := range cases {
		if got := AuditorAllowed(tc.method, tc.path); got != tc.want {
			t.Errorf("AuditorAllowed(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

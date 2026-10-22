// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

// Package team holds the Enterprise part of user accounts: the auditor role.
// Accounts themselves (users, invitations, teams, the administrator and
// member roles) live in internal/team under Apache-2.0.
package team

import (
	accounts "github.com/opencsgs/csglite/internal/team"
)

// AuditorAllowed reports whether an auditor may make this request. Auditors
// read everything an administrator reads except the secrets that grant access
// (the cluster join credentials and the interactive shell), manage their own
// API keys, and change nothing else; they cannot run inference, which spends
// money. Paths reached through /providers/{id}/v1/... are judged as the
// /v1/... path they serve.
func AuditorAllowed(method, path string) bool {
	path = accounts.InferencePathOf(path)
	if accounts.SecretRead(path) {
		return false
	}
	if accounts.SafeMethod(method) {
		return !accounts.InferencePath(path) || path == "/v1/models"
	}
	return accounts.OwnKeyWrite(method, path) || accounts.LookupPost(method, path)
}

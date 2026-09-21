// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/inference"
)

// ErrNoNode is returned when no member can serve the model.
type ErrNoNode struct {
	Model string
	Tried []string
	Last  error
	Ex    Explain
}

func (e *ErrNoNode) Error() string {
	if len(e.Tried) == 0 {
		return fmt.Sprintf("no cluster node can serve model %q right now", e.Model)
	}
	msg := fmt.Sprintf("model %q failed on every eligible cluster node (%d tried)", e.Model, len(e.Tried))
	if e.Last != nil {
		msg += ": " + e.Last.Error()
	}
	return msg
}

// LocalChoice is the error RouteRaw returns when this node is the best
// place for the request. The caller runs the request locally and calls
// Release when it is done, so the scheduler keeps counting the request
// against this node while it is in flight.
type LocalChoice struct {
	release func()
}

func (c *LocalChoice) Error() string { return ErrServeLocally.Error() }

func (c *LocalChoice) Is(target error) bool {
	return target == ErrServeLocally
}

// Release ends the local reservation; safe to call more than once.
func (c *LocalChoice) Release() {
	if c != nil && c.release != nil {
		c.release()
		c.release = nil
	}
}

// Source values understood by the cluster router.
const (
	SourceCluster    = "cluster"
	SourceNodePrefix = "node:"
)

// NodeUUIDFromSource extracts the UUID from "node:<uuid>", or "".
func NodeUUIDFromSource(source string) string {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, SourceNodePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(source, SourceNodePrefix))
}

// IsClusterSource reports whether source is handled by the cluster router.
func IsClusterSource(source string) bool {
	source = strings.TrimSpace(source)
	return strings.EqualFold(source, SourceCluster) || NodeUUIDFromSource(source) != ""
}

// ErrNoNodeCode is the machine-readable code for ErrNoNode.
const ErrNoNodeCode = "cluster_no_available_node"

// ErrServeLocally tells the caller that the scheduler chose this node, so
// the request should run through the ordinary local path.
var ErrServeLocally = errors.New("cluster: serve locally")

// StatusCodeForError maps a cluster routing failure onto the HTTP status the
// server should answer with.
func StatusCodeForError(err error) int {
	if code := inference.HTTPStatusCode(err); code != 0 {
		return code
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

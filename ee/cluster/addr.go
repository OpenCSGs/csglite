// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"net"
	"strconv"
	"strings"
)

// endpoint joins a host and a member's cluster port into "host:port". A port
// of zero means the member never told us one, which happens for a node that
// has not answered a status poll yet, so the default listener port stands in.
func endpoint(host string, port int) string {
	if port <= 0 {
		port = portOf(DefaultListenAddr)
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// withDefaultPort completes an operator-supplied address. A seed, a static
// address or a join target may be written as a bare host, and every such place
// has to reach the same conclusion about which port that means.
func withDefaultPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return endpoint(addr, 0)
}

// withMemberPort completes a bare address for one member, preferring the port
// that member actually listens on over the default.
func withMemberPort(addr string, memberPort int) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return endpoint(addr, memberPort)
}

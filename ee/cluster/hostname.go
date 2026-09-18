// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"net/netip"
	"os"
	"strings"
)

// hostnameShort returns the machine's host name without a domain suffix.
func hostnameShort() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "", err
	}
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h, nil
}

// defaultNodeName is the display name a node gets on first start: the host
// name when it is a real name, otherwise a generic one (a host named after
// its IP address, as some appliances are, would otherwise show up as "192").
func defaultNodeName() string {
	h, err := hostnameShort()
	if err != nil || h == "" {
		return "csglite-node"
	}
	full, _ := os.Hostname()
	if _, err := netip.ParseAddr(strings.TrimSpace(full)); err == nil {
		return "csglite-node"
	}
	allDigits := true
	for _, r := range h {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "csglite-node"
	}
	return h
}

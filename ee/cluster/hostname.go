// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Cache the system name for this process so status polling does not repeatedly
// invoke platform utilities. An explicit operator rename is stored separately.
var systemHostname = sync.OnceValues(func() (string, error) {
	host, err := os.Hostname()
	if name := usableHostname(host); name != "" {
		return name, nil
	}
	// DHCP can set a Mac's Unix hostname to its IP address. LocalHostName is
	// the stable system name shown in macOS Sharing settings.
	if runtime.GOOS == "darwin" {
		for _, key := range []string{"LocalHostName", "ComputerName"} {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			out, commandErr := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", key).Output()
			cancel()
			if commandErr == nil {
				if name := usableHostname(string(out)); name != "" {
					return name, nil
				}
			}
		}
	}
	return "", err
})

func hostnameShort() (string, error) { return systemHostname() }

func usableHostname(host string) string {
	host = strings.TrimSpace(host)
	if _, err := netip.ParseAddr(host); err == nil {
		return ""
	}
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	if strings.Trim(host, "0123456789.:") == "" {
		return ""
	}
	return host
}

const genericNodeName = "csglite-node"

func defaultNodeName() string {
	name, _ := hostnameShort()
	if name == "" {
		return genericNodeName
	}
	return name
}

// Only migrate generated names that belong to this identity; custom names are
// preserved, and the UUID and certificate remain unchanged.
func isGeneratedNodeName(name, nodeUUID string) bool {
	return name == genericNodeName || name == defaultShortName(nodeUUID) ||
		name == "csglite-node-"+nodeUUID ||
		name == "csglite-node-"+strings.TrimPrefix(defaultShortName(nodeUUID), "node-")
}

// defaultShortName is the generated name for a node without a usable host
// name: "node-" plus the first six hex digits of its UUID.
func defaultShortName(nodeUUID string) string {
	hex := strings.ReplaceAll(nodeUUID, "-", "")
	if len(hex) > 6 {
		hex = hex[:6]
	}
	return "node-" + hex
}

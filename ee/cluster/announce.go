// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (m *Manager) announcement() Announcement {
	a := Announcement{
		UUID:        m.identity.UUID,
		Name:        m.identity.DisplayName(),
		Version:     m.opts.Host.Version(),
		Protocol:    ProtocolVersion,
		ClusterPort: m.ListenPort(),
		APIPort:     m.opts.Host.APIPort(),
		Licensed:    m.opts.Host.Licensed(),
	}
	if c := m.store.Cluster(); c != nil {
		a.ClusterUUID = c.UUID
	}
	return a
}

func (m *Manager) reannounce() {
	if err := m.discoverer.Update(m.announcement()); err != nil {
		m.logf("cluster: re-announce: %v", err)
	}
}

func (m *Manager) onObservation(obs Observation) {
	if obs.Protocol != 0 && obs.Protocol != ProtocolVersion {
		return
	}
	if m.store.IsTombstoned(obs.UUID) {
		return
	}
	m.dir.Observe(obs, func(id string) bool { _, ok := m.store.Member(id); return ok })
}

// probeSeed asks a static endpoint who it is; a member's address is learned
// and an unpaired node is listed as discovered.
func (m *Manager) probeSeed(seed string) {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return
	}
	seed = withDefaultPort(seed)
	go func() {
		ctx, cancel := context.WithTimeout(m.context(), 5*time.Second)
		defer cancel()
		st, err := m.fetchStatusUnpinned(ctx, seed)
		if err != nil || st.UUID == m.identity.UUID {
			return
		}
		addr, _ := netip.ParseAddr(endpointHost(seed))
		m.onObservation(Observation{
			Announcement: Announcement{UUID: st.UUID, ClusterUUID: st.ClusterUUID, Name: st.Name, Version: st.Version,
				Protocol: st.Protocol, ClusterPort: portOf(seed), APIPort: st.APIPort, Licensed: st.Licensed},
			Addr: addr, Seen: time.Now(), Source: "seed",
		})
	}()
}

func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// seedAddressesFor lists the endpoints worth trying for a member before
// discovery answers: the operator's static address, then recent successes.
func (m *Manager) seedAddressesFor(mem Member) []string {
	var seeds []string
	if static := m.store.Settings().StaticAddresses[mem.UUID]; static != "" {
		seeds = append(seeds, withMemberPort(static, mem.ClusterPort))
	}
	seeds = append(seeds, mem.LastAddresses...)
	return seeds
}

func (m *Manager) noteObservedIP(ip string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	if parsed, err := netip.ParseAddr(ip); err != nil || parsed.IsLoopback() || parsed.IsUnspecified() {
		return
	}
	m.observedMu.Lock()
	m.observedIPs[ip] = time.Now()
	m.observedMu.Unlock()
}

func (m *Manager) localAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	sort.Strings(out)
	return out
}

// advertisedEndpoints are cluster endpoints peers may try for this node:
// the configured advertise host first, then addresses peers have actually
// reached us on, then every local IPv4.
func (m *Manager) advertisedEndpoints() []string {
	port := m.ListenPort()
	seen := map[string]bool{}
	var out []string
	add := func(host string) {
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		out = append(out, endpoint(host, port))
	}
	add(strings.TrimSpace(m.opts.AdvertiseHost))
	if host := endpointHost(m.opts.ListenAddr); host != "" {
		if ip, err := netip.ParseAddr(host); err == nil && !ip.IsUnspecified() {
			// Bound to one address: that is the only one peers can use.
			add(host)
			return out
		}
	}
	m.observedMu.Lock()
	type obs struct {
		ip string
		at time.Time
	}
	var observed []obs
	for ip, at := range m.observedIPs {
		if time.Since(at) < 10*time.Minute {
			observed = append(observed, obs{ip, at})
		} else {
			delete(m.observedIPs, ip)
		}
	}
	m.observedMu.Unlock()
	sort.Slice(observed, func(i, j int) bool { return observed[i].at.After(observed[j].at) })
	for _, o := range observed {
		add(o.ip)
	}
	for _, ip := range m.localAddrs() {
		add(ip)
	}
	return out
}

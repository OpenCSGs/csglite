// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/mdns/v2"
	"golang.org/x/net/ipv4"
)

// ServiceType is the DNS-SD service every CSGLite node advertises.
const ServiceType = "_csglite-node._tcp"

// Announcement is what a node publishes about itself. It is small on purpose:
// mDNS TXT records do not hold a model list, that comes from /cluster/v1/status.
type Announcement struct {
	UUID        string
	ClusterUUID string
	Name        string
	Version     string
	Protocol    int
	ClusterPort int
	APIPort     int
	Licensed    bool
}

// Observation is an Announcement seen at an address.
type Observation struct {
	Announcement
	Addr   netip.Addr
	Seen   time.Time
	Source string
}

// Endpoint is the cluster listener address of the observed node.
func (o Observation) Endpoint() string {
	return net.JoinHostPort(o.Addr.String(), strconv.Itoa(o.ClusterPort))
}

// Discoverer advertises this node and reports other nodes on the LAN.
type Discoverer interface {
	// Start begins advertising self and browsing; observations go to onObserve
	// until ctx ends.
	Start(ctx context.Context, self Announcement, onObserve func(Observation)) error
	// Update re-announces a changed self (cluster joined, name changed).
	Update(self Announcement) error
	Close() error
}

func announcementTXT(a Announcement) []mdns.TXTEntry {
	entries := []mdns.TXTEntry{
		mdns.NewTXTString("v", strconv.Itoa(a.Protocol)),
		mdns.NewTXTString("uuid", a.UUID),
		mdns.NewTXTString("port", strconv.Itoa(a.ClusterPort)),
		mdns.NewTXTString("api", strconv.Itoa(a.APIPort)),
		mdns.NewTXTString("name", a.Name),
		mdns.NewTXTString("ver", a.Version),
	}
	if a.ClusterUUID != "" {
		entries = append(entries, mdns.NewTXTString("cluster", a.ClusterUUID))
	}
	if a.Licensed {
		entries = append(entries, mdns.NewTXTFlag("lic"))
	}
	return entries
}

func announcementFromTXT(text []mdns.TXTEntry, port uint16) (Announcement, bool) {
	var a Announcement
	for _, e := range text {
		val := string(e.Value)
		switch strings.ToLower(e.Key) {
		case "v":
			a.Protocol, _ = strconv.Atoi(val)
		case "uuid":
			a.UUID = val
		case "cluster":
			a.ClusterUUID = val
		case "port":
			a.ClusterPort, _ = strconv.Atoi(val)
		case "api":
			a.APIPort, _ = strconv.Atoi(val)
		case "name":
			a.Name = val
		case "ver":
			a.Version = val
		case "lic":
			a.Licensed = true
		}
	}
	if a.ClusterPort == 0 {
		a.ClusterPort = int(port)
	}
	if a.UUID == "" || a.ClusterPort == 0 {
		return Announcement{}, false
	}
	return a, true
}

// ---- mDNS ----

// MDNSDiscoverer advertises and browses over multicast DNS using the pion
// responder, so Windows hosts without Bonjour work too.
type MDNSDiscoverer struct {
	mu   sync.Mutex
	conn *mdns.Conn
	self Announcement
	stop context.CancelFunc
}

// NewMDNSDiscoverer returns a discoverer that is started lazily by Start.
func NewMDNSDiscoverer() *MDNSDiscoverer { return &MDNSDiscoverer{} }

func (d *MDNSDiscoverer) Start(ctx context.Context, self Announcement, onObserve func(Observation)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	addr4, err := net.ResolveUDPAddr("udp4", mdns.DefaultAddressIPv4)
	if err != nil {
		return err
	}
	l4, err := net.ListenUDP("udp4", addr4)
	if err != nil {
		return err
	}
	// The SRV target is a name of our own rather than the machine's host
	// name: it must be unique per node (two nodes on one machine would
	// otherwise fight over one A record) and canonical, with the trailing
	// dot the DNS packer insists on.
	hostname := mdnsHostName(self.UUID)
	conn, err := mdns.NewServer(ipv4.NewPacketConn(l4), nil,
		mdns.WithName("csglite-cluster"),
		mdns.WithLocalNames(hostname),
		mdns.WithService(mdns.ServiceInstance{
			Instance: self.UUID,
			Service:  ServiceType,
			Host:     hostname,
			Port:     uint16(self.ClusterPort),
			Text:     announcementTXT(self),
		}),
	)
	if err != nil {
		_ = l4.Close()
		return err
	}
	d.conn = conn
	d.self = self
	conn.OnServiceDiscovered(func(evt mdns.ServiceEvent) {
		if evt.Instance.Service != ServiceType {
			return
		}
		a, ok := announcementFromTXT(evt.Instance.Text, evt.Instance.Port)
		if !ok || a.UUID == self.UUID {
			return
		}
		addr := evt.Addr.Unmap()
		if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() {
			return
		}
		// Only the IPv4 socket is open, so answers are IPv4; a responder that
		// also lists link-local IPv6 addresses is ignored for those.
		if !addr.Is4() {
			return
		}
		onObserve(Observation{Announcement: a, Addr: addr, Seen: time.Now(), Source: "mdns"})
	})
	browseCtx, cancel := context.WithCancel(ctx)
	d.stop = cancel
	if err := conn.Browse(browseCtx, ServiceType); err != nil {
		cancel()
		_ = conn.Close()
		return err
	}
	// Re-announce periodically: a peer that missed the startup announcement
	// (or that itself just booted) learns about us without waiting for its
	// own query to reach us.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-browseCtx.Done():
				return
			case <-ticker.C:
				d.mu.Lock()
				cur := d.self
				c := d.conn
				d.mu.Unlock()
				if c != nil {
					if err := c.UpdateTXT(cur.UUID, ServiceType, announcementTXT(cur)); err != nil {
						log.Printf("cluster: mdns re-announce: %v", err)
					}
				}
			}
		}
	}()
	return nil
}

func (d *MDNSDiscoverer) Update(self Announcement) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.self = self
	if d.conn == nil {
		return nil
	}
	return d.conn.UpdateTXT(self.UUID, ServiceType, announcementTXT(self))
}

func (d *MDNSDiscoverer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stop != nil {
		d.stop()
	}
	if d.conn != nil {
		err := d.conn.Close()
		d.conn = nil
		return err
	}
	return nil
}

// mdnsHostName is the canonical .local host name this node publishes.
func mdnsHostName(nodeUUID string) string {
	short := strings.ReplaceAll(nodeUUID, "-", "")
	if len(short) > 12 {
		short = short[:12]
	}
	return "csglite-" + short + ".local."
}

// ---- in-memory bus for tests and single-host demos ----

// MemoryBus links MemoryDiscoverers in one process.
type MemoryBus struct {
	mu    sync.Mutex
	nodes map[string]*memoryDiscoverer
}

// NewMemoryBus creates an empty bus.
func NewMemoryBus() *MemoryBus { return &MemoryBus{nodes: map[string]*memoryDiscoverer{}} }

type memoryDiscoverer struct {
	bus     *MemoryBus
	addr    netip.Addr
	self    Announcement
	observe func(Observation)
	started bool
}

// NewDiscoverer returns a discoverer reachable at addr on this bus.
func (b *MemoryBus) NewDiscoverer(addr netip.Addr) Discoverer {
	return &memoryDiscoverer{bus: b, addr: addr}
}

func (d *memoryDiscoverer) Start(ctx context.Context, self Announcement, onObserve func(Observation)) error {
	d.bus.mu.Lock()
	d.self = self
	d.observe = onObserve
	d.started = true
	d.bus.nodes[self.UUID] = d
	others := make([]*memoryDiscoverer, 0, len(d.bus.nodes))
	for _, o := range d.bus.nodes {
		if o != d && o.started {
			others = append(others, o)
		}
	}
	d.bus.mu.Unlock()
	for _, o := range others {
		o.deliver(d)
		d.deliver(o)
	}
	go func() {
		<-ctx.Done()
		d.bus.mu.Lock()
		delete(d.bus.nodes, self.UUID)
		d.bus.mu.Unlock()
	}()
	return nil
}

// deliver hands from's announcement to d.
func (d *memoryDiscoverer) deliver(from *memoryDiscoverer) {
	d.bus.mu.Lock()
	observe := d.observe
	obs := Observation{Announcement: from.self, Addr: from.addr, Seen: time.Now(), Source: "memory"}
	d.bus.mu.Unlock()
	if observe != nil {
		observe(obs)
	}
}

func (d *memoryDiscoverer) Update(self Announcement) error {
	d.bus.mu.Lock()
	d.self = self
	others := make([]*memoryDiscoverer, 0, len(d.bus.nodes))
	for _, o := range d.bus.nodes {
		if o != d && o.started {
			others = append(others, o)
		}
	}
	d.bus.mu.Unlock()
	for _, o := range others {
		o.deliver(d)
	}
	return nil
}

func (d *memoryDiscoverer) Close() error {
	d.bus.mu.Lock()
	delete(d.bus.nodes, d.self.UUID)
	d.bus.mu.Unlock()
	return nil
}

// NopDiscoverer discovers nothing; static addresses and gossip still work.
type NopDiscoverer struct{}

func (NopDiscoverer) Start(context.Context, Announcement, func(Observation)) error { return nil }
func (NopDiscoverer) Update(Announcement) error                                    { return nil }
func (NopDiscoverer) Close() error                                                 { return nil }

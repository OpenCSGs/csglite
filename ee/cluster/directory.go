// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Health is the entry node's view of a member's availability.
type Health string

const (
	HealthUnknown Health = "unknown"
	HealthHealthy Health = "healthy"
	HealthSuspect Health = "suspect"
	HealthDown    Health = "down"
	HealthProbing Health = "probing"
)

const (
	// pollHealthy is the telemetry cadence for a reachable member.
	pollHealthy = 2 * time.Second
	// downAfterFailures and downAfterSilence decide when a suspect member is
	// declared down: three consecutive failures, or a minute without a
	// successful status, whichever comes first.
	downAfterFailures = 3
	downAfterSilence  = 60 * time.Second
	// modelBreakerTTL is how long a (node, model) pair stays excluded after a
	// model-specific failure such as a corrupt file on one node.
	modelBreakerTTL = 5 * time.Minute
	// reservationTTL bounds how long a dispatched-but-unreported request
	// counts against a node if the caller forgets to release it.
	reservationTTL = 2 * time.Minute
)

var pollBackoff = []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}

// pollStuckAfter abandons a poll that never reported back. Without it one
// request that never returns leaves the member marked "probing" for good:
// no further poll is scheduled, so its health, address and last error stay
// frozen even after the member is plainly reachable again.
const pollStuckAfter = 45 * time.Second

// NodeRuntime is the live state kept for one member.
type NodeRuntime struct {
	UUID      string
	Health    Health
	Addr      string
	Status    *Status
	LastSeen  time.Time
	LastError string
	Failures  int
	NextPoll  time.Time
	// Observed maps candidate endpoints to when they were last reported by
	// discovery, gossip or a successful connection.
	Observed map[string]time.Time
	// reservations are requests this entry node dispatched that telemetry
	// has not reflected yet.
	reservations map[int64]time.Time
	reserveSeq   int64
	probing      bool
	probingSince time.Time
}

// Reserved counts outstanding dispatched requests.
func (n *NodeRuntime) Reserved(now time.Time) int {
	count := 0
	for id, at := range n.reservations {
		if now.Sub(at) > reservationTTL {
			delete(n.reservations, id)
			continue
		}
		count++
	}
	return count
}

// Online reports whether the node can be sent work.
func (n *NodeRuntime) Online() bool {
	return n.Health == HealthHealthy || n.Health == HealthSuspect
}

// Directory is the entry node's live view of the cluster: members' addresses,
// health and last status, plus unpaired nodes seen on the LAN.
type Directory struct {
	mu         sync.Mutex
	nodes      map[string]*NodeRuntime
	discovered map[string]Observation
	breakers   map[string]time.Time
	now        func() time.Time
}

// NewDirectory creates an empty directory.
func NewDirectory() *Directory {
	return &Directory{
		nodes:      map[string]*NodeRuntime{},
		discovered: map[string]Observation{},
		breakers:   map[string]time.Time{},
		now:        time.Now,
	}
}

func (d *Directory) node(nodeUUID string) *NodeRuntime {
	n, ok := d.nodes[nodeUUID]
	if !ok {
		n = &NodeRuntime{UUID: nodeUUID, Health: HealthUnknown, Observed: map[string]time.Time{}, reservations: map[int64]time.Time{}}
		d.nodes[nodeUUID] = n
	}
	return n
}

// Track ensures a member is tracked; seed addresses are probed first.
func (d *Directory) Track(nodeUUID string, seeds []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	for i, addr := range seeds {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := n.Observed[addr]; !ok {
			// Older seeds get older timestamps so the newest known address
			// is tried first.
			n.Observed[addr] = d.now().Add(-time.Duration(i+1) * time.Hour)
		}
	}
	if n.Addr == "" {
		n.Addr = d.bestAddrLocked(n)
	}
}

// Forget drops a member from the runtime view.
func (d *Directory) Forget(nodeUUID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.nodes, nodeUUID)
}

// Reset drops every member (on leave).
func (d *Directory) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nodes = map[string]*NodeRuntime{}
	d.breakers = map[string]time.Time{}
}

// LearnAddress records an endpoint for a member from discovery or gossip. A
// fresh address becomes the preferred one immediately, so an IP change is
// picked up on the very next poll.
func (d *Directory) LearnAddress(nodeUUID, addr string, when time.Time) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	if prev, ok := n.Observed[addr]; !ok || when.After(prev) {
		n.Observed[addr] = when
	}
	if n.Addr != addr && (n.Health != HealthHealthy || n.Addr == "") {
		n.Addr = d.bestAddrLocked(n)
		n.NextPoll = d.now()
	}
}

// Observe records a discovery observation. Members get an address update;
// unpaired nodes are listed for the operator.
func (d *Directory) Observe(obs Observation, isMember func(string) bool) {
	if isMember(obs.UUID) {
		d.LearnAddress(obs.UUID, obs.Endpoint(), obs.Seen)
		d.mu.Lock()
		delete(d.discovered, obs.UUID)
		d.mu.Unlock()
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.discovered[obs.UUID] = obs
}

// Discovered lists unpaired nodes seen recently.
func (d *Directory) Discovered(maxAge time.Duration) []Observation {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	out := make([]Observation, 0, len(d.discovered))
	for id, obs := range d.discovered {
		if now.Sub(obs.Seen) > maxAge {
			delete(d.discovered, id)
			continue
		}
		out = append(out, obs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name+out[i].UUID < out[j].Name+out[j].UUID })
	return out
}

// DiscoveredAll lists unpaired nodes regardless of age, for refresh probes.
func (d *Directory) DiscoveredAll() []Observation {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Observation, 0, len(d.discovered))
	for _, obs := range d.discovered {
		out = append(out, obs)
	}
	return out
}

// ExpireDiscovered drops unpaired nodes not seen for maxAge.
func (d *Directory) ExpireDiscovered(maxAge time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	for id, obs := range d.discovered {
		if now.Sub(obs.Seen) > maxAge {
			delete(d.discovered, id)
		}
	}
}

// DropDiscovered removes an unpaired node once it has been paired.
func (d *Directory) DropDiscovered(nodeUUID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.discovered, nodeUUID)
}

// bestAddrLocked picks the most recently observed endpoint.
func (d *Directory) bestAddrLocked(n *NodeRuntime) string {
	best, bestAt := "", time.Time{}
	for addr, at := range n.Observed {
		if at.After(bestAt) || (at.Equal(bestAt) && addr < best) {
			best, bestAt = addr, at
		}
	}
	return best
}

// Candidates returns endpoints to try for a member, preferred first.
func (d *Directory) Candidates(nodeUUID string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nodes[nodeUUID]
	if !ok {
		return nil
	}
	type pair struct {
		addr string
		at   time.Time
	}
	pairs := make([]pair, 0, len(n.Observed))
	for addr, at := range n.Observed {
		pairs = append(pairs, pair{addr, at})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].at.Equal(pairs[j].at) {
			return pairs[i].addr < pairs[j].addr
		}
		return pairs[i].at.After(pairs[j].at)
	})
	out := make([]string, 0, len(pairs)+1)
	if n.Addr != "" {
		out = append(out, n.Addr)
	}
	for _, p := range pairs {
		if p.addr != n.Addr {
			out = append(out, p.addr)
		}
	}
	return out
}

// Due returns members whose next poll is due.
func (d *Directory) Due() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	var due []string
	for id, n := range d.nodes {
		if n.probing && now.Sub(n.probingSince) < pollStuckAfter {
			continue
		}
		if now.Before(n.NextPoll) {
			continue
		}
		due = append(due, id)
	}
	sort.Strings(due)
	return due
}

// BeginPoll marks a member as being polled so overlapping polls do not stack.
// A poll that has not reported back within pollStuckAfter is abandoned rather
// than blocking every later poll.
func (d *Directory) BeginPoll(nodeUUID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nodes[nodeUUID]
	if !ok {
		return false
	}
	now := d.now()
	if n.probing && now.Sub(n.probingSince) < pollStuckAfter {
		return false
	}
	n.probing = true
	n.probingSince = now
	if n.Health == HealthDown {
		n.Health = HealthProbing
	}
	return true
}

// MarkSuccess records a good status fetch over addr.
func (d *Directory) MarkSuccess(nodeUUID, addr string, st *Status) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	now := d.now()
	n.probing = false
	n.Health = HealthHealthy
	n.Failures = 0
	n.LastError = ""
	n.LastSeen = now
	n.Status = st
	n.Addr = addr
	n.Observed[addr] = now
	n.NextPoll = now.Add(pollHealthy + jitter(nodeUUID, pollHealthy/4))
	// Telemetry now reflects every request dispatched before this sample.
	n.reservations = map[int64]time.Time{}
}

// MarkFailure records a failed status fetch and schedules the next attempt
// with backoff. A member that keeps failing is declared down.
func (d *Directory) MarkFailure(nodeUUID string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	now := d.now()
	n.probing = false
	n.Failures++
	if err != nil {
		n.LastError = err.Error()
	}
	switch {
	case n.Failures >= downAfterFailures && (n.LastSeen.IsZero() || now.Sub(n.LastSeen) >= downAfterSilence):
		n.Health = HealthDown
	case n.Health == HealthHealthy || n.Health == HealthUnknown:
		n.Health = HealthSuspect
	case n.Failures >= downAfterFailures*2:
		// Long silence but LastSeen was recent when failures started; do
		// not wait the full minute after six misses.
		n.Health = HealthDown
	}
	step := n.Failures - 1
	if step >= len(pollBackoff) {
		step = len(pollBackoff) - 1
	}
	if step < 0 {
		step = 0
	}
	n.NextPoll = now.Add(pollBackoff[step])
	// Try another known address next time; the current one just failed.
	if alt := d.nextAddrLocked(n); alt != "" {
		n.Addr = alt
	}
}

// nextAddrLocked rotates to the next candidate after the current address.
func (d *Directory) nextAddrLocked(n *NodeRuntime) string {
	if len(n.Observed) <= 1 {
		return n.Addr
	}
	addrs := make([]string, 0, len(n.Observed))
	for a := range n.Observed {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool {
		ai, aj := n.Observed[addrs[i]], n.Observed[addrs[j]]
		if ai.Equal(aj) {
			return addrs[i] < addrs[j]
		}
		return ai.After(aj)
	})
	for i, a := range addrs {
		if a == n.Addr {
			return addrs[(i+1)%len(addrs)]
		}
	}
	return addrs[0]
}

// RequestFailed records an inference failure against a node (and optionally
// a model) so the scheduler demotes it before telemetry catches up.
func (d *Directory) RequestFailed(nodeUUID, modelID string, modelSpecific bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	now := d.now()
	if modelSpecific && modelID != "" {
		d.breakers[nodeUUID+"\x00"+modelID] = now.Add(modelBreakerTTL)
		return
	}
	n.Failures++
	if n.Health == HealthHealthy {
		n.Health = HealthSuspect
	}
	if n.Failures >= downAfterFailures {
		n.Health = HealthDown
	}
	n.NextPoll = now
}

// RequestSucceeded is proof of life: an authenticated request from the member,
// or an answer it streamed back, says it is up whatever the polls concluded.
// A member written off as down is healed here and polled again at once, which
// is what recovers a node whose address changed while this node was failing
// to reach its old one.
func (d *Directory) RequestSucceeded(nodeUUID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	now := d.now()
	n.LastSeen = now
	if n.Health != HealthHealthy {
		n.Health = HealthHealthy
		n.Failures = 0
		n.LastError = ""
		n.NextPoll = now
	}
}

// ModelBroken reports whether the (node, model) breaker is open.
func (d *Directory) ModelBroken(nodeUUID, modelID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	until, ok := d.breakers[nodeUUID+"\x00"+modelID]
	if !ok {
		return false
	}
	if d.now().After(until) {
		delete(d.breakers, nodeUUID+"\x00"+modelID)
		return false
	}
	return true
}

// Reserve counts a request dispatched to a node; the returned func releases it.
func (d *Directory) Reserve(nodeUUID string) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.node(nodeUUID)
	n.reserveSeq++
	id := n.reserveSeq
	n.reservations[id] = d.now()
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if cur, ok := d.nodes[nodeUUID]; ok {
			delete(cur.reservations, id)
		}
	}
}

// Snapshot copies the runtime view of every member.
func (d *Directory) Snapshot() []NodeRuntime {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	out := make([]NodeRuntime, 0, len(d.nodes))
	for _, n := range d.nodes {
		c := *n
		c.Observed = nil
		c.reservations = nil
		c.reserveSeq = int64(n.Reserved(now))
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UUID < out[j].UUID })
	return out
}

// Get returns one member's runtime view.
func (d *Directory) Get(nodeUUID string) (NodeRuntime, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n, ok := d.nodes[nodeUUID]
	if !ok {
		return NodeRuntime{}, false
	}
	c := *n
	c.Observed = nil
	c.reservations = nil
	c.reserveSeq = int64(n.Reserved(d.now()))
	return c, true
}

// jitter derives a stable per-node offset so polls do not align.
func jitter(seed string, max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	var h uint32 = 2166136261
	for i := 0; i < len(seed); i++ {
		h ^= uint32(seed[i])
		h *= 16777619
	}
	return time.Duration(h % uint32(max))
}

// endpointHost splits host:port, tolerating a bare host.
func endpointHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// withPort replaces or adds the port of an endpoint.
func withPort(addr string, port int) string {
	host := endpointHost(addr)
	if host == "" {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	clusterFile = "cluster.json"
	// joinTokenPrefix versions the token format so a future change can be
	// told apart from a typo.
	joinTokenPrefix = "csgl1"
	// nodeCodeTTL bounds how long an admission code shown on an unpaired
	// node stays valid.
	nodeCodeTTL = 10 * time.Minute
	// tombstoneTTL keeps a removed member out of gossip long enough for every
	// peer to hear about the removal, even ones that were offline at the time.
	tombstoneTTL = 24 * time.Hour
	// maxLastAddresses is how many recently successful addresses a member
	// keeps; they are probed first on startup, before discovery answers.
	maxLastAddresses = 3
)

// RoutingMode selects where a request goes when the caller names no source.
type RoutingMode string

const (
	// RoutingLocalFirst keeps today's behaviour: a model present on this node
	// runs here; only models this node lacks are routed to peers.
	RoutingLocalFirst RoutingMode = "local_first"
	// RoutingBalanced hands every request to the scheduler; this node is one
	// candidate among the members holding the model.
	RoutingBalanced RoutingMode = "balanced"
)

// NodeState is the operator-controlled admission state of a member.
type NodeState string

const (
	NodeStateActive      NodeState = "active"
	NodeStateDrain       NodeState = "drain"
	NodeStateMaintenance NodeState = "maintenance"
)

// ClusterInfo names the cluster this node belongs to.
type ClusterInfo struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Settings are the operator-tunable knobs; they live next to the member table
// so one file is the single source of truth for cluster state.
type Settings struct {
	AcceptWork       bool        `json:"accept_work"`
	State            NodeState   `json:"state"`
	Weight           int         `json:"weight"`
	PreferLocal      bool        `json:"prefer_local"`
	RoutingMode      RoutingMode `json:"routing_mode"`
	AffinityMaxQueue int         `json:"affinity_max_queue"`
	DiskReserveGB    int         `json:"disk_reserve_gb"`
	// StaticAddresses maps a member UUID to a host:port the operator pinned,
	// for networks that do not forward multicast.
	StaticAddresses map[string]string `json:"static_addresses"`
	// Enabled records that the operator switched the cluster feature on
	// (created, joined, invited or showed an admission code), so the node
	// comes up listening and discovering after a restart. A plain single
	// machine keeps it false and runs no cluster networking at all.
	Enabled bool `json:"enabled,omitempty"`
	// AutoFormPaused is set when an operator explicitly leaves a cluster on
	// a secret-provisioned node, so automatic formation does not pull the
	// node straight back in. Creating or joining a cluster clears it.
	AutoFormPaused bool `json:"auto_form_paused,omitempty"`
}

func defaultSettings() Settings {
	return Settings{
		AcceptWork:       true,
		State:            NodeStateActive,
		Weight:           100,
		PreferLocal:      true,
		RoutingMode:      RoutingLocalFirst,
		AffinityMaxQueue: 2,
		DiskReserveGB:    50,
		StaticAddresses:  map[string]string{},
	}
}

func (s *Settings) normalize() {
	if s.Weight < 10 {
		s.Weight = 10
	}
	if s.Weight > 200 {
		s.Weight = 200
	}
	switch s.State {
	case NodeStateActive, NodeStateDrain, NodeStateMaintenance:
	default:
		s.State = NodeStateActive
	}
	switch s.RoutingMode {
	case RoutingLocalFirst, RoutingBalanced:
	default:
		s.RoutingMode = RoutingLocalFirst
	}
	if s.AffinityMaxQueue < 0 {
		s.AffinityMaxQueue = 0
	}
	if s.DiskReserveGB < 0 {
		s.DiskReserveGB = 0
	}
	if s.StaticAddresses == nil {
		s.StaticAddresses = map[string]string{}
	}
}

// Member is a paired node as this node knows it. The certificate fingerprint
// is the identity; addresses are hints that change freely.
type Member struct {
	UUID            string    `json:"uuid"`
	Name            string    `json:"name"`
	CertFingerprint string    `json:"cert_fingerprint"`
	CertPEM         string    `json:"cert_pem"`
	JoinedAt        time.Time `json:"joined_at"`
	// LastAddresses are host:port cluster endpoints that recently worked,
	// newest first.
	LastAddresses []string `json:"last_addresses"`
	APIPort       int      `json:"api_port"`
	ClusterPort   int      `json:"cluster_port"`
}

// Tombstone records a removal so gossip cannot resurrect the member.
type Tombstone struct {
	UUID      string    `json:"uuid"`
	RemovedAt time.Time `json:"removed_at"`
}

type nodeCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// storeFile is the on-disk shape of cluster.json.
type storeFile struct {
	Version       int          `json:"version"`
	Cluster       *ClusterInfo `json:"cluster,omitempty"`
	JoinTokenHash string       `json:"join_token_hash,omitempty"`
	Settings      Settings     `json:"settings"`
	Members       []Member     `json:"members"`
	Tombstones    []Tombstone  `json:"tombstones,omitempty"`
	NodeCode      *nodeCode    `json:"node_code,omitempty"`
}

// Store persists membership under <dir>/cluster.json. Every mutation is
// written through so a crash never loses a pairing.
type Store struct {
	mu   sync.RWMutex
	path string
	data storeFile
	// joinToken is kept in memory so the operator can read it back; only the
	// hash is persisted. After a restart the token must be rotated to be shown.
	joinToken string
}

// OpenStore loads the store from dir, creating an empty one when absent.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st := &Store{path: filepath.Join(dir, clusterFile)}
	raw, err := os.ReadFile(st.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &st.data); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", clusterFile, err)
		}
	case errors.Is(err, os.ErrNotExist):
		st.data = storeFile{Version: 1, Settings: defaultSettings(), Members: []Member{}}
	default:
		return nil, err
	}
	if st.data.Version == 0 {
		st.data.Version = 1
	}
	if st.data.Members == nil {
		st.data.Members = []Member{}
	}
	st.data.Settings.normalize()
	st.pruneLocked(time.Now())
	return st, nil
}

func (st *Store) saveLocked() error {
	st.data.Settings.normalize()
	sort.Slice(st.data.Members, func(i, j int) bool { return st.data.Members[i].UUID < st.data.Members[j].UUID })
	raw, err := json.MarshalIndent(st.data, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(st.path, raw, 0o600)
}

func (st *Store) pruneLocked(now time.Time) {
	kept := st.data.Tombstones[:0]
	for _, t := range st.data.Tombstones {
		if now.Sub(t.RemovedAt) < tombstoneTTL {
			kept = append(kept, t)
		}
	}
	st.data.Tombstones = kept
	if st.data.NodeCode != nil && now.After(st.data.NodeCode.ExpiresAt) {
		st.data.NodeCode = nil
	}
}

// Cluster returns the cluster this node belongs to, or nil.
func (st *Store) Cluster() *ClusterInfo {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.data.Cluster == nil {
		return nil
	}
	c := *st.data.Cluster
	return &c
}

// InCluster reports whether this node has a cluster identity.
func (st *Store) InCluster() bool { return st.Cluster() != nil }

// Settings returns a copy of the current settings.
func (st *Store) Settings() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s := st.data.Settings
	s.StaticAddresses = copyMap(s.StaticAddresses)
	return s
}

// UpdateSettings applies fn to a copy of the settings and persists it.
func (st *Store) UpdateSettings(fn func(*Settings)) (Settings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.data.Settings
	s.StaticAddresses = copyMap(s.StaticAddresses)
	fn(&s)
	s.normalize()
	st.data.Settings = s
	if err := st.saveLocked(); err != nil {
		return Settings{}, err
	}
	out := s
	out.StaticAddresses = copyMap(s.StaticAddresses)
	return out, nil
}

// Members returns a copy of the member table, sorted by UUID.
func (st *Store) Members() []Member {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return copyMembers(st.data.Members)
}

// Member looks up one member by UUID.
func (st *Store) Member(nodeUUID string) (Member, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	for _, m := range st.data.Members {
		if m.UUID == nodeUUID {
			m.LastAddresses = append([]string(nil), m.LastAddresses...)
			return m, true
		}
	}
	return Member{}, false
}

// FingerprintFor returns the pinned certificate fingerprint of a member.
func (st *Store) FingerprintFor(nodeUUID string) (string, bool) {
	m, ok := st.Member(nodeUUID)
	if !ok {
		return "", false
	}
	return m.CertFingerprint, true
}

// IsTombstoned reports whether the member was removed recently.
func (st *Store) IsTombstoned(nodeUUID string) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.tombstonedLocked(nodeUUID, time.Now())
}

func (st *Store) tombstonedLocked(nodeUUID string, now time.Time) bool {
	for _, t := range st.data.Tombstones {
		if t.UUID == nodeUUID && now.Sub(t.RemovedAt) < tombstoneTTL {
			return true
		}
	}
	return false
}

// Tombstones returns the recent removals for gossip.
func (st *Store) Tombstones() []Tombstone {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return append([]Tombstone(nil), st.data.Tombstones...)
}

// ErrAlreadyClustered is returned when creating or joining while a member.
var ErrAlreadyClustered = errors.New("this node already belongs to a cluster")

// ErrNotClustered is returned by operations that need a cluster.
var ErrNotClustered = errors.New("this node does not belong to a cluster")

// Create founds a new cluster of one. The join token is returned once.
func (st *Store) Create(name string) (ClusterInfo, string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster != nil {
		return ClusterInfo{}, "", ErrAlreadyClustered
	}
	info := ClusterInfo{UUID: uuid.NewString(), Name: strings.TrimSpace(name), CreatedAt: time.Now().UTC()}
	if info.Name == "" {
		info.Name = "CSGLite Cluster"
	}
	token, hash, err := newJoinToken(info.UUID)
	if err != nil {
		return ClusterInfo{}, "", err
	}
	st.data.Cluster = &info
	st.data.JoinTokenHash = hash
	st.data.Members = []Member{}
	st.data.Tombstones = nil
	st.data.NodeCode = nil
	st.data.Settings.AutoFormPaused = false
	st.joinToken = token
	if err := st.saveLocked(); err != nil {
		return ClusterInfo{}, "", err
	}
	return info, token, nil
}

// CreateDerived founds a cluster with an identity and join-token hash that
// were derived from a shared secret, so every node with the secret founds or
// joins the very same cluster.
func (st *Store) CreateDerived(info ClusterInfo, tokenHash string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster != nil {
		return ErrAlreadyClustered
	}
	st.data.Cluster = &info
	st.data.JoinTokenHash = tokenHash
	st.data.Members = []Member{}
	st.data.Tombstones = nil
	st.data.NodeCode = nil
	st.data.Settings.AutoFormPaused = false
	st.joinToken = ""
	return st.saveLocked()
}

// Adopt makes this node a member of an existing cluster with the given
// members, as learned in a join or invite handshake. The join token hash is
// recorded so this node can admit further members with the same token.
func (st *Store) Adopt(info ClusterInfo, tokenHash string, members []Member, selfUUID string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster != nil && st.data.Cluster.UUID != info.UUID {
		return ErrAlreadyClustered
	}
	st.data.Cluster = &info
	if tokenHash != "" {
		st.data.JoinTokenHash = tokenHash
	}
	st.data.Settings.AutoFormPaused = false
	st.data.NodeCode = nil
	st.data.Tombstones = nil
	st.data.Members = []Member{}
	for _, m := range members {
		if m.UUID == selfUUID || m.UUID == "" || m.CertFingerprint == "" {
			continue
		}
		st.data.Members = append(st.data.Members, m)
	}
	return st.saveLocked()
}

// Leave forgets the cluster and every member but keeps the node identity.
func (st *Store) Leave() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster == nil {
		return ErrNotClustered
	}
	st.data.Cluster = nil
	st.data.JoinTokenHash = ""
	st.data.Members = []Member{}
	st.data.Tombstones = nil
	st.joinToken = ""
	return st.saveLocked()
}

// Upsert adds or refreshes a member. A tombstoned UUID is ignored unless force
// is set (a fresh, explicit pairing overrides an old removal).
func (st *Store) Upsert(m Member, force bool) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster == nil {
		return false, ErrNotClustered
	}
	if m.UUID == "" || m.CertFingerprint == "" {
		return false, errors.New("member needs a uuid and a certificate fingerprint")
	}
	now := time.Now()
	if st.tombstonedLocked(m.UUID, now) {
		if !force {
			return false, nil
		}
		kept := st.data.Tombstones[:0]
		for _, t := range st.data.Tombstones {
			if t.UUID != m.UUID {
				kept = append(kept, t)
			}
		}
		st.data.Tombstones = kept
	}
	changed := false
	found := false
	for i := range st.data.Members {
		cur := &st.data.Members[i]
		if cur.UUID != m.UUID {
			continue
		}
		found = true
		if cur.CertFingerprint != m.CertFingerprint {
			// A different certificate under a known UUID is either a
			// re-installed node or an impostor. Only an explicit pairing
			// may replace the pin.
			if !force {
				return false, fmt.Errorf("member %s presents a different certificate; remove and pair it again", m.UUID)
			}
			cur.CertFingerprint = m.CertFingerprint
			cur.CertPEM = m.CertPEM
			changed = true
		}
		if m.Name != "" && m.Name != cur.Name {
			cur.Name = m.Name
			changed = true
		}
		if m.APIPort != 0 && m.APIPort != cur.APIPort {
			cur.APIPort = m.APIPort
			changed = true
		}
		if m.ClusterPort != 0 && m.ClusterPort != cur.ClusterPort {
			cur.ClusterPort = m.ClusterPort
			changed = true
		}
		if cur.CertPEM == "" && m.CertPEM != "" {
			cur.CertPEM = m.CertPEM
			changed = true
		}
		for _, addr := range m.LastAddresses {
			if pushAddress(&cur.LastAddresses, addr) {
				changed = true
			}
		}
	}
	if !found {
		if m.JoinedAt.IsZero() {
			m.JoinedAt = now.UTC()
		}
		st.data.Members = append(st.data.Members, m)
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, st.saveLocked()
}

// RecordAddress remembers a cluster endpoint that just worked for a member.
func (st *Store) RecordAddress(nodeUUID, addr string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.data.Members {
		if st.data.Members[i].UUID == nodeUUID {
			if pushAddress(&st.data.Members[i].LastAddresses, addr) {
				_ = st.saveLocked()
			}
			return
		}
	}
}

// Remove drops a member and leaves a tombstone.
func (st *Store) Remove(nodeUUID string) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.removeLocked(nodeUUID, time.Now())
}

func (st *Store) removeLocked(nodeUUID string, now time.Time) (bool, error) {
	kept := st.data.Members[:0]
	removed := false
	for _, m := range st.data.Members {
		if m.UUID == nodeUUID {
			removed = true
			continue
		}
		kept = append(kept, m)
	}
	st.data.Members = kept
	if !removed && st.tombstonedLocked(nodeUUID, now) {
		return false, nil
	}
	st.data.Tombstones = append(st.data.Tombstones, Tombstone{UUID: nodeUUID, RemovedAt: now.UTC()})
	return removed, st.saveLocked()
}

// ApplyTombstones removes members that peers report as removed.
func (st *Store) ApplyTombstones(ts []Tombstone) (removed []string, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	for _, t := range ts {
		if t.UUID == "" || now.Sub(t.RemovedAt) >= tombstoneTTL {
			continue
		}
		if st.tombstonedLocked(t.UUID, now) {
			continue
		}
		gone, rmErr := st.removeLocked(t.UUID, t.RemovedAt)
		if rmErr != nil {
			return removed, rmErr
		}
		if gone {
			removed = append(removed, t.UUID)
		}
	}
	return removed, nil
}

// ---- join tokens and admission codes ----

// JoinToken returns the token minted in this process, or "" after a restart.
func (st *Store) JoinToken() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.joinToken
}

// JoinTokenHash returns the persisted hash for sharing with new members.
func (st *Store) JoinTokenHash() string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.data.JoinTokenHash
}

// RotateJoinToken mints a new token; already paired members are unaffected.
func (st *Store) RotateJoinToken() (string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster == nil {
		return "", ErrNotClustered
	}
	token, hash, err := newJoinToken(st.data.Cluster.UUID)
	if err != nil {
		return "", err
	}
	st.data.JoinTokenHash = hash
	st.joinToken = token
	return token, st.saveLocked()
}

// VerifyJoinToken checks a presented token against the stored hash.
func (st *Store) VerifyJoinToken(token string) bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.data.Cluster == nil || st.data.JoinTokenHash == "" {
		return false
	}
	clusterUUID, secret, err := ParseJoinToken(token)
	if err != nil || clusterUUID != st.data.Cluster.UUID {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(st.data.JoinTokenHash)) == 1
}

// NodeCode returns the current admission code for this unpaired node,
// minting one when none is valid.
func (st *Store) NodeCode() (string, time.Time, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster != nil {
		return "", time.Time{}, ErrAlreadyClustered
	}
	now := time.Now()
	if st.data.NodeCode == nil || now.After(st.data.NodeCode.ExpiresAt) {
		code, err := randomDigits(8)
		if err != nil {
			return "", time.Time{}, err
		}
		st.data.NodeCode = &nodeCode{Code: code, ExpiresAt: now.Add(nodeCodeTTL).UTC()}
		if err := st.saveLocked(); err != nil {
			return "", time.Time{}, err
		}
	}
	return st.data.NodeCode.Code, st.data.NodeCode.ExpiresAt, nil
}

// VerifyNodeCode checks an admission code and consumes it on success.
func (st *Store) VerifyNodeCode(code string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.data.Cluster != nil || st.data.NodeCode == nil {
		return false
	}
	if time.Now().After(st.data.NodeCode.ExpiresAt) {
		return false
	}
	ok := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(st.data.NodeCode.Code)) == 1
	if ok {
		st.data.NodeCode = nil
		_ = st.saveLocked()
	}
	return ok
}

func newJoinToken(clusterUUID string) (token, hash string, err error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	secret := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	return joinTokenPrefix + "-" + clusterUUID + "-" + secret, hashSecret(secret), nil
}

// ParseJoinToken splits a token into the cluster UUID it names and its secret.
func ParseJoinToken(token string) (clusterUUID, secret string, err error) {
	token = strings.TrimSpace(token)
	parts := strings.SplitN(token, "-", 2)
	if len(parts) != 2 || parts[0] != joinTokenPrefix {
		return "", "", errors.New("join token must start with csgl1-")
	}
	rest := parts[1]
	if len(rest) < 37 {
		return "", "", errors.New("join token is too short")
	}
	clusterUUID = rest[:36]
	if _, err := uuid.Parse(clusterUUID); err != nil || rest[36] != '-' {
		return "", "", errors.New("join token does not name a cluster")
	}
	secret = rest[37:]
	if secret == "" {
		return "", "", errors.New("join token has no secret")
	}
	return clusterUUID, secret, nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HandshakeMAC authenticates a join or invite handshake with a shared secret
// (the join token secret or an admission code). Everything the verifier will
// pin is covered, so a relay cannot swap in its own certificate.
func HandshakeMAC(secret, nodeUUID, fingerprint, nonce string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s\n%s\n%s\n%d", nodeUUID, fingerprint, nonce, ts)
	return hex.EncodeToString(mac.Sum(nil))
}

func randomDigits(n int) (string, error) {
	const digits = "0123456789"
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range raw {
		out[i] = digits[int(b)%len(digits)]
	}
	return string(out), nil
}

func pushAddress(list *[]string, addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	if len(*list) > 0 && (*list)[0] == addr {
		return false
	}
	out := []string{addr}
	for _, a := range *list {
		if a != addr {
			out = append(out, a)
		}
	}
	if len(out) > maxLastAddresses {
		out = out[:maxLastAddresses]
	}
	*list = out
	return true
}

func copyMembers(in []Member) []Member {
	out := make([]Member, len(in))
	for i, m := range in {
		m.LastAddresses = append([]string(nil), m.LastAddresses...)
		out[i] = m
	}
	return out
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

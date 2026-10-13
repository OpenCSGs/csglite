package license

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultSyncInterval is how often Syncer.Run asks the issuer for the
// account's license.
const DefaultSyncInterval = 6 * time.Hour

// Remote license statuses reported by the issuer.
const (
	RemoteStatusActive  = "active"
	RemoteStatusRevoked = "revoked"
	RemoteStatusNone    = "none"
)

// ErrNoCredentials means there is no signed-in CSGHub account to sync for.
// Sync treats it as a no-op rather than a failure.
var ErrNoCredentials = errors.New("not signed in to CSGHub")

// RemoteLicense is the issuer's answer for the signed-in account: the license
// text when Status is RemoteStatusActive, the product the issuer says it is
// for, and how many devices the account has used it on.
type RemoteLicense struct {
	Status      string
	Product     string
	License     string
	DeviceCount int
}

// FetchFunc asks the issuer for the current CSGLite license of the signed-in
// account, reporting this installation as deviceID so the issuer can count
// it. It returns ErrNoCredentials when nobody is signed in.
type FetchFunc func(ctx context.Context, deviceID string) (RemoteLicense, error)

// Sync actions.
const (
	SyncInstalled = "installed"
	SyncRemoved   = "removed"
	SyncUnchanged = "unchanged"
	SyncSkipped   = "skipped"
)

// SyncResult reports what one Sync did and the license state afterwards.
type SyncResult struct {
	Action string
	// Reason explains SyncSkipped and SyncUnchanged.
	Reason string
	State  State
}

// Syncer keeps an automatically fetched license in step with the issuer. It
// only ever replaces or removes a license it installed itself (OriginAuto);
// a license imported by hand or supplied through the environment is left
// alone unless it no longer grants anything.
type Syncer struct {
	manager *Manager
	fetch   FetchFunc

	mu sync.Mutex
	// deviceCount is the issuer's device count from the last successful
	// fetch. It is kept apart from mu so reading it never waits on a sync.
	deviceCount atomic.Int64
}

// NewSyncer returns a Syncer installing into manager what fetch returns.
func NewSyncer(manager *Manager, fetch FetchFunc) *Syncer {
	return &Syncer{manager: manager, fetch: fetch}
}

// Sync fetches the account's license once and applies it. A network or
// issuer error leaves the local license untouched and is returned.
func (s *Syncer) Sync(ctx context.Context) (SyncResult, error) {
	if s == nil || s.manager == nil || s.fetch == nil {
		return SyncResult{Action: SyncSkipped, Reason: "license sync is not configured"}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	local := s.manager.State()
	if s.manager.ManagedByEnv() {
		return SyncResult{Action: SyncSkipped, Reason: "the license is provided by " + EnvLicense, State: local}, nil
	}
	remote, err := s.fetch(ctx, s.manager.DeviceID())
	if errors.Is(err, ErrNoCredentials) {
		// The count belonged to the account that signed out.
		s.deviceCount.Store(0)
		return SyncResult{Action: SyncSkipped, Reason: err.Error(), State: local}, nil
	}
	if err != nil {
		return SyncResult{Action: SyncUnchanged, State: local}, err
	}
	s.deviceCount.Store(int64(max(remote.DeviceCount, 0)))

	switch remote.Status {
	case RemoteStatusActive:
		if remote.Product != ProductCSGLite {
			return SyncResult{Action: SyncUnchanged, State: local}, fmt.Errorf("the issuer returned a license for product %q, not %s", remote.Product, ProductCSGLite)
		}
		return s.applyActive(local, remote.License)
	case RemoteStatusRevoked:
		if local.Origin != OriginAuto {
			return SyncResult{Action: SyncUnchanged, Reason: "the issuer revoked the license, but the installed one was not fetched automatically", State: local}, nil
		}
		state, err := s.manager.Remove()
		if err != nil {
			return SyncResult{Action: SyncUnchanged, State: state}, fmt.Errorf("removing revoked license: %w", err)
		}
		log.Printf("license: removed a license the issuer revoked")
		return SyncResult{Action: SyncRemoved, State: state}, nil
	case RemoteStatusNone, "":
		return SyncResult{Action: SyncUnchanged, Reason: "the account has no license", State: local}, nil
	default:
		return SyncResult{Action: SyncUnchanged, State: local}, fmt.Errorf("unknown remote license status %q", remote.Status)
	}
}

func (s *Syncer) applyActive(local State, data string) (SyncResult, error) {
	candidate := s.manager.Verify(data)
	if !candidate.Licensed() {
		return SyncResult{Action: SyncUnchanged, State: local}, fmt.Errorf("the issuer's license does not verify here: %s", candidate.Reason)
	}
	if ok, reason := shouldReplace(local, candidate); !ok {
		return SyncResult{Action: SyncUnchanged, Reason: reason, State: local}, nil
	}
	state, err := s.manager.InstallAuto(data)
	if err != nil {
		return SyncResult{Action: SyncUnchanged, State: state}, err
	}
	log.Printf("license: installed %s license fetched from CSGHub (expires %s)", state.Edition(), candidate.Payload.ExpireTime.UTC().Format("2006-01-02"))
	return SyncResult{Action: SyncInstalled, State: state}, nil
}

// shouldReplace decides whether a verified candidate from the issuer replaces
// the local license.
func shouldReplace(local, candidate State) (bool, string) {
	// Nothing in effect and nothing waiting to start: there is nothing to
	// protect.
	if local.Payload == nil || local.Status == StatusInvalid || local.Status == StatusExpired || local.Status == StatusNone {
		return true, ""
	}
	if local.Origin != OriginAuto {
		return false, "a manually installed license is in place"
	}
	if local.Owner != candidate.Owner {
		return false, "the installed license belongs to another CSGHub account"
	}
	if sameLicense(local.Payload, candidate.Payload) {
		return false, "the installed license is up to date"
	}
	return true, ""
}

func sameLicense(a, b *RSAPayload) bool {
	return a.Key == b.Key &&
		a.Edition == b.Edition &&
		a.Extra == b.Extra &&
		a.StartTime.Equal(b.StartTime) &&
		a.ExpireTime.Equal(b.ExpireTime)
}

// DeviceCount is how many devices the signed-in account has used its license
// on, as the issuer reported at the last successful sync; 0 when unknown.
func (s *Syncer) DeviceCount() int {
	if s == nil {
		return 0
	}
	return int(s.deviceCount.Load())
}

// Run syncs once right away and then every interval until ctx is done.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	if s == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	s.syncAndLog(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.syncAndLog(ctx)
		}
	}
}

func (s *Syncer) syncAndLog(ctx context.Context) {
	if _, err := s.Sync(ctx); err != nil && ctx.Err() == nil {
		log.Printf("license: sync with CSGHub failed: %v", err)
	}
}

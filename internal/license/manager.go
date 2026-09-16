package license

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/safefile"
)

const (
	// EnvLicenseFile overrides the license file path (default
	// <storage root>/license.key).
	EnvLicenseFile = "CSGHUB_LITE_LICENSE_FILE"
	// EnvLicense supplies the license text directly, for containers and CI.
	// When set it takes precedence over the file and Install is refused.
	EnvLicense = "CSGHUB_LITE_LICENSE"
	// EnvPublicKeyFile points at a PEM public key that replaces the embedded
	// issuer keys.
	EnvPublicKeyFile = "CSGHUB_LITE_LICENSE_PUBLIC_KEY_FILE"

	// FileName is the license file inside the storage root.
	FileName = "license.key"
	// lastCheckFileName records the newest wall-clock time at which a license
	// was evaluated, to detect a clock that was turned back.
	lastCheckFileName = "license.lastcheck"

	// DefaultGracePeriod keeps features enabled after ExpireTime while the UI
	// warns about renewal.
	DefaultGracePeriod = 14 * 24 * time.Hour
	// DefaultRefreshInterval is how often Run re-reads the license source.
	DefaultRefreshInterval = time.Hour
	// clockRollbackTolerance is how far behind the last recorded check the
	// clock may be before the license is treated as suspect.
	clockRollbackTolerance = 24 * time.Hour
)

// Status is the evaluated state of the license source.
type Status string

const (
	// StatusNone means no license is installed; CSGLite runs as Community.
	StatusNone Status = "none"
	// StatusNotStarted means the license verifies but StartTime is in the future.
	StatusNotStarted Status = "not_started"
	// StatusValid means the license verifies and is within its term.
	StatusValid Status = "valid"
	// StatusGrace means ExpireTime passed less than the grace period ago (or
	// the clock appears to have been turned back); features stay enabled.
	StatusGrace Status = "grace"
	// StatusExpired means the grace period is over; CSGLite runs as Community.
	StatusExpired Status = "expired"
	// StatusInvalid means the data is unreadable, unsigned by a known key,
	// issued for another product, or requires a newer CSGLite.
	StatusInvalid Status = "invalid"
)

// State is an immutable snapshot of one evaluation.
type State struct {
	Status   Status
	Payload  *RSAPayload
	Features map[string]bool
	Limits   map[string]int
	// Warnings are non-fatal notes such as unknown Extra keys.
	Warnings []string
	// Reason explains StatusInvalid, StatusGrace and StatusExpired.
	Reason string
	// GraceUntil is set for StatusValid and StatusGrace.
	GraceUntil time.Time
	CheckedAt  time.Time
	// Source is "env", "file" or "" when nothing was loaded.
	Source string
}

// Licensed reports whether features from the license are in effect.
func (s State) Licensed() bool {
	return s.Status == StatusValid || s.Status == StatusGrace
}

// Edition is the edition to display: the payload's edition while licensed,
// otherwise Community.
func (s State) Edition() string {
	if s.Licensed() && s.Payload != nil && strings.TrimSpace(s.Payload.Edition) != "" {
		return s.Payload.Edition
	}
	return EditionCommunity
}

// Enabled reports whether a boolean feature is in effect. A feature that is
// not gated is always enabled; a gated one follows the license.
func (s State) Enabled(def FeatureDefinition) bool {
	if def.Type != FeatureTypeBoolean {
		return false
	}
	if !def.Gated {
		return true
	}
	return s.Features[def.Key]
}

// Limit returns an integer limit; 0 means unlimited. A limit that is not
// gated is always unlimited; a gated one comes from the license.
func (s State) Limit(def FeatureDefinition) int {
	if def.Type != FeatureTypeInt || !def.Gated {
		return 0
	}
	return s.Limits[def.Key]
}

// EnabledKeys lists the boolean features currently in effect, sorted.
func (s State) EnabledKeys() []string {
	keys := make([]string, 0, len(s.Features))
	for key, on := range s.Features {
		if on {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// Options configures a Manager.
type Options struct {
	// FilePath is the license file. Required unless Inline is set.
	FilePath string
	// Inline is license text that overrides the file (from EnvLicense).
	Inline string
	// PublicKeys verify signatures.
	PublicKeys []*rsa.PublicKey
	// Version is the running CSGLite version, checked against DataBody.Version.
	Version string
	// Catalog defaults to Catalog().
	Catalog []FeatureDefinition
	// Now defaults to time.Now.
	Now func() time.Time
	// GracePeriod defaults to DefaultGracePeriod.
	GracePeriod time.Duration
	// LastCheckPath stores the clock-rollback marker; empty disables it.
	LastCheckPath string
}

// DefaultOptions builds Options from the storage root and environment.
func DefaultOptions(storageRoot, version string) (Options, error) {
	keys, err := ResolvePublicKeys(os.Getenv)
	if err != nil {
		return Options{}, err
	}
	filePath := strings.TrimSpace(os.Getenv(EnvLicenseFile))
	if filePath == "" {
		filePath = filepath.Join(storageRoot, FileName)
	}
	lastCheck := ""
	if strings.TrimSpace(storageRoot) != "" {
		lastCheck = filepath.Join(storageRoot, lastCheckFileName)
	}
	return Options{
		FilePath:      filePath,
		Inline:        os.Getenv(EnvLicense),
		PublicKeys:    keys,
		Version:       version,
		LastCheckPath: lastCheck,
	}, nil
}

// Manager loads, verifies and caches the license state.
type Manager struct {
	opts Options

	mu    sync.RWMutex
	state State
}

// NewManager returns a Manager with no state loaded; call Refresh.
func NewManager(opts Options) *Manager {
	if opts.Catalog == nil {
		opts.Catalog = Catalog()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.GracePeriod <= 0 {
		opts.GracePeriod = DefaultGracePeriod
	}
	return &Manager{opts: opts, state: State{Status: StatusNone}}
}

// FilePath returns where Install writes the license.
func (m *Manager) FilePath() string {
	if m == nil {
		return ""
	}
	return m.opts.FilePath
}

// State returns the last evaluated state. A nil Manager reports Community.
func (m *Manager) State() State {
	if m == nil {
		return State{Status: StatusNone}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Enabled is State().Enabled.
func (m *Manager) Enabled(def FeatureDefinition) bool { return m.State().Enabled(def) }

// Limit is State().Limit.
func (m *Manager) Limit(def FeatureDefinition) int { return m.State().Limit(def) }

// Refresh re-reads the license source and re-evaluates it.
func (m *Manager) Refresh() State {
	if m == nil {
		return State{Status: StatusNone}
	}
	data, source, readErr := m.read()
	now := m.opts.Now()

	var state State
	switch {
	case readErr != nil:
		state = State{Status: StatusInvalid, Reason: readErr.Error()}
	case data == "":
		state = State{Status: StatusNone}
	default:
		state = m.evaluate(data, now)
	}
	state.Source = source
	state.CheckedAt = now
	m.fillUnlicensed(&state)

	if state.Licensed() && m.clockTurnedBack(now) {
		state.Status = StatusGrace
		state.Reason = "system clock is earlier than the last recorded license check; verify the system time"
	}
	m.recordCheck(now)

	m.mu.Lock()
	m.state = state
	m.mu.Unlock()
	return state
}

// Verify decodes and evaluates data without saving it.
func (m *Manager) Verify(data string) State {
	if m == nil {
		return State{Status: StatusInvalid, Reason: "license manager is not initialised"}
	}
	state := m.evaluate(data, m.opts.Now())
	state.CheckedAt = m.opts.Now()
	m.fillUnlicensed(&state)
	return state
}

// fillUnlicensed gives an unlicensed state the entitlements every edition
// has: ungated features on, gated ones off.
func (m *Manager) fillUnlicensed(state *State) {
	if state.Licensed() {
		return
	}
	state.Features, state.Limits = resolveEntitlements("", Extra{}, m.opts.Catalog)
}

// Install verifies data, writes it to the license file and refreshes. Like
// CSGHub's import it refuses a license that cannot become active: invalid
// data, another product, or one whose term has already ended.
func (m *Manager) Install(data string) (State, error) {
	if m == nil {
		return State{}, errors.New("license manager is not initialised")
	}
	if strings.TrimSpace(m.opts.Inline) != "" {
		return State{}, fmt.Errorf("the license is provided by %s; unset it before installing a file", EnvLicense)
	}
	if strings.TrimSpace(m.opts.FilePath) == "" {
		return State{}, errors.New("no license file path configured")
	}
	candidate := m.evaluate(data, m.opts.Now())
	switch candidate.Status {
	case StatusInvalid:
		return candidate, fmt.Errorf("license rejected: %s", candidate.Reason)
	case StatusExpired:
		return candidate, fmt.Errorf("license rejected: it expired on %s", candidate.Payload.ExpireTime.UTC().Format(time.RFC3339))
	}
	normalized := strings.TrimSpace(strings.ReplaceAll(data, "\r\n", "\n")) + "\n"
	if err := safefile.Write(m.opts.FilePath, []byte(normalized), 0o600); err != nil {
		return candidate, fmt.Errorf("writing license file: %w", err)
	}
	return m.Refresh(), nil
}

// Remove deletes the license file and refreshes.
func (m *Manager) Remove() (State, error) {
	if m == nil {
		return State{}, errors.New("license manager is not initialised")
	}
	if strings.TrimSpace(m.opts.Inline) != "" {
		return State{}, fmt.Errorf("the license is provided by %s; unset it to remove the license", EnvLicense)
	}
	if m.opts.FilePath != "" {
		if err := os.Remove(m.opts.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return m.State(), fmt.Errorf("removing license file: %w", err)
		}
	}
	return m.Refresh(), nil
}

// Run refreshes periodically until ctx is done.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	if m == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			before := m.State().Status
			after := m.Refresh().Status
			if before != after {
				log.Printf("license: status changed from %s to %s", before, after)
			}
		}
	}
}

func (m *Manager) read() (data, source string, err error) {
	if inline := strings.TrimSpace(m.opts.Inline); inline != "" {
		return inline, "env", nil
	}
	if strings.TrimSpace(m.opts.FilePath) == "" {
		return "", "", nil
	}
	raw, err := os.ReadFile(m.opts.FilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "file", fmt.Errorf("reading license file %s: %w", m.opts.FilePath, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", "file", nil
	}
	return text, "file", nil
}

// evaluate is the pure core: it never touches the filesystem or the cache.
func (m *Manager) evaluate(data string, now time.Time) State {
	payload, err := Decode(data, m.opts.PublicKeys)
	if err != nil {
		return State{Status: StatusInvalid, Reason: err.Error()}
	}
	state := State{Payload: payload}

	if payload.Product != ProductCSGLite {
		state.Status = StatusInvalid
		state.Reason = fmt.Sprintf("license is issued for product %q, not %s", payload.Product, ProductCSGLite)
		return state
	}
	if !versionSatisfies(m.opts.Version, payload.Version) {
		state.Status = StatusInvalid
		state.Reason = fmt.Sprintf("license requires CSGLite %s or newer, this is %s", payload.Version, m.opts.Version)
		return state
	}
	extra, warnings, err := ParseExtra(payload.Extra, m.opts.Catalog)
	if err != nil {
		state.Status = StatusInvalid
		state.Reason = err.Error()
		return state
	}
	state.Warnings = warnings
	if payload.ExpireTime.Before(payload.StartTime) {
		state.Status = StatusInvalid
		state.Reason = "license expires before it starts"
		return state
	}

	graceUntil := payload.ExpireTime.Add(m.opts.GracePeriod)
	switch {
	case now.Before(payload.StartTime):
		state.Status = StatusNotStarted
		state.Reason = fmt.Sprintf("license starts on %s", payload.StartTime.UTC().Format(time.RFC3339))
		return state
	case !now.After(payload.ExpireTime):
		state.Status = StatusValid
	case !now.After(graceUntil):
		state.Status = StatusGrace
		state.Reason = fmt.Sprintf("license expired on %s; features remain enabled until %s",
			payload.ExpireTime.UTC().Format(time.RFC3339), graceUntil.UTC().Format(time.RFC3339))
	default:
		state.Status = StatusExpired
		state.Reason = fmt.Sprintf("license expired on %s", payload.ExpireTime.UTC().Format(time.RFC3339))
		return state
	}
	state.GraceUntil = graceUntil
	state.Features, state.Limits = resolveEntitlements(payload.Edition, extra, m.opts.Catalog)
	if payload.Edition != EditionEnterprise {
		state.Warnings = append(state.Warnings,
			fmt.Sprintf("edition %q has no default entitlements; only features listed in the license extra are enabled", payload.Edition))
	}
	return state
}

// resolveEntitlements applies CSGHub's provider semantics to the gated
// entries: an explicit value in Extra wins; otherwise an Enterprise license
// gets each catalog default and any other edition gets nothing. Ungated
// entries are always on (booleans) or unlimited (limits). An empty edition
// means "no license".
func resolveEntitlements(edition string, extra Extra, defs []FeatureDefinition) (map[string]bool, map[string]int) {
	features := make(map[string]bool, len(defs))
	limits := make(map[string]int, len(defs))
	enterprise := edition == EditionEnterprise
	for _, def := range defs {
		if !def.Gated {
			switch def.Type {
			case FeatureTypeBoolean:
				features[def.Key] = true
			case FeatureTypeInt:
				limits[def.Key] = 0
			}
			continue
		}
		switch def.Type {
		case FeatureTypeBoolean:
			if v, ok := extra.Features[def.Key]; ok {
				features[def.Key] = v
			} else if enterprise {
				features[def.Key], _ = def.DefaultValue.(bool)
			} else {
				features[def.Key] = false
			}
		case FeatureTypeInt:
			if v, ok := extra.Limits[def.Key]; ok {
				limits[def.Key] = v
			} else if enterprise {
				limits[def.Key], _ = def.DefaultValue.(int)
			} else {
				limits[def.Key] = 0
			}
		}
	}
	return features, limits
}

func (m *Manager) clockTurnedBack(now time.Time) bool {
	if m.opts.LastCheckPath == "" {
		return false
	}
	raw, err := os.ReadFile(m.opts.LastCheckPath)
	if err != nil {
		return false
	}
	last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return false
	}
	return now.Before(last.Add(-clockRollbackTolerance))
}

func (m *Manager) recordCheck(now time.Time) {
	if m.opts.LastCheckPath == "" {
		return
	}
	if raw, err := os.ReadFile(m.opts.LastCheckPath); err == nil {
		if last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw))); err == nil && !now.After(last) {
			return
		}
	}
	if err := safefile.Write(m.opts.LastCheckPath, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		log.Printf("license: recording check time: %v", err)
	}
}

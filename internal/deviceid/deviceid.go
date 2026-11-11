// Package deviceid derives a stable, machine-level identifier used when a
// CSGLite instance reports its device against a license.
//
// The identifier must be the same for two CSGLite instances that share a
// machine but not a storage directory (the desktop app and the CLI, for
// example), so it is computed from the operating system's machine id rather
// than from anything under the storage root. Only when no machine id can be
// read does it fall back to a UUID kept in the storage root, which is a
// documented exception: two storage directories then count as two devices.
package deviceid

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/internal/safefile"
)

const (
	// fallbackFileName holds the UUID used when the platform machine id is
	// unavailable. It lives in the storage root.
	fallbackFileName = "device_id"
	// hashPrefix is part of the id format: changing it changes every device id
	// and permanently consumes a new seat per machine.
	hashPrefix = "csglite-device-v1|"
)

// fallbackPattern accepts the UUIDs this package writes and the variants a
// user might paste in. Anything else is regenerated.
var fallbackPattern = regexp.MustCompile(`^[0-9a-f][0-9a-f-]{7,127}$`)

var (
	cacheMu sync.Mutex
	cache   = make(map[string]string)
)

// Resolve returns the device id for storageRoot, computing it once per storage
// root per process.
//
// storageRoot is where the fallback UUID is kept when the platform machine id
// cannot be read; an empty root makes that fallback unavailable and Resolve
// returns an error.
func Resolve(storageRoot string) (string, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if id := cache[storageRoot]; id != "" {
		return id, nil
	}
	id, err := resolve(machineID, storageRoot)
	if err != nil {
		return "", err
	}
	cache[storageRoot] = id
	return id, nil
}

// Hash derives the public device id from a platform machine id. It is exported
// for tests and for the normalization contract.
func Hash(goos, raw string) string {
	normalized := normalize(raw)
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hashPrefix + goos + "|" + normalized))
	return hex.EncodeToString(sum[:])
}

// normalize makes platform spellings of the same machine id compare equal:
// case, surrounding whitespace, quotes or braces, and runs of whitespace.
func normalize(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.Trim(value, `"'`)
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "{")
	value = strings.TrimSuffix(value, "}")
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimSpace(value)
}

func resolve(reader func() (string, error), storageRoot string) (string, error) {
	if raw, err := reader(); err == nil {
		if id := Hash(runtime.GOOS, raw); id != "" {
			return id, nil
		}
	} else {
		log.Printf("device id: platform machine id unavailable: %v", err)
	}
	id, err := fallback(storageRoot)
	if err != nil {
		return "", err
	}
	log.Printf("device id: using the storage-root fallback; this machine counts as a separate device from other storage directories")
	return id, nil
}

// fallback reads or creates the storage-root UUID and hashes it like a machine
// id. The file is created once and never rotated, because rotating it would
// consume a new license seat on every start.
//
// Two processes (desktop and CLI) starting for the first time at the same
// moment may each write a UUID. The atomic write keeps the file whole, and the
// last rename wins, so at worst the two processes use different ids until the
// next start reads the file back.
func fallback(storageRoot string) (string, error) {
	if strings.TrimSpace(storageRoot) == "" {
		return "", errors.New("no machine id available and no storage root for the fallback device id")
	}
	path := filepath.Join(storageRoot, fallbackFileName)
	if raw, err := os.ReadFile(path); err == nil {
		value := normalize(string(raw))
		if fallbackPattern.MatchString(value) {
			return Hash(runtime.GOOS, value), nil
		}
		log.Printf("device id: %s is invalid; generating a new fallback id", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("reading fallback device id %s: %w", path, err)
	}
	value := uuid.NewString()
	if err := safefile.Write(path, []byte(value+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing fallback device id %s: %w", path, err)
	}
	return Hash(runtime.GOOS, value), nil
}

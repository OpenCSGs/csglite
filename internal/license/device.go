package license

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/safefile"
)

// DeviceIDFileName holds this installation's device ID inside the storage
// root. The issuer records it to count the devices an account's license is
// used on; it does not restrict where a license works.
const DeviceIDFileName = "device.id"

// maxDeviceIDLength bounds what is accepted from the file, so a corrupted or
// hand-edited file cannot inject an arbitrarily long value into requests.
const maxDeviceIDLength = 128

// LoadOrCreateDeviceID returns the device ID stored under storageRoot,
// creating a random one on first use. It is a random identifier, not a
// hardware fingerprint: it survives restarts and upgrades but not a fresh
// storage root.
func LoadOrCreateDeviceID(storageRoot string) (string, error) {
	if strings.TrimSpace(storageRoot) == "" {
		return "", errors.New("no storage root for the device ID")
	}
	path := filepath.Join(storageRoot, DeviceIDFileName)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		id := strings.TrimSpace(string(raw))
		if validDeviceID(id) {
			return id, nil
		}
		// An unreadable value is replaced rather than trusted; the issuer
		// then counts this installation as a new device.
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("reading device ID: %w", err)
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating device ID: %w", err)
	}
	id := hex.EncodeToString(buf)
	if err := safefile.Write(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing device ID: %w", err)
	}
	return id, nil
}

func validDeviceID(id string) bool {
	if id == "" || len(id) > maxDeviceIDLength {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

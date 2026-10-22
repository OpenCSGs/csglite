package team

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Passwords are stored as "pbkdf2-sha256$<iterations>$<salt>$<key>" with
// base64 (raw, standard alphabet) salt and key. The iteration count follows
// the OWASP recommendation for PBKDF2-HMAC-SHA256.
const (
	passwordScheme    = "pbkdf2-sha256"
	passwordSaltBytes = 16
	passwordKeyBytes  = 32
)

// passwordIterations is a variable only so tests can make hashing cheap.
var passwordIterations = 600_000

var (
	dummyHashOnce sync.Once
	dummyHash     string
)

// dummyPasswordHash is checked against when a username does not exist, so the
// response time does not tell an attacker which usernames are real. It is
// computed on first use: hashing at package init would slow every CLI run.
func dummyPasswordHash() string {
	dummyHashOnce.Do(func() {
		dummyHash, _ = hashPassword("csglite-no-such-user")
	})
	return dummyHash
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", passwordScheme, passwordIterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func checkPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordScheme {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

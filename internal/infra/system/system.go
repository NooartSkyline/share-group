// Package system provides clock and ID generation backed by the real system.
package system

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"lanshare/internal/usecase/port"
)

// Clock implements port.Clock.
type Clock struct{}

func (Clock) Now() time.Time { return time.Now() }

// IDGenerator implements port.IDGenerator with 128-bit random hex IDs.
// The output matches the filestore's safe-name rule ([A-Za-z0-9_-]).
type IDGenerator struct{}

func (IDGenerator) NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("system: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// SecretGenerator implements port.SecretGenerator with 128 random bits from
// crypto/rand, hex encoded (32 lowercase characters). Every call draws fresh
// bytes, so a Session ID and a Resume_Key generated one after the other are
// independent and neither can be derived from the other.
//
// port.SecretGenerator.NewSecret has no error return. Like IDGenerator, an
// unreadable randomness source panics rather than returning a predictable
// value: a weak secret is worse than a failed request.
type SecretGenerator struct{}

// NewSecret returns 32 lowercase hex characters. It panics if crypto/rand
// cannot be read.
func (SecretGenerator) NewSecret() string {
	s, err := newSecret()
	if err != nil {
		panic(err.Error())
	}
	return s
}

// newSecret reads 128 bits from crypto/rand and wraps any failure with %w.
func newSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("system: read secret from crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

var _ port.SecretGenerator = SecretGenerator{}

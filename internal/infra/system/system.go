// Package system provides clock and ID generation backed by the real system.
package system

import (
	"crypto/rand"
	"encoding/hex"
	"time"
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

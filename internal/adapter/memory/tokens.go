package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

type session struct {
	device  domain.DeviceID
	expires time.Time
}

// TokenService implements port.TokenService with random opaque tokens.
type TokenService struct {
	clock port.Clock
	ttl   time.Duration

	mu       sync.Mutex
	sessions map[string]session
}

func NewTokenService(clock port.Clock, ttl time.Duration) *TokenService {
	return &TokenService{clock: clock, ttl: ttl, sessions: make(map[string]session)}
}

func (t *TokenService) Issue(_ context.Context, id domain.DeviceID) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()
	for k, s := range t.sessions { // drop expired sessions lazily
		if now.After(s.expires) {
			delete(t.sessions, k)
		}
	}
	t.sessions[token] = session{device: id, expires: now.Add(t.ttl)}
	return token, nil
}

func (t *TokenService) Resolve(_ context.Context, token string) (domain.DeviceID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sessions[token]
	if !ok {
		return "", domain.ErrUnauthorized
	}
	if t.clock.Now().After(s.expires) {
		delete(t.sessions, token)
		return "", domain.ErrUnauthorized
	}
	return s.device, nil
}

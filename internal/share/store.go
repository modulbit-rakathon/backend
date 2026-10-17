// Package share holds medication lists (and data handed back to the patient)
// in memory under one-time 4-digit PINs.
package share

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// ErrFull: no free PIN could be found (too many active shares).
var ErrFull = errors.New("pin store full")

const (
	// MaxActive caps live PINs. Every active PIN raises the odds that a
	// random guess hits something, so keep it far below 10 000.
	MaxActive = 1000
	// MockPIN is never issued; in mock mode it always returns the fixture.
	MockPIN = "0000"
)

type entry[T any] struct {
	value   T
	expires time.Time
}

type Store[T any] struct {
	ttl    time.Duration
	now    func() time.Time
	newPIN func() (string, error)

	mu      sync.Mutex
	entries map[string]entry[T]
}

func NewStore[T any](ttl time.Duration) *Store[T] {
	return &Store[T]{
		ttl:     ttl,
		now:     time.Now,
		newPIN:  randomPIN,
		entries: map[string]entry[T]{},
	}
}

func randomPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

// Create stores value under a fresh PIN unique among active ones.
func (s *Store[T]) Create(value T) (pin string, expires time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	if len(s.entries) >= MaxActive {
		return "", time.Time{}, ErrFull
	}
	for range 50 {
		pin, err := s.newPIN()
		if err != nil {
			return "", time.Time{}, err
		}
		if _, taken := s.entries[pin]; taken || pin == MockPIN {
			continue
		}
		expires = now.Add(s.ttl)
		s.entries[pin] = entry[T]{value: value, expires: expires}
		return pin, expires, nil
	}
	return "", time.Time{}, ErrFull
}

// Claim returns and deletes the value for pin. Missing and expired PINs look
// the same to the caller.
func (s *Store[T]) Claim(pin string) (T, bool) {
	var zero T
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[pin]
	if !ok {
		return zero, false
	}
	delete(s.entries, pin)
	if !s.now().Before(e.expires) {
		return zero, false
	}
	return e.value, true
}

func (s *Store[T]) TTL() time.Duration { return s.ttl }

func (s *Store[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *Store[T]) sweepLocked(now time.Time) {
	for pin, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, pin)
		}
	}
}

// RunJanitor drops expired entries every interval until ctx is done, so
// expired lists do not linger in memory between requests.
func (s *Store[T]) RunJanitor(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			s.sweepLocked(s.now())
			s.mu.Unlock()
		}
	}
}

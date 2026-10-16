// Package share holds medication lists in memory under one-time 4-digit PINs.
package share

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"pillgaze/internal/model"
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

type entry struct {
	meds    []model.SharedMedication
	expires time.Time
}

type Store struct {
	ttl    time.Duration
	now    func() time.Time
	newPIN func() (string, error)

	mu      sync.Mutex
	entries map[string]entry
}

func NewStore(ttl time.Duration) *Store {
	return &Store{
		ttl:     ttl,
		now:     time.Now,
		newPIN:  randomPIN,
		entries: map[string]entry{},
	}
}

func randomPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

// Create stores meds under a fresh PIN unique among active ones.
func (s *Store) Create(meds []model.SharedMedication) (pin string, expires time.Time, err error) {
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
		s.entries[pin] = entry{meds: meds, expires: expires}
		return pin, expires, nil
	}
	return "", time.Time{}, ErrFull
}

// Claim returns and deletes the list for pin. Missing and expired PINs look
// the same to the caller.
func (s *Store) Claim(pin string) ([]model.SharedMedication, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[pin]
	if !ok {
		return nil, false
	}
	delete(s.entries, pin)
	if !s.now().Before(e.expires) {
		return nil, false
	}
	return e.meds, true
}

func (s *Store) TTL() time.Duration { return s.ttl }

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *Store) sweepLocked(now time.Time) {
	for pin, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, pin)
		}
	}
}

// RunJanitor drops expired entries every interval until ctx is done, so
// expired lists do not linger in memory between requests.
func (s *Store) RunJanitor(ctx context.Context, interval time.Duration) {
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

package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SessionStore persists per-channel Claude session IDs to disk.
// A session ID is what we pass to `claude -p --resume <id>` to continue
// the same conversation across turns. When an error marks a session as
// stale, Rotate allocates a new ID and subsequent LoadOrCreate returns it.
type SessionStore struct {
	dir string

	mu       sync.Mutex
	sessions map[string]string // channel -> id
	fresh    map[string]bool   // channel -> whether it was just created/rotated
}

// NewSessionStore constructs a store backed by <stateDir>/sessions/.
func NewSessionStore(stateDir string) (*SessionStore, error) {
	sdir := filepath.Join(stateDir, "sessions")
	if err := os.MkdirAll(sdir, 0700); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	return &SessionStore{
		dir:      sdir,
		sessions: map[string]string{},
		fresh:    map[string]bool{},
	}, nil
}

// LoadOrCreate returns the session ID for a channel, creating (and
// persisting) a new one if none exists. The second return value is true
// iff the ID was newly created in this call — clients use this to decide
// whether to pass `--session-id` (new) or `--resume` (existing) to claude.
func (s *SessionStore) LoadOrCreate(channel string) (id string, fresh bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok := s.sessions[channel]; ok {
		wasFresh := s.fresh[channel]
		s.fresh[channel] = false
		return cached, wasFresh
	}

	// Try disk.
	path := s.filePath(channel)
	if data, err := os.ReadFile(path); err == nil {
		id := string(data)
		s.sessions[channel] = id
		s.fresh[channel] = false
		return id, false
	}

	// Create fresh.
	id = newSessionID()
	_ = os.WriteFile(path, []byte(id), 0600)
	s.sessions[channel] = id
	s.fresh[channel] = false
	return id, true
}

// Rotate discards the current session ID for a channel and allocates a new
// one. Used when claude reports a session error (e.g. resume target not
// found). The returned ID is also persisted and marked fresh.
func (s *SessionStore) Rotate(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := newSessionID()
	_ = os.WriteFile(s.filePath(channel), []byte(id), 0600)
	s.sessions[channel] = id
	s.fresh[channel] = true
	return id
}

// MarkResumed is called after a successful resume to clear the fresh flag.
func (s *SessionStore) MarkResumed(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fresh[channel] = false
}

func (s *SessionStore) filePath(channel string) string {
	return filepath.Join(s.dir, channel+".session")
}

func newSessionID() string {
	// 16 bytes → 32 hex chars → suffices as a UUID-like handle.
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("crypto/rand: %w", err))
	}
	return hex.EncodeToString(b[:])
}

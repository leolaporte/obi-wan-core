package r1

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrAlreadyPaired is returned when the store already holds a device and
// a new Pair request arrives. The shim is single-R1 by design.
var ErrAlreadyPaired = errors.New("r1 already paired; reject-second-device policy")

// Device is one persisted R1 identity.
type Device struct {
	DeviceID    string   `json:"deviceId"`
	PublicKey   string   `json:"publicKey"`   // base64url raw ed25519 pubkey
	Role        string   `json:"role"`
	Scopes      []string `json:"scopes"`
	DeviceToken string   `json:"deviceToken"` // long-lived, opaque to the client
	CreatedAtMs int64    `json:"createdAtMs"`
}

// PairRequest is the input to DeviceStore.Pair.
type PairRequest struct {
	DeviceID  string
	PublicKey string
	Role      string
	Scopes    []string
}

// DeviceStore persists a single R1 device identity to disk.
// Concurrent-safe for use from the shim's handshake path.
type DeviceStore struct {
	path string
	mu   sync.Mutex
	dev  *Device // nil until paired
}

type storeFile struct {
	Version int     `json:"version"`
	Device  *Device `json:"device"`
}

// OpenDeviceStore loads the store from disk or creates an empty one.
func OpenDeviceStore(path string) (*DeviceStore, error) {
	s := &DeviceStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read device store: %w", err)
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse device store: %w", err)
	}
	s.dev = file.Device
	return s, nil
}

// Paired reports whether a device has been paired.
func (s *DeviceStore) Paired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dev != nil
}

// Pair stores a new device identity. Returns ErrAlreadyPaired if the
// store already holds one. Mints a random device token and persists
// atomically.
func (s *DeviceStore) Pair(req PairRequest) (*Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dev != nil {
		return nil, ErrAlreadyPaired
	}
	token, err := mintDeviceToken()
	if err != nil {
		return nil, err
	}
	dev := &Device{
		DeviceID:    req.DeviceID,
		PublicKey:   req.PublicKey,
		Role:        req.Role,
		Scopes:      append([]string(nil), req.Scopes...),
		DeviceToken: token,
		CreatedAtMs: time.Now().UnixMilli(),
	}
	if err := s.writeLocked(dev); err != nil {
		return nil, err
	}
	s.dev = dev
	return dev, nil
}

// LookupByToken resolves a device token back to the stored device, or
// (zero, false) if the token does not match the paired device.
func (s *DeviceStore) LookupByToken(token string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dev == nil || token == "" || s.dev.DeviceToken != token {
		return Device{}, false
	}
	return *s.dev, true
}

// Current returns the paired device, if any. For handshake callers that
// need to read pubkey + role without matching on token.
func (s *DeviceStore) Current() (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dev == nil {
		return Device{}, false
	}
	return *s.dev, true
}

func (s *DeviceStore) writeLocked(dev *Device) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("mkdir device store dir: %w", err)
	}
	file := storeFile{Version: 1, Device: dev}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal device store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp device store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename device store: %w", err)
	}
	return nil
}

func mintDeviceToken() (string, error) {
	buf := make([]byte, 32) // 256 bits of entropy → 43 base64url chars
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint device token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

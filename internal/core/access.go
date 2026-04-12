package core

import "github.com/leolaporte/obi-wan-core/internal/config"

// Access is the per-channel allowlist check.
type Access struct {
	cfg *config.Config
}

// NewAccess constructs an Access from a config.
func NewAccess(cfg *config.Config) *Access {
	return &Access{cfg: cfg}
}

// Allowed reports whether the given userID is permitted to send turns on
// the given channel. A disabled or unknown channel always denies.
func (a *Access) Allowed(channel, userID string) bool {
	ch, ok := a.cfg.Channels[channel]
	if !ok || !ch.Enabled {
		return false
	}
	for _, id := range ch.AllowFrom {
		if id == userID {
			return true
		}
	}
	return false
}

package core

import (
	"testing"

	"github.com/leolaporte/obi-wan-core/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAccess_allowedUser(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"12345", "67890"}},
		},
	}
	a := NewAccess(cfg)
	require.True(t, a.Allowed("telegram", "12345"))
	require.True(t, a.Allowed("telegram", "67890"))
}

func TestAccess_deniedUser(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.Channel{
			"telegram": {Enabled: true, AllowFrom: []string{"12345"}},
		},
	}
	a := NewAccess(cfg)
	require.False(t, a.Allowed("telegram", "99999"))
}

func TestAccess_disabledChannel(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.Channel{
			"telegram": {Enabled: false, AllowFrom: []string{"12345"}},
		},
	}
	a := NewAccess(cfg)
	require.False(t, a.Allowed("telegram", "12345"), "disabled channel must deny even allowlisted users")
}

func TestAccess_unknownChannel(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.Channel{},
	}
	a := NewAccess(cfg)
	require.False(t, a.Allowed("telegram", "12345"))
}

func TestAccess_openAccessAllowsAnyUser(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.Channel{
			"watch": {Enabled: true, OpenAccess: true},
		},
	}
	a := NewAccess(cfg)
	require.True(t, a.Allowed("watch", "anyone"))
	require.True(t, a.Allowed("watch", ""))
}

func TestAccess_openAccessRequiresEnabled(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.Channel{
			"watch": {Enabled: false, OpenAccess: true},
		},
	}
	a := NewAccess(cfg)
	require.False(t, a.Allowed("watch", "anyone"), "disabled channel denies even with open_access")
}

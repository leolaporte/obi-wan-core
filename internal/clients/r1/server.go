package r1

import (
	"context"

	"github.com/leolaporte/obi-wan-core/internal/core"
)

// Dispatcher is the subset of core.Dispatcher the r1 shim needs.
type Dispatcher interface {
	Dispatch(ctx context.Context, turn core.Turn) (*core.Reply, error)
}

// Config holds runtime configuration for the R1 shim.
type Config struct {
	Port           int    // HTTP port to listen on
	BootstrapToken string // single-use pairing secret; empty disables pairing
	Channel        string // "r1"
	StatePath      string // absolute path to r1-devices.json
}

// Server is the R1 gateway shim.
type Server struct {
	cfg        Config
	dispatcher Dispatcher
}

// NewServer constructs but does not start the server.
func NewServer(cfg Config, d Dispatcher) *Server {
	return &Server{cfg: cfg, dispatcher: d}
}

// Start is a stub for Task 1. Real implementation lands in Task 9.
func (s *Server) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

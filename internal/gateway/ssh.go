package gateway

import (
	"errors"
	"net"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// SSHConfig configures the gateway's SSH server: one port for every sandbox.
type SSHConfig struct {
	Store  Store
	Router Router
	// HostKeyFile holds the server's ed25519 host key; it is created, 0600,
	// when missing. One key for the whole fleet, published by GET /v1/ssh so
	// clients can pin it.
	HostKeyFile string
	// Host and Port are what clients are told to connect to (GET /v1/ssh,
	// ssh-access), which behind a load balancer is not the listen address.
	Host string
	Port int
	Logf func(format string, args ...any)
}

// SSHServer is the gateway's SSH server.
//
// This is the contract the gateway core is wired against; the implementation
// replaces this file.
type SSHServer struct{ cfg SSHConfig }

// NewSSHServer loads or creates the host key and returns a server ready to
// Serve.
func NewSSHServer(cfg SSHConfig) (*SSHServer, error) {
	return nil, errors.New("the SSH server is not built yet")
}

// Serve accepts SSH connections on ln until Close.
func (s *SSHServer) Serve(ln net.Listener) error { return errors.New("not built") }

// Info is GET /v1/ssh: where to connect and the host key to pin.
func (s *SSHServer) Info() api.SSHInfo { return api.SSHInfo{Host: s.cfg.Host, Port: s.cfg.Port} }

// Close stops accepting and ends open sessions.
func (s *SSHServer) Close() error { return nil }

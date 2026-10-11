//go:build !linux

package wg

import (
	"fmt"
	"time"
)

// The overlay is Linux-only: it configures a kernel WireGuard interface.
// These stubs exist so the repo-server and agent still BUILD on Windows and
// macOS for development, and report the limitation rather than failing to
// compile — the same pattern the container runtime uses.

const Interface = "sixter0"

func Available() error {
	return fmt.Errorf("the private overlay requires Linux")
}

func Apply(cfg Config) error { return Available() }

func Down() error { return nil }

type PeerStatus struct {
	PublicKey     string
	Endpoint      string
	LastHandshake time.Time
	RxBytes       int64
	TxBytes       int64
}

func Status() ([]PeerStatus, error) { return nil, Available() }

//go:build !linux

package amm

import (
	"context"
	"fmt"
	"time"
)

// ContainerRuntime stub for non-Linux hosts. Namespaces, cgroups and overlayfs
// are Linux kernel features with no equivalent elsewhere, so this backend
// simply reports itself unavailable and the supervisor falls back to
// ProcessRuntime. Keeps `go build` working on a Windows/macOS dev machine.
type ContainerRuntime struct{ StateDir string }

func NewContainerRuntime(stateDir string) *ContainerRuntime {
	return &ContainerRuntime{StateDir: stateDir}
}

func (c *ContainerRuntime) Name() string { return "container" }

func (c *ContainerRuntime) Available() error {
	return fmt.Errorf("%w: containers require Linux", ErrUnsupported)
}

func (c *ContainerRuntime) Create(ctx context.Context, spec Spec) (*Instance, error) {
	return nil, c.Available()
}
func (c *ContainerRuntime) Start(ctx context.Context, id string) error { return c.Available() }
func (c *ContainerRuntime) Stop(ctx context.Context, id string, grace time.Duration) error {
	return c.Available()
}
func (c *ContainerRuntime) Destroy(ctx context.Context, id string) error { return c.Available() }
func (c *ContainerRuntime) Get(id string) (*Instance, bool)             { return nil, false }
func (c *ContainerRuntime) List() []*Instance                           { return nil }
func (c *ContainerRuntime) Stats(id string) (*Stats, error)             { return nil, c.Available() }
func (c *ContainerRuntime) Exec(ctx context.Context, id string, cmd []string, tty bool) (*Stream, error) {
	return nil, c.Available()
}

// IsInit / RunInit stubs so main() can call them unconditionally.
func IsInit() bool { return false }
func RunInit()     {}

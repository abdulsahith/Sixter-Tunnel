//go:build !linux

package amm

import "fmt"

// Pod networking configures Linux network namespaces and bridges. These stubs
// keep the agent and repo-server building on Windows and macOS for development,
// reporting the limitation instead of failing to compile — the same pattern the
// container runtime and overlay use.

const Bridge = "sixter-br"

func EnsureBridge(podCIDR string) error {
	return fmt.Errorf("amm: pod networking requires Linux")
}

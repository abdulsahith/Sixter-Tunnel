package amm

import "runtime"

// goArch maps Go's GOARCH to the OCI platform architecture string. They agree
// for amd64, but differ for ARM (Go says arm64, OCI says arm64 too, while
// 32-bit is "arm" with a variant) — keep the mapping explicit so a wrong
// architecture is a compile-time-obvious bug rather than a confusing pull error.
func goArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "arm"
	case "386":
		return "386"
	}
	return runtime.GOARCH
}

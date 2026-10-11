//go:build linux || darwin

package amm

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the app in its own process group so we can signal the
// whole tree, and kills it if the supervisor dies rather than leaking orphans.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate signals the entire process group, not just the shell we spawned —
// otherwise `sh -lc "npm start"` dies and node keeps running.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

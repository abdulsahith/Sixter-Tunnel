//go:build windows

package main

import (
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

/* ═══════════════ emulated shell session (Windows) ═══════════════
   Windows has no easy PTY without cgo, so we emulate a line-mode shell:
   echo keystrokes back to xterm, run each line through PowerShell
   (which has ls/cat/pwd aliases), and keep cd persistent + jailed.
   Production (Linux VPS) gets the real PTY in shell_unix.go. */

type winSession struct {
	mu     sync.Mutex
	root   string
	cwd    string
	line   []byte
	outR   *io.PipeReader
	outW   *io.PipeWriter
	closed bool
}

func newShellSession(dir string) (shellSession, error) {
	r, w := io.Pipe()
	s := &winSession{root: dir, cwd: dir, outR: r, outW: w}
	go func() {
		s.emit("\x1b[35mSixter shell (Windows test mode — PowerShell)\x1b[0m\r\n")
		s.emit("project root: " + dir + "\r\n\r\n")
		s.prompt()
	}()
	return s, nil
}

func (s *winSession) emit(str string) {
	_, _ = s.outW.Write([]byte(str))
}

func (s *winSession) prompt() {
	s.emit("\x1b[32mPS " + s.cwd + ">\x1b[0m ")
}

func (s *winSession) Read(p []byte) (int, error) { return s.outR.Read(p) }

func (s *winSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	for _, b := range p {
		switch b {
		case '\r', '\n':
			s.emit("\r\n")
			line := strings.TrimSpace(string(s.line))
			s.line = nil
			s.runLine(line)
		case 0x7f, 0x08: // backspace
			if len(s.line) > 0 {
				s.line = s.line[:len(s.line)-1]
				s.emit("\b \b")
			}
		case 0x03: // ctrl+c — drop current line
			s.line = nil
			s.emit("^C\r\n")
			s.prompt()
		default:
			if b >= 0x20 || b == '\t' {
				s.line = append(s.line, b)
				s.emit(string(rune(b)))
			}
		}
	}
	return len(p), nil
}

func (s *winSession) runLine(line string) {
	if line == "" {
		s.prompt()
		return
	}

	// persistent, jailed cd
	lower := strings.ToLower(line)
	if lower == "cd" || strings.HasPrefix(lower, "cd ") {
		target := strings.TrimSpace(line[2:])
		target = strings.Trim(target, `"'`)
		candidate := s.root
		if target != "" {
			if !filepath.IsAbs(target) {
				candidate = filepath.Join(s.cwd, target)
			} else {
				candidate = target
			}
		}
		candidate = filepath.Clean(candidate)
		rel, err := filepath.Rel(s.root, candidate)
		if err != nil || strings.HasPrefix(rel, "..") {
			s.emit("\x1b[31msixter: cannot leave project folder\x1b[0m\r\n")
		} else if st, err := os.Stat(candidate); err != nil || !st.IsDir() {
			s.emit("\x1b[31mdirectory not found\x1b[0m\r\n")
		} else {
			s.cwd = candidate
		}
		s.prompt()
		return
	}

	go func() {
		c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", line)
		c.Dir = s.cwd
		out, err := c.CombinedOutput()

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return
		}
		text := strings.ReplaceAll(string(out), "\r\n", "\n")
		text = strings.ReplaceAll(text, "\n", "\r\n")
		s.emit(text)
		if err != nil && len(out) == 0 {
			s.emit("\x1b[31m" + err.Error() + "\x1b[0m\r\n")
		}
		s.prompt()
	}()
}

func (s *winSession) Resize(cols, rows int) {} // no-op in emulated mode

func (s *winSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.outW.Close()
}

/* ═══════════════ SSH service (not available on Windows) ═══════════════ */

func startSSHServer(port int) {
	log.Printf("SSH service requires Linux — skipped (deploy on your VPS to enable it on port %d)", port)
}

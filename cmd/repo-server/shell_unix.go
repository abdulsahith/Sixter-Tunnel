//go:build !windows

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/creack/pty"
	gossh "github.com/gliderlabs/ssh"
	xssh "golang.org/x/crypto/ssh"
)

/* ═══════════════ PTY shell session (Linux/macOS) ═══════════════ */

type ptySession struct {
	f   *os.File
	cmd *exec.Cmd
}

// jailEnv keeps the shell inside the project folder: if the user cds out,
// PROMPT_COMMAND bounces them straight back. (Soft jail — for a hard jail
// use the per-project container roadmap.)
func jailEnv(dir string) []string {
	guard := `case "$PWD/" in "$SIXTER_ROOT"/*) ;; *) echo "sixter: cannot leave project folder"; cd "$SIXTER_ROOT";; esac`
	env := append(os.Environ(),
		"TERM=xterm-256color",
		"SIXTER_ROOT="+dir,
		"PROMPT_COMMAND="+guard,
	)

	// Give the shell the SAME production config the app runs with.
	//
	// Without this, a migration or one-off command typed here silently falls
	// back to whatever .env is checked into the repo — usually the developer's
	// laptop credentials. That either fails confusingly or, far worse, succeeds
	// against the wrong database. Nobody should have to know to source a file
	// in /opt/sixter/env before running anything.
	for k, v := range loadEnv(projectFromDir(dir)) {
		env = append(env, k+"="+v)
	}
	return env
}

func newShellSession(dir string) (shellSession, error) {
	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	c := exec.Command(shell)
	c.Dir = dir
	c.Env = jailEnv(dir)

	f, err := pty.Start(c)
	if err != nil {
		return nil, err
	}
	return &ptySession{f: f, cmd: c}, nil
}

func (s *ptySession) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *ptySession) Write(p []byte) (int, error) { return s.f.Write(p) }

func (s *ptySession) Resize(cols, rows int) {
	_ = pty.Setsize(s.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *ptySession) Close() error {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	return s.f.Close()
}

/* ═══════════════ SSH service ═══════════════ */

// ensureHostKey creates a persistent ed25519 host key so SSH clients don't
// see "host key changed" warnings after every restart.
func ensureHostKey() string {
	p := filepath.Join(installDir, "ssh_host_key.pem")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ""
	}
	block, err := xssh.MarshalPrivateKey(priv, "sixter host key")
	if err != nil {
		return ""
	}
	if err := os.WriteFile(p, pem.EncodeToMemory(block), 0600); err != nil {
		return ""
	}
	return p
}

// startSSHServer exposes each project over SSH, jailed to its folder:
//
//	ssh <project>@your-server -p 2222     (password = your repo-server token)
func startSSHServer(port int) {
	srv := &gossh.Server{
		Addr: fmt.Sprintf(":%d", port),
		PasswordHandler: func(ctx gossh.Context, pass string) bool {
			if subtle.ConstantTimeCompare([]byte(pass), []byte(uploadToken)) != 1 {
				return false
			}
			proj := ctx.User()
			if !isSafeName(proj) {
				return false
			}
			st, err := os.Stat(filepath.Join(dataDir, proj))
			return err == nil && st.IsDir()
		},
		Handler: func(s gossh.Session) {
			proj := s.User()
			dir := filepath.Join(dataDir, proj)

			ptyReq, winCh, isPty := s.Pty()
			if !isPty {
				_, _ = io.WriteString(s, "interactive PTY required — connect with a normal ssh client\n")
				_ = s.Exit(1)
				return
			}

			sess, err := newShellSession(dir)
			if err != nil {
				_, _ = io.WriteString(s, "failed to start shell: "+err.Error()+"\n")
				_ = s.Exit(1)
				return
			}
			defer sess.Close()

			sess.Resize(ptyReq.Window.Width, ptyReq.Window.Height)
			go func() {
				for win := range winCh {
					sess.Resize(win.Width, win.Height)
				}
			}()

			_, _ = io.WriteString(s, "Sixter SSH — project: "+proj+" (jailed to project folder)\r\n")

			go func() { _, _ = io.Copy(sess, s) }() // ssh client → shell
			_, _ = io.Copy(s, sess)                 // shell → ssh client
			_ = s.Exit(0)
		},
	}

	if kp := ensureHostKey(); kp != "" {
		if err := gossh.HostKeyFile(kp)(srv); err != nil {
			log.Printf("ssh host key: %v", err)
		}
	}

	go func() {
		log.Printf("✓ SSH service on :%d — ssh <project>@<server-ip> -p %d (password = your token)", port, port)
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("SSH service stopped: %v", err)
		}
	}()
}

package main

// `sixter repo-server -setup` — one-command interactive setup. Prompts for the
// repo subdomain, HTTPS email and token, writes a 0600 env file (so secrets
// never appear in the unit file or `ps`), installs a systemd service called
// "sixter", and starts it.

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"

	"sixter.in/tunnel/internal/prompt"
)

func runSetup() {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "setup is Linux-only (it creates a systemd service). Run the server directly on other OSes.")
		os.Exit(1)
	}
	if !isRoot() {
		fmt.Fprintln(os.Stderr, "setup needs root to install the service:\n  sudo sixter repo-server -setup")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("  Sixter Repo Server — setup")
	fmt.Println("  ──────────────────────────")
	server := prompt.Line("  Repo subdomain (e.g. sahith-repo)", os.Getenv("SIXTER_SERVER"))
	email := prompt.Line("  Email for HTTPS certificates", os.Getenv("SIXTER_CERTBOT_EMAIL"))
	token, _ := prompt.Secret("  Dashboard/upload token: ")
	if server == "" || token == "" {
		fmt.Fprintln(os.Stderr, "\n  A subdomain and token are required.")
		os.Exit(1)
	}

	// the tunnel needs the credentials from `sixter login` (~/.sixter/config.yaml).
	// under sudo that lives in the invoking user's home, not root's.
	home := "/root"
	if su := os.Getenv("SUDO_USER"); su != "" {
		if u, err := user.Lookup(su); err == nil {
			home = u.HomeDir
		}
	}
	cfg := filepath.Join(home, ".sixter", "config.yaml")
	if _, err := os.Stat(cfg); err != nil {
		fmt.Printf("\n  ⚠ No Sixter login found at %s\n", cfg)
		fmt.Println("    Run `sixter login` as that user first, then re-run setup.")
		os.Exit(1)
	}

	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "  cannot find own path:", err)
		os.Exit(1)
	}

	// secrets → 0600 env file (kept out of the unit file and process list)
	if err := os.MkdirAll("/etc/sixter", 0755); err != nil {
		fmt.Fprintln(os.Stderr, "  ", err)
		os.Exit(1)
	}
	envBody := fmt.Sprintf("SIXTER_TOKEN=%s\nSIXTER_SERVER=%s\nSIXTER_CERTBOT_EMAIL=%s\n", token, server, email)
	if err := os.WriteFile("/etc/sixter/reposerver.env", []byte(envBody), 0600); err != nil {
		fmt.Fprintln(os.Stderr, "  write env:", err)
		os.Exit(1)
	}

	unit := fmt.Sprintf(`[Unit]
Description=Sixter Repo Server
After=network.target

[Service]
Type=simple
User=root
Environment=HOME=%s
EnvironmentFile=/etc/sixter/reposerver.env
ExecStart=%s
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, home, bin)
	if err := os.WriteFile("/etc/systemd/system/sixter.service", []byte(unit), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "  write unit:", err)
		os.Exit(1)
	}

	for _, c := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", "sixter"},
	} {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "  %v failed: %s\n", c, string(out))
			os.Exit(1)
		}
	}

	fmt.Println()
	fmt.Println("  ✓ Service 'sixter' created and started.")
	fmt.Println("    Dashboard : https://" + server + ".sixter.in")
	fmt.Println("    Status    : systemctl status sixter")
	fmt.Println("    Logs      : journalctl -u sixter -f")
	fmt.Println("    Restart   : sudo systemctl restart sixter")
	fmt.Println()
}

// sixter — the user-facing CLI.
//
//	sixter login          device-flow login → writes ~/.sixter/config.yaml
//	sixter whoami         show the logged-in account
//	sixter logout         delete local credentials
//	sixter tunnel ...     dispatch to sixter-client
//	sixter repo ...       dispatch to sixter-repo
//	sixter repo-server .. dispatch to sixter-reposerver
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sixter.in/tunnel/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "login":
		cmdLogin(args)
	case "whoami":
		cmdWhoami()
	case "logout":
		cmdLogout()
	case "tunnel":
		dispatch("sixter-client", args)
	case "repo":
		dispatch("sixter-repo", args)
	case "project":
		cmdProject(args)
	case "push":
		cmdPush(args)
	case "pull":
		cmdPull(args)
	case "status":
		cmdStatus(args)
	case "branch", "branches":
		cmdBranch(args)
	case "members", "team":
		cmdMembers(args)
	case "log", "activity":
		cmdLog(args)
	case "pr":
		cmdPR(args)
	case "repo-server":
		dispatch("sixter-reposerver", args)
	case "help", "-h", "--help":
		printHelp()
	default:
		printHelp()
		os.Exit(1)
	}
}

/* ─────────── login (device flow) ─────────── */

type deviceCodeResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type tokenResp struct {
	Status    string `json:"status"`
	Email     string `json:"email"`
	Token     string `json:"token"`
	TunnelSub string `json:"tunnel_subdomain"`
	RepoSub   string `json:"repo_subdomain"`
	Server    string `json:"server"`
	API       string `json:"api"`
	Error     string `json:"error"`
}

func cmdLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	api := fs.String("api", "https://sixter.in", "Sixter API base url")
	noBrowser := fs.Bool("no-browser", false, "don't try to open a browser (headless/SSH)")
	_ = fs.Parse(args)

	base := strings.TrimRight(*api, "/")
	client := &http.Client{Timeout: 15 * time.Second}

	// 1. request a device code
	resp, err := client.Post(base+"/api/device/code", "application/json", nil)
	if err != nil {
		fail("cannot reach %s: %v", base, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		fail("server error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var dc deviceCodeResp
	if err := json.Unmarshal(body, &dc); err != nil {
		fail("bad server response: %v", err)
	}

	codeURL := dc.VerificationURI + "?code=" + dc.UserCode

	fmt.Println()
	fmt.Println("  To log in, open this URL on ANY device (phone or laptop is fine):")
	fmt.Println()
	fmt.Println("      " + codeURL)
	fmt.Println()
	fmt.Println("  Or go to " + dc.VerificationURI + " and enter the code:")
	fmt.Println()
	fmt.Println("      " + dc.UserCode)
	fmt.Println()

	if !*noBrowser {
		openBrowser(codeURL)
	}

	// 2. poll until approved
	interval := dc.Interval
	if interval < 2 {
		interval = 3
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	fmt.Print("  Waiting for approval")
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		fmt.Print(".")

		reqBody, _ := json.Marshal(map[string]string{"device_code": dc.DeviceCode})
		resp, err := client.Post(base+"/api/device/token", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			continue // transient network error — keep polling
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var tr tokenResp
		if err := json.Unmarshal(body, &tr); err != nil {
			continue
		}

		switch tr.Status {
		case "pending":
			continue
		case "approved":
			cfg := &config.Config{
				Email:     tr.Email,
				Token:     tr.Token,
				TunnelSub: tr.TunnelSub,
				RepoSub:   tr.RepoSub,
				Server:    tr.Server,
				API:       tr.API,
			}
			if err := cfg.Save(); err != nil {
				fail("could not save config: %v", err)
			}
			p, _ := config.Path()
			fmt.Println()
			fmt.Println()
			fmt.Println("  ✓ Logged in as " + tr.Email)
			fmt.Println("  ✓ Credentials saved to " + p)
			fmt.Println()
			fmt.Println("  Your subdomains:")
			fmt.Println("    tunnel : https://" + tr.TunnelSub + ".sixter.in")
			fmt.Println("    repo   : https://" + tr.RepoSub + ".sixter.in")
			fmt.Println()
			fmt.Println("  Try:  sixter tunnel -port 3000")
			return
		default: // expired
			fmt.Println()
			fail("login expired or was already used — run `sixter login` again")
		}
	}
	fmt.Println()
	fail("login timed out — run `sixter login` again")
}

func cmdWhoami() {
	cfg, err := config.Load()
	if err != nil {
		fail("read config: %v", err)
	}
	if cfg == nil || cfg.Token == "" {
		fmt.Println("Not logged in. Run: sixter login")
		os.Exit(1)
	}
	fmt.Println("Email           :", cfg.Email)
	fmt.Println("Tunnel subdomain:", cfg.TunnelSub+".sixter.in")
	fmt.Println("Repo subdomain  :", cfg.RepoSub+".sixter.in")
	fmt.Println("Server          :", cfg.Server)
}

func cmdLogout() {
	p, err := config.Path()
	if err != nil {
		fail("%v", err)
	}
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Already logged out.")
			return
		}
		fail("could not remove config: %v", err)
	}
	fmt.Println("Logged out — local credentials removed.")
}

/* ─────────── dispatch to sibling binaries ─────────── */

func dispatch(name string, args []string) {
	selfPath, err := os.Executable()
	if err != nil {
		fail("%v", err)
	}
	baseDir := filepath.Dir(selfPath)

	exe := filepath.Join(baseDir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	if _, err := os.Stat(exe); err != nil {
		fail("executable not found: %s\nplace all sixter binaries in the same folder", exe)
	}

	c := exec.Command(exe, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin

	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fail("failed to run: %v", err)
	}
}

/* ─────────── misc ─────────── */

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func fail(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	os.Exit(1)
}

func printHelp() {
	fmt.Println(`Sixter — tunnels & repo deployment

Usage:
  sixter login [-api https://sixter.in] [-no-browser]
      Log in via your browser (works on SSH-only servers:
      open the printed URL on any other device).

  sixter whoami      Show the logged-in account
  sixter logout      Remove local credentials

  sixter tunnel -port <port> [-subdomain <name>]
      Expose a local port on your tunnel subdomain.

  sixter repo -server <name> [-project <name>]
      Upload the current folder to a repo server.
      You'll be prompted for the token securely (or set SIXTER_TOKEN).

  Coworkspace (team members):
    sixter project init <workspace-link> [-component <name>]
        Join a workspace with the team's shared link (you must be logged in).
        -component names your own folder (default: this directory's name).
    sixter pull [-branch <name>] [-force]
        Download the component's main (or a branch) into this folder.
    sixter push [-branch <name>] [-m "message"]
        Push your work. Owners/leads update main; others open a pull request
        the team lead reviews and merges.
    sixter status        What this folder is, your role, what's pending
    sixter branch        Branches / open pull requests on this component
    sixter members       Everyone in the workspace and their role
    sixter log [-n 20]   Recent workspace activity
    sixter pr list                 Open pull requests
    sixter pr view <branch>        Show the diff in your terminal
    sixter pr merge <branch>       Merge it (lead only)
    sixter pr close <branch>       Discard it (lead only)
    sixter project deploy
        Promote the current main (lead / deployment role only).

  sixter repo-server -setup
      One-command setup: prompts for subdomain, email & token, then
      creates and starts the managed "sixter" service.

Files:
  ~/.sixter/config.yaml   credentials (created by login)`)
}

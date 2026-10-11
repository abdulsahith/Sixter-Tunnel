package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sixter.in/tunnel/internal/config"
)

/*
Coworkspace member workflow:

	sixter project init <token>   # enroll this folder into a workspace project
	sixter push                    # upload your work → pending review (a lead deploys)

The token comes from the workspace owner (landing → Workspaces → Add member).
Binding is stored in ./.sixter/project.json.
*/

type projectBinding struct {
	Gateway   string `json:"gateway"`
	Server    string `json:"server"`
	Project   string `json:"project"`   // the folder id on the server: <workspace>-<component>
	Component string `json:"component"` // this member's own named folder inside the workspace
	Token     string `json:"token"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Workspace string `json:"workspace"`
}

// componentProject namespaces a member-chosen component under its workspace so
// two workspaces (or two members) never share a server folder by accident.
func componentProject(wsName, component string) string {
	c := sanitizeName(component)
	wsSlug := sanitizeName(wsName)
	if wsSlug == "" {
		return c
	}
	if c == "" {
		return wsSlug
	}
	if strings.HasPrefix(c, wsSlug+"-") || c == wsSlug {
		return c // already namespaced
	}
	return wsSlug + "-" + c
}

// defaultComponent derives a component name from the current folder, so a
// developer who runs `sixter project init <token>` in ./api gets "api" —
// never the shared workspace name that caused folders to overwrite each other.
func defaultComponent() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return sanitizeName(filepath.Base(wd))
}

func bindingPath() string { return filepath.Join(".sixter", "project.json") }

func loadBinding() (*projectBinding, error) {
	b, err := os.ReadFile(bindingPath())
	if err != nil {
		return nil, err
	}
	var pb projectBinding
	if err := json.Unmarshal(b, &pb); err != nil {
		return nil, err
	}
	return &pb, nil
}

func gatewayDefault() string {
	if c, err := config.Load(); err == nil && c != nil && c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return "https://sixter.in"
}

func cmdProject(args []string) {
	if len(args) < 1 {
		fail("usage: sixter project init <token> [-server <repo-url>] [-project <name>]")
	}
	switch args[0] {
	case "init":
		cmdProjectInit(args[1:])
	case "deploy":
		cmdProjectDeploy(args[1:])
	default:
		fail("unknown project subcommand %q (try: init, deploy)", args[0])
	}
}

// cmdProjectDeploy lets a lead/deployment member promote the pending push.
func cmdProjectDeploy(_ []string) {
	pb, err := loadBinding()
	if err != nil {
		fail("not a Sixter project folder — run `sixter project init <token>` here first")
	}
	req, err := http.NewRequest(http.MethodPost, pb.Server+"/api/member-deploy?project="+pb.Project, nil)
	if err != nil {
		fail("%v", err)
	}
	req.Header.Set("X-Enroll-Token", pb.Token)
	req.Header.Set("User-Agent", "Sixter-CLI/1.0")
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		fail("deploy failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("deploy rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	fmt.Println("  ✓ Deploy started for", pb.Project, "— building & promoting to production.")
}

func cmdProjectInit(args []string) {
	// pull the positional token out before flag parsing
	var token string
	rest := args
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		token = rest[0]
		rest = rest[1:]
	}
	fs := flag.NewFlagSet("project init", flag.ExitOnError)
	server := fs.String("server", "", "repo-server URL (default: derived from the workspace owner)")
	component := fs.String("component", "", "your component name inside the workspace (default: this folder's name)")
	project := fs.String("project", "", "alias for -component (kept for compatibility)")
	gwFlag := fs.String("gateway", "", "gateway base url (default: from config or https://sixter.in)")
	_ = fs.Parse(rest)
	if token == "" {
		fail("token required:  sixter project init <token>")
	}

	gw := *gwFlag
	if gw == "" {
		gw = gatewayDefault()
	}
	gw = strings.TrimRight(gw, "/")

	type enrollResp struct {
		OK      bool   `json:"ok"`
		Token   string `json:"token"`
		Email   string `json:"email"`
		Role    string `json:"role"`
		WsName  string `json:"ws_name"`
		RepoSub string `json:"repo_sub"`
		Owner   string `json:"owner"`
		Error   string `json:"error"`
	}
	cl := &http.Client{Timeout: 15 * time.Second}
	var rr enrollResp

	// Preferred path: one shared workspace link for the whole team. We prove who
	// we are with this machine's logged-in account, and the gateway hands back a
	// PERSONAL token — so a shared link never blurs who pushed what.
	joined := false
	if cfg, cErr := config.Load(); cErr == nil && cfg != nil && cfg.Token != "" {
		payload, _ := json.Marshal(map[string]string{
			"token": token, "subdomain": cfg.TunnelSub, "api_token": cfg.Token,
		})
		jResp, jErr := cl.Post(gw+"/api/workspaces/join", "application/json", bytes.NewReader(payload))
		if jErr == nil {
			jBody, _ := io.ReadAll(jResp.Body)
			jResp.Body.Close()
			var jr enrollResp
			if json.Unmarshal(jBody, &jr) == nil && jr.OK && jr.Token != "" {
				rr, joined = jr, true
				token = jr.Token // store the personal token, not the shared link
			} else if jResp.StatusCode == http.StatusForbidden && jr.Error != "" {
				// the link was valid but this account isn't on the team yet
				fail("%s", jr.Error)
			}
		}
	}

	// Fallback: a legacy per-member token (still supported).
	if !joined {
		resp, err := cl.Get(gw + "/api/enroll/redeem?token=" + urlpkg.QueryEscape(token))
		if err != nil {
			fail("cannot reach gateway %s: %v", gw, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if json.Unmarshal(body, &rr) != nil || !rr.OK {
			fail("invalid invite link — ask your team lead for the workspace link,\n" +
				"       and make sure you're logged in here (sixter login)")
		}
	}

	srv := strings.TrimRight(*server, "/")
	if srv == "" {
		if rr.RepoSub != "" {
			srv = "https://" + rr.RepoSub + ".sixter.in"
		} else {
			fail("could not determine the repo server — re-run with -server <url>")
		}
	}
	// The component is the member's own folder inside the workspace. It defaults
	// to this directory's name — NEVER the workspace name, which is what made
	// every member share (and clobber) one folder.
	comp := sanitizeName(*component)
	if comp == "" {
		comp = sanitizeName(*project) // -project is a legacy alias for -component
	}
	if comp == "" {
		comp = defaultComponent()
	}
	if comp == "" {
		fail("could not determine a component name — re-run with -component <name>")
	}
	proj := componentProject(rr.WsName, comp)

	pb := projectBinding{
		Gateway: gw, Server: srv, Project: proj, Component: comp,
		Token: token, Email: rr.Email, Role: rr.Role, Workspace: rr.WsName,
	}
	if err := os.MkdirAll(".sixter", 0755); err != nil {
		fail("could not create .sixter: %v", err)
	}
	b, _ := json.MarshalIndent(pb, "", "  ")
	if err := os.WriteFile(bindingPath(), b, 0600); err != nil {
		fail("could not write binding: %v", err)
	}

	fmt.Println()
	fmt.Println("  ✓ Linked this folder to workspace:", rr.WsName)
	fmt.Println("    component :", comp, "→ server folder", proj)
	fmt.Println("    you       :", rr.Email, "("+rr.Role+")")
	fmt.Println("    server    :", srv)
	fmt.Println()
	fmt.Println("  This component is yours — teammates push their own and can't overwrite it.")
	fmt.Println("  Get the latest code :  sixter pull")
	fmt.Println("  Push your work      :  sixter push -m \"what you changed\"")
}

// migrateBinding upgrades a pre-component binding in place (older CLIs stored
// Project = the workspace name and had no Component, which made every member
// share one server folder). Applied by both push and pull so the two always
// agree on which component this folder is.
func migrateBinding(pb *projectBinding) {
	if pb.Component != "" {
		return
	}
	pb.Component = defaultComponent()
	if pb.Component == "" {
		pb.Component = sanitizeName(pb.Project)
	}
	pb.Project = componentProject(pb.Workspace, pb.Component)
	if b, err := json.MarshalIndent(pb, "", "  "); err == nil {
		_ = os.WriteFile(bindingPath(), b, 0600)
	}
	fmt.Println("  ↪ This folder is now its own component:", pb.Component, "→", pb.Project)
}

func cmdPush(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	branch := fs.String("branch", "", "push to a branch as a pull request (default: main if you own the component, else a branch named after you)")
	message := fs.String("m", "", "short message describing this push (like a commit message)")
	_ = fs.Parse(args)

	pb, err := loadBinding()
	if err != nil {
		fail("not a Sixter project folder — run `sixter project init <token>` here first")
	}

	migrateBinding(pb)

	fmt.Println("Packing", pb.Component, "…")
	buf := &bytes.Buffer{}
	if err := tarGz(".", buf); err != nil {
		fail("pack failed: %v", err)
	}

	q := urlpkg.Values{}
	q.Set("project", pb.Project)
	q.Set("component", pb.Component)
	if *branch != "" {
		q.Set("branch", *branch)
	}
	if *message != "" {
		q.Set("message", *message)
	}
	req, err := http.NewRequest(http.MethodPost, pb.Server+"/upload?"+q.Encode(), buf)
	if err != nil {
		fail("%v", err)
	}
	req.Header.Set("X-Enroll-Token", pb.Token)
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("User-Agent", "Sixter-CLI/1.0")

	fmt.Println("Pushing to", pb.Server, "…")
	cl := &http.Client{Timeout: 180 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		fail("push failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("push rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		PullRequest bool   `json:"pullRequest"`
		Branch      string `json:"branch"`
	}
	_ = json.Unmarshal(body, &res)
	fmt.Println()
	if res.PullRequest {
		fmt.Println("  ✓ Pull request updated on branch:", res.Branch)
		fmt.Println("    Your team lead will review the diff and merge it.")
	} else {
		fmt.Println("  ✓ Pushed to", pb.Component, "— pending review.")
		fmt.Println("    A project lead / deployment member will deploy it.")
	}
}

// cmdPull downloads the component's main (or a branch) into the current folder,
// so a teammate can start from the lead's template — like `git pull`.
func cmdPull(args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	branch := fs.String("branch", "", "pull a specific branch instead of main")
	force := fs.Bool("force", false, "overwrite local files that differ")
	_ = fs.Parse(args)

	pb, err := loadBinding()
	if err != nil {
		fail("not a Sixter project folder — run `sixter project init <token>` here first")
	}
	migrateBinding(pb)

	q := urlpkg.Values{}
	q.Set("project", pb.Project)
	if *branch != "" {
		q.Set("branch", *branch)
	}
	req, err := http.NewRequest(http.MethodGet, pb.Server+"/api/pull?"+q.Encode(), nil)
	if err != nil {
		fail("%v", err)
	}
	req.Header.Set("X-Enroll-Token", pb.Token)
	req.Header.Set("User-Agent", "Sixter-CLI/1.0")

	what := "main"
	if *branch != "" {
		what = "branch " + *branch
	}
	fmt.Println("Pulling", pb.Component, "("+what+") from", pb.Server, "…")
	cl := &http.Client{Timeout: 180 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		fail("pull failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fail("pull rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	n, err := untarGz(resp.Body, ".", *force)
	if err != nil {
		fail("unpack failed: %v", err)
	}
	fmt.Println()
	fmt.Println("  ✓ Pulled", n, "files into this folder.")
	if !*force {
		fmt.Println("    Existing files were kept where they differ — re-run with -force to overwrite.")
	}
}

// untarGz unpacks a gzip'd tar stream into dest. Path-traversal entries are
// rejected. When force is false, existing files are left untouched. Returns the
// number of files written.
func untarGz(r io.Reader, dest string, force bool) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	absDest, _ := filepath.Abs(dest)
	written := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return written, err
		}
		clean := filepath.Clean(filepath.FromSlash(hdr.Name))
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
			continue // skip anything trying to escape the folder
		}
		target := filepath.Join(dest, clean)
		if abs, _ := filepath.Abs(target); !strings.HasPrefix(abs, absDest) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0755)
		case tar.TypeReg:
			if !force {
				if _, statErr := os.Stat(target); statErr == nil {
					continue // keep the local copy
				}
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return written, err
			}
			f, oerr := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)|0600)
			if oerr != nil {
				return written, oerr
			}
			if _, cerr := io.Copy(f, tr); cerr != nil {
				f.Close()
				return written, cerr
			}
			f.Close()
			written++
		}
	}
	return written, nil
}

// sanitizeName turns a workspace name into a safe project slug.
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// tarGz packs a folder into gzip'd tar, skipping heavy/irrelevant dirs.
func tarGz(root string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		base := strings.Split(filepath.ToSlash(rel), "/")[0]
		if base == ".git" || base == "node_modules" || base == ".sixter" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return herr
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, oerr := os.Open(path)
			if oerr != nil {
				return oerr
			}
			_, cerr := io.Copy(tw, f)
			f.Close()
			if cerr != nil {
				return cerr
			}
		}
		return nil
	})
	if walkErr != nil {
		tw.Close()
		gz.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

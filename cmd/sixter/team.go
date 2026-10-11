package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"os"
	"strings"
	"time"
)

/*
Team-facing CLI commands for a coworkspace component:

	sixter status     what this folder is, who you are, what's pending
	sixter branch     branches (open pull requests) on this component
	sixter members    everyone in the workspace and their role
	sixter log        recent workspace activity
	sixter pr         list | view <branch> | merge <branch>

All of them authenticate with the personal enroll token stored in
.sixter/project.json by `sixter project init`.
*/

// memberGet performs an authenticated GET against the repo server and decodes
// the JSON body into out.
func memberGet(pb *projectBinding, path string, q urlpkg.Values, out interface{}) error {
	u := pb.Server + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Enroll-Token", pb.Token)
	req.Header.Set("User-Agent", "Sixter-CLI/1.0")
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func memberPost(pb *projectBinding, path string, payload interface{}, out interface{}) error {
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(http.MethodPost, pb.Server+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-Enroll-Token", pb.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sixter-CLI/1.0")
	cl := &http.Client{Timeout: 60 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// mustBinding loads the folder's workspace binding or exits with guidance.
func mustBinding() *projectBinding {
	pb, err := loadBinding()
	if err != nil {
		fail("not a Sixter project folder — run `sixter project init <workspace-link>` here first")
	}
	migrateBinding(pb)
	return pb
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

/* ─────────── sixter status ─────────── */

func cmdStatus(_ []string) {
	pb := mustBinding()

	fmt.Println()
	fmt.Println("  Workspace :", pb.Workspace)
	fmt.Println("  Component :", pb.Component, "→", pb.Project)
	fmt.Println("  You       :", pb.Email, "("+pb.Role+")")
	fmt.Println("  Server    :", pb.Server)

	var br struct {
		Main struct {
			Pending   bool   `json:"pending"`
			PendingBy string `json:"pendingBy"`
			Owner     string `json:"owner"`
		} `json:"main"`
		Branches []struct {
			Name    string    `json:"name"`
			Author  string    `json:"author"`
			Updated time.Time `json:"updated"`
		} `json:"branches"`
	}
	if err := memberGet(pb, "/api/branches", urlpkg.Values{"project": {pb.Project}}, &br); err != nil {
		fmt.Println()
		fmt.Println("  (could not reach the server:", err, ")")
		return
	}

	fmt.Println()
	if br.Main.Owner != "" {
		mine := strings.EqualFold(br.Main.Owner, pb.Email)
		who := br.Main.Owner
		if mine {
			who += " (you)"
		}
		fmt.Println("  main owner:", who)
		if !mine && pb.Role != "lead" {
			fmt.Println("  → your pushes open a pull request for review.")
		}
	}
	if br.Main.Pending {
		fmt.Println("  ⏳ main has a push pending deploy by", br.Main.PendingBy)
	}
	if len(br.Branches) == 0 {
		fmt.Println("  No open pull requests.")
	} else {
		fmt.Println("  Open pull requests:")
		for _, b := range br.Branches {
			fmt.Printf("    • %-20s %s  %s\n", b.Name, b.Author, ago(b.Updated))
		}
	}
	fmt.Println()
}

/* ─────────── sixter branch ─────────── */

func cmdBranch(_ []string) {
	pb := mustBinding()
	var br struct {
		Main struct {
			Pending   bool   `json:"pending"`
			PendingBy string `json:"pendingBy"`
			Owner     string `json:"owner"`
		} `json:"main"`
		Branches []struct {
			Name    string    `json:"name"`
			Author  string    `json:"author"`
			Message string    `json:"message"`
			Files   int       `json:"files"`
			Updated time.Time `json:"updated"`
		} `json:"branches"`
	}
	if err := memberGet(pb, "/api/branches", urlpkg.Values{"project": {pb.Project}}, &br); err != nil {
		fail("could not list branches: %v", err)
	}

	fmt.Println()
	fmt.Println("  Branches on", pb.Component)
	fmt.Println()
	state := "live"
	if br.Main.Pending {
		state = "pending deploy (" + br.Main.PendingBy + ")"
	}
	fmt.Printf("  * %-22s %s\n", "main", state)
	for _, b := range br.Branches {
		marker := " "
		fmt.Printf("  %s %-22s %s · %d files · %s\n", marker, b.Name, b.Author, b.Files, ago(b.Updated))
		if b.Message != "" {
			fmt.Printf("    %s\n", b.Message)
		}
	}
	if len(br.Branches) == 0 {
		fmt.Println("    (no open branches)")
	}
	fmt.Println()
	fmt.Println("  Pull one:  sixter pull -branch <name>")
	fmt.Println()
}

/* ─────────── sixter members ─────────── */

func cmdMembers(_ []string) {
	pb := mustBinding()
	var res struct {
		Components []struct {
			Component string `json:"component"`
			Owner     string `json:"owner"`
			Role      string `json:"role"`
			Workspace string `json:"workspace"`
			Pending   *struct {
				Author string `json:"author"`
			} `json:"pending"`
		} `json:"components"`
	}
	if err := memberGet(pb, "/api/components", nil, &res); err != nil {
		fail("could not list members: %v", err)
	}

	fmt.Println()
	fmt.Println("  Members of", pb.Workspace)
	fmt.Println()
	found := 0
	for _, c := range res.Components {
		if pb.Workspace != "" && !strings.EqualFold(c.Workspace, pb.Workspace) {
			continue
		}
		found++
		you := ""
		if strings.EqualFold(c.Owner, pb.Email) {
			you = "  ← you"
		}
		status := "live"
		if c.Pending != nil {
			status = "pending review"
		}
		fmt.Printf("  %-28s %-11s %-16s %s%s\n", c.Owner, c.Role, c.Component, status, you)
	}
	if found == 0 {
		fmt.Println("  (nobody has pushed a component yet)")
	}
	fmt.Println()
}

/* ─────────── sixter log ─────────── */

func cmdLog(args []string) {
	fs := flag.NewFlagSet("log", flag.ExitOnError)
	limit := fs.Int("n", 20, "how many entries to show")
	_ = fs.Parse(args)

	pb := mustBinding()
	var res struct {
		Events []struct {
			Time    time.Time `json:"time"`
			Actor   string    `json:"actor"`
			Action  string    `json:"action"`
			Project string    `json:"project"`
		} `json:"events"`
	}
	if err := memberGet(pb, "/api/activity", nil, &res); err != nil {
		fail("could not read activity: %v", err)
	}

	fmt.Println()
	fmt.Println("  Recent activity")
	fmt.Println()
	n := 0
	for _, e := range res.Events {
		if n >= *limit {
			break
		}
		n++
		fmt.Printf("  %-11s %-28s %s", ago(e.Time), e.Actor, e.Action)
		if e.Project != "" {
			fmt.Printf("  [%s]", e.Project)
		}
		fmt.Println()
	}
	if n == 0 {
		fmt.Println("  (nothing yet)")
	}
	fmt.Println()
}

/* ─────────── sixter pr ─────────── */

func cmdPR(args []string) {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "ls":
		prList()
	case "view", "show", "diff":
		if len(args) == 0 {
			fail("usage: sixter pr view <branch>")
		}
		prView(args[0])
	case "merge":
		if len(args) == 0 {
			fail("usage: sixter pr merge <branch>")
		}
		prMerge(args[0], "/api/pr/merge", "Merged")
	case "close":
		if len(args) == 0 {
			fail("usage: sixter pr close <branch>")
		}
		prMerge(args[0], "/api/pr/close", "Closed")
	default:
		fail("unknown pr subcommand %q (try: list, view, merge, close)", sub)
	}
}

func prList() {
	pb := mustBinding()
	var res struct {
		PRs []struct {
			Project string    `json:"project"`
			Branch  string    `json:"branch"`
			Author  string    `json:"author"`
			Message string    `json:"message"`
			Files   int       `json:"files"`
			Updated time.Time `json:"updated"`
		} `json:"prs"`
	}
	if err := memberGet(pb, "/api/prs", nil, &res); err != nil {
		fail("could not list pull requests: %v", err)
	}
	fmt.Println()
	fmt.Println("  Open pull requests")
	fmt.Println()
	for _, p := range res.PRs {
		fmt.Printf("  %-20s %-28s %d files  %s\n", p.Branch, p.Author, p.Files, ago(p.Updated))
		if p.Message != "" {
			fmt.Printf("    %s\n", p.Message)
		}
		fmt.Printf("    %s\n", p.Project)
	}
	if len(res.PRs) == 0 {
		fmt.Println("  (none)")
	}
	fmt.Println()
	fmt.Println("  Review:  sixter pr view <branch>")
	fmt.Println("  Merge :  sixter pr merge <branch>   (lead only)")
	fmt.Println()
}

func prView(branch string) {
	pb := mustBinding()
	var res struct {
		Files []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
			Binary bool   `json:"binary"`
			Add    int    `json:"add"`
			Del    int    `json:"del"`
			Diff   string `json:"diff"`
		} `json:"files"`
	}
	q := urlpkg.Values{"project": {pb.Project}, "branch": {branch}}
	if err := memberGet(pb, "/api/pr/diff", q, &res); err != nil {
		fail("could not read the diff: %v", err)
	}

	// ANSI colour only when stdout is a terminal-ish destination
	green, red, dim, reset := "\033[32m", "\033[31m", "\033[90m", "\033[0m"
	if os.Getenv("NO_COLOR") != "" {
		green, red, dim, reset = "", "", "", ""
	}

	fmt.Println()
	fmt.Println("  Pull request:", branch)
	fmt.Println()
	for _, f := range res.Files {
		fmt.Printf("  %s  %s(+%d -%d)%s\n", f.Path, dim, f.Add, f.Del, reset)
		if f.Binary || f.Diff == "" {
			fmt.Println("    (binary or too large to display)")
			continue
		}
		for _, ln := range strings.Split(strings.TrimRight(f.Diff, "\n"), "\n") {
			switch {
			case strings.HasPrefix(ln, "+"):
				fmt.Println("   ", green+ln+reset)
			case strings.HasPrefix(ln, "-"):
				fmt.Println("   ", red+ln+reset)
			case strings.HasPrefix(ln, "⋯"):
				fmt.Println("   ", dim+ln+reset)
			default:
				fmt.Println("   ", ln)
			}
		}
		fmt.Println()
	}
	if len(res.Files) == 0 {
		fmt.Println("  (no changes)")
	}
}

func prMerge(branch, path, verb string) {
	pb := mustBinding()
	err := memberPost(pb, path, map[string]string{"project": pb.Project, "branch": branch}, nil)
	if err != nil {
		fail("%s failed: %v", strings.ToLower(verb), err)
	}
	fmt.Println()
	fmt.Println("  ✓", verb, branch)
	if verb == "Merged" {
		fmt.Println("    main now has this work — deploy it with:  sixter project deploy")
	}
	fmt.Println()
}

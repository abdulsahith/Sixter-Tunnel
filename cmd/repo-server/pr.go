package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

/*
Pull requests (Coworkspace Step 3) — a lightweight, snapshot-based review flow.

A component has a canonical `main` (the live folder in dataDir/<project>) plus
zero or more BRANCHES. A member who doesn't own the component pushes their work
as a branch — that's a pull request. The lead reviews the diff in the dashboard
and merges: the branch replaces main (main is snapshotted first for rollback),
and the merge is marked pending-deploy for a lead/deployment member to promote.

No real git: branches are stored copies, merge is a whole-branch replace, and
diffs are computed server-side. Everything is recorded in the activity log.
*/

// PullRequest is one member's branch submitted for review.
type PullRequest struct {
	Project   string    `json:"project"`
	Branch    string    `json:"branch"`
	Workspace string    `json:"workspace"`
	Author    string    `json:"author"`
	Role      string    `json:"role"`
	Message   string    `json:"message"`
	Status    string    `json:"status"` // open | merged | closed
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	MergedBy  string    `json:"merged_by,omitempty"`
	MergedAt  time.Time `json:"merged_at,omitempty"`
}

func (p PullRequest) key() string { return p.Project + "\x00" + p.Branch }

var (
	prMu sync.RWMutex
	prs  = map[string]PullRequest{} // "project\x00branch" → PR
)

func branchesRoot() string      { return filepath.Join(installDir, "branches") }
func branchDir(project, branch string) string {
	return filepath.Join(branchesRoot(), project, branch)
}
func prFile() string { return filepath.Join(dataDir, ".sixter-prs.json") }

func loadPRs() {
	b, err := os.ReadFile(prFile())
	if err != nil {
		return
	}
	var m map[string]PullRequest
	if json.Unmarshal(b, &m) == nil && m != nil {
		prMu.Lock()
		prs = m
		prMu.Unlock()
	}
}

func savePRsLocked() {
	b, _ := json.MarshalIndent(prs, "", "  ")
	_ = atomicWriteFile(prFile(), b, 0644)
}

// safeSegment is isSafeName plus an explicit guard against path traversal —
// isSafeName permits dots, so "." / ".." / "a..b" must be rejected before a
// branch name is ever joined into a filesystem path.
func safeSegment(s string) bool {
	return isSafeName(s) && s != "." && s != ".." && !strings.Contains(s, "..")
}


// defaultBranchName derives a branch name from a member's email local-part, so
// `sixter push` without -branch still lands on a sensible, per-member branch.
func defaultBranchName(email string) string {
	local := email
	if i := strings.IndexByte(email, '@'); i > 0 {
		local = email[:i]
	}
	b := sanitizeSlug(local)
	if b == "" {
		b = "work"
	}
	return b
}

// sanitizeSlug lowercases and keeps [a-z0-9-]; used for branch names.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// upsertBranchPR records (or refreshes) the pull request for a branch push.
func upsertBranchPR(project, branch, workspace, author, role, message string, files int, bytes int64) PullRequest {
	prMu.Lock()
	defer prMu.Unlock()
	k := project + "\x00" + branch
	now := time.Now()
	pr, ok := prs[k]
	if !ok || pr.Status != "open" {
		pr = PullRequest{Project: project, Branch: branch, Author: author, Created: now}
	}
	pr.Workspace = workspace
	pr.Author = author // the pusher owns their branch
	pr.Role = role
	if message != "" {
		pr.Message = message
	}
	pr.Status = "open"
	pr.Files = files
	pr.Bytes = bytes
	pr.Updated = now
	pr.MergedBy = ""
	pr.MergedAt = time.Time{}
	prs[k] = pr
	savePRsLocked()
	return pr
}

func getPR(project, branch string) (PullRequest, bool) {
	prMu.RLock()
	defer prMu.RUnlock()
	pr, ok := prs[project+"\x00"+branch]
	return pr, ok
}

// listOpenPRs returns open PRs, optionally filtered by project ("" = all).
func listOpenPRs(project string) []PullRequest {
	prMu.RLock()
	defer prMu.RUnlock()
	out := []PullRequest{}
	for _, pr := range prs {
		if pr.Status != "open" {
			continue
		}
		if project == "" || pr.Project == project {
			out = append(out, pr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// mergePR replaces main with the branch content (snapshotting main first), marks
// the PR merged, removes the branch, and stages the result for deployment.
func mergePR(project, branch, mergedBy string) (PullRequest, error) {
	bdir := branchDir(project, branch)
	if st, err := os.Stat(bdir); err != nil || !st.IsDir() {
		return PullRequest{}, os.ErrNotExist
	}

	// back up current main so the merge is reversible
	_ = snapshotProject(project)

	mainPath := filepath.Join(dataDir, project)
	_ = os.RemoveAll(mainPath)
	if err := copyTree(bdir, mainPath); err != nil {
		return PullRequest{}, err
	}
	files, bytesN := countTree(mainPath)
	_ = writeProjectMeta(mainPath, ProjectMeta{Name: project, Files: files, Bytes: bytesN})

	// consume the branch
	_ = os.RemoveAll(bdir)

	prMu.Lock()
	k := project + "\x00" + branch
	pr := prs[k]
	pr.Project, pr.Branch = project, branch
	pr.Status = "merged"
	pr.MergedBy = mergedBy
	pr.MergedAt = time.Now()
	pr.Updated = pr.MergedAt
	prs[k] = pr
	savePRsLocked()
	prMu.Unlock()

	// a merge is a new main awaiting promotion — stage it like a member push
	setPendingPush(project, mergedBy)
	return pr, nil
}

func closePR(project, branch string) {
	_ = os.RemoveAll(branchDir(project, branch))
	prMu.Lock()
	k := project + "\x00" + branch
	if pr, ok := prs[k]; ok {
		pr.Status = "closed"
		pr.Updated = time.Now()
		prs[k] = pr
		savePRsLocked()
	}
	prMu.Unlock()
}

/* ─────────── file helpers ─────────── */

// copyTree recursively copies src into dst (regular files + dirs only).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer in.Close()
		out, cerr := os.Create(target)
		if cerr != nil {
			return cerr
		}
		defer out.Close()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := in.Read(buf)
			if n > 0 {
				if _, werr := out.Write(buf[:n]); werr != nil {
					return werr
				}
			}
			if rerr != nil {
				break
			}
		}
		return nil
	})
}

func countTree(root string) (files int, bytesN int64) {
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			files++
			bytesN += info.Size()
		}
		return nil
	})
	return
}

/* ─────────── diff engine ─────────── */

// FileDiff is the review payload for one changed file.
type FileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | removed | modified
	Binary bool   `json:"binary,omitempty"`
	Add    int    `json:"add"`
	Del    int    `json:"del"`
	Diff   string `json:"diff,omitempty"` // prefixed lines: ' ' ctx, '-' del, '+' add
}

const diffMaxFileBytes = 512 * 1024
const diffMaxLines = 6000

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

// readTree returns path → file bytes for every regular file under root.
func readTree(root string) map[string][]byte {
	out := map[string][]byte{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.Size() > diffMaxFileBytes {
			out[rel] = nil // present but too big to diff
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr == nil {
			out[rel] = b
		}
		return nil
	})
	return out
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var lines []string
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	return lines
}

// unifiedLineDiff produces a compact prefixed diff between two line slices,
// collapsing long unchanged runs. Returns added/removed counts and the text.
func unifiedLineDiff(a, b []string) (add, del int, text string) {
	n, m := len(a), len(b)
	if n > diffMaxLines || m > diffMaxLines {
		// too large for an O(n*m) diff — report size delta only
		return m, n, "(file too large to show a line diff)"
	}
	// LCS length table
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	type op struct {
		k byte
		s string
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
			del++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
			add++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
		del++
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
		add++
	}

	// collapse runs of >6 unchanged lines to keep the payload small
	const ctx = 3
	var out strings.Builder
	writeCtx := func(lines []string) {
		if len(lines) <= 2*ctx+1 {
			for _, l := range lines {
				out.WriteByte(' ')
				out.WriteString(l)
				out.WriteByte('\n')
			}
			return
		}
		for _, l := range lines[:ctx] {
			out.WriteByte(' ')
			out.WriteString(l)
			out.WriteByte('\n')
		}
		out.WriteString("⋯ " + itoa(len(lines)-2*ctx) + " unchanged lines\n")
		for _, l := range lines[len(lines)-ctx:] {
			out.WriteByte(' ')
			out.WriteString(l)
			out.WriteByte('\n')
		}
	}
	var run []string
	for _, o := range ops {
		if o.k == ' ' {
			run = append(run, o.s)
			continue
		}
		if len(run) > 0 {
			writeCtx(run)
			run = run[:0]
		}
		out.WriteByte(o.k)
		out.WriteString(o.s)
		out.WriteByte('\n')
	}
	if len(run) > 0 {
		writeCtx(run)
	}
	text = out.String()
	if len(text) > 200*1024 {
		text = text[:200*1024] + "\n… diff truncated\n"
	}
	return add, del, text
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// diffTrees compares the branch against main and returns per-file diffs.
func diffTrees(mainDir, branchDir string) []FileDiff {
	base := readTree(mainDir)
	head := readTree(branchDir)
	seen := map[string]bool{}
	out := []FileDiff{}

	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		bBytes, inBase := base[path]
		hBytes, inHead := head[path]

		fd := FileDiff{Path: path}
		switch {
		case inBase && !inHead:
			fd.Status = "removed"
		case !inBase && inHead:
			fd.Status = "added"
		default:
			fd.Status = "modified"
		}

		oversize := (inBase && bBytes == nil) || (inHead && hBytes == nil)
		if oversize || isBinary(bBytes) || isBinary(hBytes) {
			fd.Binary = true // too large or non-text: report status only
		} else {
			fd.Add, fd.Del, fd.Diff = unifiedLineDiff(splitLines(bBytes), splitLines(hBytes))
			if fd.Add == 0 && fd.Del == 0 && fd.Status == "modified" {
				return // identical content, skip
			}
		}
		out = append(out, fd)
	}

	for p := range base {
		add(p)
	}
	for p := range head {
		add(p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// handleBranchUpload extracts a member's push into a branch folder and opens or
// updates its pull request. Called from uploadHandler; main is never touched.
func handleBranchUpload(w http.ResponseWriter, r *http.Request, project, branch, workspace, author, role string) {
	message := strings.TrimSpace(r.URL.Query().Get("message"))
	maxBytes := int64(maxUploadGB) * 1024 * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()

	dir := branchDir(project, branch)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, "failed to create branch dir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	files, byteCount, err := untarGzSafeWithStats(r.Body, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, "extract failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	setProjWorkspace(project, workspace)
	// Register the component now — a PR-only member (the common case) never
	// writes main, so this is the only place their component gets recorded and
	// shown in the workspace view.
	recordComponent(project, workspace, strings.TrimSpace(r.URL.Query().Get("component")), author, role)
	pr := upsertBranchPR(project, branch, workspace, author, role, message, files, byteCount)
	logActivity(author, "Opened/updated PR "+branch, project, 200)
	writeJSONResp(w, map[string]any{
		"ok": true, "pullRequest": true, "project": project, "branch": branch,
		"files": files, "sizeBytes": byteCount, "sizeHuman": formatSize(byteCount),
		"status": pr.Status,
	})
}

/* ─────────── handlers ─────────── */

// GET /api/prs[?project=] — open pull requests (dashboard, rbac: view).
func handlePRs(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	writeJSONResp(w, map[string]any{"prs": listOpenPRs(project)})
}

// GET /api/branches?project= — branches for a component, with their PR state.
// Enroll-token (CLI) or dashboard auth; used by `sixter branch`.
func handleBranches(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}
	if !enrollOrDashboardAuth(w, r) {
		return
	}

	type branchRow struct {
		Name    string    `json:"name"`
		Author  string    `json:"author"`
		Message string    `json:"message"`
		Files   int       `json:"files"`
		Updated time.Time `json:"updated"`
		Open    bool      `json:"open"`
	}
	rows := []branchRow{}
	prMu.RLock()
	for _, pr := range prs {
		if pr.Project == project && pr.Status == "open" {
			rows = append(rows, branchRow{
				Name: pr.Branch, Author: pr.Author, Message: pr.Message,
				Files: pr.Files, Updated: pr.Updated, Open: true,
			})
		}
	}
	prMu.RUnlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Updated.After(rows[j].Updated) })

	main := map[string]any{"name": "main", "live": true}
	if p, ok := getPendingPush(project); ok {
		main["pending"] = true
		main["pendingBy"] = p.Author
	}
	if ci, ok := getComponent(project); ok {
		main["owner"] = ci.Owner
		main["component"] = ci.Component
		main["workspace"] = ci.Workspace
	}
	writeJSONResp(w, map[string]any{"project": project, "main": main, "branches": rows})
}

// canActOnProject checks that a caller may act on a project that carries its
// workspace in the body rather than the URL (so the central path-based RBAC
// can't scope it). The owner may act anywhere; a member must belong to that
// project's workspace.
func canActOnProject(p *Principal, project string) bool {
	if p == nil {
		return false
	}
	if p.IsOwner || len(p.Workspaces) == 0 {
		return true // owner, or a legacy global-team member
	}
	wsName := getProjWorkspace(project)
	if wsName == "" {
		if ci, ok := getComponent(project); ok {
			wsName = ci.Workspace
		}
	}
	if wsName == "" {
		return false // unattributed project → members can't touch it
	}
	_, ok := p.Workspaces[wsName]
	return ok
}

// enrollOrDashboardAuth accepts a member's enroll token (CLI) or the normal
// owner/SSO dashboard credentials. Used by endpoints the CLI also calls.
func enrollOrDashboardAuth(w http.ResponseWriter, r *http.Request) bool {
	if enroll := strings.TrimSpace(r.Header.Get("X-Enroll-Token")); enroll != "" {
		if _, _, _, ok := verifyEnroll(enroll); ok {
			return true
		}
	}
	if principal(r) != nil {
		return true
	}
	http.Error(w, "invalid or expired enrollment token", http.StatusUnauthorized)
	return false
}

// GET /api/pr/diff?project=&branch= — changed files + diffs (rbac: view).
func handlePRDiff(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if !isSafeName(project) || !safeSegment(branch) {
		http.Error(w, "invalid project or branch", http.StatusBadRequest)
		return
	}
	pr, ok := getPR(project, branch)
	if !ok {
		http.Error(w, "pull request not found", http.StatusNotFound)
		return
	}
	files := diffTrees(filepath.Join(dataDir, project), branchDir(project, branch))
	writeJSONResp(w, map[string]any{"pr": pr, "files": files})
}

// POST /api/pr/merge {project, branch} — lead-only (rbac: merge).
func handlePRMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct{ Project, Branch string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !isSafeName(body.Project) || !isSafeName(body.Branch) {
		http.Error(w, "invalid project or branch", http.StatusBadRequest)
		return
	}
	actor := "owner"
	if p := principal(r); p != nil {
		if p.Email != "" {
			actor = p.Email
		}
		if !canActOnProject(p, body.Project) {
			http.Error(w, "forbidden: that project is outside your workspace", http.StatusForbidden)
			return
		}
	}
	pr, err := mergePR(body.Project, body.Branch, actor)
	if err != nil {
		http.Error(w, "merge failed: branch not found", http.StatusNotFound)
		return
	}
	logActivity(actor, "Merged PR "+body.Branch+" by "+pr.Author, body.Project, 200)
	writeJSONResp(w, map[string]any{"ok": true, "merged": pr, "pendingDeploy": true})
}

// POST /api/pr/close {project, branch} — lead-only (rbac: merge).
func handlePRClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct{ Project, Branch string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !isSafeName(body.Project) || !isSafeName(body.Branch) {
		http.Error(w, "invalid project or branch", http.StatusBadRequest)
		return
	}
	actor := "owner"
	if p := principal(r); p != nil {
		if p.Email != "" {
			actor = p.Email
		}
		if !canActOnProject(p, body.Project) {
			http.Error(w, "forbidden: that project is outside your workspace", http.StatusForbidden)
			return
		}
	}
	closePR(body.Project, body.Branch)
	logActivity(actor, "Closed PR "+body.Branch, body.Project, 200)
	writeJSONResp(w, map[string]any{"ok": true})
}

// GET /api/pull?project=&branch= — the CLI `sixter pull`. Enroll-token (member)
// or owner-token auth; streams a gzip tar of main (or a branch). Bypasses the
// SSO rbac gate like /upload and /api/member-deploy.
func handlePull(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	authed := false
	if enroll := strings.TrimSpace(r.Header.Get("X-Enroll-Token")); enroll != "" {
		if _, _, _, ok := verifyEnroll(enroll); ok {
			authed = true
		}
	}
	// A fleet node fetches project source with a scoped token that grants read
	// access to exactly this project — never the owner token.
	if !authed && validSourceToken(r.Header.Get("Authorization"), project) {
		authed = true
	}
	if !authed && reqToken(r) != "" {
		if principal(r) != nil {
			authed = true
		}
	}
	if !authed {
		http.Error(w, "invalid or expired enrollment token", http.StatusUnauthorized)
		return
	}

	src := filepath.Join(dataDir, project)
	if branch != "" {
		if !safeSegment(branch) {
			http.Error(w, "invalid branch", http.StatusBadRequest)
			return
		}
		src = branchDir(project, branch)
	}
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		http.Error(w, "nothing to pull yet", http.StatusNotFound)
		return
	}

	tmp, err := os.CreateTemp("", "sixter-pull-*.tar.gz")
	if err != nil {
		http.Error(w, "pull failed", http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := tarGzDir(src, tmpPath); err != nil {
		http.Error(w, "pull failed", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		http.Error(w, "pull failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	buf := make([]byte, 64*1024)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/*
Coworkspace Step 2 — member push.

A developer enrolls with `sixter project init <token>` and pushes with
`sixter push`. The push carries the enrollment token (X-Enroll-Token); the
repo-server verifies it against the gateway, then STORES the upload without
deploying and marks the project "pending" (attributed to the pusher). A project
lead / deployment member then hits Deploy to promote it live.
*/

// verifyEnroll asks the gateway to resolve an enrollment token.
func verifyEnroll(token string) (email, role, wsName string, ok bool) {
	if token == "" {
		return "", "", "", false
	}
	u := gatewayBase() + "/api/enroll/redeem?token=" + url.QueryEscape(token)
	cl := &http.Client{Timeout: 8 * time.Second}
	resp, err := cl.Get(u)
	if err != nil {
		return "", "", "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var v struct {
		OK     bool   `json:"ok"`
		Email  string `json:"email"`
		Role   string `json:"role"`
		WsName string `json:"ws_name"`
	}
	if json.Unmarshal(body, &v) != nil || !v.OK {
		return "", "", "", false
	}
	return v.Email, v.Role, v.WsName, true
}

/* ─────────── pending pushes ─────────── */

type PendingPush struct {
	Author string    `json:"author"`
	At     time.Time `json:"at"`
}

var (
	pendingMu sync.RWMutex
	pending   = map[string]PendingPush{} // project → pending push awaiting deploy
)

func pendingFile() string { return filepath.Join(dataDir, ".sixter-pending.json") }

func loadPending() {
	b, err := os.ReadFile(pendingFile())
	if err != nil {
		return
	}
	var m map[string]PendingPush
	if json.Unmarshal(b, &m) == nil && m != nil {
		pendingMu.Lock()
		pending = m
		pendingMu.Unlock()
	}
}

func savePendingLocked() {
	b, _ := json.MarshalIndent(pending, "", "  ")
	_ = atomicWriteFile(pendingFile(), b, 0644)
}

func setPendingPush(project, author string) {
	pendingMu.Lock()
	pending[project] = PendingPush{Author: author, At: time.Now()}
	savePendingLocked()
	pendingMu.Unlock()
}

func clearPendingPush(project string) {
	pendingMu.Lock()
	if _, ok := pending[project]; ok {
		delete(pending, project)
		savePendingLocked()
	}
	pendingMu.Unlock()
}

func getPendingPush(project string) (PendingPush, bool) {
	pendingMu.RLock()
	defer pendingMu.RUnlock()
	p, ok := pending[project]
	return p, ok
}

/* ─────────── project → workspace (recorded at push) ─────────── */

var (
	projWSMu sync.RWMutex
	projWS   = map[string]string{} // project → workspace name (from the push token)
)

func projWSFile() string { return filepath.Join(dataDir, ".sixter-projws.json") }

func loadProjWS() {
	b, err := os.ReadFile(projWSFile())
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(b, &m) == nil && m != nil {
		projWSMu.Lock()
		projWS = m
		projWSMu.Unlock()
	}
}

func setProjWorkspace(project, ws string) {
	if ws == "" {
		return
	}
	projWSMu.Lock()
	projWS[project] = ws
	b, _ := json.MarshalIndent(projWS, "", "  ")
	_ = atomicWriteFile(projWSFile(), b, 0644)
	projWSMu.Unlock()
}

func getProjWorkspace(project string) string {
	projWSMu.RLock()
	defer projWSMu.RUnlock()
	return projWS[project]
}

/* ─────────── components (per-member folders inside a workspace) ─────────── */

// componentInfo records who owns a component (a named, independently deployable
// folder inside a workspace) so members can't overwrite each other's work. The
// first member to push a component name claims it; afterwards only that owner —
// or a workspace lead — may push to it.
type componentInfo struct {
	Workspace string    `json:"workspace"`
	Component string    `json:"component"`
	Owner     string    `json:"owner"`
	Role      string    `json:"role"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

var (
	compMu     sync.RWMutex
	components = map[string]componentInfo{} // project id → component info
)

func componentsFile() string { return filepath.Join(dataDir, ".sixter-components.json") }

func loadComponents() {
	b, err := os.ReadFile(componentsFile())
	if err != nil {
		return
	}
	var m map[string]componentInfo
	if json.Unmarshal(b, &m) == nil && m != nil {
		compMu.Lock()
		components = m
		compMu.Unlock()
	}
}

func saveComponentsLocked() {
	b, _ := json.MarshalIndent(components, "", "  ")
	_ = atomicWriteFile(componentsFile(), b, 0644)
}

func getComponent(project string) (componentInfo, bool) {
	compMu.RLock()
	defer compMu.RUnlock()
	c, ok := components[project]
	return c, ok
}

// claimOrCheckComponent decides whether email/role may push to project.
//
//   - unclaimed → the pusher claims ownership (records workspace/component/owner)
//   - owned by the pusher → allowed
//   - owned by someone else → allowed only for a workspace lead
//
// Returns ok=false with a human reason when the push must be rejected.
func claimOrCheckComponent(project, workspace, component, email, role string) (ok bool, reason string) {
	email = strings.ToLower(strings.TrimSpace(email))
	compMu.Lock()
	defer compMu.Unlock()

	existing, found := components[project]
	if found && existing.Owner != "" {
		if !strings.EqualFold(existing.Owner, email) && role != "lead" {
			return false, project + " belongs to " + existing.Owner +
				" — push to your own component (sixter project init … -component <name>)"
		}
		// owner (or a lead) re-pushing: refresh metadata, keep original owner
		existing.Updated = time.Now()
		if component != "" {
			existing.Component = component
		}
		if workspace != "" {
			existing.Workspace = workspace
		}
		components[project] = existing
		saveComponentsLocked()
		return true, ""
	}

	// first push claims the component for this member
	now := time.Now()
	components[project] = componentInfo{
		Workspace: workspace, Component: component, Owner: email, Role: role,
		Created: now, Updated: now,
	}
	saveComponentsLocked()
	return true, ""
}

// recordComponent registers a component WITHOUT rejecting. Branch/PR pushes go
// through here: the first pusher claims ownership, later pushes just refresh
// metadata. Branches are isolated per-branch on disk, so there's no clobber to
// guard against — that check only matters for direct main writes
// (claimOrCheckComponent). Without this, a member who only ever opens PRs never
// appears in the workspace's component list.
func recordComponent(project, workspace, component, owner, role string) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if project == "" {
		return
	}
	compMu.Lock()
	defer compMu.Unlock()
	c, ok := components[project]
	now := time.Now()
	if !ok {
		c = componentInfo{Created: now}
	}
	if c.Owner == "" {
		c.Owner = owner // first pusher owns it
	}
	if component != "" {
		c.Component = component
	}
	if workspace != "" {
		c.Workspace = workspace
	}
	if role != "" {
		c.Role = role
	}
	c.Updated = now
	components[project] = c
	saveComponentsLocked()
}

// listComponents returns every recorded component, optionally filtered by
// workspace name ("" = all).
func listComponents(workspace string) []componentInfo {
	compMu.RLock()
	defer compMu.RUnlock()
	out := make([]componentInfo, 0, len(components))
	for _, c := range components {
		if workspace == "" || strings.EqualFold(c.Workspace, workspace) {
			out = append(out, c)
		}
	}
	return out
}

// GET /api/components — the dashboard's workspace → components view. Each entry
// carries its owner, role, whether a push is pending review, and the folder id.
func handleComponents(w http.ResponseWriter, r *http.Request) {
	compMu.RLock()
	rows := make([]map[string]any, 0, len(components))
	for proj, c := range components {
		row := map[string]any{
			"project":   proj,
			"workspace": c.Workspace,
			"component": c.Component,
			"owner":     c.Owner,
			"role":      c.Role,
			"updated":   c.Updated,
		}
		if p, ok := getPendingPush(proj); ok {
			row["pending"] = map[string]any{"author": p.Author, "at": p.At}
		}
		rows = append(rows, row)
	}
	compMu.RUnlock()

	// group by workspace name for the UI
	groups := map[string][]map[string]any{}
	for _, row := range rows {
		wsName, _ := row["workspace"].(string)
		groups[wsName] = append(groups[wsName], row)
	}
	writeJSONResp(w, map[string]any{"components": rows, "byWorkspace": groups})
}

/* ─────────── member deploy (role-gated promote) ─────────── */

// POST /api/member-deploy?project=<p> — a workspace lead or deployment member
// promotes a pending push. Authenticated by the enrollment token (bypasses the
// owner/SSO rbac gate). Developers/frontend/backend/viewer cannot deploy.
func handleMemberDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	email, role, _, ok := verifyEnroll(strings.TrimSpace(r.Header.Get("X-Enroll-Token")))
	if !ok {
		http.Error(w, "invalid or expired enrollment token", http.StatusUnauthorized)
		return
	}
	if !roleCanDeploy(role) {
		http.Error(w, "your role ("+role+") can't deploy — only a lead or deployment member can promote to production", http.StatusForbidden)
		return
	}
	project := r.URL.Query().Get("project")
	if !isSafeName(project) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(dataDir, project)); err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	// A lead may promote any component in their workspace; a deployment member
	// too. (Ownership only restricts *pushes*, not promotion.)
	clearPendingPush(project)
	logActivity(email, "Deployed (promote)", project, 200)
	go runPipeline(project, "member-deploy", email)
	writeJSONResp(w, map[string]any{"ok": true, "started": true, "project": project})
}

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/*
Workspaces + per-project teams (Coworkspace, Step 1).

A workspace groups the projects of one app (e.g. "E-commerce" = a frontend
project + a backend project). Each PROJECT has its own team: a project lead,
developers, an optional deployment role, and viewers. Members are enrolled with
a per-project token (`sixter project init <token>`) so this works on localhost
without the gateway.

All state lives on the repo-server (next to the projects), persisted in
workspaces.json. Everything here is additive — the solo/auto-deploy flow is
untouched.
*/

type Workspace struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

type ProjMember struct {
	Email   string    `json:"email"`
	Role    string    `json:"role"` // lead | developer | deployment | viewer
	AddedAt time.Time `json:"added_at"`
}

type enrollToken struct {
	Project string    `json:"project"`
	Email   string    `json:"email"`
	Role    string    `json:"role"`
	Created time.Time `json:"created"`
}

type wsState struct {
	Workspaces  map[string]*Workspace   `json:"workspaces"`   // id → workspace
	ProjectWS   map[string]string       `json:"project_ws"`   // project → workspace id
	ProjMembers map[string][]ProjMember `json:"proj_members"` // project → members
	Tokens      map[string]enrollToken  `json:"tokens"`       // enrollment token → info
}

var (
	wsMu sync.RWMutex
	ws   = wsState{
		Workspaces:  map[string]*Workspace{},
		ProjectWS:   map[string]string{},
		ProjMembers: map[string][]ProjMember{},
		Tokens:      map[string]enrollToken{},
	}
)

func validProjRole(r string) bool {
	switch r {
	case "lead", "developer", "deployment", "tester", "viewer":
		return true
	}
	return false
}

// roleCanPush / roleCanDeploy mirror the gateway's workspace RBAC on the
// repo-server. Gateway dev roles arrive as "backend"/"frontend"; the local
// members API uses "developer". All of them may push (staged); tester and
// viewer may not. Only lead and deployment promote to production.
func roleCanPush(role string) bool {
	switch role {
	case "lead", "backend", "frontend", "developer", "deployment":
		return true
	}
	return false // tester, viewer
}

func roleCanDeploy(role string) bool { return role == "lead" || role == "deployment" }

func wsFile() string { return filepath.Join(dataDir, ".sixter-workspaces.json") }

func loadWorkspaces() {
	b, err := os.ReadFile(wsFile())
	if err != nil {
		return
	}
	var s wsState
	if json.Unmarshal(b, &s) != nil {
		return
	}
	wsMu.Lock()
	if s.Workspaces != nil {
		ws.Workspaces = s.Workspaces
	}
	if s.ProjectWS != nil {
		ws.ProjectWS = s.ProjectWS
	}
	if s.ProjMembers != nil {
		ws.ProjMembers = s.ProjMembers
	}
	if s.Tokens != nil {
		ws.Tokens = s.Tokens
	}
	wsMu.Unlock()
}

func saveWorkspacesLocked() {
	b, _ := json.MarshalIndent(ws, "", "  ")
	_ = atomicWriteFile(wsFile(), b, 0644)
}

func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func newEnrollToken() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// projectWorkspace returns the workspace id a project belongs to ("" if none).
func projectWorkspace(project string) string {
	wsMu.RLock()
	defer wsMu.RUnlock()
	return ws.ProjectWS[project]
}

// workspaceName resolves a workspace id to its display name. Returns "" for an
// unknown id, so callers never surface a raw "ws_…" handle in the UI.
func workspaceName(id string) string {
	if id == "" {
		return ""
	}
	wsMu.RLock()
	defer wsMu.RUnlock()
	if w, ok := ws.Workspaces[id]; ok && w != nil {
		return w.Name
	}
	return ""
}

/* ─────────── workspace handlers ─────────── */

// GET  /api/workspaces                → { workspaces, projectWs }
// POST /api/workspaces {name}         → create
func handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		wsMu.RLock()
		list := make([]*Workspace, 0, len(ws.Workspaces))
		for _, v := range ws.Workspaces {
			list = append(list, v)
		}
		projectWs := map[string]string{}
		for k, v := range ws.ProjectWS {
			projectWs[k] = v
		}
		wsMu.RUnlock()
		writeJSONResp(w, map[string]any{"workspaces": list, "projectWs": projectWs})

	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		wsMu.Lock()
		id := newID("ws_")
		ws.Workspaces[id] = &Workspace{ID: id, Name: name, Created: time.Now()}
		saveWorkspacesLocked()
		wsMu.Unlock()
		writeJSONResp(w, map[string]any{"ok": true, "id": id, "name": name})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST   /api/workspaces/assign {project, workspace}  → assign a project ("" = unassign)
// DELETE /api/workspaces/<id>                         → delete a workspace
func handleWorkspaceItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")

	if rest == "assign" && r.Method == http.MethodPost {
		var body struct {
			Project   string `json:"project"`
			Workspace string `json:"workspace"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !isSafeName(body.Project) {
			http.Error(w, "invalid project", http.StatusBadRequest)
			return
		}
		wsMu.Lock()
		if body.Workspace == "" {
			delete(ws.ProjectWS, body.Project)
		} else if _, ok := ws.Workspaces[body.Workspace]; ok {
			ws.ProjectWS[body.Project] = body.Workspace
		} else {
			wsMu.Unlock()
			http.Error(w, "unknown workspace", http.StatusBadRequest)
			return
		}
		saveWorkspacesLocked()
		wsMu.Unlock()
		writeJSONResp(w, map[string]any{"ok": true})
		return
	}

	if r.Method == http.MethodDelete && rest != "" {
		wsMu.Lock()
		delete(ws.Workspaces, rest)
		for p, id := range ws.ProjectWS {
			if id == rest {
				delete(ws.ProjectWS, p)
			}
		}
		saveWorkspacesLocked()
		wsMu.Unlock()
		writeJSONResp(w, map[string]any{"ok": true})
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

/* ─────────── per-project members ─────────── */

// GET    /api/project-members/<project>            → list members
// POST   /api/project-members/<project> {email,role} → add/update + issue enroll token
// DELETE /api/project-members/<project>?email=      → remove
func handleProjectMembers(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimPrefix(r.URL.Path, "/api/project-members/")
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		wsMu.RLock()
		members := append([]ProjMember(nil), ws.ProjMembers[project]...)
		wsMu.RUnlock()
		writeJSONResp(w, map[string]any{"members": members})

	case http.MethodPost:
		var body struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		body.Email = strings.ToLower(strings.TrimSpace(body.Email))
		if body.Email == "" {
			http.Error(w, "email required", http.StatusBadRequest)
			return
		}
		if body.Role == "" {
			body.Role = "developer"
		}
		if !validProjRole(body.Role) {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		wsMu.Lock()
		list := ws.ProjMembers[project]
		found := false
		for i := range list {
			if strings.EqualFold(list[i].Email, body.Email) {
				list[i].Role = body.Role
				found = true
			}
		}
		if !found {
			list = append(list, ProjMember{Email: body.Email, Role: body.Role, AddedAt: time.Now()})
		}
		ws.ProjMembers[project] = list
		// issue a fresh enrollment token for this member
		tok := newEnrollToken()
		ws.Tokens[tok] = enrollToken{Project: project, Email: body.Email, Role: body.Role, Created: time.Now()}
		saveWorkspacesLocked()
		wsMu.Unlock()
		writeJSONResp(w, map[string]any{"ok": true, "token": tok})

	case http.MethodDelete:
		email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
		wsMu.Lock()
		list := ws.ProjMembers[project]
		out := list[:0]
		for _, m := range list {
			if !strings.EqualFold(m.Email, email) {
				out = append(out, m)
			}
		}
		ws.ProjMembers[project] = out
		saveWorkspacesLocked()
		wsMu.Unlock()
		writeJSONResp(w, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

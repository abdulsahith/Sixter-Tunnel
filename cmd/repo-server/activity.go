package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/*
Phase 4 — team activity feed / audit log.

Every mutating action (deploy, rollback, env change, domain/track change, DB op,
IP ban, delete, upload) is appended with who did it, when, and the result. The
dashboard renders this as a team timeline; it doubles as a security audit trail.
Logging is automatic — the root middleware records any successful mutating /api
request, so handlers don't each need to remember to log.
*/

type Activity struct {
	Time    time.Time `json:"time"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Project string    `json:"project,omitempty"`
	Status  int       `json:"status"`
}

const activityMax = 500

var (
	activityMu sync.Mutex
	activity   []Activity // ring buffer, most-recent-last
)

func activityFile() string { return filepath.Join(dataDir, ".sixter-activity.json") }

func loadActivity() {
	b, err := os.ReadFile(activityFile())
	if err != nil {
		return
	}
	var a []Activity
	if json.Unmarshal(b, &a) == nil && a != nil {
		activityMu.Lock()
		activity = a
		activityMu.Unlock()
	}
}

// caller must hold activityMu
func saveActivityLocked() {
	b, _ := json.MarshalIndent(activity, "", "  ")
	_ = atomicWriteFile(activityFile(), b, 0644)
}

func logActivity(actor, action, project string, status int) {
	if actor == "" {
		actor = "system"
	}
	activityMu.Lock()
	activity = append(activity, Activity{Time: time.Now(), Actor: actor, Action: action, Project: project, Status: status})
	if len(activity) > activityMax {
		activity = activity[len(activity)-activityMax:]
	}
	saveActivityLocked()
	activityMu.Unlock()
}

func recentActivity(n int) []Activity {
	activityMu.Lock()
	defer activityMu.Unlock()
	if n <= 0 || n > len(activity) {
		n = len(activity)
	}
	out := make([]Activity, n)
	copy(out, activity[len(activity)-n:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // most-recent-first
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func handleActivity(w http.ResponseWriter, r *http.Request) {
	writeJSONResp(w, map[string]any{"events": recentActivity(120)})
}

// auditable decides whether a request should be recorded, and with what label.
// Returns ("", false) for reads and noisy/own-status endpoints.
func auditAction(r *http.Request) (string, bool) {
	if r.Method == http.MethodGet || r.Method == http.MethodOptions {
		return "", false
	}
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/whoami"),
		strings.HasPrefix(p, "/api/activity"),
		strings.HasPrefix(p, "/api/presence"):
		return "", false
	case strings.HasPrefix(p, "/api/deploy/"):
		return "Deployed", true
	case strings.HasPrefix(p, "/api/rollback/"):
		return "Rolled back", true
	case strings.HasPrefix(p, "/api/autodeploy/"):
		return "Changed auto-deploy", true
	case strings.HasPrefix(p, "/api/env/"):
		return "Updated environment", true
	case strings.HasPrefix(p, "/api/domain"):
		return "Updated domain", true
	case strings.HasPrefix(p, "/api/track/"):
		return "Changed project track", true
	case strings.HasPrefix(p, "/api/rename/"):
		return "Renamed project", true
	case strings.HasPrefix(p, "/api/databases"):
		return "Database change", true
	case strings.HasPrefix(p, "/api/security/") && strings.HasSuffix(p, "/ban"):
		return "Blocked an IP", true
	case strings.HasPrefix(p, "/api/projects/") && r.Method == http.MethodDelete:
		return "Deleted project", true
	}
	return "", false
}

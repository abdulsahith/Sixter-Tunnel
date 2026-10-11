package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

/*
Release commands — the "run this once against the database" phase.

A deploy has three phases, and Sixter only had two:

	build     compile / install dependencies      per machine
	release   migrate the database, seed data     ONCE per deploy
	run       serve traffic                       per machine

Without a release phase, developers put migrations in the app's startup path.
That is fine with one instance and broken with several: N replicas starting
together means N concurrent migrations on the same tables, and Postgres
deadlocks on concurrent DDL. The alternative — running them by hand over SSH —
isn't a deployment process at all, and quietly stops happening.

So the release command runs on the repo server, once, after the build and
before anything starts serving, with the project's production environment
loaded. If it fails the deploy stops: an app running against a schema that
didn't migrate produces 500s on every request instead of one clear error.
*/

var (
	relMu   sync.RWMutex
	relCmds = map[string]string{} // project → release command
)

func relFile() string { return filepath.Join(dataDir, ".sixter-release.json") }

func loadReleaseCmds() {
	b, err := os.ReadFile(relFile())
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(b, &m) == nil && m != nil {
		relMu.Lock()
		relCmds = m
		relMu.Unlock()
	}
}

func saveReleaseCmdsLocked() {
	b, _ := json.MarshalIndent(relCmds, "", "  ")
	_ = atomicWriteFile(relFile(), b, 0644)
}

func releaseCmdFor(project string) string {
	relMu.RLock()
	defer relMu.RUnlock()
	return strings.TrimSpace(relCmds[project])
}

func setReleaseCmd(project, cmd string) {
	relMu.Lock()
	if strings.TrimSpace(cmd) == "" {
		delete(relCmds, project)
	} else {
		relCmds[project] = strings.TrimSpace(cmd)
	}
	saveReleaseCmdsLocked()
	relMu.Unlock()
}

// suggestReleaseCmd proposes the migration command for a detected stack.
//
// Only offered when there's evidence the project actually has migrations —
// a suggestion that runs nothing is worse than none, because it looks like the
// step is covered when it isn't.
func suggestReleaseCmd(plan DeployPlan, dir string) string {
	switch plan.Kind {
	case KindGo:
		// A separate `package main` that calls AutoMigrate is the common Go
		// pattern; it never runs on deploy because `go build .` only builds
		// the root package.
		for _, d := range []string{"migrate", "migrations", "cmd/migrate"} {
			if exists(filepath.Join(dir, d)) {
				return "go run ./" + d
			}
		}

	case KindPython:
		if exists(filepath.Join(dir, "alembic.ini")) {
			return "alembic upgrade head"
		}
		if exists(filepath.Join(dir, "manage.py")) {
			return "python manage.py migrate --noinput"
		}

	case KindNode:
		pkg := readJSONFile(filepath.Join(dir, "package.json"))
		if scripts, ok := pkg["scripts"].(map[string]any); ok {
			for _, name := range []string{"migrate", "db:migrate"} {
				if _, has := scripts[name]; has {
					return "npm run " + name
				}
			}
		}
		if exists(filepath.Join(dir, "prisma", "schema.prisma")) {
			return "npx prisma migrate deploy"
		}
	}
	return ""
}

// GET/POST /api/release/<project>
//
//	GET  → the configured command plus a suggestion for this stack
//	POST → save it ({"command": "..."}; empty string disables the step)
func handleRelease(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	project := strings.TrimPrefix(r.URL.Path, "/api/release/")
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		plan := detectProject(filepath.Join(dataDir, project), projectPort(project))
		writeJSONResp(w, map[string]any{
			"project":   project,
			"command":   releaseCmdFor(project),
			"suggested": suggestReleaseCmd(plan, plan.WorkDir),
		})

	case http.MethodPost:
		var body struct {
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		setReleaseCmd(project, body.Command)
		writeJSONResp(w, map[string]any{"ok": true, "command": releaseCmdFor(project)})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

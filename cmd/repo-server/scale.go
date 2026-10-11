package main

import (
	"crypto/rand"
	"encoding/hex"
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
Scaling a repo-server project onto fleet nodes.

The problem: a node has no copy of your project. Rather than invent a
distribution channel, we reuse the tarball endpoint the CLI already pulls from
(`/api/pull`) and hand the node a **scoped source token** that grants read
access to exactly one project — never the owner token, never an enroll token.

Flow:

	repo dashboard  → POST /api/scale/<project>   (mint source token)
	                ← { sourceURL, sourceToken, image, command, port }
	dashboard       → POST https://sixter.in/api/fleet/apps  (with the above)
	gateway         → schedules onto a node
	node agent      → GET sourceURL (Bearer sourceToken) → extract → run

The token is per-project and revocable, so a compromised node can read one
project's code and nothing else.
*/

type sourceToken struct {
	Project string    `json:"project"`
	Created time.Time `json:"created"`
}

var (
	srcTokMu sync.RWMutex
	srcToks  = map[string]sourceToken{} // token → scope
)

func srcTokFile() string { return filepath.Join(dataDir, ".sixter-srctokens.json") }

func loadSourceTokens() {
	b, err := os.ReadFile(srcTokFile())
	if err != nil {
		return
	}
	var m map[string]sourceToken
	if json.Unmarshal(b, &m) == nil && m != nil {
		srcTokMu.Lock()
		srcToks = m
		srcTokMu.Unlock()
	}
}

func saveSourceTokensLocked() {
	b, _ := json.MarshalIndent(srcToks, "", "  ")
	_ = atomicWriteFile(srcTokFile(), b, 0600) // grants source read — keep private
}

// sourceTokenFor returns (creating if needed) the token that lets a node fetch
// this project's source. Stable per project so re-scaling doesn't invalidate
// running nodes.
func sourceTokenFor(project string) string {
	srcTokMu.RLock()
	for tok, s := range srcToks {
		if s.Project == project {
			srcTokMu.RUnlock()
			return tok
		}
	}
	srcTokMu.RUnlock()

	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := "src_" + hex.EncodeToString(b)

	srcTokMu.Lock()
	srcToks[tok] = sourceToken{Project: project, Created: time.Now()}
	saveSourceTokensLocked()
	srcTokMu.Unlock()
	return tok
}

// validSourceToken reports whether tok grants read access to project.
func validSourceToken(tok, project string) bool {
	tok = strings.TrimSpace(strings.TrimPrefix(tok, "Bearer "))
	if tok == "" {
		return false
	}
	srcTokMu.RLock()
	defer srcTokMu.RUnlock()
	s, ok := srcToks[tok]
	return ok && s.Project == project
}

// revokeSourceTokens drops every token for a project (used when scaling off).
func revokeSourceTokens(project string) {
	srcTokMu.Lock()
	for tok, s := range srcToks {
		if s.Project == project {
			delete(srcToks, tok)
		}
	}
	saveSourceTokensLocked()
	srcTokMu.Unlock()
}

// GET/POST /api/scale/<project>
//
//	GET  → what the dashboard needs to schedule this project on the fleet
//	POST → same, but mints the token (idempotent) and marks it scalable
func handleScale(w http.ResponseWriter, r *http.Request) {
	// This endpoint mints a token that grants read access to project source —
	// it must never answer an unauthenticated caller.
	if !requireAuth(w, r) {
		return
	}
	project := strings.TrimPrefix(r.URL.Path, "/api/scale/")
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodDelete {
		revokeSourceTokens(project)
		writeJSONResp(w, map[string]any{"ok": true, "revoked": true})
		return
	}

	projPath := filepath.Join(dataDir, project)
	plan := detectProject(projPath, projectPort(project))
	cfg := ammConfigFor(project)

	// The node needs a runtime. If the project has no image set we suggest one
	// from the detected stack rather than failing — a Go project can't run on
	// a filesystem that has no Go. The suggestion is derived from the project's
	// OWN declared version (go.mod, .python-version, engines.node), because an
	// image whose toolchain is too old fails at build time with a message most
	// developers shouldn't have to decode.
	image := cfg.Image
	suggested := recommendImage(plan, plan.WorkDir)
	if image == "" {
		image = suggested
	}
	rec := recommendResources(plan, plan.WorkDir, image)

	// A replica needs the SAME production config as this server, or it starts
	// with no database URL and no API keys and fails on its first real request.
	// Only returned to an authenticated caller (checked above).
	projEnv := loadEnv(project)

	// Loopback addresses are correct HERE and meaningless on another machine.
	// Rewrite them to an address a LAN peer could use, and report exactly what
	// changed — a silent rewrite would be its own debugging nightmare.
	fleetEnv, rewritten, dbHost := rewriteForFleet(projEnv)

	// The endpoints the replica must reach. The agent probes these before
	// starting anything, so an unreachable database is reported as a clear
	// failure instead of an app that starts and 500s on every request.
	endpoints := dbEndpoints(fleetEnv)
	sort.Strings(rewritten)

	tok := ""
	if r.Method == http.MethodPost {
		tok = sourceTokenFor(project)
	} else {
		srcTokMu.RLock()
		for t, s := range srcToks {
			if s.Project == project {
				tok = t
				break
			}
		}
		srcTokMu.RUnlock()
	}

	writeJSONResp(w, map[string]any{
		"project":        project,
		"sourceURL":      "https://" + serverName + ".sixter.in/api/pull?project=" + project,
		"sourceToken":    tok,
		"image":          image,
		"suggestedImage": suggested,
		"command":        plan.RunCmd,
		// The command that actually works inside the image: venv replaced with
		// the image's python, Go rebuilt from source, node_modules reinstalled.
		"buildRunCmd": containerCommand(plan, plan.WorkDir, image),
		"port":        plan.Port,
		"kind":        string(plan.Kind),
		"needsProc":   plan.NeedsProc,
		"memoryMB":    cfg.MemoryMB,
		"cpuPercent":  cfg.CPUPercent,

		// What this project needs to build AND run, so the developer isn't
		// guessing a limit that silently OOM-kills their deploy.
		"recommendedMemoryMB":   rec.MemoryMB,
		"recommendedCPUPercent": rec.CPUPercent,
		"sizingWhy":             rec.Why,

		// Config for the replica, with loopback rewritten to a routable address.
		"env":           fleetEnv,
		"rewrittenVars": rewritten,
		"dbHost":        dbHost,
		"probe":         endpoints,
		// Set when scaling cannot work at all from this machine's network
		// position, so the UI can say so before a deploy is attempted.
		"fleetBlocker": fleetBlocker(projEnv),
	})
}

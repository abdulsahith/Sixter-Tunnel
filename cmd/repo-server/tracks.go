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
Phase 3 — project tracks.

Each project carries a track: "frontend", "backend" or "shared" (default).
Combined with a member's role this scopes who can see + work on a project:
  - frontend member → frontend + shared projects
  - backend member  → backend + shared projects
  - lead / viewer    → everything

Tracks are owner/lead-set and persisted next to the projects.
*/

var (
	tracksMu sync.RWMutex
	tracks   = map[string]string{} // project → track
)

func tracksFile() string { return filepath.Join(dataDir, ".sixter-tracks.json") }

func loadTracks() {
	b, err := os.ReadFile(tracksFile())
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(b, &m) == nil && m != nil {
		tracksMu.Lock()
		tracks = m
		tracksMu.Unlock()
	}
}

func saveTracks() {
	tracksMu.RLock()
	b, _ := json.MarshalIndent(tracks, "", "  ")
	tracksMu.RUnlock()
	_ = atomicWriteFile(tracksFile(), b, 0644)
}

func validTrack(t string) bool {
	return t == "frontend" || t == "backend" || t == "shared"
}

// projectTrack returns a project's track ("shared" if unset).
func projectTrack(name string) string {
	tracksMu.RLock()
	t := tracks[name]
	tracksMu.RUnlock()
	if t == "" {
		return "shared"
	}
	return t
}

func setProjectTrack(name, track string) {
	tracksMu.Lock()
	tracks[name] = track
	tracksMu.Unlock()
	saveTracks()
}

// POST /api/track/<project> {track} — set a project's track (lead-only; the
// rbac gate enforces the "track" capability before this runs).
func handleTrack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/track/")
	if !isSafeName(name) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	var body struct {
		Track string `json:"track"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !validTrack(body.Track) {
		http.Error(w, "track must be frontend, backend or shared", http.StatusBadRequest)
		return
	}
	setProjectTrack(name, body.Track)
	writeJSONResp(w, map[string]any{"ok": true, "project": name, "track": body.Track})
}

// roleSeesTrack reports whether a role may see/act on a project of this track.
// Lead and viewer see everything; frontend/backend are scoped to their track
// (plus "shared"). Unknown roles see nothing.
func roleSeesTrack(role, track string) bool {
	switch role {
	case "lead", "viewer":
		return true
	case "backend":
		return track == "backend" || track == "shared"
	case "frontend":
		return track == "frontend" || track == "shared"
	}
	return false
}

package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

/*
Phase 5 — presence (poll-based).

Each open dashboard heartbeats POST /api/presence every ~15s with the project
it's viewing. We keep the last-seen time per member and report everyone seen in
the last 35s as "online", with which project they're on. Poll-based (not a
WebSocket) keeps it simple and robust behind the tunnel.
*/

type presenceEntry struct {
	Actor   string
	Project string
	Role    string
	Last    time.Time
}

var (
	presenceMu sync.Mutex
	presence   = map[string]presenceEntry{}
)

func handlePresence(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	actor, role := "owner", "lead"
	if p != nil {
		role = p.Role
		if !p.IsOwner {
			actor = p.Email
		}
	}
	if r.Method == http.MethodPost {
		var body struct {
			Project string `json:"project"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		presenceMu.Lock()
		presence[actor] = presenceEntry{Actor: actor, Project: body.Project, Role: role, Last: time.Now()}
		presenceMu.Unlock()
	}
	writeJSONResp(w, map[string]any{"online": onlineMembers()})
}

func onlineMembers() []map[string]any {
	cutoff := time.Now().Add(-35 * time.Second)
	presenceMu.Lock()
	defer presenceMu.Unlock()
	out := []map[string]any{}
	for k, e := range presence {
		if e.Last.Before(cutoff) {
			delete(presence, k)
			continue
		}
		out = append(out, map[string]any{"actor": e.Actor, "project": e.Project, "role": e.Role})
	}
	return out
}

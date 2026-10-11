package main

// Project rename. Renames the project's data folder and migrates ALL of its
// state (domain config, env vars, version snapshots, pipeline history/toggle),
// removes the old systemd service, then redeploys under the new name so the
// running service (sixter-<name>) matches. The nginx config is keyed by domain,
// so the public URL is unaffected.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func renameProject(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if !isSafeName(newName) {
		return errors.New("invalid name: use letters, digits, dot, dash, underscore")
	}
	if newName == oldName {
		return nil
	}
	oldPath := filepath.Join(dataDir, oldName)
	newPath := filepath.Join(dataDir, newName)
	if _, err := os.Stat(oldPath); err != nil {
		return errors.New("project not found")
	}
	if _, err := os.Stat(newPath); err == nil {
		return errors.New("a project with that name already exists")
	}

	// stop + remove the old service first (its unit name embeds the old project)
	removeService(oldName)

	// rename the data directory
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	// migrate domain config
	domainsMu.Lock()
	if d, ok := domains[oldName]; ok {
		domains[newName] = d
		delete(domains, oldName)
	}
	domainsMu.Unlock()
	saveDomains()

	// migrate env file
	if _, err := os.Stat(envFilePath(oldName)); err == nil {
		_ = os.MkdirAll(envDir(), 0700)
		_ = os.Rename(envFilePath(oldName), envFilePath(newName))
	}

	// migrate version snapshots
	if _, err := os.Stat(versionsDir(oldName)); err == nil {
		_ = os.Rename(versionsDir(oldName), versionsDir(newName))
	}

	// migrate pipeline history + auto-deploy toggle
	pipeMu.Lock()
	if h, ok := pipeHistory[oldName]; ok {
		pipeHistory[newName] = h
		delete(pipeHistory, oldName)
	}
	if a, ok := pipeAuto[oldName]; ok {
		pipeAuto[newName] = a
		pipeAutoSeen[newName] = true
		delete(pipeAuto, oldName)
		delete(pipeAutoSeen, oldName)
	}
	delete(pipeRuns, oldName)
	pipeMu.Unlock()
	savePipeline()

	// redeploy under the new name (creates sixter-<newName>, nginx unchanged)
	go runPipeline(newName, "rename", "system")
	return nil
}

// POST /api/rename/<project> {newName}
func handleRename(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	old := strings.TrimPrefix(r.URL.Path, "/api/rename/")
	if !isSafeName(old) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	var req struct {
		NewName string `json:"newName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := renameProject(old, req.NewName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONResp(w, map[string]any{"ok": true, "name": strings.TrimSpace(req.NewName)})
}

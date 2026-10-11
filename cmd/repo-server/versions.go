package main

// Project version control. Every upload archives the previous project state as
// a tar.gz snapshot (keeping the most recent maxVersions). A developer can list
// versions and roll back: we snapshot the current state first (so they can roll
// forward again), restore the chosen version, and redeploy it live.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxVersions = 5

var versionIDRe = regexp.MustCompile(`^\d{8}-\d{6}$`)

func versionsDir(project string) string { return filepath.Join(installDir, "versions", project) }

// VersionInfo describes one saved snapshot for the dashboard.
type VersionInfo struct {
	ID        string `json:"id"`        // timestamp id (also the filename stem)
	Time      string `json:"time"`      // RFC3339 for display
	SizeBytes int64  `json:"sizeBytes"`
	SizeHuman string `json:"sizeHuman"`
}

// snapshotProject archives the current project dir; no-op if it doesn't exist.
func snapshotProject(project string) error {
	src := filepath.Join(dataDir, project)
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return nil
	}
	if err := os.MkdirAll(versionsDir(project), 0755); err != nil {
		return err
	}
	id := time.Now().Format("20060102-150405")
	out := filepath.Join(versionsDir(project), id+".tar.gz")
	if err := tarGzDir(src, out); err != nil {
		_ = os.Remove(out)
		return err
	}
	pruneVersions(project)
	return nil
}

// tarGzDir writes a gzip-compressed tar of everything under src to outPath.
func tarGzDir(src, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// only directories and regular files (skip symlinks/devices for safety)
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			if _, err := io.Copy(tw, in); err != nil {
				return err
			}
		}
		return nil
	})
}

// listVersions returns snapshots newest-first.
func listVersions(project string) []VersionInfo {
	out := []VersionInfo{}
	entries, err := os.ReadDir(versionsDir(project))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".tar.gz")
		if !versionIDRe.MatchString(id) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		t, _ := time.Parse("20060102-150405", id)
		out = append(out, VersionInfo{
			ID:        id,
			Time:      t.Format(time.RFC3339),
			SizeBytes: info.Size(),
			SizeHuman: formatSize(info.Size()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// pruneVersions keeps only the newest maxVersions snapshots.
func pruneVersions(project string) {
	vs := listVersions(project)
	for i := maxVersions; i < len(vs); i++ {
		_ = os.Remove(filepath.Join(versionsDir(project), vs[i].ID+".tar.gz"))
	}
}

// rollbackProject restores a snapshot and redeploys it. Returns a log.
func rollbackProject(project, versionID string) (string, error) {
	if !versionIDRe.MatchString(versionID) {
		return "", os.ErrInvalid
	}
	archive := filepath.Join(versionsDir(project), versionID+".tar.gz")
	if _, err := os.Stat(archive); err != nil {
		return "", err
	}

	var log strings.Builder
	logf := func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) }

	// 1. snapshot the CURRENT state first, so a rollback is itself reversible
	logf("Saving current state before rollback…")
	if err := snapshotProject(project); err != nil {
		logf("  (warning: could not snapshot current state: %v)", err)
	}

	// 2. restore the chosen version
	projPath := filepath.Join(dataDir, project)
	_ = os.RemoveAll(projPath)
	if err := os.MkdirAll(projPath, 0755); err != nil {
		return log.String(), err
	}
	in, err := os.Open(archive)
	if err != nil {
		return log.String(), err
	}
	defer in.Close()
	files, bytes, err := untarGzSafeWithStats(in, projPath)
	if err != nil {
		return log.String(), err
	}
	_ = writeProjectMeta(projPath, ProjectMeta{Name: project, Files: files, Bytes: bytes})
	logf("✓ restored version %s (%d files, %s)", versionID, files, formatSize(bytes))

	// 3. redeploy it live, reusing the saved domain + port
	domainsMu.Lock()
	cfg := domains[project]
	domainsMu.Unlock()
	domain, port := "", 3000
	if cfg != nil {
		domain = cfg.Domain
		if cfg.Port > 0 {
			port = cfg.Port
		}
	}
	plan := detectProject(projPath, port)
	logf("Redeploying as: %s", plan.Title)
	dlog, derr := deployProject(project, plan, domain)
	logf("%s", strings.TrimSpace(dlog))
	if derr != nil {
		return log.String(), derr
	}
	// the redeploy rewrote the HTTP nginx conf — restore HTTPS if it had it
	if cfg != nil && cfg.HTTPS && cfg.Domain != "" {
		logf("Re-applying HTTPS for %s…", cfg.Domain)
		reapplyCertbot(cfg)
	}
	logf("✓ rollback complete — version %s is now live", versionID)
	return log.String(), nil
}

/* ─────────── API ─────────── */

// GET /api/versions/<project> → list snapshots
func handleVersions(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/versions/")
	if !isSafeName(name) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	writeJSONResp(w, map[string]any{"versions": listVersions(name)})
}

// POST /api/rollback/<project> {version} → restore + redeploy
func handleRollback(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/rollback/")
	if !isSafeName(name) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	logOut, err := rollbackProject(name, req.Version)
	resp := map[string]any{"log": logOut, "ok": err == nil}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSONResp(w, resp)
}

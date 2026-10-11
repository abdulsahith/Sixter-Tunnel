package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

/*
Binary distribution for the node installer.

	GET /dl/linux-amd64/sixter-agent

install.sh fetches the agent (and its companions) from here, so enrolling a
machine needs nothing but curl. Binaries are served from the static overlay
directory, which is how the rest of the installers (.deb, .exe) already ship —
drop new builds in and they're live, no rebuild of the gateway required.

Layout on the server:

	<static>/dl/linux-amd64/sixter-agent
	<static>/dl/linux-arm64/sixter-agent
*/

// allowedBinaries is an explicit allowlist. The path is user-supplied, so
// serving an arbitrary filename from disk would be a file-read primitive.
var allowedBinaries = map[string]bool{
	"sixter":            true,
	"sixter-agent":      true,
	"sixter-client":     true,
	"sixter-repo":       true,
	"sixter-reposerver": true,
	"sixter-server":     true,
}

var allowedPlatforms = map[string]bool{
	"linux-amd64": true,
	"linux-arm64": true,
}

func handleBinaryDownload(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/dl/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	platform, name := parts[0], parts[1]

	// Allowlist both segments — never join user input into a path directly.
	if !allowedPlatforms[platform] || !allowedBinaries[name] {
		http.NotFound(w, r)
		return
	}
	if staticOverlay == "" {
		http.Error(w, "binary distribution not configured on this server", http.StatusNotFound)
		return
	}

	path := filepath.Join(staticOverlay, "dl", platform, name)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "build not available for "+platform+" yet", http.StatusNotFound)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

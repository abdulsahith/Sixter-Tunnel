package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sixter.in/tunnel/internal/prompt"
)

func main() {
	token := flag.String("token", "", "Secret upload token (omit to be prompted securely)")
	project := flag.String("project", "", "Project name (optional)")
	server := flag.String("server", "", "Your repo subdomain (e.g. sahith-repo)")
	flag.Parse()

	if *server == "" {
		fmt.Fprintln(os.Stderr, "Usage: sixter repo -server <name> [-project <name>]")
		os.Exit(1)
	}

	serverURL := fmt.Sprintf("https://%s.sixter.in/upload", *server)

	// token resolution: flag → SIXTER_TOKEN env → secure hidden prompt
	if *token == "" {
		*token = os.Getenv("SIXTER_TOKEN")
	}
	if *token == "" {
		t, err := prompt.Secret("Token: ")
		if err != nil || t == "" {
			fmt.Fprintln(os.Stderr, "a token is required")
			os.Exit(1)
		}
		*token = t
	}

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	fmt.Println("Uploading folder:", cwd)
	fmt.Println("To server:", serverURL)

	// Create tar.gz in memory first
	var buf bytes.Buffer
	if err := writeTarGzExcluding(&buf, cwd); err != nil {
		panic(err)
	}

	size := buf.Len()
	fmt.Printf("Archive size: %.2f MB\n", float64(size)/(1024*1024))

	// Build request
	reqURL := serverURL
	if *project != "" {
		reqURL += "?project=" + *project
	}

	req, err := http.NewRequest("POST", reqURL, &buf)
	if err != nil {
		panic(err)
	}
	req.Header.Set("X-Token", *token)
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("User-Agent", "Sixter-CLI/1.0")

	fmt.Println("Uploading...")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println("Status:", resp.Status)
	fmt.Println(string(body))

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		fmt.Println("\n✓ Upload successful!")
	}
}

var skipNames = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".cache":       true,
	".idea":        true,
	".vscode":      true,
	"__pycache__":  true,
	"vendor":       true,
	"target":       true,
}

func shouldSkip(relUnix string) bool {
	parts := strings.Split(relUnix, "/")
	for _, p := range parts {
		if skipNames[p] {
			return true
		}
		// Skip hidden files/folders (starting with .)
		if strings.HasPrefix(p, ".") && p != "." {
			return true
		}
	}
	// Skip common patterns
	if strings.HasSuffix(relUnix, ".log") ||
		strings.HasSuffix(relUnix, ".tmp") ||
		strings.HasSuffix(relUnix, "~") {
		return true
	}
	return false
}

func writeTarGzExcluding(w io.Writer, root string) error {
	gzw := gzip.NewWriter(w)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	root = filepath.Clean(root)
	parent := filepath.Dir(root)

	fileCount := 0
	totalSize := int64(0)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(parent, path)
		if err != nil {
			return err
		}
		relUnix := filepath.ToSlash(rel)

		if relUnix != "." && shouldSkip(relUnix) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = relUnix

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			n, err := io.Copy(tw, f)
			if err != nil {
				return err
			}
			fileCount++
			totalSize += n
		}
		return nil
	})

	fmt.Printf("Packed %d files (%.2f MB)\n", fileCount, float64(totalSize)/(1024*1024))
	return err
}
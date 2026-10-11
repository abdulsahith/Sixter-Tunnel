package amm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/*
OCI image support — the piece that makes a container portable.

Until now a container's root filesystem was assembled from the HOST's /bin,
/usr and /lib. That works on one machine and is unportable by construction:
there is no artifact to send anywhere, and a different distro gives a different
runtime. Multi-node scheduling is impossible without this.

We speak the standard OCI/Docker registry protocol rather than inventing a
format, because that buys every existing registry (Docker Hub, ECR, GCR,
Harbor) and every existing base image (node:20-slim, python:3.12-slim) for the
cost of an HTTP client. Inventing an image format would be the "rebuilding
Docker, badly" trap.

Layout on disk:

	<cache>/blobs/<digest>          downloaded layer tarballs (shared)
	<cache>/rootfs/<image-digest>/  extracted, flattened rootfs (shared, ro)

Layers are flattened into one rootfs per image rather than stacked as separate
overlay lowerdirs. Slightly less dedup, dramatically simpler whiteout handling,
and the extraction happens once per image regardless of how many containers
use it.
*/

const dockerHub = "registry-1.docker.io"

type imageRef struct {
	registry string // registry-1.docker.io
	repo     string // library/node
	tag      string // 20-slim
	digest   string // sha256:… (if the ref was pinned)
}

// ParseImageRef understands the usual shorthands:
//
//	node:20-slim              → registry-1.docker.io/library/node:20-slim
//	sixter/api:v3             → registry-1.docker.io/sixter/api:v3
//	ghcr.io/org/app:latest    → ghcr.io/org/app:latest
func ParseImageRef(ref string) imageRef {
	out := imageRef{registry: dockerHub, tag: "latest"}
	s := strings.TrimSpace(ref)

	// pinned digest
	if i := strings.Index(s, "@"); i >= 0 {
		out.digest = s[i+1:]
		s = s[:i]
	}

	// A leading segment is a registry only if it looks like a host (has a dot
	// or a colon, or is localhost) — otherwise "sixter/api" would be misread.
	parts := strings.Split(s, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		out.registry = parts[0]
		parts = parts[1:]
	}

	last := parts[len(parts)-1]
	if i := strings.LastIndex(last, ":"); i >= 0 {
		out.tag = last[i+1:]
		parts[len(parts)-1] = last[:i]
	}

	out.repo = strings.Join(parts, "/")
	// Docker Hub official images live under library/
	if out.registry == dockerHub && !strings.Contains(out.repo, "/") {
		out.repo = "library/" + out.repo
	}
	return out
}

func (r imageRef) String() string {
	if r.digest != "" {
		return r.registry + "/" + r.repo + "@" + r.digest
	}
	return r.registry + "/" + r.repo + ":" + r.tag
}

// cacheKey is a filesystem-safe identifier for the extracted rootfs.
func (r imageRef) cacheKey() string {
	k := r.repo + "_" + r.tag
	if r.digest != "" {
		k = r.repo + "_" + strings.TrimPrefix(r.digest, "sha256:")[:16]
	}
	return strings.NewReplacer("/", "_", ":", "_").Replace(k)
}

/* ─────────── puller ─────────── */

// ImageStore pulls and caches OCI images.
type ImageStore struct {
	Dir  string // cache root
	HTTP *http.Client

	mu      sync.Mutex
	pulling map[string]*sync.WaitGroup // dedupes concurrent pulls of one image
}

func NewImageStore(dir string) *ImageStore {
	return &ImageStore{
		Dir:     dir,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
		pulling: map[string]*sync.WaitGroup{},
	}
}

func (s *ImageStore) rootfsDir(r imageRef) string {
	return filepath.Join(s.Dir, "rootfs", r.cacheKey())
}

// Pull fetches an image if it isn't cached and returns the extracted rootfs
// path, ready to be used as an overlay lowerdir.
//
// Concurrent pulls of the same image are collapsed into one download — with N
// containers starting at once you'd otherwise fetch the same 200 MB N times.
func (s *ImageStore) Pull(ctx context.Context, ref string) (string, error) {
	r := ParseImageRef(ref)
	dest := s.rootfsDir(r)

	// already extracted? (marker file, so a half-finished extract isn't reused)
	if _, err := os.Stat(filepath.Join(dest, ".amm-complete")); err == nil {
		// Repair images cached before env support: without the config's Env
		// the image's own PATH is lost and its binaries aren't findable.
		if _, err := os.Stat(filepath.Join(dest, imageEnvFile)); err != nil {
			if token, aerr := s.authToken(ctx, r); aerr == nil {
				if m, merr := s.fetchManifest(ctx, r, token); merr == nil {
					s.saveConfigEnv(ctx, r, token, m, dest)
				}
			}
		}
		return dest, nil
	}

	s.mu.Lock()
	if wg, busy := s.pulling[r.cacheKey()]; busy {
		s.mu.Unlock()
		wg.Wait() // someone else is fetching it
		if _, err := os.Stat(filepath.Join(dest, ".amm-complete")); err == nil {
			return dest, nil
		}
		return "", fmt.Errorf("amm: concurrent pull of %s failed", ref)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	s.pulling[r.cacheKey()] = wg
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pulling, r.cacheKey())
		s.mu.Unlock()
		wg.Done()
	}()

	if err := s.pull(ctx, r, dest); err != nil {
		_ = os.RemoveAll(dest) // never leave a partial rootfs behind
		return "", err
	}
	return dest, nil
}

func (s *ImageStore) pull(ctx context.Context, r imageRef, dest string) error {
	token, err := s.authToken(ctx, r)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	manifest, err := s.fetchManifest(ctx, r, token)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if len(manifest.Layers) == 0 {
		return fmt.Errorf("image %s has no layers", r)
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	// Layers are ordered base → top and must be applied in that order.
	for i, layer := range manifest.Layers {
		blob, err := s.fetchBlob(ctx, r, token, layer.Digest)
		if err != nil {
			return fmt.Errorf("layer %d/%d: %w", i+1, len(manifest.Layers), err)
		}
		err = extractLayer(blob, dest)
		blob.Close()
		if err != nil {
			return fmt.Errorf("extract layer %d/%d: %w", i+1, len(manifest.Layers), err)
		}
	}

	// The config blob carries the image's ENVIRONMENT — PATH, GOPATH, and
	// friends. Dropping it means "go" isn't on PATH in a golang image and
	// "node" may not be in a node image: the app dies instantly with
	// "not found" and the container looks broken for no visible reason.
	// Best-effort: an image without a usable config still runs with defaults.
	s.saveConfigEnv(ctx, r, token, manifest, dest)

	return os.WriteFile(filepath.Join(dest, ".amm-complete"), []byte(r.String()), 0644)
}

// imageEnvFile stores the image config's Env as JSON next to the rootfs.
const imageEnvFile = ".amm-env"

// saveConfigEnv fetches the OCI config blob and persists its Env list.
func (s *ImageStore) saveConfigEnv(ctx context.Context, r imageRef, token string, m *ociManifest, dest string) {
	if m.Config.Digest == "" {
		return
	}
	blob, err := s.fetchBlob(ctx, r, token, m.Config.Digest)
	if err != nil {
		return
	}
	defer blob.Close()

	var cfg struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"config"`
	}
	if json.NewDecoder(io.LimitReader(blob, 1<<20)).Decode(&cfg) != nil || len(cfg.Config.Env) == 0 {
		return
	}
	if b, err := json.Marshal(cfg.Config.Env); err == nil {
		_ = os.WriteFile(filepath.Join(dest, imageEnvFile), b, 0644)
	}
}

// ReadImageEnv returns the environment an image declares for itself, or nil.
// Missing file is normal: host-derived rootfs, or an image cached before env
// support existed.
func ReadImageEnv(imageRoot string) []string {
	if imageRoot == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(imageRoot, imageEnvFile))
	if err != nil {
		return nil
	}
	var env []string
	if json.Unmarshal(b, &env) != nil {
		return nil
	}
	return env
}

/* ─────────── registry protocol ─────────── */

// authToken performs the registry's anonymous token dance. A 401 carries a
// WWW-Authenticate header telling us where to get a token and for what scope;
// we follow it. This is what makes public images work with no credentials.
func (s *ImageStore) authToken(ctx context.Context, r imageRef) (string, error) {
	probe := fmt.Sprintf("https://%s/v2/", r.registry)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusOK {
		return "", nil // registry allows anonymous access outright
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return "", fmt.Errorf("registry %s returned %d", r.registry, resp.StatusCode)
	}

	realm, service := parseAuthHeader(resp.Header.Get("WWW-Authenticate"))
	if realm == "" {
		return "", nil
	}
	u := fmt.Sprintf("%s?scope=repository:%s:pull", realm, r.repo)
	if service != "" {
		u += "&service=" + service
	}
	treq, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	tresp, err := s.HTTP.Do(treq)
	if err != nil {
		return "", err
	}
	defer tresp.Body.Close()
	if tresp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", tresp.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tresp.Body, 1<<20)).Decode(&tok); err != nil {
		return "", err
	}
	if tok.Token != "" {
		return tok.Token, nil
	}
	return tok.AccessToken, nil
}

// parseAuthHeader pulls realm/service out of: Bearer realm="…",service="…"
func parseAuthHeader(h string) (realm, service string) {
	h = strings.TrimPrefix(h, "Bearer ")
	for _, part := range strings.Split(h, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		v := strings.Trim(kv[1], `"`)
		switch kv[0] {
		case "realm":
			realm = v
		case "service":
			service = v
		}
	}
	return
}

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Config        ociDescriptor   `json:"config"`
	Layers        []ociDescriptor `json:"layers"`
	Manifests     []ociDescriptor `json:"manifests"` // present on a manifest LIST
}

// acceptManifests advertises both OCI and legacy Docker media types, plus the
// multi-arch index types — otherwise registries hand back a schema1 blob.
const acceptManifests = "application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json"

func (s *ImageStore) fetchManifest(ctx context.Context, r imageRef, token string) (*ociManifest, error) {
	reference := r.tag
	if r.digest != "" {
		reference = r.digest
	}
	m, err := s.getManifest(ctx, r, token, reference)
	if err != nil {
		return nil, err
	}

	// A manifest list is multi-arch: resolve to the linux/amd64 entry.
	if len(m.Manifests) > 0 && len(m.Layers) == 0 {
		want := ""
		for _, d := range m.Manifests {
			if d.Platform.OS == "linux" && d.Platform.Architecture == goArch() {
				want = d.Digest
				break
			}
		}
		if want == "" {
			return nil, fmt.Errorf("image has no linux/%s variant", goArch())
		}
		return s.getManifest(ctx, r, token, want)
	}
	return m, nil
}

func (s *ImageStore) getManifest(ctx context.Context, r imageRef, token, reference string) (*ociManifest, error) {
	u := fmt.Sprintf("https://%s/v2/%s/manifests/%s", r.registry, r.repo, reference)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", acceptManifests)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("GET manifest %s: %d %s", reference, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var m ociManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// fetchBlob streams a layer, using the on-disk cache when present. Blobs are
// content-addressed, so a cached digest is always valid.
func (s *ImageStore) fetchBlob(ctx context.Context, r imageRef, token, digest string) (io.ReadCloser, error) {
	cached := filepath.Join(s.Dir, "blobs", strings.ReplaceAll(digest, ":", "_"))
	if f, err := os.Open(cached); err == nil {
		return f, nil
	}

	u := fmt.Sprintf("https://%s/v2/%s/blobs/%s", r.registry, r.repo, digest)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET blob: %d", resp.StatusCode)
	}

	// Tee to the cache so the next container reuses it.
	if err := os.MkdirAll(filepath.Dir(cached), 0755); err == nil {
		tmp := cached + ".part"
		if f, err := os.Create(tmp); err == nil {
			if _, err := io.Copy(f, resp.Body); err == nil {
				f.Close()
				resp.Body.Close()
				_ = os.Rename(tmp, cached)
				return os.Open(cached)
			}
			f.Close()
			_ = os.Remove(tmp)
		}
	}
	return resp.Body, nil
}

/* ─────────── extraction ─────────── */

// extractLayer applies one gzip'd tar layer onto dest, honouring OCI whiteouts.
//
// Whiteouts mark deletions from lower layers:
//
//	.wh.<name>        → delete <name>
//	.wh..wh..opq      → this directory's lower contents are hidden
//
// We flatten as we go, so these become real deletions rather than overlayfs
// metadata. Path traversal (../) is rejected — a malicious image must not be
// able to write outside the rootfs.
func extractLayer(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		// some layers are uncompressed tar
		if seeker, ok := r.(io.ReadSeeker); ok {
			if _, serr := seeker.Seek(0, io.SeekStart); serr == nil {
				return untarTo(seeker, dest)
			}
		}
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	return untarTo(gz, dest)
}

// ExtractTarGz unpacks a gzip'd tar into dest, with the same path-traversal
// protection the image extractor uses. Exported for the node agent, which
// fetches project source as a tarball from the repo server.
func ExtractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	return untarTo(gz, dest)
}

func untarTo(r io.Reader, dest string) error {
	absDest, _ := filepath.Abs(dest)
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue // refuse to escape
		}
		target := filepath.Join(dest, name)
		if abs, _ := filepath.Abs(target); !strings.HasPrefix(abs, absDest) {
			continue
		}

		base := filepath.Base(name)
		// opaque directory: drop everything below it from lower layers
		if base == ".wh..wh..opq" {
			_ = os.RemoveAll(filepath.Dir(target))
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			continue
		}
		// file whiteout: delete the named entry
		if strings.HasPrefix(base, ".wh.") {
			_ = os.RemoveAll(filepath.Join(filepath.Dir(target), strings.TrimPrefix(base, ".wh.")))
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)|0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			// A later layer replacing a file must overwrite, not append.
			_ = os.RemoveAll(target)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)|0600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			_ = os.RemoveAll(target)
			_ = os.Symlink(hdr.Linkname, target)
		case tar.TypeLink:
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			_ = os.RemoveAll(target)
			if err := os.Link(filepath.Join(dest, filepath.Clean(hdr.Linkname)), target); err != nil {
				// hard link across layers can fail; fall back to a copy
				if src, oerr := os.Open(filepath.Join(dest, filepath.Clean(hdr.Linkname))); oerr == nil {
					if dst, cerr := os.Create(target); cerr == nil {
						_, _ = io.Copy(dst, src)
						dst.Close()
					}
					src.Close()
				}
			}
		}
	}
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

/*
Portability — making a deploy plan that works on THIS host also work inside an
image, on any machine.

Every AMM/scaling failure so far had the same shape: the deploy plan is written
for the host, then handed to a container whose filesystem is a base image where
the plan's assumptions are false.

	Go      "./app"                  the binary was built HERE; a node gets source
	Go      golang:1.24              go.mod said 1.25.5; the image pins GOTOOLCHAIN=local
	Python  ".venv/bin/python …"     a venv symlinks the interpreter that made it,
	                                 so in another rootfs it's a dangling link
	Node    "npm start"              node_modules were installed for the host's ABI

So a plan has two forms, and the difference is not cosmetic:

	host form       runs against the machine's own toolchain and artifacts
	container form  recreates everything from the image + the source tree

This file derives the container form, the image that can actually run it, and
the resources it needs — instead of asking the developer to know all three.
*/

/* ─────────── image selection ─────────── */

// goVersionRe matches the "go 1.25.5" directive in go.mod.
var goVersionRe = regexp.MustCompile(`(?m)^go\s+(\d+)\.(\d+)`)

// goImageFor returns the golang image tag satisfying the project's go.mod.
//
// The official golang images set GOTOOLCHAIN=local, which means the image's Go
// will NOT auto-download a newer toolchain — it fails with "go.mod requires go
// >= X". So the image tag has to be chosen from go.mod, not guessed.
func goImageFor(dir string) string {
	const fallback = "golang:1.25"
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return fallback
	}
	m := goVersionRe.FindStringSubmatch(string(b))
	if len(m) < 3 {
		return fallback
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil || major != 1 {
		return fallback
	}
	// Ask for the MAJOR.MINOR line; the registry resolves it to the newest
	// patch, which is what satisfies a "go >= 1.25.5" style requirement.
	return fmt.Sprintf("golang:%d.%d", major, minor)
}

// pythonImageFor honours .python-version / runtime.txt when present.
func pythonImageFor(dir string) string {
	const fallback = "python:3.12-slim"
	for _, f := range []string{".python-version", "runtime.txt"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		s := strings.TrimSpace(strings.ToLower(string(b)))
		s = strings.TrimPrefix(s, "python-")
		s = strings.TrimPrefix(s, "python")
		s = strings.TrimSpace(s)
		if m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindStringSubmatch(s); len(m) == 3 {
			return fmt.Sprintf("python:%s.%s-slim", m[1], m[2])
		}
	}
	return fallback
}

// nodeImageFor honours package.json "engines.node" when present.
func nodeImageFor(dir string) string {
	const fallback = "node:20-slim"
	pkg := readJSONFile(filepath.Join(dir, "package.json"))
	eng, _ := pkg["engines"].(map[string]any)
	v, _ := eng["node"].(string)
	if m := regexp.MustCompile(`(\d+)`).FindStringSubmatch(v); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 14 && n <= 40 {
			return fmt.Sprintf("node:%d-slim", n)
		}
	}
	return fallback
}

// recommendImage picks a base image the project can actually run on.
func recommendImage(plan DeployPlan, dir string) string {
	switch plan.Kind {
	case KindGo:
		return goImageFor(dir)
	case KindPython:
		return pythonImageFor(dir)
	case KindNode, KindNodeS:
		return nodeImageFor(dir)
	case KindStatic:
		// debian:12-slim has no python and no node — nothing that can serve
		// files. Pick an image that ships a server.
		return "python:3.12-slim"
	}
	return "debian:12-slim"
}

/* ─────────── command rewriting ─────────── */

// containerCommand converts a plan's HOST run command into one that works
// inside a base image, where nothing is pre-built and no venv is valid.
//
// Returns the command unchanged when there's no image: without one the
// container is assembled from the host's own directories, so the host form is
// already correct (and a venv's symlinks still resolve).
func containerCommand(plan DeployPlan, dir, image string) string {
	if strings.TrimSpace(image) == "" {
		return plan.RunCmd // host rootfs — host command is right
	}
	run := strings.TrimSpace(plan.RunCmd)
	// A frontend legitimately has NO run command — that's what makes it static.
	// It still needs one inside a container, so don't bail out here.
	if run == "" && plan.Kind != KindNodeS && plan.Kind != KindStatic {
		return ""
	}

	switch plan.Kind {
	case KindPython:
		// A virtualenv is NOT portable: .venv/bin/python is a symlink to the
		// interpreter that created it, so inside an image it dangles and the
		// shell reports "not found" for a file that visibly exists. The image
		// IS the environment — install into its system python instead.
		cmd := strings.ReplaceAll(run, ".venv/bin/python", "python")
		install := "pip install --no-cache-dir --root-user-action=ignore"
		steps := []string{}
		if exists(filepath.Join(dir, "requirements.txt")) {
			steps = append(steps, install+" -r requirements.txt")
		}
		// The server it's launched with must exist even if requirements.txt
		// forgot it — that's a very common omission.
		if strings.Contains(cmd, "uvicorn") {
			steps = append(steps, install+" uvicorn")
		} else if strings.Contains(cmd, "gunicorn") {
			steps = append(steps, install+" gunicorn")
		}
		steps = append(steps, boundWorkers(cmd))
		return strings.Join(steps, " && ")

	case KindGo:
		// The node receives SOURCE; the binary was built on the repo server.
		if strings.Contains(run, "go run") {
			return run // already builds itself
		}
		if strings.HasPrefix(run, "./") {
			bin := strings.TrimPrefix(strings.Fields(run)[0], "./")
			// -p=1 compiles one package at a time. Slower, but roughly halves
			// peak memory — the difference between building and being
			// OOM-killed on a small node.
			return "GOFLAGS=-p=1 go build -o " + bin + " . && " + run
		}
		return run

	case KindNode:
		// node_modules are compiled against the host's ABI and often aren't in
		// the tarball at all — reinstall inside the image.
		install := "npm ci --no-audit --no-fund"
		if !exists(filepath.Join(dir, "package-lock.json")) {
			install = "npm install --no-audit --no-fund" // npm ci REQUIRES a lockfile
		}
		if plan.BuildCmd != "" && strings.Contains(plan.BuildCmd, "run build") {
			install += " && npm run build"
		}
		return install + " && " + run

	case KindNodeS, KindStatic:
		// A frontend has no server process of its own — on this host nginx or
		// the repo server serves the files. A container has neither, and a
		// replica with nothing listening is just a 502. So give it one.
		return staticServeCmd(plan, dir, plan.Port)
	}
	return run
}

// staticPortFor assigns a frontend a stable local port.
//
// Static projects have no port because nginx serves their files directly. Once
// one runs as a container it needs to listen somewhere, and that somewhere must
// be the SAME on every restart — the domain route points at it. Derived from
// the project name so it's deterministic without persisting anything, in the
// 20000–29999 range to stay clear of the ports real apps pick.
func staticPortFor(project string) int {
	h := 0
	for _, c := range project {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	return 20000 + h%10000
}

// staticServeCmd gives a frontend project a real server so it can run in a
// container and join a load-balancing pool like any backend.
//
// SPAs need history fallback — /dashboard must return index.html, not 404, or
// every deep link breaks on refresh. `serve -s` does exactly that. Plain static
// sites have no such requirement, so the smaller python image is enough.
func staticServeCmd(plan DeployPlan, dir string, port int) string {
	p := strconv.Itoa(port)
	if port == 0 {
		p = "3000"
	}

	// Directory the built site lands in, relative to the project root.
	serve := "."
	if plan.ServeDir != "" {
		if rel, err := filepath.Rel(dir, plan.ServeDir); err == nil &&
			!strings.HasPrefix(rel, "..") {
			serve = filepath.ToSlash(rel)
		}
	}

	if plan.Kind == KindNodeS {
		install := "npm ci --no-audit --no-fund"
		if !exists(filepath.Join(dir, "package-lock.json")) {
			install = "npm install --no-audit --no-fund"
		}
		// -s: single-page mode (history fallback). -l: listen port.
		return install + " && npm run build && npx --yes serve -s " + serve + " -l " + p
	}
	return "python3 -m http.server " + p + " --directory " + serve
}

// boundWorkers caps gunicorn/uvicorn worker count.
//
// Gunicorn's default is derived from CPU count and each worker is a full
// interpreter. Unlimited on the host that's merely wasteful; under a memory
// limit it's fatal — the workers collectively exceed the cgroup and everything
// is killed. Two workers is a safe default that still survives one hanging
// request.
func boundWorkers(cmd string) string {
	if strings.Contains(cmd, "--workers") || strings.Contains(cmd, "-w ") {
		return cmd
	}
	if strings.Contains(cmd, "gunicorn") || strings.Contains(cmd, "uvicorn") {
		return cmd + " --workers 2"
	}
	return cmd
}

/* ─────────── resource sizing ─────────── */

// Resources is a recommendation, not a limit: what this project needs to build
// and run without being OOM-killed.
type Resources struct {
	MemoryMB   int    `json:"memoryMB"`
	CPUPercent int    `json:"cpuPercent"`
	Why        string `json:"why"`
}

// recommendResources sizes a project from what it actually has to do.
//
// The number is dominated by the BUILD, not the steady state: compiling Go with
// a few large dependencies, or running `npm install`, peaks several times
// higher than the finished process ever will. A limit set for the running app
// kills it during startup — which is exactly the failure developers hit and
// can't explain.
func recommendResources(plan DeployPlan, dir string, image string) Resources {
	building := image != "" // an image means build-from-source on every start

	switch plan.Kind {
	case KindGo:
		if building {
			return Resources{1536, 100,
				"Go compilation is memory-hungry — a few hundred MB per package, more with large " +
					"dependency trees. The finished binary needs ~64 MB; the build is what sets the floor."}
		}
		return Resources{256, 100, "A compiled Go binary is small — this is the running app only."}

	case KindPython:
		mem := 768
		why := "Python needs room for pip to resolve and install wheels, plus ~120 MB per " +
			"gunicorn/uvicorn worker. Workers are capped at 2."
		if !building {
			mem = 512
			why = "Around 120 MB per worker plus the interpreter and your dependencies."
		}
		if hasHeavyPython(dir) {
			mem = 2048
			why = "Scientific/ML packages (numpy, pandas, torch, scipy) are large and load " +
					"substantial data into memory."
		}
		return Resources{mem, 100, why}

	case KindNodeS:
		if building {
			return Resources{1536, 100,
				"A bundler build (vite/webpack) is the peak — often over 1 GB. Once built, " +
					"serving the files needs almost nothing."}
		}
		return Resources{256, 50, "Serving pre-built files — minimal."}

	case KindNode:
		if building {
			return Resources{1536, 100,
				"npm install and a bundler build (vite/webpack) are the peak — often over 1 GB. " +
					"The running server needs far less."}
		}
		return Resources{512, 100, "Node's heap plus your dependencies."}

	case KindStatic:
		return Resources{128, 50, "Static files are served by a tiny HTTP server — almost no memory needed."}
	}
	return Resources{512, 100, "General-purpose default."}
}

// hasHeavyPython reports whether requirements.txt pulls in the big scientific
// stack, which changes the sizing by an order of magnitude.
func hasHeavyPython(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		return false
	}
	req := strings.ToLower(string(b))
	for _, p := range []string{"torch", "tensorflow", "numpy", "pandas", "scipy",
		"scikit-learn", "transformers", "opencv"} {
		if strings.Contains(req, p) {
			return true
		}
	}
	return false
}

// fitsNode reports whether a recommendation fits a machine with memMB of RAM,
// leaving headroom for the OS and the agent itself.
func fitsNode(rec Resources, nodeMemMB int) (bool, string) {
	if nodeMemMB <= 0 {
		return true, ""
	}
	usable := nodeMemMB - 400 // OS + agent + tunnel client
	if usable < 0 {
		usable = 0
	}
	if rec.MemoryMB <= usable {
		return true, ""
	}
	return false, fmt.Sprintf(
		"this machine has %d MB RAM (~%d MB usable) but this project needs about %d MB to build — "+
			"add swap, use a larger machine, or deploy a prebuilt artifact",
		nodeMemMB, usable, rec.MemoryMB)
}

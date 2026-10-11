package main

// Local database management for the repo dashboard. Lets a developer create
// PostgreSQL / MySQL / Redis / SQLite databases right on their server and attach
// the connection string to a deployed project's env (DATABASE_URL / REDIS_URL).
// Engines are detected and installed on demand (apt). Runs as root.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var dbNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,40}$`)

// engines we support
var dbEngines = []string{"postgres", "mysql", "redis", "sqlite"}

// DBInfo is one created database (persisted).
type DBInfo struct {
	ID         string    `json:"id"`
	Engine     string    `json:"engine"`
	Name       string    `json:"name"`
	User       string    `json:"user,omitempty"`
	Password   string    `json:"-"`          // never serialized to clients
	PasswordEn string    `json:"pw,omitempty"` // stored (this file is 0600)
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	ConnString string    `json:"-"` // computed, returned only to the owner
	Path       string    `json:"path,omitempty"`
	Project    string    `json:"project,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

var (
	dbMu   sync.Mutex
	dbList []DBInfo
)

func dbFile() string      { return filepath.Join(installDir, "databases.json") }
func sqliteDir() string   { return filepath.Join(installDir, "sqlite") }

func loadDatabases() {
	b, err := os.ReadFile(dbFile())
	if err != nil {
		return
	}
	dbMu.Lock()
	_ = json.Unmarshal(b, &dbList)
	dbMu.Unlock()
}

func saveDatabases() {
	dbMu.Lock()
	b, _ := json.MarshalIndent(dbList, "", "  ")
	dbMu.Unlock()
	_ = atomicWriteFile(dbFile(), b, 0600)
}

func randomPassword() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) // 36 alnum chars — no shell-quoting headaches
}

// engineBinary returns the command we probe to know an engine is installed.
func engineBinary(engine string) string {
	switch engine {
	case "postgres":
		return "psql"
	case "mysql":
		return "mysql"
	case "redis":
		return "redis-cli"
	case "sqlite":
		return "sqlite3"
	}
	return ""
}

func engineInstalled(engine string) bool {
	bin := engineBinary(engine)
	if bin == "" {
		return false
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// installEngine apt-installs an engine (root). Returns combined output.
func installEngine(engine string) (string, error) {
	var pkg string
	switch engine {
	case "postgres":
		pkg = "postgresql"
	case "mysql":
		pkg = "mariadb-server"
	case "redis":
		pkg = "redis-server"
	case "sqlite":
		pkg = "sqlite3"
	default:
		return "", fmt.Errorf("unknown engine")
	}
	if !isRoot() {
		return "", fmt.Errorf("installing %s needs root (run the server with sudo)", pkg)
	}
	out, err := runShell("/", "DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y "+pkg, 6*time.Minute)
	return out, err
}

func connString(d DBInfo) string {
	switch d.Engine {
	case "postgres":
		return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s", d.User, d.Password, d.Port, d.Name)
	case "mysql":
		return fmt.Sprintf("mysql://%s:%s@127.0.0.1:%d/%s", d.User, d.Password, d.Port, d.Name)
	case "redis":
		return fmt.Sprintf("redis://127.0.0.1:%d/%d", d.Port, d.Port-6379) // index encoded? see create
	case "sqlite":
		return "file:" + d.Path
	}
	return ""
}

// dbEnvVars returns EVERY common env-var convention for a database, so whatever
// the app reads (DATABASE_URL, DB_*, PG*, MYSQL_*, REDIS_*) is already set.
func dbEnvVars(d DBInfo) map[string]string {
	m := map[string]string{}
	port := fmt.Sprintf("%d", d.Port)
	switch d.Engine {
	case "postgres":
		m["DATABASE_URL"] = d.ConnString
		m["DB_HOST"], m["DB_PORT"], m["DB_NAME"], m["DB_USER"], m["DB_PASSWORD"] = "127.0.0.1", port, d.Name, d.User, d.Password
		m["PGHOST"], m["PGPORT"], m["PGDATABASE"], m["PGUSER"], m["PGPASSWORD"] = "127.0.0.1", port, d.Name, d.User, d.Password
	case "mysql":
		m["DATABASE_URL"] = d.ConnString
		m["DB_HOST"], m["DB_PORT"], m["DB_NAME"], m["DB_USER"], m["DB_PASSWORD"] = "127.0.0.1", port, d.Name, d.User, d.Password
		m["MYSQL_HOST"], m["MYSQL_PORT"], m["MYSQL_DATABASE"], m["MYSQL_USER"], m["MYSQL_PASSWORD"] = "127.0.0.1", port, d.Name, d.User, d.Password
	case "redis":
		m["REDIS_URL"] = d.ConnString
		m["REDIS_HOST"], m["REDIS_PORT"], m["REDIS_DB"] = "127.0.0.1", "6379", d.Path
	case "sqlite":
		m["DATABASE_URL"] = "file:" + d.Path
		m["DB_PATH"] = d.Path
	}
	return m
}

// dbUsageHint detects how the project reads config and returns a copy-paste
// snippet showing how to use the injected variable.
func dbUsageHint(project, engine string) (framework, lang, snippet string) {
	root := filepath.Join(dataDir, project)
	if rr := resolveProjectRoot(root); rr != "" {
		root = rr
	}
	has := func(f string) bool { return exists(filepath.Join(root, f)) }
	fileHas := func(f, sub string) bool {
		b, err := os.ReadFile(filepath.Join(root, f))
		return err == nil && strings.Contains(string(b), sub)
	}
	urlVar := "DATABASE_URL"
	if engine == "redis" {
		urlVar = "REDIS_URL"
	}
	switch {
	case has("schema.prisma") || fileHas("package.json", "prisma"):
		return "Prisma", "prisma", "// schema.prisma\ndatasource db {\n  provider = \"" + engine + "\"\n  url      = env(\"" + urlVar + "\")\n}"
	case has("manage.py"):
		return "Django", "python", "# settings.py\nimport os, dj_database_url\nDATABASES = {\"default\": dj_database_url.parse(os.environ[\"" + urlVar + "\"])}"
	case has("package.json"):
		return "Node.js", "js", "const url = process.env." + urlVar + "\n// e.g. new Pool({ connectionString: url })"
	case has("requirements.txt") || has("app.py") || has("main.py"):
		return "Python", "python", "import os\nurl = os.environ[\"" + urlVar + "\"]"
	case has("go.mod"):
		return "Go", "go", "url := os.Getenv(\"" + urlVar + "\")"
	case has("composer.json") || fileHas("index.php", "<?php"):
		return "PHP", "php", "$url = getenv(\"" + urlVar + "\");"
	}
	return "", "", "Read the " + urlVar + " environment variable in your database config."
}

// createDatabase provisions a database + (where relevant) a dedicated user.
func createDatabase(engine, name string) (DBInfo, error) {
	if !dbNameRe.MatchString(name) {
		return DBInfo{}, fmt.Errorf("invalid name: letters/digits/underscore, must start with a letter")
	}
	if !engineInstalled(engine) {
		return DBInfo{}, fmt.Errorf("%s is not installed — install it first", engine)
	}
	now := time.Now()
	id := fmt.Sprintf("%s-%d", engine, now.UnixNano())

	switch engine {
	case "postgres":
		user := name + "_u"
		pass := randomPassword()
		// CREATE DATABASE cannot run inside a transaction block, so each statement
		// goes in its OWN -c (psql runs multiple statements in one -c as a single tx).
		roleSQL := fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s';", user, pass)
		dbSQL := fmt.Sprintf("CREATE DATABASE %s OWNER %s;", name, user)
		cmd := "sudo -u postgres psql -v ON_ERROR_STOP=1 -c " + shellQuote(roleSQL) + " -c " + shellQuote(dbSQL)
		if out, err := runShell("/", cmd, time.Minute); err != nil {
			return DBInfo{}, fmt.Errorf("postgres: %s", lastLine(out))
		}
		d := DBInfo{ID: id, Engine: engine, Name: name, User: user, Password: pass, Host: "127.0.0.1", Port: 5432, CreatedAt: now}
		d.ConnString = connString(d)
		return d, nil

	case "mysql":
		user := name + "_u"
		pass := randomPassword()
		sql := fmt.Sprintf("CREATE DATABASE %s; CREATE USER '%s'@'localhost' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON %s.* TO '%s'@'localhost'; FLUSH PRIVILEGES;", name, user, pass, name, user)
		if out, err := runShell("/", "mysql -e "+shellQuote(sql), time.Minute); err != nil {
			return DBInfo{}, fmt.Errorf("mysql: %s", lastLine(out))
		}
		d := DBInfo{ID: id, Engine: engine, Name: name, User: user, Password: pass, Host: "127.0.0.1", Port: 3306, CreatedAt: now}
		d.ConnString = connString(d)
		return d, nil

	case "sqlite":
		_ = os.MkdirAll(sqliteDir(), 0755)
		path := filepath.Join(sqliteDir(), name+".db")
		if out, err := runShell("/", "sqlite3 "+shellQuote(path)+" 'VACUUM;'", time.Minute); err != nil {
			return DBInfo{}, fmt.Errorf("sqlite: %s", lastLine(out))
		}
		d := DBInfo{ID: id, Engine: engine, Name: name, Host: "", Port: 0, Path: path, CreatedAt: now}
		d.ConnString = connString(d)
		return d, nil

	case "redis":
		// redis has 16 logical DBs (0-15); assign the next free index
		dbMu.Lock()
		idx := 0
		for _, x := range dbList {
			if x.Engine == "redis" {
				idx++
			}
		}
		dbMu.Unlock()
		if idx > 15 {
			return DBInfo{}, fmt.Errorf("redis supports 16 logical DBs (0-15); limit reached")
		}
		d := DBInfo{ID: id, Engine: engine, Name: name, Host: "127.0.0.1", Port: 6379, CreatedAt: now}
		d.ConnString = fmt.Sprintf("redis://127.0.0.1:6379/%d", idx)
		d.Path = fmt.Sprintf("%d", idx) // store the index in Path
		return d, nil
	}
	return DBInfo{}, fmt.Errorf("unknown engine")
}

// listTables returns the tables (or key count for redis) of a database.
func listTables(d DBInfo) ([]string, error) {
	switch d.Engine {
	case "postgres":
		out, err := runShell("/", "psql "+shellQuote(d.ConnString)+" -At -c \"SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename;\"", 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("%s", lastLine(out))
		}
		return splitNonEmpty(out), nil
	case "mysql":
		cmd := fmt.Sprintf("MYSQL_PWD=%s mysql -u %s -h 127.0.0.1 %s -N -e 'SHOW TABLES;'", d.Password, d.User, d.Name)
		out, err := runShell("/", cmd, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("%s", lastLine(out))
		}
		return splitNonEmpty(out), nil
	case "sqlite":
		out, err := runShell("/", "sqlite3 "+shellQuote(d.Path)+" '.tables'", 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("%s", lastLine(out))
		}
		return strings.Fields(out), nil
	case "redis":
		out, err := runShell("/", fmt.Sprintf("redis-cli -n %s DBSIZE", d.Path), 15*time.Second)
		if err != nil {
			return nil, fmt.Errorf("%s", lastLine(out))
		}
		return []string{strings.TrimSpace(out) + " keys"}, nil
	}
	return nil, fmt.Errorf("unsupported engine")
}

func dropDatabase(d DBInfo) {
	switch d.Engine {
	case "postgres":
		// DROP DATABASE also can't run in a transaction block → separate -c flags
		cmd := "sudo -u postgres psql -c " + shellQuote(fmt.Sprintf("DROP DATABASE IF EXISTS %s;", d.Name)) +
			" -c " + shellQuote(fmt.Sprintf("DROP ROLE IF EXISTS %s;", d.User))
		_, _ = runShell("/", cmd, time.Minute)
	case "mysql":
		_, _ = runShell("/", "mysql -e "+shellQuote(fmt.Sprintf("DROP DATABASE IF EXISTS %s; DROP USER IF EXISTS '%s'@'localhost';", d.Name, d.User)), time.Minute)
	case "sqlite":
		_ = os.Remove(d.Path)
	}
}

/* ─────────── helpers ─────────── */

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func splitNonEmpty(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// rehydrate computed fields (Password/ConnString) from the stored entry
func hydrate(d *DBInfo) {
	if d.PasswordEn != "" {
		d.Password = d.PasswordEn
	}
	if d.Engine == "redis" {
		d.ConnString = fmt.Sprintf("redis://127.0.0.1:6379/%s", d.Path)
	} else {
		d.ConnString = connString(*d)
	}
}

/* ─────────── API ─────────── */

func dbJSONView(d DBInfo) map[string]any {
	return map[string]any{
		"id": d.ID, "engine": d.Engine, "name": d.Name, "user": d.User,
		"connString": d.ConnString, "project": d.Project,
		"createdAt": d.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// GET /api/databases — engines (with installed flag) + the created databases
func handleDatabases(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	engines := []map[string]any{}
	for _, e := range dbEngines {
		engines = append(engines, map[string]any{"id": e, "installed": engineInstalled(e)})
	}
	dbMu.Lock()
	views := make([]map[string]any, 0, len(dbList))
	for i := range dbList {
		hydrate(&dbList[i])
		views = append(views, dbJSONView(dbList[i]))
	}
	dbMu.Unlock()
	writeJSONResp(w, map[string]any{"engines": engines, "databases": views})
}

// POST /api/databases/install {engine}
func handleDBInstall(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	var req struct {
		Engine string `json:"engine"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := installEngine(req.Engine)
	if err != nil {
		writeJSONResp(w, map[string]any{"ok": false, "error": err.Error(), "log": lastLine(out)})
		return
	}
	writeJSONResp(w, map[string]any{"ok": true, "installed": engineInstalled(req.Engine)})
}

// POST /api/databases/create {engine, name}
func handleDBCreate(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	var req struct{ Engine, Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	d, err := createDatabase(strings.TrimSpace(req.Engine), strings.TrimSpace(req.Name))
	if err != nil {
		writeJSONResp(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	d.PasswordEn = d.Password
	dbMu.Lock()
	dbList = append(dbList, d)
	dbMu.Unlock()
	saveDatabases()
	writeJSONResp(w, map[string]any{"ok": true, "database": dbJSONView(d)})
}

// GET /api/databases/<id>/tables
// POST /api/databases/<id>/attach {project}
// DELETE /api/databases/<id>
func handleDBItem(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/databases/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	dbMu.Lock()
	var found *DBInfo
	for i := range dbList {
		if dbList[i].ID == id {
			hydrate(&dbList[i])
			found = &dbList[i]
			break
		}
	}
	dbMu.Unlock()
	if found == nil {
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}

	switch {
	case action == "tables" && r.Method == http.MethodGet:
		tables, err := listTables(*found)
		if err != nil {
			writeJSONResp(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSONResp(w, map[string]any{"ok": true, "tables": tables})

	case action == "attach" && r.Method == http.MethodPost:
		var req struct{ Project string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !isSafeName(req.Project) {
			http.Error(w, "invalid project", http.StatusBadRequest)
			return
		}
		// inject EVERY common convention so any framework picks it up
		inject := dbEnvVars(*found)
		vars := loadEnv(req.Project)
		keys := []string{}
		for k, v := range inject {
			vars[k] = v
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if err := saveEnv(req.Project, vars); err != nil {
			writeJSONResp(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		dbMu.Lock()
		found.Project = req.Project
		dbMu.Unlock()
		saveDatabases()
		restarted := restartService(req.Project)
		framework, lang, snippet := dbUsageHint(req.Project, found.Engine)
		tail := " (applies on next deploy)"
		if restarted {
			tail = " and the service was restarted"
		}
		writeJSONResp(w, map[string]any{
			"ok": true, "restarted": restarted, "vars": keys,
			"framework": framework, "lang": lang, "snippet": snippet,
			"note": fmt.Sprintf("Injected %d env vars into %s%s", len(keys), req.Project, tail),
		})

	case action == "" && r.Method == http.MethodDelete:
		dropDatabase(*found)
		dbMu.Lock()
		out := dbList[:0]
		for _, x := range dbList {
			if x.ID != id {
				out = append(out, x)
			}
		}
		dbList = out
		dbMu.Unlock()
		saveDatabases()
		writeJSONResp(w, map[string]any{"ok": true})

	default:
		http.NotFound(w, r)
	}
}

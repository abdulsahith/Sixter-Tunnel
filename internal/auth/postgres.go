package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// PGStore is the PostgreSQL implementation of Storage.
// Enable with:  sixter-server -db "postgres://user:pass@localhost/sixter?sslmode=disable"
type PGStore struct {
	db     *sql.DB
	encKey []byte // AES key for TOTP secrets (persisted in app_settings)
}

const pgSchema = `
CREATE TABLE IF NOT EXISTS users (
    email       TEXT PRIMARY KEY,
    pass_hash   TEXT NOT NULL,
    salt        TEXT NOT NULL,
    tunnel_sub  TEXT UNIQUE NOT NULL,
    repo_sub    TEXT UNIQUE NOT NULL,
    token_hash  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
    id      TEXT PRIMARY KEY,
    email   TEXT NOT NULL REFERENCES users(email) ON DELETE CASCADE,
    expires TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
    code      TEXT PRIMARY KEY,
    user_code TEXT NOT NULL,
    email     TEXT NOT NULL DEFAULT '',
    approved  BOOLEAN NOT NULL DEFAULT FALSE,
    issued    BOOLEAN NOT NULL DEFAULT FALSE,
    expires   TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS firewall (
    subdomain TEXT PRIMARY KEY,
    settings  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS app_settings (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tunnel_ids (
    subdomain TEXT PRIMARY KEY,
    id        TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS team_members (
    owner_email  TEXT NOT NULL,
    member_email TEXT NOT NULL,
    role         TEXT NOT NULL,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_email, member_email)
);
CREATE TABLE IF NOT EXISTS profiles (
    email        TEXT PRIMARY KEY,
    name         TEXT NOT NULL DEFAULT '',
    account_type TEXT NOT NULL DEFAULT 'individual',
    company      TEXT NOT NULL DEFAULT '',
    job_title    TEXT NOT NULL DEFAULT '',
    website      TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',
    bio          TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS workspaces (
    id          TEXT PRIMARY KEY,
    owner_email TEXT NOT NULL,
    name        TEXT NOT NULL,
    created     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS workspace_members (
    ws_id        TEXT NOT NULL,
    member_email TEXT NOT NULL,
    role         TEXT NOT NULL,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ws_id, member_email)
);
CREATE TABLE IF NOT EXISTS enroll_tokens (
    token        TEXT PRIMARY KEY,
    ws_id        TEXT NOT NULL,
    ws_name      TEXT NOT NULL,
    owner_email  TEXT NOT NULL,
    member_email TEXT NOT NULL,
    role         TEXT NOT NULL,
    created      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS oauth_identities (
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    email       TEXT NOT NULL REFERENCES users(email) ON DELETE CASCADE,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_id)
);
CREATE INDEX IF NOT EXISTS oauth_identities_email_idx ON oauth_identities (email);
-- 2FA columns (idempotent migrations for existing installs)
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret  TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS backup_hashes TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;
-- OAuth signups have no subdomain until onboarding, and no password at all.
ALTER TABLE users ALTER COLUMN tunnel_sub DROP NOT NULL;
ALTER TABLE users ALTER COLUMN repo_sub   DROP NOT NULL;
ALTER TABLE users ALTER COLUMN pass_hash  SET DEFAULT '';
ALTER TABLE users ALTER COLUMN salt       SET DEFAULT '';
-- one permanent invite link per workspace
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS join_token TEXT NOT NULL DEFAULT '';
-- admin suspension (blocks login, tunnel auth and API token)
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended BOOLEAN NOT NULL DEFAULT FALSE;
-- fleet enrolment: one permanent join token per account
ALTER TABLE users ADD COLUMN IF NOT EXISTS node_join_token TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS nodes (
    id           TEXT PRIMARY KEY,
    owner_email  TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    secret_hash  TEXT NOT NULL,
    os           TEXT NOT NULL DEFAULT '',
    arch         TEXT NOT NULL DEFAULT '',
    cpus         INTEGER NOT NULL DEFAULT 0,
    mem_total    BIGINT NOT NULL DEFAULT 0,
    agent_ver    TEXT NOT NULL DEFAULT '',
    isolation    TEXT NOT NULL DEFAULT '',
    cpu_pct      INTEGER NOT NULL DEFAULT 0,
    mem_pct      INTEGER NOT NULL DEFAULT 0,
    instances    INTEGER NOT NULL DEFAULT 0,
    load_avg     TEXT NOT NULL DEFAULT '',
    created      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS nodes_owner_idx ON nodes (owner_email);
CREATE TABLE IF NOT EXISTS assignments (
    id          TEXT PRIMARY KEY,
    owner_email TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    project     TEXT NOT NULL,
    image       TEXT NOT NULL DEFAULT '',
    command     TEXT NOT NULL DEFAULT '',
    port        INTEGER NOT NULL DEFAULT 0,
    env         TEXT NOT NULL DEFAULT '{}',
    memory_mb   INTEGER NOT NULL DEFAULT 0,
    cpu_percent INTEGER NOT NULL DEFAULT 0,
    state       TEXT NOT NULL DEFAULT 'pending',
    message     TEXT NOT NULL DEFAULT '',
    created     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_email, node_id, project)
);
CREATE INDEX IF NOT EXISTS assignments_node_idx ON assignments (node_id);
CREATE INDEX IF NOT EXISTS workspaces_join_token_idx ON workspaces (join_token);
-- IMPORTANT: columns added to an EXISTING table must use ALTER, not the
-- CREATE TABLE body. "CREATE TABLE IF NOT EXISTS" is skipped wholesale when the
-- table is already there, so a new column inside it is silently never created —
-- and every query naming it then fails at runtime.
ALTER TABLE nodes       ADD COLUMN IF NOT EXISTS iso_reason  TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes       ADD COLUMN IF NOT EXISTS private_ips TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes       ADD COLUMN IF NOT EXISTS public_ip   TEXT NOT NULL DEFAULT '';
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS subdomain   TEXT NOT NULL DEFAULT '';
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS route_token  TEXT NOT NULL DEFAULT '';
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS source_url   TEXT NOT NULL DEFAULT '';
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS source_token TEXT NOT NULL DEFAULT '';
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS probe        TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS mesh_members (
    id          TEXT NOT NULL,
    owner_email TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    public_key  TEXT NOT NULL DEFAULT '',
    overlay_ip  TEXT NOT NULL DEFAULT '',
    endpoint    TEXT NOT NULL DEFAULT '',
    listen_port INTEGER NOT NULL DEFAULT 51820,
    role        TEXT NOT NULL DEFAULT '',
    updated     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_email, id)
);
ALTER TABLE mesh_members ADD COLUMN IF NOT EXISTS pod_cidr TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS mesh_accounts (
    owner_email TEXT PRIMARY KEY,
    slot        INTEGER NOT NULL
);
`

// OpenPostgres connects and creates the schema if missing.
func OpenPostgres(dsn string) (*PGStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if _, err := db.Exec(pgSchema); err != nil {
		return nil, fmt.Errorf("postgres schema: %w", err)
	}
	s := &PGStore{db: db}
	key, err := s.loadOrCreateKey()
	if err != nil {
		return nil, fmt.Errorf("2fa key: %w", err)
	}
	s.encKey = key
	return s, nil
}

// loadOrCreateKey persists a 32-byte AES key for TOTP secrets in app_settings.
func (s *PGStore) loadOrCreateKey() ([]byte, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM app_settings WHERE k='totp_key'`).Scan(&v)
	if err == nil {
		if key, derr := hex.DecodeString(v); derr == nil && len(key) == 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, rerr := rand.Read(key); rerr != nil {
		return nil, rerr
	}
	if _, ierr := s.db.Exec(
		`INSERT INTO app_settings (k, v) VALUES ('totp_key', $1)
		 ON CONFLICT (k) DO UPDATE SET v=$1`, hex.EncodeToString(key),
	); ierr != nil {
		return nil, ierr
	}
	return key, nil
}

/* ─────────── users ─────────── */

func (s *PGStore) CreateUser(email, password, sub string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	sub = strings.ToLower(strings.TrimSpace(sub))

	if !validEmail(email) {
		return fmt.Errorf("invalid email address")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if !ValidSubdomain(sub) {
		return fmt.Errorf("subdomain must be 3-32 chars: lowercase letters, digits, hyphens (and not reserved)")
	}
	repoSub := sub + "-repo"
	salt := randHex(16)

	_, err := s.db.Exec(
		`INSERT INTO users (email, pass_hash, salt, tunnel_sub, repo_sub) VALUES ($1,$2,$3,$4,$5)`,
		email, hashPassword(password, salt), salt, sub, repoSub,
	)
	if err != nil {
		es := err.Error()
		if strings.Contains(es, "users_pkey") {
			return fmt.Errorf("an account with this email already exists")
		}
		if strings.Contains(es, "tunnel_sub") || strings.Contains(es, "repo_sub") {
			return fmt.Errorf("subdomain already taken")
		}
		return err
	}
	return nil
}

func (s *PGStore) Authenticate(email, password string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	var passHash, salt string
	var suspended bool
	err := s.db.QueryRow(`SELECT pass_hash, salt, suspended FROM users WHERE email=$1`, email).
		Scan(&passHash, &salt, &suspended)
	if err != nil {
		_ = hashPassword(password, "00000000000000000000000000000000") // timing
		return false
	}
	if suspended {
		return false
	}
	return constEq(hashPassword(password, salt), passHash)
}

func (s *PGStore) GetUser(email string) (User, bool) {
	var u User
	email = strings.ToLower(email)
	// COALESCE: OAuth signups have NULL subdomains until they finish onboarding.
	err := s.db.QueryRow(
		`SELECT email, pass_hash, salt, COALESCE(tunnel_sub,''), COALESCE(repo_sub,''),
		        token_hash, created_at, totp_secret, totp_enabled, backup_hashes, is_admin, suspended
		   FROM users WHERE email=$1`,
		email,
	).Scan(&u.Email, &u.PassHash, &u.Salt, &u.TunnelSub, &u.RepoSub, &u.TokenHash, &u.CreatedAt,
		&u.TOTPSecret, &u.TOTPEnabled, pq.Array(&u.BackupHashes), &u.IsAdmin, &u.Suspended)
	if err != nil {
		return User{}, false
	}
	if rows, rerr := s.db.Query(`SELECT provider, provider_id FROM oauth_identities WHERE email=$1`, email); rerr == nil {
		defer rows.Close()
		for rows.Next() {
			var p, id string
			if rows.Scan(&p, &id) == nil {
				if u.Providers == nil {
					u.Providers = map[string]string{}
				}
				u.Providers[p] = id
			}
		}
	}
	return u, true
}

/* ─────────── OAuth ─────────── */

// UpsertOAuthUser finds or creates the account behind an OAuth identity.
// Match order: linked provider id, then email (links to an existing password
// account instead of creating a duplicate). New accounts get no subdomain
// until the user picks one in onboarding.
func (s *PGStore) UpsertOAuthUser(provider, providerID, email, name string) (User, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	providerID = strings.TrimSpace(providerID)
	email = strings.ToLower(strings.TrimSpace(email))
	if provider == "" || providerID == "" {
		return User{}, errors.New("missing provider identity")
	}
	if !validEmail(email) {
		return User{}, errors.New("provider did not return a usable email address")
	}

	// 1) already linked
	var linked string
	if err := s.db.QueryRow(
		`SELECT email FROM oauth_identities WHERE provider=$1 AND provider_id=$2`,
		provider, providerID).Scan(&linked); err == nil && linked != "" {
		if u, ok := s.GetUser(linked); ok {
			return u, nil
		}
	}

	// 2) create the account if the email is new
	if _, exists := s.GetUser(email); !exists {
		if _, err := s.db.Exec(
			`INSERT INTO users (email, pass_hash, salt) VALUES ($1,'','')
			 ON CONFLICT (email) DO NOTHING`, email); err != nil {
			return User{}, err
		}
		if strings.TrimSpace(name) != "" {
			_, _ = s.db.Exec(
				`INSERT INTO profiles (email, name, account_type) VALUES ($1,$2,'individual')
				 ON CONFLICT (email) DO NOTHING`, email, strings.TrimSpace(name))
		}
	}

	// 3) link the identity
	if _, err := s.db.Exec(
		`INSERT INTO oauth_identities (provider, provider_id, email) VALUES ($1,$2,$3)
		 ON CONFLICT (provider, provider_id) DO UPDATE SET email=EXCLUDED.email`,
		provider, providerID, email); err != nil {
		return User{}, err
	}

	u, ok := s.GetUser(email)
	if !ok {
		return User{}, errors.New("account creation failed")
	}
	return u, nil
}

func (s *PGStore) SubdomainAvailable(sub string) bool {
	sub = strings.ToLower(strings.TrimSpace(sub))
	if !ValidSubdomain(sub) {
		return false
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM users WHERE tunnel_sub IN ($1,$2) OR repo_sub IN ($1,$2)`,
		sub, sub+"-repo").Scan(&n); err != nil {
		return false
	}
	return n == 0
}

func (s *PGStore) ClaimSubdomain(email, sub string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	sub = strings.ToLower(strings.TrimSpace(sub))
	if !ValidSubdomain(sub) {
		return errors.New("subdomain must be 3-32 chars: lowercase letters, digits, hyphens (and not reserved)")
	}
	res, err := s.db.Exec(
		`UPDATE users SET tunnel_sub=$2, repo_sub=$3
		  WHERE email=$1 AND (tunnel_sub IS NULL OR tunnel_sub='')`,
		email, sub, sub+"-repo")
	if err != nil {
		if strings.Contains(err.Error(), "tunnel_sub") || strings.Contains(err.Error(), "repo_sub") {
			return errors.New("subdomain already taken")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("this account already has a subdomain")
	}
	return nil
}

/* ─────────── tunnel id ─────────── */

func (s *PGStore) TunnelID(sub string) string {
	sub = strings.ToLower(strings.TrimSpace(sub))
	var id string
	if err := s.db.QueryRow(`SELECT id FROM tunnel_ids WHERE subdomain=$1`, sub).Scan(&id); err == nil && id != "" {
		return id
	}
	id = genUUID()
	_, _ = s.db.Exec(`INSERT INTO tunnel_ids (subdomain, id) VALUES ($1,$2) ON CONFLICT (subdomain) DO NOTHING`, sub, id)
	// re-read in case another connection inserted first
	_ = s.db.QueryRow(`SELECT id FROM tunnel_ids WHERE subdomain=$1`, sub).Scan(&id)
	return id
}

// OwnerOf resolves a subdomain to the owning account email.
func (s *PGStore) OwnerOf(sub string) (string, bool) {
	var email string
	err := s.db.QueryRow(`SELECT email FROM users WHERE tunnel_sub=$1 OR repo_sub=$1`,
		strings.ToLower(strings.TrimSpace(sub))).Scan(&email)
	if err != nil {
		return "", false
	}
	return email, true
}

/* ─────────── teams ─────────── */

func (s *PGStore) AddMember(owner, member, role string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	if owner == "" || member == "" {
		return errors.New("owner and member required")
	}
	if member == owner {
		return errors.New("you are the team owner")
	}
	if !ValidRole(role) {
		return errors.New("invalid role")
	}
	_, err := s.db.Exec(`
		INSERT INTO team_members (owner_email, member_email, role)
		VALUES ($1,$2,$3)
		ON CONFLICT (owner_email, member_email) DO UPDATE SET role = EXCLUDED.role`,
		owner, member, role)
	return err
}

func (s *PGStore) SetRole(owner, member, role string) error {
	if !ValidRole(role) {
		return errors.New("invalid role")
	}
	return s.AddMember(owner, member, role)
}

func (s *PGStore) RemoveMember(owner, member string) error {
	_, err := s.db.Exec(`DELETE FROM team_members WHERE owner_email=$1 AND member_email=$2`,
		strings.ToLower(strings.TrimSpace(owner)), strings.ToLower(strings.TrimSpace(member)))
	return err
}

func (s *PGStore) ListMembers(owner string) []TeamMember {
	rows, err := s.db.Query(`SELECT member_email, role, added_at FROM team_members WHERE owner_email=$1 ORDER BY added_at`,
		strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TeamMember
	for rows.Next() {
		var m TeamMember
		if rows.Scan(&m.Email, &m.Role, &m.AddedAt) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *PGStore) MemberRole(owner, member string) (string, bool) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM team_members WHERE owner_email=$1 AND member_email=$2`,
		strings.ToLower(strings.TrimSpace(owner)), strings.ToLower(strings.TrimSpace(member))).Scan(&role)
	if err != nil {
		return "", false
	}
	return role, true
}

/* ─────────── profile ─────────── */

func (s *PGStore) GetProfile(email string) (Profile, bool) {
	var p Profile
	err := s.db.QueryRow(`
		SELECT name, account_type, company, job_title, website, location, bio, updated_at
		FROM profiles WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&p.Name, &p.AccountType, &p.Company, &p.JobTitle, &p.Website, &p.Location, &p.Bio, &p.UpdatedAt)
	if err != nil {
		return Profile{}, false
	}
	return p, true
}

func (s *PGStore) SetProfile(email string, p Profile) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return errors.New("email required")
	}
	_, err := s.db.Exec(`
		INSERT INTO profiles (email, name, account_type, company, job_title, website, location, bio, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (email) DO UPDATE SET
			name=EXCLUDED.name, account_type=EXCLUDED.account_type, company=EXCLUDED.company,
			job_title=EXCLUDED.job_title, website=EXCLUDED.website, location=EXCLUDED.location,
			bio=EXCLUDED.bio, updated_at=now()`,
		email, p.Name, p.AccountType, p.Company, p.JobTitle, p.Website, p.Location, p.Bio)
	return err
}

/* ─────────── workspaces ─────────── */

func (s *PGStore) CreateWorkspace(owner, name string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	name = strings.TrimSpace(name)
	if owner == "" || name == "" {
		return "", errors.New("owner and name required")
	}
	id := genUUID()
	_, err := s.db.Exec(`INSERT INTO workspaces (id, owner_email, name) VALUES ($1,$2,$3)`, id, owner, name)
	return id, err
}

func (s *PGStore) ListWorkspaces(owner string) []Workspace {
	rows, err := s.db.Query(
		`SELECT id, name, owner_email, COALESCE(join_token,''), created
		   FROM workspaces WHERE owner_email=$1 ORDER BY created`,
		strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		if rows.Scan(&w.ID, &w.Name, &w.Owner, &w.JoinToken, &w.Created) == nil {
			out = append(out, w)
		}
	}
	return out
}

func (s *PGStore) DeleteWorkspace(owner, id string) error {
	res, err := s.db.Exec(`DELETE FROM workspaces WHERE id=$1 AND owner_email=$2`, id, strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("workspace not found")
	}
	_, _ = s.db.Exec(`DELETE FROM workspace_members WHERE ws_id=$1`, id)
	_, _ = s.db.Exec(`DELETE FROM enroll_tokens WHERE ws_id=$1`, id)
	return nil
}

func (s *PGStore) AddWorkspaceMember(owner, wsID, member, role string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	if !ValidWSRole(role) {
		return "", errors.New("invalid role")
	}
	var wsName string
	if err := s.db.QueryRow(`SELECT name FROM workspaces WHERE id=$1 AND owner_email=$2`, wsID, owner).Scan(&wsName); err != nil {
		return "", errors.New("workspace not found")
	}
	if _, err := s.db.Exec(`
		INSERT INTO workspace_members (ws_id, member_email, role) VALUES ($1,$2,$3)
		ON CONFLICT (ws_id, member_email) DO UPDATE SET role = EXCLUDED.role`, wsID, member, role); err != nil {
		return "", err
	}
	tok := genEnrollToken()
	_, err := s.db.Exec(`INSERT INTO enroll_tokens (token, ws_id, ws_name, owner_email, member_email, role) VALUES ($1,$2,$3,$4,$5,$6)`,
		tok, wsID, wsName, owner, member, role)
	return tok, err
}

func (s *PGStore) RemoveWorkspaceMember(owner, wsID, member string) error {
	// ensure the caller owns the workspace
	var x string
	if err := s.db.QueryRow(`SELECT id FROM workspaces WHERE id=$1 AND owner_email=$2`, wsID, strings.ToLower(strings.TrimSpace(owner))).Scan(&x); err != nil {
		return errors.New("workspace not found")
	}
	_, err := s.db.Exec(`DELETE FROM workspace_members WHERE ws_id=$1 AND member_email=$2`, wsID, strings.ToLower(strings.TrimSpace(member)))
	return err
}

func (s *PGStore) ListWorkspaceMembers(owner, wsID string) []TeamMember {
	var x string
	if err := s.db.QueryRow(`SELECT id FROM workspaces WHERE id=$1 AND owner_email=$2`, wsID, strings.ToLower(strings.TrimSpace(owner))).Scan(&x); err != nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT member_email, role, added_at FROM workspace_members WHERE ws_id=$1 ORDER BY added_at`, wsID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TeamMember
	for rows.Next() {
		var m TeamMember
		if rows.Scan(&m.Email, &m.Role, &m.AddedAt) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *PGStore) ResolveEnrollToken(token string) (EnrollInfo, bool) {
	var e EnrollInfo
	err := s.db.QueryRow(`SELECT ws_id, ws_name, owner_email, member_email, role, created FROM enroll_tokens WHERE token=$1`,
		strings.TrimSpace(token)).Scan(&e.WsID, &e.WsName, &e.Owner, &e.Email, &e.Role, &e.Created)
	if err != nil {
		return EnrollInfo{}, false
	}
	return e, true
}

/* ─────────── node registry ─────────── */

func (s *PGStore) NodeJoinToken(owner string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	var tok string
	if err := s.db.QueryRow(`SELECT COALESCE(node_join_token,'') FROM users WHERE email=$1`, owner).Scan(&tok); err != nil {
		return "", errors.New("account not found")
	}
	if tok == "" {
		tok = "sx_node_" + genEnrollToken()
		if _, err := s.db.Exec(`UPDATE users SET node_join_token=$2 WHERE email=$1`, owner, tok); err != nil {
			return "", err
		}
	}
	return tok, nil
}

func (s *PGStore) RotateNodeJoin(owner string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	tok := "sx_node_" + genEnrollToken()
	res, err := s.db.Exec(`UPDATE users SET node_join_token=$2 WHERE email=$1`, owner, tok)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errors.New("account not found")
	}
	return tok, nil
}

func (s *PGStore) EnrollNode(joinToken string, n Node) (Node, string, error) {
	joinToken = strings.TrimSpace(joinToken)
	var owner string
	if err := s.db.QueryRow(
		`SELECT email FROM users WHERE node_join_token=$1 AND node_join_token<>''`,
		joinToken).Scan(&owner); err != nil {
		return Node{}, "", errors.New("invalid join token — get a fresh one from your console")
	}

	secret := genEnrollToken()

	// Re-enrolling the same machine refreshes its credentials IN PLACE. The
	// node id must survive a reinstall: assignments are pinned to it, so
	// minting a new id would orphan every workload this machine runs. The
	// oldest row wins; duplicates from the old delete-and-recreate behaviour
	// are cleaned up.
	var keepID string
	err := s.db.QueryRow(
		`SELECT id FROM nodes WHERE owner_email=$1 AND lower(name)=lower($2)
		  ORDER BY created LIMIT 1`, owner, n.Name).Scan(&keepID)
	if err == nil && keepID != "" {
		_, _ = s.db.Exec(`DELETE FROM nodes WHERE owner_email=$1 AND lower(name)=lower($2) AND id<>$3`,
			owner, n.Name, keepID)
		_, uerr := s.db.Exec(
			`UPDATE nodes SET secret_hash=$2, os=$3, arch=$4, cpus=$5, mem_total=$6,
			        agent_ver=$7, isolation=$8, iso_reason=$9, private_ips=$10,
			        public_ip=$11, last_seen=now()
			  WHERE id=$1`,
			keepID, sha256Hex(secret), n.OS, n.Arch, n.CPUs, n.MemTotal,
			n.AgentVer, n.Isolation, n.IsolationReason,
			strings.Join(n.PrivateIPs, ","), n.PublicIP)
		if uerr != nil {
			return Node{}, "", uerr
		}
		n.ID, n.Owner, n.Secret = keepID, owner, sha256Hex(secret)
		n.LastSeen = time.Now()
		return n, secret, nil
	}

	n.ID = "node_" + genEnrollToken()[:12]
	n.Owner = owner
	n.Secret = sha256Hex(secret)
	n.Created, n.LastSeen = time.Now(), time.Now()

	_, err = s.db.Exec(
		`INSERT INTO nodes (id, owner_email, name, secret_hash, os, arch, cpus, mem_total, agent_ver, isolation, iso_reason, private_ips, public_ip)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		n.ID, n.Owner, n.Name, n.Secret, n.OS, n.Arch, n.CPUs, n.MemTotal, n.AgentVer, n.Isolation, n.IsolationReason,
		strings.Join(n.PrivateIPs, ","), n.PublicIP)
	if err != nil {
		return Node{}, "", err
	}
	return n, secret, nil
}

func (s *PGStore) AuthNode(nodeID, secret string) (Node, bool) {
	var n Node
	err := s.db.QueryRow(
		`SELECT id, owner_email, name, secret_hash, os, arch, cpus, mem_total,
		        agent_ver, isolation, iso_reason, created, last_seen
		   FROM nodes WHERE id=$1`, nodeID).
		Scan(&n.ID, &n.Owner, &n.Name, &n.Secret, &n.OS, &n.Arch, &n.CPUs, &n.MemTotal,
			&n.AgentVer, &n.Isolation, &n.IsolationReason, &n.Created, &n.LastSeen)
	if err != nil || !constEq(sha256Hex(secret), n.Secret) {
		return Node{}, false
	}
	return n, true
}

func (s *PGStore) TouchNode(nodeID string, cpuPct, memPct, instances int, load string) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET cpu_pct=$2, mem_pct=$3, instances=$4, load_avg=$5, last_seen=now()
		  WHERE id=$1`, nodeID, cpuPct, memPct, instances, load)
	return err
}

func (s *PGStore) ListNodes(owner string) []Node {
	rows, err := s.db.Query(
		`SELECT id, owner_email, name, os, arch, cpus, mem_total, agent_ver, isolation,
		        iso_reason, cpu_pct, mem_pct, instances, load_avg, created, last_seen,
		        private_ips, public_ip
		   FROM nodes WHERE owner_email=$1 ORDER BY name`,
		strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var n Node
		var privIPs string
		if rows.Scan(&n.ID, &n.Owner, &n.Name, &n.OS, &n.Arch, &n.CPUs, &n.MemTotal,
			&n.AgentVer, &n.Isolation, &n.IsolationReason, &n.CPUPct, &n.MemPct,
			&n.Instances, &n.LoadAvg, &n.Created, &n.LastSeen,
			&privIPs, &n.PublicIP) == nil {
			if privIPs != "" {
				n.PrivateIPs = strings.Split(privIPs, ",")
			}
			out = append(out, n)
		}
	}
	return out
}

func (s *PGStore) DeleteNode(owner, nodeID string) error {
	res, err := s.db.Exec(`DELETE FROM nodes WHERE id=$1 AND owner_email=$2`,
		nodeID, strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("node not found")
	}
	return nil
}

/* ─────────── assignments (desired state) ─────────── */

func (s *PGStore) CreateAssignment(owner string, a Assignment) (Assignment, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if owner == "" || a.NodeID == "" || a.Project == "" {
		return Assignment{}, errors.New("owner, node and project required")
	}
	normalizeAssignment(&a)
	a.Owner = owner
	if a.ID == "" {
		a.ID = "as_" + genEnrollToken()[:12]
	}
	envJSON, _ := json.Marshal(a.Env)

	// UNIQUE(owner,node,project) makes re-deploy an update rather than a
	// duplicate placement of the same app.
	err := s.db.QueryRow(
		`INSERT INTO assignments (id, owner_email, node_id, project, image, command, port,
		                          env, memory_mb, cpu_percent, subdomain, route_token,
		                          source_url, source_token, state, message, probe)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'',$16)
		 ON CONFLICT (owner_email, node_id, project) DO UPDATE SET
		   image=EXCLUDED.image, command=EXCLUDED.command, port=EXCLUDED.port,
		   env=EXCLUDED.env, memory_mb=EXCLUDED.memory_mb,
		   cpu_percent=EXCLUDED.cpu_percent, subdomain=EXCLUDED.subdomain,
		   source_url=EXCLUDED.source_url, source_token=EXCLUDED.source_token,
		   probe=EXCLUDED.probe, state='pending', updated=now()
		 RETURNING id, created, route_token`,
		a.ID, owner, a.NodeID, a.Project, a.Image, a.Command, a.Port,
		string(envJSON), a.MemoryMB, a.CPUPercent, a.Subdomain, a.RouteToken,
		a.SourceURL, a.SourceToken, string(a.State), strings.Join(a.Probe, ","),
	).Scan(&a.ID, &a.Created, &a.RouteToken)
	if err != nil {
		return Assignment{}, err
	}
	return a, nil
}

func (s *PGStore) scanAssignments(rows *sql.Rows) []Assignment {
	defer rows.Close()
	out := []Assignment{}
	for rows.Next() {
		var a Assignment
		var envJSON, state, probe string
		if rows.Scan(&a.ID, &a.Owner, &a.NodeID, &a.Project, &a.Image, &a.Command,
			&a.Port, &envJSON, &a.MemoryMB, &a.CPUPercent, &a.Subdomain, &a.RouteToken,
			&a.SourceURL, &a.SourceToken, &state, &a.Message, &a.Created, &a.Updated,
			&probe) != nil {
			continue
		}
		a.State = AssignState(state)
		_ = json.Unmarshal([]byte(envJSON), &a.Env)
		if probe != "" {
			a.Probe = strings.Split(probe, ",")
		}
		out = append(out, a)
	}
	return out
}

const assignCols = `id, owner_email, node_id, project, image, command, port,
                    env, memory_mb, cpu_percent, subdomain, route_token,
                    source_url, source_token, state, message, created, updated, probe`

func (s *PGStore) ListAssignments(owner string) []Assignment {
	rows, err := s.db.Query(`SELECT `+assignCols+` FROM assignments WHERE owner_email=$1 ORDER BY project`,
		strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return nil
	}
	return s.scanAssignments(rows)
}

func (s *PGStore) AllAssignments() []Assignment {
	rows, err := s.db.Query(`SELECT ` + assignCols + ` FROM assignments`)
	if err != nil {
		return nil
	}
	return s.scanAssignments(rows)
}

func (s *PGStore) AssignmentsForNode(nodeID string) []Assignment {
	rows, err := s.db.Query(`SELECT `+assignCols+` FROM assignments WHERE node_id=$1`, nodeID)
	if err != nil {
		return nil
	}
	return s.scanAssignments(rows)
}

func (s *PGStore) SetAssignmentState(id string, state AssignState, msg string) error {
	_, err := s.db.Exec(`UPDATE assignments SET state=$2, message=$3, updated=now() WHERE id=$1`,
		id, string(state), msg)
	return err
}

func (s *PGStore) DeleteAssignment(owner, id string) error {
	res, err := s.db.Exec(`DELETE FROM assignments WHERE id=$1 AND owner_email=$2`,
		id, strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("assignment not found")
	}
	return nil
}

/* ─────────── workspace join link ─────────── */

func (s *PGStore) WorkspaceJoinToken(owner, wsID string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	var tok string
	err := s.db.QueryRow(`SELECT COALESCE(join_token,'') FROM workspaces WHERE id=$1 AND owner_email=$2`,
		wsID, owner).Scan(&tok)
	if err != nil {
		return "", errors.New("workspace not found")
	}
	if tok == "" { // create on first read so existing workspaces get one
		tok = genEnrollToken()
		if _, err := s.db.Exec(`UPDATE workspaces SET join_token=$2 WHERE id=$1`, wsID, tok); err != nil {
			return "", err
		}
	}
	return tok, nil
}

func (s *PGStore) RotateWorkspaceJoin(owner, wsID string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	tok := genEnrollToken()
	res, err := s.db.Exec(`UPDATE workspaces SET join_token=$3 WHERE id=$1 AND owner_email=$2`, wsID, owner, tok)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errors.New("workspace not found")
	}
	return tok, nil
}

func (s *PGStore) JoinWorkspace(joinToken, email string) (string, EnrollInfo, error) {
	joinToken = strings.TrimSpace(joinToken)
	email = strings.ToLower(strings.TrimSpace(email))
	if joinToken == "" || email == "" {
		return "", EnrollInfo{}, errors.New("invite token and account required")
	}

	var wsID, wsName, owner string
	if err := s.db.QueryRow(
		`SELECT id, name, owner_email FROM workspaces WHERE join_token=$1 AND join_token<>''`,
		joinToken).Scan(&wsID, &wsName, &owner); err != nil {
		return "", EnrollInfo{}, errors.New("invalid invite link — ask your team lead for the workspace link")
	}

	role := ""
	if strings.EqualFold(owner, email) {
		role = "lead" // the owner always leads their own workspace
	} else {
		_ = s.db.QueryRow(`SELECT role FROM workspace_members WHERE ws_id=$1 AND lower(member_email)=$2`,
			wsID, email).Scan(&role)
	}
	if role == "" {
		return "", EnrollInfo{}, errors.New(email + " isn't a member of this workspace — ask the lead to add you first")
	}

	// reuse the member's existing personal token so re-running init is idempotent
	var tok string
	if err := s.db.QueryRow(
		`SELECT token FROM enroll_tokens WHERE ws_id=$1 AND lower(member_email)=$2 LIMIT 1`,
		wsID, email).Scan(&tok); err == nil && tok != "" {
		_, _ = s.db.Exec(`UPDATE enroll_tokens SET role=$2 WHERE token=$1`, tok, role)
		info, _ := s.ResolveEnrollToken(tok)
		info.Role = role
		return tok, info, nil
	}

	tok = genEnrollToken()
	if _, err := s.db.Exec(
		`INSERT INTO enroll_tokens (token, ws_id, ws_name, owner_email, member_email, role)
		 VALUES ($1,$2,$3,$4,$5,$6)`, tok, wsID, wsName, owner, email, role); err != nil {
		return "", EnrollInfo{}, err
	}
	return tok, EnrollInfo{WsID: wsID, WsName: wsName, Owner: owner, Email: email, Role: role, Created: time.Now()}, nil
}

func (s *PGStore) MemberProfile(owner, member string) (Profile, User, bool) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	if owner == "" || member == "" {
		return Profile{}, User{}, false
	}
	if owner != member {
		var n int
		err := s.db.QueryRow(
			`SELECT count(*) FROM workspace_members m
			   JOIN workspaces w ON w.id = m.ws_id
			  WHERE w.owner_email=$1 AND lower(m.member_email)=$2`, owner, member).Scan(&n)
		if err != nil || n == 0 {
			return Profile{}, User{}, false
		}
	}
	u, ok := s.GetUser(member)
	if !ok {
		return Profile{}, User{}, false
	}
	p, _ := s.GetProfile(member)
	return p, u, true
}

/* ─────────── admin ─────────── */

func (s *PGStore) ListUsers() []User {
	out := []User{}
	rows, err := s.db.Query(
		`SELECT email, COALESCE(tunnel_sub,''), COALESCE(repo_sub,''), token_hash,
		        created_at, totp_enabled, is_admin, suspended
		   FROM users ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Email, &u.TunnelSub, &u.RepoSub, &u.TokenHash,
			&u.CreatedAt, &u.TOTPEnabled, &u.IsAdmin, &u.Suspended); err == nil {
			out = append(out, u)
		}
	}
	return out
}

func (s *PGStore) SetAdmin(email string, admin bool) error {
	_, err := s.db.Exec(`UPDATE users SET is_admin=$1 WHERE email=$2`, admin, strings.ToLower(email))
	return err
}

func (s *PGStore) SetSuspended(email string, suspended bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := s.db.Exec(`UPDATE users SET suspended=$1 WHERE email=$2`, suspended, email); err != nil {
		return err
	}
	if suspended {
		// sign them out everywhere immediately
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE email=$1`, email)
	}
	return nil
}

func (s *PGStore) RevokeUserSessions(email string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE email=$1`, strings.ToLower(email))
	return err
}

func (s *PGStore) DeleteUserFull(email string) error {
	// sessions cascade via FK; users row removal frees the unique subdomains
	_, err := s.db.Exec(`DELETE FROM users WHERE email=$1`, strings.ToLower(email))
	return err
}

/* ─────────── TOTP 2FA ─────────── */

func (s *PGStore) BeginTOTPSetup(email string) (string, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	secret := newTOTPSecret()
	enc, err := aesEncrypt(s.encKey, secret)
	if err != nil {
		return "", "", err
	}
	res, err := s.db.Exec(
		`UPDATE users SET totp_secret=$1, totp_enabled=FALSE, backup_hashes='{}' WHERE email=$2`,
		enc, email,
	)
	if err != nil {
		return "", "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", "", fmt.Errorf("user not found")
	}
	return secret, otpauthURI(email, secret), nil
}

func (s *PGStore) ConfirmTOTP(email, code string) ([]string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var enc string
	if err := s.db.QueryRow(`SELECT totp_secret FROM users WHERE email=$1`, email).Scan(&enc); err != nil {
		return nil, fmt.Errorf("user not found")
	}
	if enc == "" {
		return nil, fmt.Errorf("no pending 2FA setup; start setup first")
	}
	secret, err := aesDecrypt(s.encKey, enc)
	if err != nil {
		return nil, fmt.Errorf("could not read 2FA secret")
	}
	if !validateTOTP(secret, code, time.Now()) {
		return nil, fmt.Errorf("incorrect code — check your authenticator app")
	}
	plain, hashes := newBackupCodes(8)
	if _, err := s.db.Exec(
		`UPDATE users SET totp_enabled=TRUE, backup_hashes=$1 WHERE email=$2`,
		pq.Array(hashes), email,
	); err != nil {
		return nil, err
	}
	return plain, nil
}

func (s *PGStore) ValidateTOTP(email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	var enc string
	var enabled bool
	if err := s.db.QueryRow(`SELECT totp_secret, totp_enabled FROM users WHERE email=$1`, email).Scan(&enc, &enabled); err != nil {
		return false
	}
	if !enabled || enc == "" {
		return false
	}
	secret, err := aesDecrypt(s.encKey, enc)
	if err != nil {
		return false
	}
	return validateTOTP(secret, code, time.Now())
}

func (s *PGStore) ConsumeBackupCode(email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	want := sha256Hex(normalizeBackup(code))
	res, err := s.db.Exec(
		`UPDATE users SET backup_hashes = array_remove(backup_hashes, $1)
		 WHERE email=$2 AND totp_enabled=TRUE AND $1 = ANY(backup_hashes)`,
		want, email,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *PGStore) DisableTOTP(email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := s.db.Exec(
		`UPDATE users SET totp_secret='', totp_enabled=FALSE, backup_hashes='{}' WHERE email=$1`,
		email,
	)
	return err
}

/* ─────────── sessions ─────────── */

func (s *PGStore) NewSession(email string) (string, error) {
	sid := randHex(32)
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, email, expires) VALUES ($1,$2,$3)`,
		sid, strings.ToLower(email), time.Now().Add(sessionTTL),
	)
	return sid, err
}

func (s *PGStore) SessionEmail(sid string) string {
	if sid == "" {
		return ""
	}
	var email string
	var expires time.Time
	err := s.db.QueryRow(`SELECT email, expires FROM sessions WHERE id=$1`, sid).Scan(&email, &expires)
	if err != nil {
		return ""
	}
	if time.Now().After(expires) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE id=$1`, sid)
		return ""
	}
	return email
}

func (s *PGStore) DeleteSession(sid string) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE id=$1`, sid)
}

/* ─────────── device flow ─────────── */

func (s *PGStore) NewDevice() (string, string, error) {
	deviceCode := randHex(32)
	userCode, err := newUserCode()
	if err != nil {
		return "", "", err
	}
	_, _ = s.db.Exec(`DELETE FROM devices WHERE expires < now()`)
	_, err = s.db.Exec(
		`INSERT INTO devices (code, user_code, expires) VALUES ($1,$2,$3)`,
		deviceCode, userCode, time.Now().Add(deviceTTL),
	)
	return deviceCode, userCode, err
}

func (s *PGStore) ApproveDevice(userCode, email string) error {
	userCode = normalizeUserCode(userCode)

	res, err := s.db.Exec(
		`UPDATE devices SET email=$1, approved=TRUE
		 WHERE user_code=$2 AND approved=FALSE AND expires > now()`,
		strings.ToLower(email), userCode,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("code not found, expired, or already used")
	}
	return nil
}

func (s *PGStore) PollDevice(deviceCode string) PollResult {
	var email string
	var approved, issued bool
	var expires time.Time
	err := s.db.QueryRow(
		`SELECT email, approved, issued, expires FROM devices WHERE code=$1`, deviceCode,
	).Scan(&email, &approved, &issued, &expires)
	if err != nil || time.Now().After(expires) || issued {
		return PollResult{Status: "expired"}
	}
	if !approved {
		return PollResult{Status: "pending"}
	}

	u, ok := s.GetUser(email)
	if !ok {
		return PollResult{Status: "expired"}
	}

	token := randHex(32)
	if _, err := s.db.Exec(`UPDATE users SET token_hash=$1 WHERE email=$2`, sha256Hex(token), email); err != nil {
		return PollResult{Status: "expired"}
	}
	_, _ = s.db.Exec(`DELETE FROM devices WHERE code=$1`, deviceCode)

	return PollResult{
		Status:    "approved",
		Email:     u.Email,
		Token:     token,
		TunnelSub: u.TunnelSub,
		RepoSub:   u.RepoSub,
	}
}

/* ─────────── tokens ─────────── */

func (s *PGStore) ValidateToken(sub, rawToken string) bool {
	if rawToken == "" {
		return false
	}
	var tokenHash string
	var suspended bool
	err := s.db.QueryRow(
		`SELECT token_hash, suspended FROM users WHERE tunnel_sub=$1 OR repo_sub=$1`, sub,
	).Scan(&tokenHash, &suspended)
	// A suspended account's token stops working everywhere at once — this is
	// the choke point the tunnel client, CLI and repo push all pass through.
	if err != nil || tokenHash == "" || suspended {
		return false
	}
	return constEq(sha256Hex(rawToken), tokenHash)
}

/* ─────────── firewall ─────────── */

func (s *PGStore) GetFirewall(sub string) (string, bool) {
	var data string
	err := s.db.QueryRow(`SELECT settings FROM firewall WHERE subdomain=$1`, sub).Scan(&data)
	if err != nil {
		return "", false
	}
	return data, true
}

func (s *PGStore) SetFirewall(sub, data string) error {
	_, err := s.db.Exec(
		`INSERT INTO firewall (subdomain, settings) VALUES ($1,$2)
		 ON CONFLICT (subdomain) DO UPDATE SET settings=$2`,
		sub, data,
	)
	return err
}


/* ─────────── overlay mesh ─────────── */

// meshSlot returns (allocating once) this account's /24 inside the overlay.
//
// Allocated under a transaction with a uniqueness constraint rather than
// "pick the max and add one": two machines enrolling at the same instant would
// otherwise be handed the same subnet, and two customers sharing overlay
// addresses is the worst failure this system can have.
func (s *PGStore) meshSlot(owner string) (int, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	var slot int
	err := s.db.QueryRow(`SELECT slot FROM mesh_accounts WHERE owner_email=$1`, owner).Scan(&slot)
	if err == nil {
		return slot, nil
	}
	err = s.db.QueryRow(
		`INSERT INTO mesh_accounts (owner_email, slot)
		 SELECT $1, COALESCE(MIN(t.s), 0) FROM (
		     SELECT generate_series(0,255) AS s
		     EXCEPT SELECT slot FROM mesh_accounts
		 ) t
		 ON CONFLICT (owner_email) DO UPDATE SET owner_email=EXCLUDED.owner_email
		 RETURNING slot`, owner).Scan(&slot)
	if err != nil {
		return 0, err
	}
	return slot, nil
}

func (s *PGStore) RegisterMesh(owner string, m MeshMember) (MeshMember, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	slot, err := s.meshSlot(owner)
	if err != nil {
		return MeshMember{}, err
	}
	saved, _, err := buildMesh(owner, m, s.AllMeshMembers(owner), slot)
	if err != nil {
		return MeshMember{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO mesh_members (id, owner_email, name, public_key, overlay_ip, endpoint, listen_port, role, pod_cidr)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (owner_email, id) DO UPDATE SET
		   name=EXCLUDED.name, public_key=EXCLUDED.public_key,
		   overlay_ip=EXCLUDED.overlay_ip, endpoint=EXCLUDED.endpoint,
		   listen_port=EXCLUDED.listen_port, role=EXCLUDED.role,
		   pod_cidr=EXCLUDED.pod_cidr, updated=now()`,
		saved.ID, owner, saved.Name, saved.PublicKey, saved.OverlayIP,
		saved.Endpoint, saved.ListenPort, saved.Role, saved.PodCIDR)
	if err != nil {
		return MeshMember{}, err
	}
	return saved, nil
}

func (s *PGStore) AllMeshMembers(owner string) []MeshMember {
	rows, err := s.db.Query(
		`SELECT id, owner_email, name, public_key, overlay_ip, endpoint, listen_port, role, pod_cidr
		   FROM mesh_members WHERE owner_email=$1 ORDER BY overlay_ip`,
		strings.ToLower(strings.TrimSpace(owner)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []MeshMember{}
	for rows.Next() {
		var m MeshMember
		if rows.Scan(&m.ID, &m.Owner, &m.Name, &m.PublicKey, &m.OverlayIP,
			&m.Endpoint, &m.ListenPort, &m.Role, &m.PodCIDR) == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *PGStore) MeshPeers(owner, selfID string) []MeshMember {
	out := []MeshMember{}
	for _, m := range s.AllMeshMembers(owner) {
		if m.ID != selfID {
			out = append(out, m)
		}
	}
	return out
}

func (s *PGStore) RemoveMesh(owner, id string) error {
	_, err := s.db.Exec(`DELETE FROM mesh_members WHERE owner_email=$1 AND id=$2`,
		strings.ToLower(strings.TrimSpace(owner)), id)
	return err
}

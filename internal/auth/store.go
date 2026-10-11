// Package auth implements the Sixter account system: user signup/login,
// browser sessions, the OAuth-style device flow used by "sixter login",
// and API-token validation for tunnel connections.
//
// Storage is a single JSON file guarded by a mutex — simple, dependency-free,
// and fine for the MVP. Swap for SQLite/Postgres later without changing the
// public methods.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	pbkdf2Iters = 210000 // OWASP-recommended ballpark for PBKDF2-SHA256
	sessionTTL  = 7 * 24 * time.Hour
	deviceTTL   = 10 * time.Minute
	// no ambiguous chars (0/O, 1/I)
	userCodeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var reservedSubs = map[string]bool{
	"www": true, "api": true, "mail": true, "admin": true, "sixter": true,
	"ns1": true, "ns2": true, "ftp": true, "smtp": true, "app": true,
	"dashboard": true, "status": true, "blog": true, "docs": true,
	"dev": true, "test": true, "staging": true, "connect": true,
}

// User is a registered Sixter account. Each user owns exactly two
// subdomains: one for tunnels, one for the repo dashboard.
type User struct {
	Email     string    `json:"email"`
	PassHash  string    `json:"pass_hash"` // hex(PBKDF2-SHA256(password, salt))
	Salt      string    `json:"salt"`      // hex
	TunnelSub string    `json:"tunnel_subdomain"`
	RepoSub   string    `json:"repo_subdomain"`
	TokenHash string    `json:"token_hash"` // hex(SHA-256(api token)); raw token never stored
	CreatedAt time.Time `json:"created_at"`

	// TOTP 2FA (authenticator app)
	TOTPSecret   string   `json:"totp_secret,omitempty"`   // AES-GCM(hex) of the base32 secret
	TOTPEnabled  bool     `json:"totp_enabled,omitempty"`  // true once the user verifies a code
	BackupHashes []string `json:"backup_hashes,omitempty"` // sha256(normalized backup code), single-use

	IsAdmin bool `json:"is_admin,omitempty"` // platform administrator

	// Suspended accounts can't log in, can't authenticate a tunnel and can't
	// use their API token — the CLI can't simply reconnect after a kick.
	// Reversible: an admin can lift it.
	Suspended bool `json:"suspended,omitempty"`

	// OAuth identities: provider ("google"/"github") → provider user id.
	// An account may have both a password and linked providers.
	Providers map[string]string `json:"providers,omitempty"`
}

// NeedsOnboarding reports whether the account still has to choose a subdomain
// (true for accounts created through OAuth, which never picked one at signup).
func (u User) NeedsOnboarding() bool { return u.TunnelSub == "" }

// Session is a browser login session.
type Session struct {
	Email   string    `json:"email"`
	Expires time.Time `json:"expires"`
}

// Device is a pending device-flow authorization ("sixter login").
type Device struct {
	UserCode string    `json:"user_code"`
	Email    string    `json:"email"`
	Approved bool      `json:"approved"`
	Issued   bool      `json:"issued"`
	Expires  time.Time `json:"expires"`
}

// Store is the JSON-file-backed account database.
type Store struct {
	mu     sync.Mutex
	path   string
	encKey []byte // AES key for TOTP secrets (loaded from <path>.2fakey)

	Users     map[string]*User    `json:"users"`    // email → user
	Subs      map[string]string   `json:"subs"`     // subdomain → email
	Sessions  map[string]*Session `json:"sessions"` // session id → session
	Devices   map[string]*Device  `json:"devices"`  // device code → device
	Firewall  map[string]string       `json:"firewall"`    // subdomain → settings JSON
	TunnelIDs map[string]string       `json:"tunnel_ids"`  // subdomain → stable tunnel id (UUID)
	Teams      map[string][]TeamMember `json:"teams"`        // owner email → team members
	Workspaces map[string]*Workspace   `json:"workspaces"`   // workspace id → workspace
	WSMembers  map[string][]TeamMember `json:"ws_members"`   // workspace id → members
	EnrollToks map[string]EnrollInfo   `json:"enroll_toks"`  // enrollment token → info
	Profiles   map[string]Profile      `json:"profiles"`     // email → profile details
	Nodes      map[string]*Node        `json:"nodes"`        // node id → enrolled machine
	NodeJoin   map[string]string       `json:"node_join"`    // owner email → fleet token
	Assigns    map[string]*Assignment  `json:"assignments"`  // id → desired placement
	Mesh       map[string][]MeshMember `json:"mesh"`         // owner → overlay members
	AcctSlots  map[string]int          `json:"acct_slots"`   // owner → /24 index in the overlay
}

// Open loads (or creates) the store at path.
func Open(path string) (*Store, error) {
	s := &Store{
		path:     path,
		Users:    map[string]*User{},
		Subs:     map[string]string{},
		Sessions: map[string]*Session{},
		Devices:  map[string]*Device{},
		Firewall: map[string]string{},
	}
	b, err := os.ReadFile(path)
	if err == nil {
		if jerr := json.Unmarshal(b, s); jerr != nil {
			return nil, fmt.Errorf("corrupt auth store %s: %w", path, jerr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.Firewall == nil {
		s.Firewall = map[string]string{}
	}
	if s.TunnelIDs == nil {
		s.TunnelIDs = map[string]string{}
	}
	if s.Teams == nil {
		s.Teams = map[string][]TeamMember{}
	}
	if s.Workspaces == nil {
		s.Workspaces = map[string]*Workspace{}
	}
	if s.WSMembers == nil {
		s.WSMembers = map[string][]TeamMember{}
	}
	if s.EnrollToks == nil {
		s.EnrollToks = map[string]EnrollInfo{}
	}
	if s.Profiles == nil {
		s.Profiles = map[string]Profile{}
	}
	if s.Nodes == nil {
		s.Nodes = map[string]*Node{}
	}
	if s.NodeJoin == nil {
		s.NodeJoin = map[string]string{}
	}
	if s.Assigns == nil {
		s.Assigns = map[string]*Assignment{}
	}
	if s.Mesh == nil {
		s.Mesh = map[string][]MeshMember{}
	}
	if s.AcctSlots == nil {
		s.AcctSlots = map[string]int{}
	}
	key, kerr := loadOrCreateKey(path + ".2fakey")
	if kerr != nil {
		return nil, fmt.Errorf("2fa key: %w", kerr)
	}
	s.encKey = key
	return s, nil
}

// GetFirewall returns the saved firewall JSON for a subdomain.
func (s *Store) GetFirewall(sub string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Firewall[sub]
	return v, ok
}

// SetFirewall persists the firewall JSON for a subdomain.
func (s *Store) SetFirewall(sub, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Firewall[sub] = data
	return s.save()
}

// save persists the store atomically. Caller must hold s.mu.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp%d", s.path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

/* ─────────── crypto helpers ─────────── */

func randHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// pbkdf2SHA256 implements RFC 2898 PBKDF2 with HMAC-SHA256.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hLen := prf.Size()
	numBlocks := (keyLen + hLen - 1) / hLen

	dk := make([]byte, 0, numBlocks*hLen)
	buf := make([]byte, 4)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf, uint32(block))
		prf.Write(buf)
		u := prf.Sum(nil)

		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

func hashPassword(password, saltHex string) string {
	salt, _ := hex.DecodeString(saltHex)
	return hex.EncodeToString(pbkdf2SHA256([]byte(password), salt, pbkdf2Iters, 32))
}

func constEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

/* ─────────── validation ─────────── */

// ValidSubdomain reports whether s is an acceptable user subdomain.
func ValidSubdomain(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, ch := range s {
		ok := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-'
		if !ok {
			return false
		}
	}
	return !reservedSubs[s]
}

func validEmail(e string) bool {
	at := strings.Index(e, "@")
	dot := strings.LastIndex(e, ".")
	return len(e) >= 5 && len(e) <= 254 && at > 0 && dot > at+1 && dot < len(e)-1
}

/* ─────────── users & sessions ─────────── */

// CreateUser registers an account and reserves <sub> and <sub>-repo.
func (s *Store) CreateUser(email, password, sub string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	sub = strings.ToLower(strings.TrimSpace(sub))

	if !validEmail(email) {
		return errors.New("invalid email address")
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if !ValidSubdomain(sub) {
		return errors.New("subdomain must be 3-32 chars: lowercase letters, digits, hyphens (and not reserved)")
	}
	repoSub := sub + "-repo"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Users[email]; exists {
		return errors.New("an account with this email already exists")
	}
	if _, taken := s.Subs[sub]; taken {
		return errors.New("subdomain already taken")
	}
	if _, taken := s.Subs[repoSub]; taken {
		return errors.New("subdomain already taken (repo variant in use)")
	}

	salt := randHex(16)
	s.Users[email] = &User{
		Email:     email,
		PassHash:  hashPassword(password, salt),
		Salt:      salt,
		TunnelSub: sub,
		RepoSub:   repoSub,
		CreatedAt: time.Now(),
	}
	s.Subs[sub] = email
	s.Subs[repoSub] = email
	return s.save()
}

// Authenticate checks email+password.
func (s *Store) Authenticate(email, password string) bool {
	email = strings.ToLower(strings.TrimSpace(email))

	s.mu.Lock()
	u, ok := s.Users[email]
	s.mu.Unlock()
	if !ok {
		// burn comparable time to avoid user enumeration via timing
		_ = hashPassword(password, "00000000000000000000000000000000")
		return false
	}
	if u.Suspended {
		return false
	}
	return constEq(hashPassword(password, u.Salt), u.PassHash)
}

// GetUser returns a copy of the user record.
func (s *Store) GetUser(email string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[strings.ToLower(email)]
	if !ok {
		return User{}, false
	}
	return *u, true
}

/* ─────────── TOTP 2FA ─────────── */

// BeginTOTPSetup generates a fresh secret for the user (stored encrypted, not yet
// enabled) and returns the plaintext secret + otpauth URI for the QR code.
func (s *Store) BeginTOTPSetup(email string) (secret, uri string, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok {
		return "", "", errors.New("user not found")
	}
	secret = newTOTPSecret()
	enc, eerr := aesEncrypt(s.encKey, secret)
	if eerr != nil {
		return "", "", eerr
	}
	u.TOTPSecret = enc
	u.TOTPEnabled = false
	u.BackupHashes = nil
	if serr := s.save(); serr != nil {
		return "", "", serr
	}
	return secret, otpauthURI(email, secret), nil
}

// ConfirmTOTP verifies the first code against the pending secret; on success it
// enables 2FA and returns freshly generated one-time backup codes (shown once).
func (s *Store) ConfirmTOTP(email, code string) ([]string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok {
		return nil, errors.New("user not found")
	}
	if u.TOTPSecret == "" {
		return nil, errors.New("no pending 2FA setup; start setup first")
	}
	secret, derr := aesDecrypt(s.encKey, u.TOTPSecret)
	if derr != nil {
		return nil, errors.New("could not read 2FA secret")
	}
	if !validateTOTP(secret, code, time.Now()) {
		return nil, errors.New("incorrect code — check your authenticator app")
	}
	plain, hashes := newBackupCodes(8)
	u.TOTPEnabled = true
	u.BackupHashes = hashes
	if serr := s.save(); serr != nil {
		return nil, serr
	}
	return plain, nil
}

// ValidateTOTP checks a login code for an enabled user.
func (s *Store) ValidateTOTP(email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok || !u.TOTPEnabled || u.TOTPSecret == "" {
		return false
	}
	secret, err := aesDecrypt(s.encKey, u.TOTPSecret)
	if err != nil {
		return false
	}
	return validateTOTP(secret, code, time.Now())
}

// ConsumeBackupCode validates and burns a single backup code.
func (s *Store) ConsumeBackupCode(email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	want := sha256Hex(normalizeBackup(code))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok || !u.TOTPEnabled {
		return false
	}
	for i, h := range u.BackupHashes {
		if constEq(h, want) {
			u.BackupHashes = append(u.BackupHashes[:i], u.BackupHashes[i+1:]...)
			_ = s.save()
			return true
		}
	}
	return false
}

// DisableTOTP turns 2FA off and clears the secret + backup codes.
func (s *Store) DisableTOTP(email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok {
		return errors.New("user not found")
	}
	u.TOTPSecret = ""
	u.TOTPEnabled = false
	u.BackupHashes = nil
	return s.save()
}

/* ─────────── tunnel id ─────────── */

// genUUID returns a random v4 UUID string.
func genUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// TunnelID returns the stable tunnel id for a subdomain, generating + persisting
// one on first use. The id is the permanent routing handle (domains may change,
// the tunnel id does not).
func (s *Store) TunnelID(sub string) string {
	sub = strings.ToLower(strings.TrimSpace(sub))
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.TunnelIDs[sub]; ok && id != "" {
		return id
	}
	id := genUUID()
	s.TunnelIDs[sub] = id
	_ = s.save()
	return id
}

// OwnerOf resolves a subdomain to the owning account email.
func (s *Store) OwnerOf(sub string) (string, bool) {
	sub = strings.ToLower(strings.TrimSpace(sub))
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Subs[sub]
	return e, ok
}

/* ─────────── teams ─────────── */

func (s *Store) AddMember(owner, member, role string) error {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.Teams[owner]
	for i, m := range list {
		if strings.EqualFold(m.Email, member) {
			list[i].Role = role
			s.Teams[owner] = list
			return s.save()
		}
	}
	s.Teams[owner] = append(list, TeamMember{Email: member, Role: role, AddedAt: time.Now()})
	return s.save()
}

func (s *Store) SetRole(owner, member, role string) error {
	if !ValidRole(role) {
		return errors.New("invalid role")
	}
	return s.AddMember(owner, member, role)
}

func (s *Store) RemoveMember(owner, member string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.Teams[owner]
	out := make([]TeamMember, 0, len(list))
	for _, m := range list {
		if !strings.EqualFold(m.Email, member) {
			out = append(out, m)
		}
	}
	s.Teams[owner] = out
	return s.save()
}

func (s *Store) ListMembers(owner string) []TeamMember {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.Teams[owner]
	out := make([]TeamMember, len(list))
	copy(out, list)
	return out
}

func (s *Store) MemberRole(owner, member string) (string, bool) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.Teams[owner] {
		if strings.EqualFold(m.Email, member) {
			return m.Role, true
		}
	}
	return "", false
}

/* ─────────── OAuth ─────────── */

// UpsertOAuthUser finds or creates the account for an OAuth identity.
//
// Matching order: the linked provider id first (so a user who changes their
// email at the provider keeps their account), then the verified email address
// (so signing in with Google on an existing password account links, rather
// than creating a duplicate).
//
// New accounts are created WITHOUT subdomains — the user picks one in the
// one-time onboarding step, which then calls ClaimSubdomain.
func (s *Store) UpsertOAuthUser(provider, providerID, email, name string) (User, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	providerID = strings.TrimSpace(providerID)
	email = strings.ToLower(strings.TrimSpace(email))
	if provider == "" || providerID == "" {
		return User{}, errors.New("missing provider identity")
	}
	if !validEmail(email) {
		return User{}, errors.New("provider did not return a usable email address")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1) already linked?
	for _, u := range s.Users {
		if u.Providers != nil && u.Providers[provider] == providerID {
			return *u, nil
		}
	}

	// 2) existing account with the same email → link the provider to it
	if u, ok := s.Users[email]; ok {
		if u.Providers == nil {
			u.Providers = map[string]string{}
		}
		u.Providers[provider] = providerID
		if err := s.save(); err != nil {
			return User{}, err
		}
		return *u, nil
	}

	// 3) brand new account — no password, no subdomain yet
	u := &User{
		Email:     email,
		CreatedAt: time.Now(),
		Providers: map[string]string{provider: providerID},
	}
	s.Users[email] = u
	if name = strings.TrimSpace(name); name != "" {
		if s.Profiles == nil {
			s.Profiles = map[string]Profile{}
		}
		if _, exists := s.Profiles[email]; !exists {
			s.Profiles[email] = Profile{Name: name, AccountType: "individual", UpdatedAt: time.Now()}
		}
	}
	if err := s.save(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// SubdomainAvailable reports whether sub (and its -repo variant) are free.
func (s *Store) SubdomainAvailable(sub string) bool {
	sub = strings.ToLower(strings.TrimSpace(sub))
	if !ValidSubdomain(sub) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, taken := s.Subs[sub]
	_, repoTaken := s.Subs[sub+"-repo"]
	return !taken && !repoTaken
}

// ClaimSubdomain allocates the tunnel + repo subdomains for an account that
// doesn't have them yet (OAuth signups). It is deliberately one-shot.
func (s *Store) ClaimSubdomain(email, sub string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	sub = strings.ToLower(strings.TrimSpace(sub))
	if !ValidSubdomain(sub) {
		return errors.New("subdomain must be 3-32 chars: lowercase letters, digits, hyphens (and not reserved)")
	}
	repoSub := sub + "-repo"

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.Users[email]
	if !ok {
		return errors.New("account not found")
	}
	if u.TunnelSub != "" {
		return errors.New("this account already has a subdomain")
	}
	if _, taken := s.Subs[sub]; taken {
		return errors.New("subdomain already taken")
	}
	if _, taken := s.Subs[repoSub]; taken {
		return errors.New("subdomain already taken (repo variant in use)")
	}

	u.TunnelSub = sub
	u.RepoSub = repoSub
	s.Subs[sub] = email
	s.Subs[repoSub] = email
	return s.save()
}

/* ─────────── profile ─────────── */

func (s *Store) GetProfile(email string) (Profile, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Profiles[email]
	return p, ok
}

func (s *Store) SetProfile(email string, p Profile) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return errors.New("email required")
	}
	p.UpdatedAt = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Profiles[email] = p
	return s.save()
}

/* ─────────── workspaces ─────────── */

func genEnrollToken() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) CreateWorkspace(owner, name string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	name = strings.TrimSpace(name)
	if owner == "" || name == "" {
		return "", errors.New("owner and name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := genUUID()
	s.Workspaces[id] = &Workspace{ID: id, Name: name, Owner: owner, Created: time.Now()}
	return id, s.save()
}

func (s *Store) ListWorkspaces(owner string) []Workspace {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Workspace{}
	for _, w := range s.Workspaces {
		if strings.EqualFold(w.Owner, owner) {
			out = append(out, *w)
		}
	}
	return out
}

func (s *Store) DeleteWorkspace(owner, id string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspaces[id]
	if !ok || !strings.EqualFold(w.Owner, owner) {
		return errors.New("workspace not found")
	}
	delete(s.Workspaces, id)
	delete(s.WSMembers, id)
	for t, e := range s.EnrollToks {
		if e.WsID == id {
			delete(s.EnrollToks, t)
		}
	}
	return s.save()
}

func (s *Store) AddWorkspaceMember(owner, wsID, member, role string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	if !ValidWSRole(role) {
		return "", errors.New("invalid role")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspaces[wsID]
	if !ok || !strings.EqualFold(w.Owner, owner) {
		return "", errors.New("workspace not found")
	}
	list := s.WSMembers[wsID]
	found := false
	for i := range list {
		if strings.EqualFold(list[i].Email, member) {
			list[i].Role = role
			found = true
		}
	}
	if !found {
		list = append(list, TeamMember{Email: member, Role: role, AddedAt: time.Now()})
	}
	s.WSMembers[wsID] = list
	tok := genEnrollToken()
	s.EnrollToks[tok] = EnrollInfo{WsID: wsID, WsName: w.Name, Owner: owner, Email: member, Role: role, Created: time.Now()}
	return tok, s.save()
}

func (s *Store) RemoveWorkspaceMember(owner, wsID, member string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspaces[wsID]
	if !ok || !strings.EqualFold(w.Owner, owner) {
		return errors.New("workspace not found")
	}
	list := s.WSMembers[wsID]
	out := make([]TeamMember, 0, len(list))
	for _, m := range list {
		if !strings.EqualFold(m.Email, member) {
			out = append(out, m)
		}
	}
	s.WSMembers[wsID] = out
	return s.save()
}

func (s *Store) ListWorkspaceMembers(owner, wsID string) []TeamMember {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspaces[wsID]
	if !ok || !strings.EqualFold(w.Owner, owner) {
		return nil
	}
	out := make([]TeamMember, len(s.WSMembers[wsID]))
	copy(out, s.WSMembers[wsID])
	return out
}

func (s *Store) ResolveEnrollToken(token string) (EnrollInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.EnrollToks[strings.TrimSpace(token)]
	return e, ok
}

/* ─────────── node registry ─────────── */

func (s *Store) NodeJoinToken(owner string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if owner == "" {
		return "", errors.New("owner required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.NodeJoin[owner]; t != "" {
		return t, nil
	}
	t := "sx_node_" + genEnrollToken()
	s.NodeJoin[owner] = t
	return t, s.save()
}

func (s *Store) RotateNodeJoin(owner string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	t := "sx_node_" + genEnrollToken()
	s.NodeJoin[owner] = t
	return t, s.save()
}

func (s *Store) EnrollNode(joinToken string, n Node) (Node, string, error) {
	joinToken = strings.TrimSpace(joinToken)
	if joinToken == "" {
		return Node{}, "", errors.New("join token required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	owner := ""
	for email, tok := range s.NodeJoin {
		if tok != "" && constEq(tok, joinToken) {
			owner = email
			break
		}
	}
	if owner == "" {
		return Node{}, "", errors.New("invalid join token — get a fresh one from your console")
	}

	secret := genEnrollToken()

	// Re-enrolling the same machine refreshes its credentials IN PLACE. The
	// node id must survive a reinstall: assignments are pinned to it, so
	// minting a new id would orphan every workload this machine runs — the
	// agent then reports "no longer assigned", stops the app, and the fleet
	// shows a duplicate node. Extra same-name rows (from the old behaviour)
	// are cleaned up here too.
	var keep *Node
	for id, ex := range s.Nodes {
		if ex != nil && strings.EqualFold(ex.Owner, owner) && strings.EqualFold(ex.Name, n.Name) {
			if keep == nil {
				keep = ex
			} else {
				delete(s.Nodes, id) // duplicate row: drop it
			}
		}
	}
	if keep != nil {
		keep.Secret = sha256Hex(secret)
		keep.OS, keep.Arch = n.OS, n.Arch
		keep.CPUs, keep.MemTotal = n.CPUs, n.MemTotal
		keep.AgentVer = n.AgentVer
		keep.Isolation, keep.IsolationReason = n.Isolation, n.IsolationReason
		keep.PrivateIPs, keep.PublicIP = n.PrivateIPs, n.PublicIP
		keep.LastSeen = time.Now()
		return *keep, secret, s.save()
	}

	n.ID = "node_" + genEnrollToken()[:12]
	n.Owner = owner
	n.Secret = sha256Hex(secret) // stored hashed; plaintext returned once
	n.Created = time.Now()
	n.LastSeen = time.Now()
	s.Nodes[n.ID] = &n
	return n, secret, s.save()
}

func (s *Store) AuthNode(nodeID, secret string) (Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.Nodes[nodeID]
	if !ok || n == nil || !constEq(sha256Hex(secret), n.Secret) {
		return Node{}, false
	}
	return *n, true
}

func (s *Store) TouchNode(nodeID string, cpuPct, memPct, instances int, load string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.Nodes[nodeID]
	if !ok || n == nil {
		return errors.New("node not found")
	}
	n.CPUPct, n.MemPct, n.Instances, n.LoadAvg = cpuPct, memPct, instances, load
	n.LastSeen = time.Now()
	// Heartbeats are frequent; persisting each one would hammer the disk.
	// The in-memory value is authoritative for liveness, and a restart simply
	// marks nodes offline until their next beat — which is correct anyway.
	return nil
}

func (s *Store) ListNodes(owner string) []Node {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Node{}
	for _, n := range s.Nodes {
		if n != nil && strings.EqualFold(n.Owner, owner) {
			out = append(out, *n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) DeleteNode(owner, nodeID string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.Nodes[nodeID]
	if !ok || n == nil || !strings.EqualFold(n.Owner, owner) {
		return errors.New("node not found")
	}
	delete(s.Nodes, nodeID)
	return s.save()
}

/* ─────────── assignments (desired state) ─────────── */

func (s *Store) CreateAssignment(owner string, a Assignment) (Assignment, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if owner == "" || a.NodeID == "" || a.Project == "" {
		return Assignment{}, errors.New("owner, node and project required")
	}
	normalizeAssignment(&a)
	a.Owner = owner

	s.mu.Lock()
	defer s.mu.Unlock()

	// One placement per project per node: re-deploying updates in place rather
	// than stacking duplicate copies of the same app.
	for id, ex := range s.Assigns {
		if ex != nil && strings.EqualFold(ex.Owner, owner) &&
			ex.Project == a.Project && ex.NodeID == a.NodeID {
			a.ID = id
			a.Created = ex.Created
			s.Assigns[id] = &a
			return a, s.save()
		}
	}

	a.ID = "as_" + genEnrollToken()[:12]
	s.Assigns[a.ID] = &a
	return a, s.save()
}

func (s *Store) ListAssignments(owner string) []Assignment {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Assignment{}
	for _, a := range s.Assigns {
		if a != nil && strings.EqualFold(a.Owner, owner) {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

func (s *Store) AllAssignments() []Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Assignment, 0, len(s.Assigns))
	for _, a := range s.Assigns {
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

func (s *Store) AssignmentsForNode(nodeID string) []Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Assignment{}
	for _, a := range s.Assigns {
		if a != nil && a.NodeID == nodeID {
			out = append(out, *a)
		}
	}
	return out
}

func (s *Store) SetAssignmentState(id string, state AssignState, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.Assigns[id]
	if !ok || a == nil {
		return errors.New("assignment not found")
	}
	a.State, a.Message, a.Updated = state, msg, time.Now()
	// State churns on every heartbeat; don't fsync the store each time.
	return nil
}

func (s *Store) DeleteAssignment(owner, id string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.Assigns[id]
	if !ok || a == nil || !strings.EqualFold(a.Owner, owner) {
		return errors.New("assignment not found")
	}
	delete(s.Assigns, id)
	return s.save()
}

/* ─────────── workspace join link ─────────── */

// WorkspaceJoinToken returns the workspace's permanent invite token, creating
// it on first read so existing workspaces get one automatically.
func (s *Store) WorkspaceJoinToken(owner, wsID string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspaces[wsID]
	if !ok || !strings.EqualFold(w.Owner, owner) {
		return "", errors.New("workspace not found")
	}
	if w.JoinToken == "" {
		w.JoinToken = genEnrollToken()
		if err := s.save(); err != nil {
			return "", err
		}
	}
	return w.JoinToken, nil
}

// RotateWorkspaceJoin issues a fresh invite token, invalidating the old link.
// Already-enrolled members keep working — their personal tokens are separate.
func (s *Store) RotateWorkspaceJoin(owner, wsID string) (string, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workspaces[wsID]
	if !ok || !strings.EqualFold(w.Owner, owner) {
		return "", errors.New("workspace not found")
	}
	w.JoinToken = genEnrollToken()
	return w.JoinToken, s.save()
}

// JoinWorkspace exchanges the shared invite for a personal enroll token.
// The email must already be a member (added by the owner) — a leaked link on
// its own grants nothing.
func (s *Store) JoinWorkspace(joinToken, email string) (string, EnrollInfo, error) {
	joinToken = strings.TrimSpace(joinToken)
	email = strings.ToLower(strings.TrimSpace(email))
	if joinToken == "" || email == "" {
		return "", EnrollInfo{}, errors.New("invite token and account required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var target *Workspace
	for _, w := range s.Workspaces {
		if w.JoinToken != "" && constEq(w.JoinToken, joinToken) {
			target = w
			break
		}
	}
	if target == nil {
		return "", EnrollInfo{}, errors.New("invalid invite link — ask your team lead for the workspace link")
	}

	// the owner is always the lead of their own workspace
	role := ""
	if strings.EqualFold(target.Owner, email) {
		role = "lead"
	} else {
		for _, m := range s.WSMembers[target.ID] {
			if strings.EqualFold(m.Email, email) {
				role = m.Role
				break
			}
		}
	}
	if role == "" {
		return "", EnrollInfo{}, errors.New(email + " isn't a member of this workspace — ask the lead to add you first")
	}

	// reuse an existing personal token so re-running init is idempotent
	for tok, e := range s.EnrollToks {
		if e.WsID == target.ID && strings.EqualFold(e.Email, email) {
			if e.Role != role { // role changed since it was issued
				e.Role = role
				s.EnrollToks[tok] = e
				if err := s.save(); err != nil {
					return "", EnrollInfo{}, err
				}
			}
			return tok, e, nil
		}
	}

	info := EnrollInfo{WsID: target.ID, WsName: target.Name, Owner: target.Owner, Email: email, Role: role, Created: time.Now()}
	tok := genEnrollToken()
	s.EnrollToks[tok] = info
	return tok, info, s.save()
}

// MemberProfile returns a teammate's profile + account, but only when they
// share a workspace with owner (or owner is asking about themselves).
func (s *Store) MemberProfile(owner, member string) (Profile, User, bool) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	member = strings.ToLower(strings.TrimSpace(member))
	if owner == "" || member == "" {
		return Profile{}, User{}, false
	}
	if owner != member {
		s.mu.Lock()
		allowed := false
		for id, w := range s.Workspaces {
			if !strings.EqualFold(w.Owner, owner) {
				continue
			}
			for _, m := range s.WSMembers[id] {
				if strings.EqualFold(m.Email, member) {
					allowed = true
					break
				}
			}
			if allowed {
				break
			}
		}
		s.mu.Unlock()
		if !allowed {
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

// ListUsers returns a copy of every account (no secrets are zeroed here; callers
// that expose this must redact). Sorted by creation time.
func (s *Store) ListUsers() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, len(s.Users))
	for _, u := range s.Users {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// SetAdmin grants/revokes the admin role.
func (s *Store) SetAdmin(email string, admin bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok {
		return errors.New("user not found")
	}
	u.IsAdmin = admin
	return s.save()
}

// SetSuspended blocks (or restores) an account platform-wide.
func (s *Store) SetSuspended(email string, suspended bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok {
		return errors.New("user not found")
	}
	u.Suspended = suspended
	if suspended {
		// drop every browser session too, so they're out immediately
		for id, sess := range s.Sessions {
			if strings.EqualFold(sess.Email, email) {
				delete(s.Sessions, id)
			}
		}
	}
	return s.save()
}

// RevokeUserSessions logs a user out everywhere (deletes all their sessions).
func (s *Store) RevokeUserSessions(email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.Sessions {
		if sess.Email == email {
			delete(s.Sessions, id)
		}
	}
	return s.save()
}

// DeleteUserFull removes a user, their subdomains, and all their sessions.
func (s *Store) DeleteUserFull(email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[email]
	if !ok {
		return errors.New("user not found")
	}
	delete(s.Subs, u.TunnelSub)
	delete(s.Subs, u.RepoSub)
	for id, sess := range s.Sessions {
		if sess.Email == email {
			delete(s.Sessions, id)
		}
	}
	delete(s.Users, email)
	return s.save()
}

// NewSession creates a browser session and returns its id.
func (s *Store) NewSession(email string) (string, error) {
	sid := randHex(32)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sessions[sid] = &Session{
		Email:   strings.ToLower(email),
		Expires: time.Now().Add(sessionTTL),
	}
	return sid, s.save()
}

// SessionEmail resolves a session id to an email ("" if invalid/expired).
func (s *Store) SessionEmail(sid string) string {
	if sid == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.Sessions[sid]
	if !ok {
		return ""
	}
	if time.Now().After(sess.Expires) {
		delete(s.Sessions, sid)
		_ = s.save()
		return ""
	}
	return sess.Email
}

// DeleteSession logs a browser session out.
func (s *Store) DeleteSession(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Sessions[sid]; ok {
		delete(s.Sessions, sid)
		_ = s.save()
	}
}

/* ─────────── device flow ─────────── */

// newUserCode generates a human-friendly XXXX-XXXX code.
func newUserCode() (string, error) {
	codeBytes := make([]byte, 8)
	if _, err := rand.Read(codeBytes); err != nil {
		return "", err
	}
	raw := make([]byte, 8)
	for i, b := range codeBytes {
		raw[i] = userCodeChars[int(b)%len(userCodeChars)]
	}
	return string(raw[:4]) + "-" + string(raw[4:]), nil
}

// normalizeUserCode uppercases, strips spaces, re-inserts the dash.
func normalizeUserCode(userCode string) string {
	userCode = strings.ToUpper(strings.TrimSpace(userCode))
	userCode = strings.ReplaceAll(userCode, " ", "")
	if len(userCode) == 8 {
		userCode = userCode[:4] + "-" + userCode[4:]
	}
	return userCode
}

// NewDevice starts a device-flow login and returns (deviceCode, userCode).
func (s *Store) NewDevice() (string, string, error) {
	deviceCode := randHex(32)

	userCode, err := newUserCode()
	if err != nil {
		return "", "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// prune expired devices while we're here
	now := time.Now()
	for k, d := range s.Devices {
		if now.After(d.Expires) {
			delete(s.Devices, k)
		}
	}

	s.Devices[deviceCode] = &Device{
		UserCode: userCode,
		Expires:  now.Add(deviceTTL),
	}
	return deviceCode, userCode, s.save()
}

// ApproveDevice is called from the web UI when a logged-in user enters
// the code shown by the CLI.
func (s *Store) ApproveDevice(userCode, email string) error {
	userCode = normalizeUserCode(userCode)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, d := range s.Devices {
		if d.UserCode == userCode {
			if time.Now().After(d.Expires) {
				return errors.New("code expired — run sixter login again")
			}
			if d.Approved {
				return errors.New("code already used")
			}
			d.Email = strings.ToLower(email)
			d.Approved = true
			return s.save()
		}
	}
	return errors.New("code not found — check for typos")
}

// PollResult is what the CLI receives when polling.
type PollResult struct {
	Status    string // "pending", "expired", "approved"
	Email     string
	Token     string // raw API token, returned exactly once
	TunnelSub string
	RepoSub   string
}

// PollDevice is called by the CLI. On approval it mints the user's API
// token (stored hashed) and returns the raw token one single time.
func (s *Store) PollDevice(deviceCode string) PollResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.Devices[deviceCode]
	if !ok {
		return PollResult{Status: "expired"}
	}
	if time.Now().After(d.Expires) {
		delete(s.Devices, deviceCode)
		_ = s.save()
		return PollResult{Status: "expired"}
	}
	if !d.Approved {
		return PollResult{Status: "pending"}
	}
	if d.Issued {
		// token already handed out for this device code
		return PollResult{Status: "expired"}
	}

	u, ok := s.Users[d.Email]
	if !ok {
		return PollResult{Status: "expired"}
	}

	token := randHex(32)
	u.TokenHash = sha256Hex(token)
	d.Issued = true
	delete(s.Devices, deviceCode)
	_ = s.save()

	return PollResult{
		Status:    "approved",
		Email:     u.Email,
		Token:     token,
		TunnelSub: u.TunnelSub,
		RepoSub:   u.RepoSub,
	}
}

/* ─────────── token validation (tunnel server) ─────────── */

// ValidateToken reports whether rawToken authorizes the given subdomain.
func (s *Store) ValidateToken(sub, rawToken string) bool {
	if rawToken == "" {
		return false
	}
	s.mu.Lock()
	email, ok := s.Subs[sub]
	var u *User
	if ok {
		u = s.Users[email]
	}
	s.mu.Unlock()

	// A suspended account's token stops working everywhere at once — this is
	// the choke point the tunnel client, CLI and repo push all pass through,
	// so suspension actually blocks reconnection instead of just kicking.
	if u == nil || u.TokenHash == "" || u.Suspended {
		return false
	}
	return constEq(sha256Hex(rawToken), u.TokenHash)
}

// SubdomainRegistered reports whether sub belongs to any account.
func (s *Store) SubdomainRegistered(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.Subs[sub]
	return ok
}


/* ─────────── overlay mesh ─────────── */

func (s *Store) RegisterMesh(owner string, m MeshMember) (MeshMember, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()

	slot, ok := s.AcctSlots[owner]
	if !ok {
		slot = accountIndex(owner, s.AcctSlots)
		s.AcctSlots[owner] = slot
	}
	saved, all, err := buildMesh(owner, m, s.Mesh[owner], slot)
	if err != nil {
		return MeshMember{}, err
	}
	s.Mesh[owner] = all
	return saved, s.save()
}

func (s *Store) MeshPeers(owner, selfID string) []MeshMember {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []MeshMember{}
	for _, m := range s.Mesh[owner] {
		if m.ID != selfID {
			out = append(out, m)
		}
	}
	return out
}

func (s *Store) AllMeshMembers(owner string) []MeshMember {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]MeshMember(nil), s.Mesh[owner]...)
}

func (s *Store) RemoveMesh(owner, id string) error {
	owner = strings.ToLower(strings.TrimSpace(owner))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Mesh[owner][:0]
	for _, m := range s.Mesh[owner] {
		if m.ID != id {
			out = append(out, m)
		}
	}
	s.Mesh[owner] = out
	return s.save()
}

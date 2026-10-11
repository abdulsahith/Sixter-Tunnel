package auth

import "time"

// TeamMember is one account's membership in a team (the team = owner's account).
type TeamMember struct {
	Email   string    `json:"email"`
	Role    string    `json:"role"` // lead | backend | frontend | viewer
	AddedAt time.Time `json:"added_at"`
}

// ValidRole reports whether r is a recognised team role.
func ValidRole(r string) bool {
	switch r {
	case "lead", "backend", "frontend", "viewer":
		return true
	}
	return false
}

// Profile is the account's public/business details, shown on the profile page.
type Profile struct {
	Name        string    `json:"name"`
	AccountType string    `json:"account_type"` // individual | company
	Company     string    `json:"company"`
	JobTitle    string    `json:"job_title"`
	Website     string    `json:"website"`
	Location    string    `json:"location"`
	Bio         string    `json:"bio"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Workspace is a coworkspace: an app grouping created by an account owner.
// Members are allocated to the workspace with a role; projects connect to it
// via `sixter project init <token>`.
type Workspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner"`
	// JoinToken is the workspace's permanent invite. One link for the whole
	// team: it identifies the WORKSPACE, never a person. A member's identity
	// comes from their own logged-in Sixter account at join time, so the link
	// can be shared, pinned and reused without ever expiring. Rotate it if it
	// leaks.
	JoinToken string    `json:"join_token,omitempty"`
	Created   time.Time `json:"created"`
}

// EnrollInfo is what a `sixter project init` token resolves to.
type EnrollInfo struct {
	WsID    string    `json:"ws_id"`
	WsName  string    `json:"ws_name"`
	Owner   string    `json:"owner"`
	Email   string    `json:"email"`
	Role    string    `json:"role"`
	Created time.Time `json:"created"`
}

// ValidWSRole reports whether r is a recognised workspace role.
//
//	lead        — owns the workspace's delivery; can push/deploy any component
//	backend     — developer; pushes their own component, deploy is staged
//	frontend    — developer; pushes their own component, deploy is staged
//	deployment  — promotes any pending push to production
//	tester      — QA; sees everything and previews staging, cannot push or deploy
//	viewer      — read-only
func ValidWSRole(r string) bool {
	switch r {
	case "lead", "backend", "frontend", "deployment", "tester", "viewer":
		return true
	}
	return false
}

// WSRoleCanPush reports whether a workspace role may push code to its component.
func WSRoleCanPush(r string) bool {
	switch r {
	case "lead", "backend", "frontend", "deployment":
		return true
	}
	return false // tester, viewer
}

// WSRoleCanDeploy reports whether a workspace role may promote a push to prod.
func WSRoleCanDeploy(r string) bool { return r == "lead" || r == "deployment" }

// Storage is the account database interface. Two implementations:
//   - *Store    : JSON file (zero config, default)
//   - *PGStore  : PostgreSQL (production, enable with -db DSN)
type Storage interface {
	CreateUser(email, password, sub string) error
	Authenticate(email, password string) bool
	GetUser(email string) (User, bool)

	// TOTP 2FA
	BeginTOTPSetup(email string) (secret, uri string, err error)
	ConfirmTOTP(email, code string) ([]string, error)
	ValidateTOTP(email, code string) bool
	ConsumeBackupCode(email, code string) bool
	DisableTOTP(email string) error

	// tunnel id (stable routing handle per subdomain)
	TunnelID(sub string) string

	// OwnerOf resolves a subdomain (tunnel or repo) to the owning account email.
	OwnerOf(sub string) (string, bool)

	// account profile (business/personal details)
	GetProfile(email string) (Profile, bool)
	SetProfile(email string, p Profile) error

	// OAuth (Google / GitHub). Accounts created this way have no password and
	// no subdomains until the user claims one during onboarding.
	UpsertOAuthUser(provider, providerID, email, name string) (User, error)
	SubdomainAvailable(sub string) bool
	ClaimSubdomain(email, sub string) error

	// workspaces (coworkspace org layer — created + managed in the landing)
	CreateWorkspace(owner, name string) (string, error)
	ListWorkspaces(owner string) []Workspace
	DeleteWorkspace(owner, id string) error
	AddWorkspaceMember(owner, wsID, member, role string) (token string, err error)
	RemoveWorkspaceMember(owner, wsID, member string) error
	ListWorkspaceMembers(owner, wsID string) []TeamMember
	ResolveEnrollToken(token string) (EnrollInfo, bool)

	// One permanent join link per workspace (see Workspace.JoinToken).
	// JoinWorkspace exchanges that shared link + a verified member email for a
	// PERSONAL enroll token, so per-member identity survives a shared invite.
	WorkspaceJoinToken(owner, wsID string) (string, error)
	RotateWorkspaceJoin(owner, wsID string) (string, error)
	JoinWorkspace(joinToken, email string) (token string, info EnrollInfo, err error)

	// MemberProfile lets a workspace owner read a teammate's profile — only if
	// that teammate is actually in one of the owner's workspaces.
	MemberProfile(owner, member string) (Profile, User, bool)

	// Node registry (see nodes.go) — machines running the Sixter agent.
	NodeStore
	MeshStore
	// Desired state for the fleet (see assignments.go).
	AssignmentStore

	// teams (coworking) — a team is keyed by its owner's email
	AddMember(owner, member, role string) error
	RemoveMember(owner, member string) error
	SetRole(owner, member, role string) error
	ListMembers(owner string) []TeamMember
	MemberRole(owner, member string) (string, bool)

	// admin
	ListUsers() []User
	SetAdmin(email string, admin bool) error
	// SetSuspended blocks/unblocks an account: no login, no tunnel, no token.
	SetSuspended(email string, suspended bool) error
	RevokeUserSessions(email string) error
	DeleteUserFull(email string) error

	NewSession(email string) (string, error)
	SessionEmail(sid string) string
	DeleteSession(sid string)

	NewDevice() (deviceCode, userCode string, err error)
	ApproveDevice(userCode, email string) error
	PollDevice(deviceCode string) PollResult

	ValidateToken(sub, rawToken string) bool

	// Firewall settings are stored as an opaque JSON string per subdomain
	// (the firewall package owns the schema; auth just persists it).
	GetFirewall(sub string) (string, bool)
	SetFirewall(sub, data string) error
}

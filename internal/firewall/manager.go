package firewall

import "sync"

// SettingsLoader fetches a subdomain's saved firewall settings.
// Returns (settings, true) if the user has customised them, else (_, false)
// and the manager uses DefaultSettings.
type SettingsLoader func(sub string) (Settings, bool)

// Manager owns one Engine per subdomain and lazily builds them, plus the
// edge-wide GlobalGuard (DDoS / L7-flood layer) shared across all subdomains.
type Manager struct {
	mu     sync.RWMutex
	engs   map[string]*Engine
	loader SettingsLoader
	Guard  *GlobalGuard
}

// NewManager creates a manager. loader may be nil (everything uses defaults).
func NewManager(loader SettingsLoader) *Manager {
	return &Manager{engs: map[string]*Engine{}, loader: loader, Guard: NewGlobalGuard(GuardConfig{})}
}

// For returns the engine for a subdomain, creating it on first use.
func (m *Manager) For(sub string) *Engine {
	m.mu.RLock()
	e := m.engs[sub]
	m.mu.RUnlock()
	if e != nil {
		return e
	}

	s := DefaultSettings()
	if m.loader != nil {
		if loaded, ok := m.loader(sub); ok {
			s = loaded
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if e = m.engs[sub]; e != nil { // double-check after lock
		return e
	}
	e = New(s)
	m.engs[sub] = e
	return e
}

// Update applies new settings to a subdomain (called when the user saves).
func (m *Manager) Update(sub string, s Settings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.engs[sub]; e != nil {
		e.SetRules(s)
		return
	}
	m.engs[sub] = New(s)
}

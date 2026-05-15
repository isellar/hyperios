// Package manifest manages the HyperiOS system manifest — a machine-readable
// description of the filesystem and service topology used by the Arbiter and
// CommandValidator to make context-aware decisions.
//
// The manifest is auto-generated and kept current via four triggers:
//  1. First-boot full scan — establishes baseline
//  2. inotify watcher — async updates for watched paths
//  3. Post-execution hook — immediate rescan of paths touched by a step
//  4. Startup reconciliation — mtime-based catch-up for offline changes
//
// The manifest is NOT the security enforcement boundary. OS permissions and
// the capability allowlist enforce access. The manifest adds context: what a
// path contains, how sensitive it is, whether PAM is required.
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sensitivity levels for manifest entries.
const (
	SensitivityLow      = "low"
	SensitivityMedium   = "medium"
	SensitivityHigh     = "high"
	SensitivityCritical = "critical"
)

// PathEntry describes a filesystem path known to the manifest.
type PathEntry struct {
	Path               string   `json:"path"`
	Owner              string   `json:"owner,omitempty"`
	Description        string   `json:"description,omitempty"`
	Affects            []string `json:"affects,omitempty"`
	RequiresPAM        bool     `json:"requires_pam"`
	RequiresCapability string   `json:"requires_capability,omitempty"`
	Sensitivity        string   `json:"sensitivity"`
	LastScanned        time.Time `json:"last_scanned"`
}

// ServiceEntry describes a systemd service known to the manifest.
type ServiceEntry struct {
	Name          string   `json:"name"`
	DependsOn     []string `json:"depends_on,omitempty"`
	Affects       []string `json:"affects,omitempty"`
	SafeToRestart bool     `json:"safe_to_restart"`
	RestartImpact string   `json:"restart_impact,omitempty"`
	LastScanned   time.Time `json:"last_scanned"`
}

// Manifest is the in-memory representation of the system manifest.
type Manifest struct {
	Paths    map[string]*PathEntry    `json:"paths"`
	Services map[string]*ServiceEntry `json:"services"`
	Generated time.Time              `json:"generated"`
	Version   int                    `json:"version"`
}

// Store is a thread-safe manifest store with load/save and lookup.
type Store struct {
	mu       sync.RWMutex
	manifest *Manifest
	path     string // path to manifest.json on disk
}

// NewStore creates a manifest Store backed by the file at path.
// If the file does not exist, an empty manifest is used.
func NewStore(path string) *Store {
	return &Store{
		path:     path,
		manifest: empty(),
	}
}

func empty() *Manifest {
	return &Manifest{
		Paths:     make(map[string]*PathEntry),
		Services:  make(map[string]*ServiceEntry),
		Generated: time.Now(),
		Version:   1,
	}
}

// Load reads the manifest from disk. If the file does not exist, the store
// remains empty (not an error — fresh install).
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	if m.Paths == nil {
		m.Paths = make(map[string]*PathEntry)
	}
	if m.Services == nil {
		m.Services = make(map[string]*ServiceEntry)
	}

	s.manifest = &m
	return nil
}

// Save writes the manifest to disk atomically.
func (s *Store) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.manifest, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Lookup returns the PathEntry for a filesystem path, or nil if not found.
// It tries exact match first, then prefix matching for directory entries.
func (s *Store) Lookup(path string) *PathEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Exact match
	if e, ok := s.manifest.Paths[path]; ok {
		return e
	}

	// Prefix match — find the most specific parent directory entry
	var bestMatch *PathEntry
	bestLen := 0
	for k, e := range s.manifest.Paths {
		if strings.HasPrefix(path, k+"/") || path == k {
			if len(k) > bestLen {
				bestLen = len(k)
				bestMatch = e
			}
		}
	}
	return bestMatch
}

// LookupService returns the ServiceEntry for a service name, or nil.
func (s *Store) LookupService(name string) *ServiceEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.manifest.Services[name]
}

// UpsertPath adds or updates a path entry.
func (s *Store) UpsertPath(entry PathEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.LastScanned = time.Now()
	s.manifest.Paths[entry.Path] = &entry
	s.manifest.Generated = time.Now()
}

// UpsertService adds or updates a service entry.
func (s *Store) UpsertService(entry ServiceEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.LastScanned = time.Now()
	s.manifest.Services[entry.Name] = &entry
	s.manifest.Generated = time.Now()
}

// SeedDefaults populates the manifest with well-known system paths and their
// sensitivity levels. Called on first boot or when the manifest is empty.
// This provides useful defaults without a full filesystem scan.
func (s *Store) SeedDefaults() {
	defaults := []PathEntry{
		{
			Path:               "/etc/sudoers.d",
			Owner:              "root",
			Description:        "sudo policy — modification grants privilege escalation",
			RequiresPAM:        true,
			Sensitivity:        SensitivityCritical,
			RequiresCapability: "execute:config",
		},
		{
			Path:               "/etc/passwd",
			Owner:              "root",
			Description:        "user account database",
			RequiresPAM:        true,
			Sensitivity:        SensitivityCritical,
		},
		{
			Path:               "/etc/shadow",
			Owner:              "root",
			Description:        "password hashes — never readable by agent",
			RequiresPAM:        true,
			Sensitivity:        SensitivityCritical,
		},
		{
			Path:               "/etc/nginx",
			Owner:              "root",
			Description:        "nginx web server configuration",
			Affects:            []string{"web-serving"},
			RequiresPAM:        false,
			Sensitivity:        SensitivityMedium,
			RequiresCapability: "execute:config",
		},
		{
			Path:               "/etc/systemd/system",
			Owner:              "root",
			Description:        "systemd service unit files",
			Affects:            []string{"service-management"},
			RequiresPAM:        true,
			Sensitivity:        SensitivityHigh,
			RequiresCapability: "execute:config",
		},
		{
			Path:               "/var/lib/hyperi",
			Owner:              "hyperi",
			Description:        "agent state, sessions, and plan documents",
			RequiresPAM:        false,
			Sensitivity:        SensitivityMedium,
			RequiresCapability: "execute:config",
		},
		{
			Path:               "/var/log/hyperi",
			Owner:              "hyperi",
			Description:        "agent audit log and operational logs",
			RequiresPAM:        false,
			Sensitivity:        SensitivityLow,
		},
		{
			Path:               "/home",
			Owner:              "root",
			Description:        "user home directories",
			RequiresPAM:        true,
			Sensitivity:        SensitivityHigh,
		},
		{
			Path:               "/root",
			Owner:              "root",
			Description:        "root home directory — never accessible to agent",
			RequiresPAM:        true,
			Sensitivity:        SensitivityCritical,
		},
	}

	for _, e := range defaults {
		s.UpsertPath(e)
	}

	// Default services
	services := []ServiceEntry{
		{
			Name:          "nginx",
			Affects:       []string{"web-serving"},
			SafeToRestart: true,
			RestartImpact: "brief HTTP downtime on port 80/443",
		},
		{
			Name:          "hyperi",
			Affects:       []string{"agent-pipeline"},
			SafeToRestart: true,
			RestartImpact: "active session lost",
		},
		{
			Name:          "ssh",
			Affects:       []string{"remote-access"},
			SafeToRestart: false,
			RestartImpact: "all active SSH sessions terminated",
		},
	}
	for _, svc := range services {
		s.UpsertService(svc)
	}
}

// ScanPath scans a single filesystem path and updates the manifest entry.
// It uses os.Stat for ownership info (limited without cgo on Linux).
func (s *Store) ScanPath(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	// Determine sensitivity from known prefixes
	sensitivity := SensitivityLow
	requiresPAM := false
	switch {
	case strings.HasPrefix(path, "/etc/sudoers") ||
		strings.HasPrefix(path, "/etc/shadow") ||
		strings.HasPrefix(path, "/root"):
		sensitivity = SensitivityCritical
		requiresPAM = true
	case strings.HasPrefix(path, "/home") ||
		strings.HasPrefix(path, "/etc/systemd"):
		sensitivity = SensitivityHigh
		requiresPAM = true
	case strings.HasPrefix(path, "/etc"):
		sensitivity = SensitivityMedium
	case strings.HasPrefix(path, "/var/lib/hyperi"):
		sensitivity = SensitivityMedium
	}

	entry := PathEntry{
		Path:        path,
		Description: "scanned: " + info.Mode().String(),
		RequiresPAM: requiresPAM,
		Sensitivity: sensitivity,
	}

	s.UpsertPath(entry)
}

// PostExecutionHook rescans all filesystem paths that appear as arguments
// in the given command. Called by the executor after each step.
func (s *Store) PostExecutionHook(command []string) {
	for _, arg := range command {
		if strings.HasPrefix(arg, "/") {
			// Looks like an absolute path
			if _, err := os.Stat(arg); err == nil {
				s.ScanPath(arg)
			}
		}
	}
}

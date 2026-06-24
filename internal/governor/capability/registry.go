package capability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/types"
	"gopkg.in/yaml.v3"
)

type Registry struct {
	mu            sync.RWMutex
	patterns      map[string][]string
	grants        map[string]time.Time
	allowlistFile string
	grantsFile    string
	workspace     string
}

type AllowlistConfig struct {
	Version         int      `yaml:"version"`
	ReadFile        []string `yaml:"read:file"`
	ExecuteShell    []string `yaml:"execute:shell"`
	ExecuteGit      []string `yaml:"execute:git"`
	ExecutePackage  []string `yaml:"execute:package"`
	ExecuteProcess  []string `yaml:"execute:process"`
	ExecuteDisplay  []string `yaml:"execute:display"`
	ExecuteConfig   []string `yaml:"execute:config"`
	ExecuteNetwork  []string `yaml:"execute:network"`
	ExecuteSchedule []string `yaml:"execute:schedule"`
	NetworkOutbound []string `yaml:"network:outbound"`
	UIOpen          []string `yaml:"ui:open"`
}

type GrantRecord struct {
	Type      string    `json:"type"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewRegistry() *Registry {
	return &Registry{
		patterns: make(map[string][]string),
		grants:   make(map[string]time.Time),
	}
}

func (r *Registry) SetWorkspace(workspace string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspace = workspace
}

func (r *Registry) LoadAllowlist(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.allowlistFile = path

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var cfg AllowlistConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	capMappings := []struct {
		key    string
		values []string
	}{
		{"read:file", cfg.ReadFile},
		{"execute:shell", cfg.ExecuteShell},
		{"execute:git", cfg.ExecuteGit},
		{"execute:package", cfg.ExecutePackage},
		{"execute:process", cfg.ExecuteProcess},
		{"execute:display", cfg.ExecuteDisplay},
		{"execute:config", cfg.ExecuteConfig},
		{"execute:network", cfg.ExecuteNetwork},
		{"execute:schedule", cfg.ExecuteSchedule},
		{"network:outbound", cfg.NetworkOutbound},
		{"ui:open", cfg.UIOpen},
	}

	for _, m := range capMappings {
		for _, scope := range m.values {
			r.patterns[m.key] = append(r.patterns[m.key], scope)
		}
	}

	return nil
}

func (r *Registry) Check(cap types.Capability) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	scope := cap.Scope

	grantKey := capabilityKey(cap.Type, scope)
	if expiry, ok := r.grants[grantKey]; ok && time.Now().Before(expiry) {
		return true
	}

	patterns, ok := r.patterns[cap.Type]
	if !ok {
		return false
	}

	for _, pattern := range patterns {
		expandedPattern := ExpandWorkspace(pattern, r.workspace)
		if Matches(expandedPattern, scope) || Matches(pattern, scope) {
			return true
		}
	}

	return false
}

func (r *Registry) Grant(cap types.Capability, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := capabilityKey(cap.Type, cap.Scope)
	r.grants[key] = time.Now().Add(ttl)
	r.saveGrants()
}

func (r *Registry) Revoke(cap types.Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := capabilityKey(cap.Type, cap.Scope)
	delete(r.grants, key)
	r.saveGrants()
}

func (r *Registry) LoadGrants(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.grantsFile = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var records []GrantRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}

	for _, rec := range records {
		if time.Now().Before(rec.ExpiresAt) {
			key := capabilityKey(rec.Type, rec.Scope)
			r.grants[key] = rec.ExpiresAt
		}
	}

	return nil
}

func (r *Registry) saveGrants() {
	if r.grantsFile == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(r.grantsFile), 0700); err != nil {
		return
	}

	records := make([]GrantRecord, 0, len(r.grants))
	now := time.Now()

	for key, expiry := range r.grants {
		if now.Before(expiry) {
			capType, scope := parseCapabilityKey(key)
			records = append(records, GrantRecord{
				Type:      capType,
				Scope:     scope,
				ExpiresAt: expiry,
			})
		}
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return
	}

	if err := os.WriteFile(r.grantsFile, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to persist capability grants to %s: %v\n", r.grantsFile, err)
	}
}

func (r *Registry) List() []types.Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var caps []types.Capability
	now := time.Now()

	for key, expiry := range r.grants {
		if now.Before(expiry) {
			capType, scope := parseCapabilityKey(key)
			caps = append(caps, types.Capability{
				Type:  capType,
				Scope: scope,
			})
		}
	}

	for capType, patterns := range r.patterns {
		for _, scope := range patterns {
			key := capabilityKey(capType, scope)
			if _, granted := r.grants[key]; !granted {
				caps = append(caps, types.Capability{
					Type:  capType,
					Scope: scope,
				})
			}
		}
	}

	return caps
}

type CapabilityInfo struct {
	Type      string
	Scope     string
	Granted   bool
	ExpiresAt time.Time
}

func (r *Registry) ListAll() []CapabilityInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var caps []CapabilityInfo
	now := time.Now()

	for capType, patterns := range r.patterns {
		for _, scope := range patterns {
			caps = append(caps, CapabilityInfo{
				Type:    capType,
				Scope:   scope,
				Granted: false,
			})
		}
	}

	for key, expiry := range r.grants {
		if now.Before(expiry) {
			capType, scope := parseCapabilityKey(key)
			caps = append(caps, CapabilityInfo{
				Type:      capType,
				Scope:     scope,
				Granted:   true,
				ExpiresAt: expiry,
			})
		}
	}

	return caps
}

func (r *Registry) SaveGrants(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.grantsFile = path
	r.saveGrants()
	return nil
}

const capabilityKeySep = "\x00"

func capabilityKey(capType, scope string) string {
	return capType + capabilityKeySep + scope
}

func parseCapabilityKey(key string) (string, string) {
	idx := strings.Index(key, capabilityKeySep)
	if idx < 0 {
		return "", ""
	}
	return key[:idx], key[idx+1:]
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "hyperi", "allowlist.yaml")
}

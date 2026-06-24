package capability

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/types"
)

func TestRegistry_Check_Allowlist(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls", "grep"}

	tests := []struct {
		name     string
		cap      types.Capability
		expected bool
	}{
		{
			name:     "exact match",
			cap:      types.Capability{Type: "execute:shell", Scope: "ls"},
			expected: true,
		},
		{
			name:     "non-allowed",
			cap:      types.Capability{Type: "execute:shell", Scope: "rm"},
			expected: false,
		},
		{
			name:     "unknown type",
			cap:      types.Capability{Type: "execute:delete", Scope: "file"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Check(tt.cap)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRegistry_Check_Grants(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:shell"] = []string{"ls"}
	r.grants[capabilityKey("execute:shell", "custom")] = time.Now().Add(time.Hour)
	r.grants[capabilityKey("execute:shell", "expired")] = time.Now().Add(-time.Hour)

	tests := []struct {
		name     string
		cap      types.Capability
		expected bool
	}{
		{
			name:     "grant takes precedence",
			cap:      types.Capability{Type: "execute:shell", Scope: "custom"},
			expected: true,
		},
		{
			name:     "expired grant",
			cap:      types.Capability{Type: "execute:shell", Scope: "expired"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Check(tt.cap)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRegistry_GrantRoundTrip_MultiSegmentType(t *testing.T) {
	dir := t.TempDir()
	grantsFile := filepath.Join(dir, "grants.json")

	r := NewRegistry()
	r.grantsFile = grantsFile

	multiSegmentCaps := []types.Capability{
		{Type: "execute:shell", Scope: "grep"},
		{Type: "execute:git", Scope: "git:status"},
		{Type: "execute:package", Scope: "apt:nginx"},
		{Type: "execute:process", Scope: "systemctl:restart:nginx"},
		{Type: "network:outbound", Scope: "api.anthropic.com"},
	}

	for _, cap := range multiSegmentCaps {
		r.Grant(cap, time.Hour)
	}

	r2 := NewRegistry()
	if err := r2.LoadGrants(grantsFile); err != nil {
		t.Fatalf("LoadGrants: %v", err)
	}

	for _, cap := range multiSegmentCaps {
		if !r2.Check(cap) {
			t.Errorf("capability %s %s lost after grant round-trip", cap.Type, cap.Scope)
		}
	}
}

func TestRegistry_Grant(t *testing.T) {
	r := NewRegistry()
	r.grantsFile = filepath.Join(t.TempDir(), "test_grants.json")

	cap := types.Capability{Type: "execute:shell", Scope: "test"}
	r.Grant(cap, time.Hour)

	if !r.Check(cap) {
		t.Error("expected granted capability to be checked")
	}
}

func TestRegistry_Revoke(t *testing.T) {
	r := NewRegistry()
	r.grantsFile = filepath.Join(t.TempDir(), "test_grants.json")

	cap := types.Capability{Type: "execute:shell", Scope: "test"}
	r.Grant(cap, time.Hour)
	r.Revoke(cap)

	if r.Check(cap) {
		t.Error("expected revoked capability to not be checked")
	}
}

func TestRegistry_Check_PackageCapability(t *testing.T) {
	r := NewRegistry()
	r.patterns["execute:package"] = []string{"apt:*", "flatpak:*"}

	tests := []struct {
		name     string
		cap      types.Capability
		expected bool
	}{
		{
			name:     "apt install allowed by wildcard",
			cap:      types.Capability{Type: "execute:package", Scope: "apt:curl"},
			expected: true,
		},
		{
			name:     "flatpak install allowed",
			cap:      types.Capability{Type: "execute:package", Scope: "flatpak:org.gnome.Gedit"},
			expected: true,
		},
		{
			name:     "snap not allowed",
			cap:      types.Capability{Type: "execute:package", Scope: "snap:firefox"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.Check(tt.cap)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
